package server_test

import (
	"errors"
	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/store"
	"github.com/go-webauthn/webauthn/webauthn"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestUITokensPageCreatesAndRevokesToken(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	user, token := e.mustProjectMemberToken(t, "ui-tokens")

	body := e.uiGet(t, "/tokens", token)
	for _, want := range []string{"Tokens", `data-modal-open="token-create"`, `id="token-create" data-client-modal class="fixed inset-0 z-50 hidden`, `role="dialog" aria-modal="true" aria-labelledby="token-create-title"`, "Create API token"} {
		if !strings.Contains(body, want) {
			t.Fatalf("tokens page missing modal behavior %q: %s", want, body)
		}
	}
	csrfToken := uiCSRFTokenForTest("session", token)
	if !strings.Contains(body, `name="csrf_token" value="`+csrfToken+`"`) {
		t.Fatalf("tokens page missing session-bound CSRF field: %s", body)
	}
	invalidForm := url.Values{"name": {""}, "csrf_token": {csrfToken}}
	invalidResponse := e.uiDoNoRedirectWithHeaders(t, http.MethodPost, "/tokens", token, strings.NewReader(invalidForm.Encode()), map[string]string{
		"Origin": e.ts.URL,
	})
	invalidBody := readBody(t, invalidResponse)
	invalidResponse.Body.Close()
	if invalidResponse.StatusCode != http.StatusOK || !strings.Contains(invalidBody, "Name required, max 200 chars.") || !strings.Contains(invalidBody, `id="token-create" data-client-modal class="fixed inset-0 z-50 grid`) {
		t.Fatalf("invalid token creation should keep modal open, code = %d body = %s", invalidResponse.StatusCode, invalidBody)
	}

	form := url.Values{"name": {"from ui"}, "csrf_token": {csrfToken}}
	res := e.uiDoNoRedirectWithHeaders(t, http.MethodPost, "/tokens", token, strings.NewReader(form.Encode()), map[string]string{
		"Origin":       e.ts.URL,
		"X-CSRF-Token": "",
	})
	defer res.Body.Close()
	body = readBody(t, res)
	// Post/Redirect/Get, so a reload cannot create a second token.
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/tokens" {
		t.Fatalf("create token code = %d location = %q body = %s", res.StatusCode, res.Header.Get("Location"), body)
	}
	reveal := findUICookieOrNil(res.Cookies(), uiTokenRevealCookieNameForTest)
	if reveal == nil {
		t.Fatalf("create token set no reveal cookie: %+v", res.Cookies())
	}
	revealed := e.uiGetWithCookies(t, "/tokens", token, reveal)
	body = readBody(t, revealed)
	revealed.Body.Close()
	if revealed.StatusCode != http.StatusOK {
		t.Fatalf("tokens page after create code = %d body = %s", revealed.StatusCode, body)
	}
	if !strings.Contains(body, "Copy this token now.") {
		t.Fatalf("body missing created token notice: %s", body)
	}
	if !strings.Contains(body, `id="token-create" data-client-modal class="fixed inset-0 z-50 grid`) {
		t.Fatalf("created token response should keep the one-time secret modal open: %s", body)
	}
	rawToken := createdTokenValue(t, body)
	if nextBody := e.uiGet(t, "/tokens", token); strings.Contains(nextBody, rawToken) || strings.Contains(nextBody, "Copy this token now.") {
		t.Fatalf("created token was shown after its one-time response: %s", nextBody)
	}
	tokens, err := e.store.ListAuthTokens(e.ctx, user.ID)
	if err != nil {
		t.Fatalf("ListAuthTokens: %v", err)
	}
	var created *model.AuthToken
	for i := range tokens {
		if tokens[i].Name == "from ui" {
			created = &tokens[i]
			break
		}
	}
	if created == nil {
		t.Fatalf("created token missing: %+v", tokens)
	}
	revokeForm := url.Values{"csrf_token": {csrfToken}}
	res = e.uiDoNoRedirectWithHeaders(t, http.MethodPost, "/tokens/"+created.ID.String()+"/revoke", token, strings.NewReader(revokeForm.Encode()), map[string]string{
		"Origin":       e.ts.URL,
		"X-CSRF-Token": "",
	})
	defer res.Body.Close()
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("revoke code = %d body = %s", res.StatusCode, readBody(t, res))
	}
	tokens, err = e.store.ListAuthTokens(e.ctx, user.ID)
	if err != nil {
		t.Fatalf("ListAuthTokens after revoke: %v", err)
	}
	for _, tok := range tokens {
		if tok.ID == created.ID && tok.RevokedAt == nil {
			t.Fatalf("token not revoked: %+v", tok)
		}
	}
}

func TestUITokenCreationCSRFVariants(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	user, token := e.mustProjectMemberToken(t, "ui-token-csrf")
	csrfToken := uiCSRFTokenForTest("session", token)

	htmxForm := url.Values{"name": {"from htmx"}}
	res := e.uiDoNoRedirectWithHeaders(t, http.MethodPost, "/tokens", token, strings.NewReader(htmxForm.Encode()), map[string]string{
		"HX-Request": "true",
		"Origin":     e.ts.URL,
	})
	body := readBody(t, res)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(body, "Copy this token now.") {
		t.Fatalf("HTMX create token code = %d body = %s", res.StatusCode, body)
	}
	htmxRawToken := createdTokenValue(t, body)
	if nextBody := e.uiGet(t, "/tokens", token); strings.Contains(nextBody, htmxRawToken) {
		t.Fatalf("HTMX-created token was shown after its one-time response: %s", nextBody)
	}

	missingForm := url.Values{"name": {"missing csrf"}}
	res = e.uiDoNoRedirectWithHeaders(t, http.MethodPost, "/tokens", token, strings.NewReader(missingForm.Encode()), map[string]string{
		"Origin":       e.ts.URL,
		"X-CSRF-Token": "",
	})
	body = readBody(t, res)
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden || !strings.Contains(body, "CSRF validation failed.") {
		t.Fatalf("missing CSRF code = %d body = %s", res.StatusCode, body)
	}

	invalidForm := url.Values{"name": {"invalid csrf"}, "csrf_token": {csrfToken}}
	res = e.uiDoNoRedirectWithHeaders(t, http.MethodPost, "/tokens", token, strings.NewReader(invalidForm.Encode()), map[string]string{
		"Origin":       e.ts.URL,
		"X-CSRF-Token": "wrong",
	})
	body = readBody(t, res)
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden || !strings.Contains(body, "CSRF validation failed.") {
		t.Fatalf("invalid CSRF code = %d body = %s", res.StatusCode, body)
	}

	past := time.Now().Add(-time.Minute)
	expired, err := e.store.CreateAuthToken(e.ctx, store.CreateAuthTokenParams{
		UserID:    user.ID,
		Kind:      model.AuthTokenKindSession,
		Name:      "expired session",
		ExpiresAt: &past,
	})
	if err != nil {
		t.Fatalf("CreateAuthToken expired session: %v", err)
	}
	expiredForm := url.Values{
		"name":       {"expired session attempt"},
		"csrf_token": {uiCSRFTokenForTest("session", expired.RawToken)},
	}
	res = e.uiDoNoRedirectWithHeaders(t, http.MethodPost, "/tokens", expired.RawToken, strings.NewReader(expiredForm.Encode()), map[string]string{"Origin": e.ts.URL})
	body = readBody(t, res)
	res.Body.Close()
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/login?next=%2Ftokens" {
		t.Fatalf("expired session code = %d location = %q body = %s", res.StatusCode, res.Header.Get("Location"), body)
	}
	if cookie := res.Header.Get("Set-Cookie"); !strings.Contains(cookie, uiCookieNameForTest+"=") || !strings.Contains(cookie, "Max-Age=0") {
		t.Fatalf("expired session Set-Cookie = %q, want cleared session", cookie)
	}

	tokens, err := e.store.ListAuthTokens(e.ctx, user.ID)
	if err != nil {
		t.Fatalf("ListAuthTokens: %v", err)
	}
	for _, authToken := range tokens {
		switch authToken.Name {
		case "missing csrf", "invalid csrf", "expired session attempt":
			t.Fatalf("rejected token creation persisted %+v", authToken)
		}
	}
}

func createdTokenValue(t *testing.T, body string) string {
	t.Helper()
	codeStart := strings.Index(body, "<code")
	if codeStart < 0 {
		t.Fatalf("created token code missing: %s", body)
	}
	valueStart := strings.Index(body[codeStart:], ">")
	if valueStart < 0 {
		t.Fatalf("created token code malformed: %s", body)
	}
	valueStart += codeStart + 1
	valueEnd := strings.Index(body[valueStart:], "</code>")
	if valueEnd < 0 {
		t.Fatalf("created token code closing tag missing: %s", body)
	}
	value := strings.TrimSpace(body[valueStart : valueStart+valueEnd])
	if value == "" {
		t.Fatalf("created token value empty: %s", body)
	}
	return value
}

// newUIPasswordAccount signs in a fresh password account and returns its
// username and session token.
func newUIPasswordAccount(t *testing.T, e *httpEnv, prefix, password string) (string, string) {
	t.Helper()
	username := prefix + strings.ToLower(uniqueProjectKey(t))
	user, err := e.store.CreateAccount(e.ctx, store.CreateAccountParams{
		Username: username,
		Password: password,
		Name:     "Old UI",
	})
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	token, err := e.store.CreateAuthToken(e.ctx, store.CreateAuthTokenParams{
		UserID: user.ID,
		Kind:   model.AuthTokenKindSession,
		Name:   "session",
	})
	if err != nil {
		t.Fatalf("CreateAuthToken: %v", err)
	}
	return username, token.RawToken
}

// Each account page carries exactly the sections that moved to it from the old
// general Settings page, plus the shared legal footer, and marks itself as the
// current page in the sidebar's account group.
func TestUIAccountPagesRenderTheirOwnSections(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	_, token := newUIPasswordAccount(t, e, "uiaccount", "correct-horse-battery")

	profileSections := []string{"Display name", `action="/settings/profile"`, `data-modal-open="profile-image-picker"`, "Save profile"}
	loginSections := []string{"data-password-login-panel", "Current password", `action="/settings/password"`, "data-passkeys-panel", "Saved passkeys", `data-modal-open="passkey-create"`}
	notificationSections := []string{"data-push-notifications", "Browser notifications", "Notification categories", `action="/settings/push/preferences"`}
	tokenSections := []string{`data-modal-open="token-create"`, "API tokens", "Connectors", "Web sessions"}

	for _, tt := range []struct {
		path    string
		view    string
		title   string
		want    []string
		notWant [][]string
	}{
		{path: "/settings/profile", view: "profile", title: "Profile", want: profileSections, notWant: [][]string{loginSections, notificationSections, tokenSections}},
		{path: "/settings/login", view: "login", title: "Login", want: loginSections, notWant: [][]string{profileSections, notificationSections, tokenSections}},
		{path: "/settings/notifications", view: "notifications", title: "Notifications", want: notificationSections, notWant: [][]string{profileSections, loginSections, tokenSections}},
		{path: "/tokens", view: "tokens", title: "Tokens", want: tokenSections, notWant: [][]string{profileSections, loginSections, notificationSections}},
	} {
		t.Run(tt.path, func(t *testing.T) {
			body := e.uiGet(t, tt.path, token)
			if !strings.Contains(body, `<section data-sidebar-view="`+tt.view+`" class="mx-auto max-w-6xl px-4 py-4 sm:px-6 sm:py-6">`) {
				t.Fatalf("%s missing the shared account page frame: %s", tt.path, body)
			}
			if !strings.Contains(body, `<h1 class="truncate text-2xl font-semibold tracking-normal">`+tt.title+`</h1>`) {
				t.Fatalf("%s missing page title %q: %s", tt.path, tt.title, body)
			}
			for _, want := range tt.want {
				if !strings.Contains(body, want) {
					t.Fatalf("%s missing its section marker %q: %s", tt.path, want, body)
				}
			}
			for _, other := range tt.notWant {
				for _, notWant := range other {
					if strings.Contains(body, notWant) {
						t.Fatalf("%s still renders another page's section %q: %s", tt.path, notWant, body)
					}
				}
			}

			footer := uiElementForTest(t, body, `<footer data-account-footer`, `</footer>`)
			for _, want := range []string{`class="border-t border-slate-200 py-4 dark:border-slate-800"`, `aria-label="Legal"`, `href="/terms"`, `href="/privacy"`, `href="/security"`} {
				if !strings.Contains(footer, want) {
					t.Fatalf("%s legal footer missing %q: %s", tt.path, want, footer)
				}
			}
			if got := strings.Count(body, `aria-label="Legal"`); got != 1 {
				t.Fatalf("%s legal navigation count = %d, want 1: %s", tt.path, got, body)
			}

			sidebar := uiElementForTest(t, body, `<aside id="app-sidebar"`, `</aside>`)
			if strings.Contains(sidebar, `aria-label="Legal"`) {
				t.Fatalf("%s sidebar contains legal links: %s", tt.path, sidebar)
			}
			account := uiElementForTest(t, sidebar, `<nav aria-label="Account" data-sidebar-account`, `</nav>`)
			if got := strings.Count(account, `aria-current="page"`); got != 1 {
				t.Fatalf("%s account group has %d current pages, want 1: %s", tt.path, got, account)
			}
			current := uiElementForTest(t, account, `data-sidebar-view="`+tt.view+`"`, `>`)
			if !strings.Contains(current, `aria-current="page"`) {
				t.Fatalf("%s is not the current page in the account group: %s", tt.path, account)
			}
		})
	}
}

// The old general Settings address lands on Profile and keeps whatever query it
// was given, so an old bookmark or shared link still works.
func TestUISettingsRedirectsToProfileKeepingTheQuery(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	_, token := newUIPasswordAccount(t, e, "uisettingsredirect", "correct-horse-battery")

	for _, tt := range []struct {
		name         string
		path         string
		wantLocation string
	}{
		{name: "no query", path: "/settings", wantLocation: "/settings/profile"},
		{name: "query survives", path: "/settings?utm_source=bookmark&tab=passkeys", wantLocation: "/settings/profile?utm_source=bookmark&tab=passkeys"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			res := e.uiDoNoRedirect(t, http.MethodGet, tt.path, token, nil)
			res.Body.Close()
			if res.StatusCode != http.StatusSeeOther {
				t.Fatalf("code = %d, want 303", res.StatusCode)
			}
			if got := res.Header.Get("Location"); got != tt.wantLocation {
				t.Fatalf("Location = %q, want %q", got, tt.wantLocation)
			}
			if body := e.uiGet(t, res.Header.Get("Location"), token); !strings.Contains(body, `data-sidebar-view="profile"`) {
				t.Fatalf("following %s did not reach Profile: %s", tt.wantLocation, body)
			}
		})
	}

	// htmx would follow a 303 and swap Profile into #main under a stale
	// address, so an htmx request is told to navigate instead.
	res := e.uiDoNoRedirectWithHeaders(t, http.MethodGet, "/settings?tab=passkeys", token, nil, map[string]string{"HX-Request": "true"})
	body := readBody(t, res)
	res.Body.Close()
	if res.StatusCode != http.StatusNoContent || body != "" {
		t.Fatalf("htmx code = %d body = %q, want 204 and no body", res.StatusCode, body)
	}
	if got := res.Header.Get("HX-Redirect"); got != "/settings/profile?tab=passkeys" {
		t.Fatalf("HX-Redirect = %q, want %q", got, "/settings/profile?tab=passkeys")
	}
}

func TestUIAccountPagesRequireSignIn(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)

	for _, path := range []string{"/settings", "/settings/profile", "/settings/login", "/settings/notifications", "/tokens"} {
		t.Run(path, func(t *testing.T) {
			res := e.uiDoNoRedirect(t, http.MethodGet, path, "", nil)
			res.Body.Close()
			want := "/login?next=" + url.QueryEscape(path)
			if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != want {
				t.Fatalf("code = %d Location = %q, want 303 to %q", res.StatusCode, res.Header.Get("Location"), want)
			}

			res = e.uiDoNoRedirectWithHeaders(t, http.MethodGet, path, "", nil, map[string]string{"HX-Request": "true"})
			res.Body.Close()
			if res.StatusCode != http.StatusNoContent || res.Header.Get("HX-Redirect") != want {
				t.Fatalf("htmx code = %d HX-Redirect = %q, want 204 to %q", res.StatusCode, res.Header.Get("HX-Redirect"), want)
			}
		})
	}
}

func TestUIProfilePageUpdatesProfile(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	_, token := newUIPasswordAccount(t, e, "uiprofile", "correct-horse-battery")

	body := e.uiGet(t, "/settings/profile", token)
	for _, want := range []string{"Display name", "Email", `value="Old UI"`, "Save profile", `data-modal-open="profile-image-picker"`, `action="/settings/profile-image"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("profile body missing %q: %s", want, body)
		}
	}

	form := url.Values{"name": {"New UI"}, "email": {"ui@example.com"}}
	res := e.uiDoNoRedirect(t, http.MethodPost, "/settings/profile", token, strings.NewReader(form.Encode()))
	body = readBody(t, res)
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("profile code = %d body = %s", res.StatusCode, body)
	}
	if !strings.Contains(body, "Profile saved.") || !strings.Contains(body, "New UI") || !strings.Contains(body, "ui@example.com") {
		t.Fatalf("profile body missing saved values: %s", body)
	}
	if !strings.Contains(body, `data-sidebar-view="profile"`) {
		t.Fatalf("profile update did not render the Profile page: %s", body)
	}

	form = url.Values{"name": {"  "}, "email": {"ui@example.com"}}
	res = e.uiDoNoRedirect(t, http.MethodPost, "/settings/profile", token, strings.NewReader(form.Encode()))
	body = readBody(t, res)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(body, "name required") || strings.Contains(body, "Profile saved.") || !strings.Contains(body, `data-sidebar-view="profile"`) {
		t.Fatalf("blank name code = %d body = %s", res.StatusCode, body)
	}

	res = e.uiDoNoRedirect(t, http.MethodPost, "/settings/profile", token, strings.NewReader("name=%zz"))
	body = readBody(t, res)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(body, "Unable to read form.") || !strings.Contains(body, `data-sidebar-view="profile"`) {
		t.Fatalf("malformed profile form code = %d body = %s", res.StatusCode, body)
	}
}

// The image picker posts as a plain form, so its response is the page the
// browser lands on: it has to be Profile, with the new image in place.
func TestUIProfileImageUploadAndDeleteRerenderProfile(t *testing.T) {
	t.Parallel()
	e, _ := newStorageHTTPEnv(t, 1<<20)
	user, token := e.mustProjectMemberToken(t, "ui-profile-image")

	res := e.uiDoMultipartContext(t, "/settings/profile-image", token, nil, "face.png", string(testPNG(t, 3, 2)))
	body := readBody(t, res)
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("UI upload code = %d body = %s", res.StatusCode, body)
	}
	updated, err := e.store.GetUser(e.ctx, user.ID)
	if err != nil {
		t.Fatalf("GetUser after UI upload: %v", err)
	}
	if updated.ProfileImageThumbnailObjectID == nil {
		t.Fatalf("UI upload user missing thumbnail id: %+v", updated)
	}
	for _, want := range []string{
		`data-sidebar-view="profile"`,
		"Profile saved.",
		"/users/" + user.ID.String() + "/profile-image/thumbnail/content?v=" + updated.ProfileImageThumbnailObjectID.String(),
		`action="/settings/profile-image/delete"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("UI upload page missing %q: %s", want, body)
		}
	}

	res = e.uiDoNoRedirect(t, http.MethodPost, "/settings/profile-image/delete", token, nil)
	body = readBody(t, res)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(body, `data-sidebar-view="profile"`) || strings.Contains(body, `action="/settings/profile-image/delete"`) || strings.Contains(body, "/profile-image/thumbnail/content") {
		t.Fatalf("UI delete code = %d body = %s", res.StatusCode, body)
	}

	// A rejected upload answers with the error instead of rendering Profile as
	// though the image had been saved.
	res = e.uiDoMultipartContext(t, "/settings/profile-image", token, nil, "face.png", "not an image")
	body = readBody(t, res)
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest || strings.Contains(body, "Profile saved.") {
		t.Fatalf("UI invalid upload code = %d body = %s", res.StatusCode, body)
	}
}

func TestUILoginPageChangesPassword(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	oldPassword := "correct-horse-battery"
	newPassword := "new-correct-horse"
	username, token := newUIPasswordAccount(t, e, "uilogin", oldPassword)

	body := e.uiGet(t, "/settings/login", token)
	for _, want := range []string{"Password login", "On", "Current password", "New password", "Passkeys", "Saved passkeys", "Add a passkey", "Passkey label", "Enter current password", "Required before changing passkeys.", "Continue", "Add passkey", "No passkeys added.", `data-modal-open="passkey-create"`, `id="passkey-create" data-client-modal class="fixed inset-0 z-50 hidden`, `role="dialog" aria-modal="true" aria-labelledby="passkey-create-title"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("login body missing %q: %s", want, body)
		}
	}
	if strings.Contains(body, "Disable password login") || strings.Contains(body, "Enable password login") {
		t.Fatalf("login body shows password login toggle without passkey: %s", body)
	}
	for _, rejected := range []string{"Use passkey", "Confirm with", "Security check", "Leave blank to confirm", "Needed to add or remove passkeys.", `for="passkey_name">Name`} {
		if strings.Contains(body, rejected) {
			t.Fatalf("login body still shows confusing passkey copy %q: %s", rejected, body)
		}
	}
	if strings.Contains(body, `data-passkey-password-modal hidden class=`) || strings.Contains(body, `data-passkey-password-modal class="fixed inset-0 z-50 grid`) {
		t.Fatalf("login body renders passkey password modal open by default: %s", body)
	}

	for _, tt := range []struct {
		name string
		body string
		want string
	}{
		{name: "malformed form", body: "current_password=%zz", want: "Unable to read form."},
		{name: "wrong current password", body: url.Values{"current_password": {"wrong-password"}, "new_password": {newPassword}}.Encode(), want: "Current password not accepted."},
		{name: "invalid new password", body: url.Values{"current_password": {oldPassword}, "new_password": {"short"}}.Encode(), want: "password must be"},
	} {
		res := e.uiDoNoRedirect(t, http.MethodPost, "/settings/password", token, strings.NewReader(tt.body))
		body = readBody(t, res)
		res.Body.Close()
		if res.StatusCode != http.StatusOK || !strings.Contains(body, tt.want) || strings.Contains(body, "Password changed.") || !strings.Contains(body, `data-sidebar-view="login"`) {
			t.Fatalf("%s: code = %d body = %s", tt.name, res.StatusCode, body)
		}
	}
	if _, err := e.store.AuthenticatePassword(e.ctx, username, oldPassword); err != nil {
		t.Fatalf("rejected changes altered the password: %v", err)
	}

	form := url.Values{"current_password": {oldPassword}, "new_password": {newPassword}}
	res := e.uiDoNoRedirect(t, http.MethodPost, "/settings/password", token, strings.NewReader(form.Encode()))
	body = readBody(t, res)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(body, "Password changed.") || !strings.Contains(body, `data-sidebar-view="login"`) {
		t.Fatalf("password code = %d body = %s", res.StatusCode, body)
	}
	if _, err := e.store.AuthenticatePassword(e.ctx, username, oldPassword); !errors.Is(err, store.ErrUnauthorized) {
		t.Fatalf("old password err = %v, want ErrUnauthorized", err)
	}
	if _, err := e.store.AuthenticatePassword(e.ctx, username, newPassword); err != nil {
		t.Fatalf("new password auth: %v", err)
	}
}

// uiElementForTest returns body from the first occurrence of start through the
// next occurrence of end.
func uiElementForTest(t *testing.T, body, start, end string) string {
	t.Helper()
	from := strings.Index(body, start)
	if from < 0 {
		t.Fatalf("missing %q: %s", start, body)
	}
	to := strings.Index(body[from:], end)
	if to < 0 {
		t.Fatalf("unterminated %q: %s", start, body)
	}
	return body[from : from+to]
}

func TestUILoginPagePasswordLoginDisabledUsesPasskeyReauth(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	username := "uipwdoff" + strings.ToLower(uniqueProjectKey(t))
	user, err := e.store.CreateAccount(e.ctx, store.CreateAccountParams{
		Username: username,
		Password: "correct-horse-battery",
		Name:     "UI Password Off",
	})
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	if _, err := e.store.AddPasskeyCredential(e.ctx, user.ID, "localhost", "Laptop", serverPasskeyCredential("credential-"+uniqueProjectKey(t), 1)); err != nil {
		t.Fatalf("AddPasskeyCredential: %v", err)
	}
	if _, err := e.store.SetPasswordLoginEnabled(e.ctx, user.ID, false); err != nil {
		t.Fatalf("SetPasswordLoginEnabled: %v", err)
	}
	token, err := e.store.CreateAuthToken(e.ctx, store.CreateAuthTokenParams{
		UserID: user.ID,
		Kind:   model.AuthTokenKindSession,
		Name:   "session",
	})
	if err != nil {
		t.Fatalf("CreateAuthToken: %v", err)
	}

	body := e.uiGet(t, "/settings/login", token.RawToken)
	for _, want := range []string{"Password login", "Off", "Enable password login", "Password login is off.", "Passkeys", "Laptop"} {
		if !strings.Contains(body, want) {
			t.Fatalf("login body missing %q: %s", want, body)
		}
	}
	for _, rejected := range []string{`id="current_password"`, `for="current_password"`, "New password", "Change password", "Enter current password", "Required before changing passkeys.", "<div data-passkey-password-modal", "Disable password login", "Security check", "Confirm with"} {
		if strings.Contains(body, rejected) {
			t.Fatalf("disabled password login page still shows %q: %s", rejected, body)
		}
	}

	reauth, err := e.store.CreatePasskeyReauthToken(e.ctx, user.ID)
	if err != nil {
		t.Fatalf("CreatePasskeyReauthToken: %v", err)
	}
	res := e.uiDoNoRedirectWithHeaders(t, http.MethodPost, "/settings/password-login", token.RawToken, strings.NewReader(`{"enabled":true,"reauth_token":"`+reauth+`"}`), map[string]string{
		"Content-Type": "application/json",
	})
	defer res.Body.Close()
	body = readBody(t, res)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("ui password-login code = %d body = %s", res.StatusCode, body)
	}
	state := decode[model.PasswordLoginState](t, []byte(body))
	if !state.Enabled || !state.CanDisable {
		t.Fatalf("ui password-login state = %+v", state)
	}
	if _, err := e.store.AuthenticatePassword(e.ctx, username, "correct-horse-battery"); err != nil {
		t.Fatalf("AuthenticatePassword after UI enable: %v", err)
	}
}

func TestUILoginPagePasskeyOnlyAccountHidesPasskeyPasswordField(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	user, err := e.store.CreatePasskeyOnlyAccount(e.ctx, store.CreatePasskeyOnlyAccountParams{
		Username:       "uipasskey" + strings.ToLower(uniqueProjectKey(t)),
		Name:           "UI Passkey",
		RPID:           "localhost",
		UserHandle:     []byte("ui-handle-" + uniqueProjectKey(t)),
		CredentialName: "MacBook",
		Credential: webauthn.Credential{
			ID:        []byte("ui-credential-" + uniqueProjectKey(t)),
			PublicKey: []byte("ui-public-key"),
			Flags: webauthn.CredentialFlags{
				UserPresent:  true,
				UserVerified: true,
			},
		},
	})
	if err != nil {
		t.Fatalf("CreatePasskeyOnlyAccount: %v", err)
	}
	token, err := e.store.CreateAuthToken(e.ctx, store.CreateAuthTokenParams{
		UserID: user.ID,
		Kind:   model.AuthTokenKindSession,
		Name:   "session",
	})
	if err != nil {
		t.Fatalf("CreateAuthToken: %v", err)
	}

	body := e.uiGet(t, "/settings/login", token.RawToken)
	for _, want := range []string{"Password login", "No password", "No password is set.", "Passkeys", "Saved passkeys", "Add a passkey", "Passkey label", "MacBook"} {
		if !strings.Contains(body, want) {
			t.Fatalf("login body missing %q: %s", want, body)
		}
	}
	for _, rejected := range []string{"Security check", "Use passkey", "Confirm with", "Leave blank to confirm", "Needed to add or remove passkeys.", `id="current_password"`, `for="current_password"`, "New password", "Change password", "Enter current password", "Required before changing passkeys.", "<div data-passkey-password-modal", `id="passkey_current_password"`, "Enable password login", "Disable password login"} {
		if strings.Contains(body, rejected) {
			t.Fatalf("login body still shows password passkey copy %q: %s", rejected, body)
		}
	}
}
