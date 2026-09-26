package server_test

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

var uiLogoutFormPattern = regexp.MustCompile(`(?s)<form method="post" action="/logout"[^>]*>(.*?)</form>`)

// Signing out must work from whatever state the browser is actually in, which
// includes states the page was not rendered in: a tab that outlived its cookie,
// and a second click on Sign out after the first cleared it.
func TestLogoutSucceedsFromEverySessionState(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	_, session := e.mustProjectMemberToken(t, "logout-states")
	csrfToken := uiCSRFTokenForTest("session", session)

	for _, tt := range []struct {
		name    string
		cookie  string
		token   string
		wantLoc string
	}{
		{name: "signed in", cookie: session, token: csrfToken, wantLoc: "/login"},
		{name: "cookie already gone", cookie: "", token: csrfToken, wantLoc: "/login"},
		{name: "cookie already gone and no token", cookie: "", token: "", wantLoc: "/login"},
		{name: "cookie expired or unknown", cookie: "not-a-session", token: uiCSRFTokenForTest("session", "not-a-session"), wantLoc: "/login"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			res := e.uiDoNoRedirectWithHeaders(t, http.MethodPost, "/logout", tt.cookie,
				strings.NewReader(url.Values{"csrf_token": {tt.token}}.Encode()),
				map[string]string{"Origin": e.ts.URL, "X-CSRF-Token": ""})
			body := readBody(t, res)
			res.Body.Close()

			if res.StatusCode != http.StatusSeeOther {
				t.Fatalf("code = %d body = %s", res.StatusCode, body)
			}
			if got := res.Header.Get("Location"); got != tt.wantLoc {
				t.Fatalf("Location = %q, want %q", got, tt.wantLoc)
			}
		})
	}
}

// A signed-in session is still protected: forcing a user to sign out is a real,
// if minor, cross-site nuisance, so the relaxation must not extend to it.
func TestLogoutStillRejectsCSRFWhileSignedIn(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	user, session := e.mustProjectMemberToken(t, "logout-csrf")

	for _, tt := range []struct {
		name   string
		token  string
		origin string
	}{
		{name: "no token", token: "", origin: e.ts.URL},
		{name: "wrong token", token: "not-the-token", origin: e.ts.URL},
		{name: "cross-site origin", token: uiCSRFTokenForTest("session", session), origin: "https://evil.example.com"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			res := e.uiDoNoRedirectWithHeaders(t, http.MethodPost, "/logout", session,
				strings.NewReader(url.Values{"csrf_token": {tt.token}}.Encode()),
				map[string]string{"Origin": tt.origin, "X-CSRF-Token": ""})
			body := readBody(t, res)
			res.Body.Close()

			if res.StatusCode != http.StatusForbidden {
				t.Fatalf("code = %d body = %s", res.StatusCode, body)
			}
		})
	}

	// The session survived every rejected attempt.
	tokens, err := e.store.ListAuthTokens(e.ctx, user.ID)
	if err != nil {
		t.Fatalf("ListAuthTokens: %v", err)
	}
	for _, token := range tokens {
		if token.RevokedAt != nil {
			t.Fatalf("a rejected logout revoked %+v", token)
		}
	}
}

// The token the page actually renders must be the one the route accepts. This
// is the whole round trip a browser performs, with nothing computed by the test.
func TestLogoutFormTokenFromTheRenderedPageIsAccepted(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	_, session := e.mustProjectMemberToken(t, "logout-round-trip")

	form := uiLogoutFormPattern.FindStringSubmatch(e.uiGet(t, "/me", session))
	if form == nil {
		t.Fatal("no logout form on the page")
	}
	field := regexp.MustCompile(`name="csrf_token" value="([^"]*)"`).FindStringSubmatch(form[1])
	if field == nil || field[1] == "" {
		t.Fatalf("logout form carries no populated csrf_token: %s", form[1])
	}

	res := e.uiDoNoRedirectWithHeaders(t, http.MethodPost, "/logout", session,
		strings.NewReader(url.Values{"csrf_token": {field[1]}}.Encode()),
		map[string]string{"Origin": e.ts.URL, "X-CSRF-Token": ""})
	body := readBody(t, res)
	res.Body.Close()

	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("code = %d body = %s", res.StatusCode, body)
	}
}
