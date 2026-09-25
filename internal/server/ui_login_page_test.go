package server

import (
	"bytes"
	"regexp"
	"strings"
	"testing"
)

var (
	uiPasswordLoginDisclosurePattern = regexp.MustCompile(`<details\b[^>]*\bdata-password-login\b[^>]*>`)
	uiUsernameInputPattern           = regexp.MustCompile(`<input\b[^>]*\bname="username"[^>]*>`)
	uiFocusableElementPattern        = regexp.MustCompile(`<(?:a|button|summary|select|textarea)\b[^>]*>|<input\b[^>]*>`)
	uiOpenAttributePattern           = regexp.MustCompile(`\sopen(?:\s|>|=)`)
	uiAutofocusAttributePattern      = regexp.MustCompile(`\sautofocus(?:\s|>|=)`)
)

func renderUILoginForTest(t *testing.T, data uiLoginData) string {
	t.Helper()
	var buf bytes.Buffer
	if err := uiTemplates.ExecuteTemplate(&buf, "login", data); err != nil {
		t.Fatalf("render login: %v", err)
	}
	return buf.String()
}

func uiPasswordLoginDisclosureForTest(t *testing.T, body string) string {
	t.Helper()
	tag := uiPasswordLoginDisclosurePattern.FindString(body)
	if tag == "" {
		t.Fatalf("login page has no password disclosure: %s", body)
	}
	return tag
}

func TestUILoginPasswordFormIsCollapsedByDefault(t *testing.T) {
	t.Parallel()

	body := renderUILoginForTest(t, uiLoginData{CSRFToken: "csrf-token", Next: "/tokens"})

	if tag := uiPasswordLoginDisclosureForTest(t, body); uiOpenAttributePattern.MatchString(tag) {
		t.Fatalf("password disclosure is open by default: %s", tag)
	}
	username := uiUsernameInputPattern.FindString(body)
	if username == "" {
		t.Fatalf("login page has no username field: %s", body)
	}
	if uiAutofocusAttributePattern.MatchString(username) {
		t.Fatalf("collapsed username field is autofocused: %s", username)
	}
	if strings.Contains(body, "role=\"alert\"") {
		t.Fatalf("login page renders an error without one: %s", body)
	}
}

func TestUILoginPasswordFormOpensWithPasswordError(t *testing.T) {
	t.Parallel()

	body := renderUILoginForTest(t, uiLoginData{CSRFToken: "csrf-token", Next: "/tokens", Error: "Username or password not accepted."})

	if tag := uiPasswordLoginDisclosureForTest(t, body); !uiOpenAttributePattern.MatchString(tag) {
		t.Fatalf("password disclosure is collapsed after a failed password login: %s", tag)
	}
	if username := uiUsernameInputPattern.FindString(body); !uiAutofocusAttributePattern.MatchString(username) {
		t.Fatalf("username field is not focused after a failed password login: %s", username)
	}
	if !strings.Contains(body, `role="alert"`) || !strings.Contains(body, "Username or password not accepted.") {
		t.Fatalf("login page does not announce the error: %s", body)
	}
}

// The passkey button leads the page: it must be the first thing a keyboard
// user reaches, ahead of the password disclosure and every link.
func TestUILoginPasskeyIsTheFirstFocusableAction(t *testing.T) {
	t.Parallel()

	for _, data := range []uiLoginData{{}, {Error: "Username or password not accepted."}} {
		body := renderUILoginForTest(t, data)
		start := strings.Index(body, "<body")
		if start < 0 {
			t.Fatalf("login page has no body: %s", body)
		}
		for _, tag := range uiFocusableElementPattern.FindAllString(body[start:], -1) {
			if strings.Contains(tag, `type="hidden"`) {
				continue
			}
			if !strings.Contains(tag, "data-passkey-login") {
				t.Fatalf("first focusable element with error %q is %s, want the passkey button", data.Error, tag)
			}
			break
		}
	}
}

// The password form is still an ordinary username/current-password form once
// expanded, so browsers and password managers keep offering to fill it, and it
// still posts the CSRF token and return path alongside the credentials.
func TestUILoginPasswordFormKeepsAutofillAndCSRF(t *testing.T) {
	t.Parallel()

	body := renderUILoginForTest(t, uiLoginData{CSRFToken: "csrf-token", Next: "/tokens"})
	formStart := strings.Index(body, `<form method="post" action="/login"`)
	if formStart < 0 {
		t.Fatalf("login page has no password form: %s", body)
	}
	form := body[formStart:]
	form = form[:strings.Index(form, "</form>")]
	for _, want := range []string{
		`name="csrf_token" value="csrf-token"`,
		`name="next" value="/tokens"`,
		`data-password-login`,
		`autocomplete="username"`,
		`name="password" type="password" autocomplete="current-password"`,
		`<button type="submit"`,
	} {
		if !strings.Contains(form, want) {
			t.Fatalf("password form missing %q: %s", want, form)
		}
	}
}

// Without WebAuthn the password form is the only way in, so auth.js opens the
// disclosure up front. Opening it any way lands focus on the username field.
func TestUILoginScriptOpensPasswordFormWithoutWebAuthn(t *testing.T) {
	t.Parallel()

	script, err := uiTemplateFS.ReadFile("static/auth.js")
	if err != nil {
		t.Fatalf("read auth asset: %v", err)
	}
	for _, want := range []string{
		`const passkeysSupported = () => !!(window.PublicKeyCredential && navigator.credentials);`,
		`const passwordLogin = document.querySelector("[data-password-login]");`,
		`if (!passkeysSupported()) passwordLogin.open = true;`,
		`passwordLogin.addEventListener("toggle", () => {
      if (passwordLogin.open) passwordLogin.querySelector("input[name='username']")?.focus();
    });`,
	} {
		if !strings.Contains(string(script), want) {
			t.Fatalf("auth.js missing %q", want)
		}
	}
}

// CSP allows neither inline styles nor third-party assets, so the branded
// backdrop must be plain markup styled by the self-hosted stylesheet.
func TestUIAuthPagesRenderBrandingWithoutInlineStyles(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name string
		data any
	}{
		{name: "login", data: uiLoginData{}},
		{name: "signup", data: uiSignupData{}},
	} {
		var buf bytes.Buffer
		if err := uiTemplates.ExecuteTemplate(&buf, tt.name, tt.data); err != nil {
			t.Fatalf("render %s: %v", tt.name, err)
		}
		body := buf.String()
		for _, want := range []string{
			`<img src="/static/icon.svg" alt="" width="64" height="64"`,
			`>trackslash</h1>`,
			`<div data-auth-backdrop class="auth-backdrop" aria-hidden="true">`,
		} {
			if !strings.Contains(body, want) {
				t.Fatalf("%s missing %q: %s", tt.name, want, body)
			}
		}
		for _, forbidden := range []string{" style=", "<style", "@import", "fonts.googleapis.com", "https://"} {
			if strings.Contains(body, forbidden) {
				t.Fatalf("%s contains %q", tt.name, forbidden)
			}
		}
	}
}

// Every auth backdrop animation lives behind prefers-reduced-motion:
// no-preference, so reduced-motion users get the same scene held still.
func TestUIAuthBackdropAnimatesOnlyWithoutReducedMotion(t *testing.T) {
	t.Parallel()

	css, err := uiTemplateFS.ReadFile("static/app.css")
	if err != nil {
		t.Fatalf("read stylesheet: %v", err)
	}
	const motionQuery = "@media (prefers-reduced-motion:no-preference){"
	rest := string(css)
	var outside, inside strings.Builder
	for {
		start := strings.Index(rest, motionQuery)
		if start < 0 {
			outside.WriteString(rest)
			break
		}
		outside.WriteString(rest[:start])
		depth, end := 1, start+len(motionQuery)
		for ; end < len(rest) && depth > 0; end++ {
			switch rest[end] {
			case '{':
				depth++
			case '}':
				depth--
			}
		}
		inside.WriteString(rest[start:end])
		rest = rest[end:]
	}
	for _, want := range []string{"animation:auth-orb-drift-a", "animation:auth-slash-sweep", "animation:auth-glow-breathe"} {
		if !strings.Contains(inside.String(), want) {
			t.Fatalf("stylesheet missing motion-gated %q", want)
		}
	}
	if strings.Contains(outside.String(), "animation:auth-") {
		t.Fatal("auth backdrop animates outside prefers-reduced-motion:no-preference")
	}
	for _, want := range []string{".auth-backdrop{position:fixed;inset:0;z-index:0;overflow:hidden;contain:strict;pointer-events:none;"} {
		if !strings.Contains(outside.String(), want) {
			t.Fatalf("stylesheet missing %q", want)
		}
	}
}
