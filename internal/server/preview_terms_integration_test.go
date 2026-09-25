package server_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/bradleymackey/track-slash/internal/server"
	"github.com/bradleymackey/track-slash/legal"
)

func newPreviewHTTPEnv(t *testing.T) *httpEnv {
	t.Helper()
	base := newHTTPEnv(t)
	ts := httptest.NewServer(server.NewWithOptions(base.store, nil, server.Options{
		PreviewTermsRequired: true,
	}).Router())
	t.Cleanup(ts.Close)

	preview := *base
	preview.ts = ts
	return &preview
}

func TestPublicLegalPagesAndLinks(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)

	for _, tc := range []struct {
		path  string
		wants []string
	}{
		{path: "/terms", wants: []string{"trackslash Preview Terms", "Bad Bundle Limited", "not ready for production or commercial use", "independent installations"}},
		{path: "/privacy", wants: []string{"trackslash Privacy Notice", "Authentication information", "independent installations"}},
		{path: "/security", wants: []string{"trackslash Security Policy", "security@trackslash.com", "does not currently operate a bug-bounty programme"}},
	} {
		res := e.uiDoNoRedirect(t, http.MethodGet, tc.path, "", nil)
		body := readBody(t, res)
		res.Body.Close()
		if res.StatusCode != http.StatusOK {
			t.Fatalf("GET %s code = %d body = %s", tc.path, res.StatusCode, body)
		}
		if contentType := res.Header.Get("Content-Type"); !strings.HasPrefix(contentType, "text/html") {
			t.Fatalf("GET %s Content-Type = %q", tc.path, contentType)
		}
		for _, want := range tc.wants {
			if !strings.Contains(body, want) {
				t.Fatalf("GET %s body missing %q: %s", tc.path, want, body)
			}
		}
		for _, href := range []string{`href="/terms"`, `href="/privacy"`, `href="/security"`} {
			if !strings.Contains(body, href) {
				t.Fatalf("GET %s body missing %q", tc.path, href)
			}
		}
		requireSecurityHeadersForTest(t, res.Header)
	}

	for _, tc := range []struct {
		path  string
		token string
	}{
		{path: "/login"},
		{path: "/signup"},
		{path: "/settings/profile", token: e.authToken},
		{path: "/settings/login", token: e.authToken},
		{path: "/settings/notifications", token: e.authToken},
		{path: "/tokens", token: e.authToken},
	} {
		body := e.uiGet(t, tc.path, tc.token)
		for _, href := range []string{`href="/terms"`, `href="/privacy"`, `href="/security"`} {
			if !strings.Contains(body, href) {
				t.Fatalf("GET %s body missing legal link %q", tc.path, href)
			}
		}
	}
}

func TestPreviewTermsRequiredForPasswordSignup(t *testing.T) {
	t.Parallel()
	e := newPreviewHTTPEnv(t)

	signup := e.uiGet(t, "/signup", "")
	for _, want := range []string{`name="accept_terms"`, "Preview Terms", "Privacy Notice"} {
		if !strings.Contains(signup, want) {
			t.Fatalf("signup body missing %q: %s", want, signup)
		}
	}

	missingUsername := "uimissing" + strings.ToLower(uniqueProjectKey(t))
	res := e.uiDoPreAuthForm(t, "/signup", url.Values{
		"username": {missingUsername},
		"password": {"correct-horse-battery"},
	})
	body := readBody(t, res)
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest || !strings.Contains(body, "must agree to the Preview Terms") {
		t.Fatalf("UI missing acceptance code = %d body = %s", res.StatusCode, body)
	}

	acceptedUsername := "uiaccepted" + strings.ToLower(uniqueProjectKey(t))
	res = e.uiDoPreAuthForm(t, "/signup", url.Values{
		"username":     {acceptedUsername},
		"password":     {"correct-horse-battery"},
		"accept_terms": {"on"},
	})
	body = readBody(t, res)
	res.Body.Close()
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("UI accepted signup code = %d body = %s", res.StatusCode, body)
	}
	assertPreviewTermsAcceptance(t, e, acceptedUsername)

	apiMissingUsername := "apimissing" + strings.ToLower(uniqueProjectKey(t))
	code, apiBody := e.doUnauth(t, http.MethodPost, "/accounts", map[string]any{
		"username": apiMissingUsername,
		"password": "correct-horse-battery",
	})
	if code != http.StatusBadRequest || !bytes.Contains(apiBody, []byte("must agree to the Preview Terms")) {
		t.Fatalf("API missing acceptance code = %d body = %s", code, apiBody)
	}

	apiAcceptedUsername := "apiaccepted" + strings.ToLower(uniqueProjectKey(t))
	code, apiBody = e.doUnauth(t, http.MethodPost, "/accounts", map[string]any{
		"username":     apiAcceptedUsername,
		"password":     "correct-horse-battery",
		"accept_terms": true,
	})
	if code != http.StatusCreated {
		t.Fatalf("API accepted signup code = %d body = %s", code, apiBody)
	}
	assertPreviewTermsAcceptance(t, e, apiAcceptedUsername)
}

func TestPreviewTermsRequiredForPasskeySignupEndpoints(t *testing.T) {
	t.Parallel()
	e := newPreviewHTTPEnv(t)

	username := "pkterms" + strings.ToLower(uniqueProjectKey(t))
	code, body := e.doUnauth(t, http.MethodPost, "/accounts/passkey/options", map[string]any{
		"username": username,
	})
	if code != http.StatusBadRequest || !bytes.Contains(body, []byte("must agree to the Preview Terms")) {
		t.Fatalf("passkey options missing acceptance code = %d body = %s", code, body)
	}

	code, body = e.doUnauth(t, http.MethodPost, "/accounts/passkey/options", map[string]any{
		"username":     username,
		"accept_terms": true,
	})
	if code != http.StatusOK {
		t.Fatalf("passkey options accepted code = %d body = %s", code, body)
	}

	code, body = e.doUnauth(t, http.MethodPost, "/accounts/passkey", map[string]any{
		"ceremony_id": uuid.New(),
		"credential":  map[string]any{},
	})
	if code != http.StatusBadRequest || !bytes.Contains(body, []byte("must agree to the Preview Terms")) {
		t.Fatalf("passkey finish missing acceptance code = %d body = %s", code, body)
	}

	res := uiDoPreAuthJSON(t, e, "/signup/passkey", map[string]any{
		"ceremony_id": uuid.New(),
		"credential":  map[string]any{},
	})
	uiBody := readBody(t, res)
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest || !strings.Contains(uiBody, "must agree to the Preview Terms") {
		t.Fatalf("UI passkey finish missing acceptance code = %d body = %s", res.StatusCode, uiBody)
	}
}

func uiDoPreAuthJSON(t *testing.T, e *httpEnv, path string, payload any) *http.Response {
	t.Helper()
	seedResponse := e.uiDoNoRedirect(t, http.MethodGet, "/signup", "", nil)
	defer seedResponse.Body.Close()
	seedCookie := findUICookieNamed(t, seedResponse.Cookies(), uiPreAuthCookieNameForTest)
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	req, err := http.NewRequestWithContext(e.ctx, http.MethodPost, e.ts.URL+path, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", e.ts.URL)
	req.Header.Set("X-CSRF-Token", uiCSRFTokenForTest("pre-auth", seedCookie.Value))
	req.AddCookie(seedCookie)
	res, err := e.ts.Client().Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	return res
}

func assertPreviewTermsAcceptance(t *testing.T, e *httpEnv, username string) {
	t.Helper()
	var version string
	var acceptedAt time.Time
	err := e.pool.QueryRow(e.ctx, `
		SELECT a.terms_version, a.accepted_at
		FROM preview_terms_acceptances a
		JOIN users u ON u.id = a.user_id
		WHERE u.username = $1
	`, username).Scan(&version, &acceptedAt)
	if err != nil {
		t.Fatalf("query acceptance for %s: %v", username, err)
	}
	if version != legal.PreviewTermsVersion || acceptedAt.IsZero() {
		t.Fatalf("acceptance = version %q at %v", version, acceptedAt)
	}
}
