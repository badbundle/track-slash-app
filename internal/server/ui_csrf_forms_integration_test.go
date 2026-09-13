package server_test

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

var uiCSRFInputPattern = regexp.MustCompile(`(?i)name="csrf_token"\s+value="([^"]*)"`)

// The static template guard proves the field is present; only a real render
// proves it is populated. A forgotten construction site renders value="" and
// fails at submit time, not at build time.
func TestRenderedPagesCarryAPopulatedCSRFToken(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	// The owner token reaches every panel, including member management.
	token := e.authToken
	issue := e.mustIssue(t, "csrf form issue")

	for _, path := range []string{
		"/me",
		"/projects",
		"/projects/new",
		"/issues/new",
		"/settings",
		"/tokens",
		e.projectPath() + "/sprint",
		e.projectPath() + "/about",
		e.projectPath() + "/members",
		e.projectPath() + "/sprints",
		e.projectPath() + "/planned",
		e.projectPath() + "/all",
		e.projectPath() + "/context",
		e.projectPath() + "/tags",
		e.projectPath() + "/deleted",
		e.projectPath() + "/issues/new",
		"/" + e.ownerUsername + "/issues/" + issue.Identifier,
		"/" + e.ownerUsername + "/issues/" + issue.Identifier + "/context",
		"/" + e.ownerUsername + "/issues/" + issue.Identifier + "/tags",
	} {
		t.Run(path, func(t *testing.T) {
			body := e.uiGet(t, path, token)
			assertPopulatedCSRFTokens(t, path, body)
		})
	}
}

// Editing forms are rendered on demand as fragments, so they need the same
// guarantee as the pages that host them.
func TestRenderedIssueEditFragmentsCarryAPopulatedCSRFToken(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	token := e.authToken
	issue := e.mustIssue(t, "csrf fragment issue")
	issuePath := "/" + e.ownerUsername + "/issues/" + issue.Identifier

	for _, path := range []string{
		issuePath + "/title/edit",
		issuePath + "/description/edit",
		issuePath + "/status/edit",
		issuePath + "/priority/edit",
		issuePath + "/due-date/edit",
		issuePath + "/assignee/edit",
		issuePath + "/reporter/edit",
		issuePath + "/sprint/edit",
		issuePath + "/links/new",
		issuePath + "/sub-issues/new",
		e.projectPath() + "/member-candidates?username=nobody",
		e.projectPath() + "/name/edit",
		e.projectPath() + "/description/edit",
	} {
		t.Run(path, func(t *testing.T) {
			body := e.uiGet(t, path, token)
			assertPopulatedCSRFTokens(t, path, body)
		})
	}
}

// Every page is served with Referrer-Policy: no-referrer, and browsers answer
// that policy by serializing the origin of a plain form submission as null and
// sending no Referer at all. fetch and XHR keep the real origin, so only the
// forms that post without htmx arrive this way — sign out, password sign-in,
// sign-up, and the settings forms. Both CSRF middlewares have to accept it, or
// every one of those forms answers 403 in a real browser while every htmx
// control on the same page keeps working.
func TestNonHTMXFormPostsSurviveTheOpaqueBrowserOrigin(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	_, session := e.mustProjectMemberToken(t, "opaque-origin")
	browser := map[string]string{"Origin": "null", "Sec-Fetch-Site": "same-origin", "X-CSRF-Token": ""}

	t.Run("password sign-in", func(t *testing.T) {
		seed := e.uiDoNoRedirect(t, http.MethodGet, "/login", "", nil)
		defer seed.Body.Close()
		if seed.StatusCode != http.StatusOK {
			t.Fatalf("GET /login code = %d", seed.StatusCode)
		}
		preAuth := findUICookieNamed(t, seed.Cookies(), uiPreAuthCookieNameForTest)
		form := url.Values{
			"username":   {"not-a-user"},
			"password":   {"not-a-password"},
			"csrf_token": {uiCSRFTokenForTest("pre-auth", preAuth.Value)},
		}
		req, err := http.NewRequestWithContext(e.ctx, http.MethodPost, e.ts.URL+"/login", strings.NewReader(form.Encode()))
		if err != nil {
			t.Fatalf("NewRequest: %v", err)
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		for key, value := range browser {
			req.Header.Set(key, value)
		}
		req.AddCookie(preAuth)
		client := *e.ts.Client()
		client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		res, err := client.Do(req)
		if err != nil {
			t.Fatalf("POST /login: %v", err)
		}
		defer res.Body.Close()
		// The credentials are wrong on purpose: 401 proves the post reached the
		// handler, which is all the source check decides.
		if res.StatusCode != http.StatusUnauthorized {
			t.Fatalf("code = %d body = %s, want the handler's 401 rather than a CSRF rejection", res.StatusCode, readBody(t, res))
		}
	})

	t.Run("sign out", func(t *testing.T) {
		res := e.uiDoNoRedirectWithHeaders(t, http.MethodPost, "/logout", session,
			strings.NewReader(url.Values{"csrf_token": {uiCSRFTokenForTest("session", session)}}.Encode()), browser)
		defer res.Body.Close()
		if res.StatusCode != http.StatusSeeOther {
			t.Fatalf("code = %d body = %s", res.StatusCode, readBody(t, res))
		}
		if got := res.Header.Get("Location"); got != "/login" {
			t.Fatalf("Location = %q, want /login", got)
		}
	})
}

// The opaque origin is also what a sandboxed cross-site frame posts with, so
// accepting it must not have disarmed the check.
func TestOpaqueOriginFromACrossSiteFrameIsStillRejected(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	_, session := e.mustProjectMemberToken(t, "opaque-origin-frame")

	res := e.uiDoNoRedirectWithHeaders(t, http.MethodPost, "/logout", session,
		strings.NewReader(url.Values{"csrf_token": {uiCSRFTokenForTest("session", session)}}.Encode()),
		map[string]string{"Origin": "null", "Sec-Fetch-Site": "cross-site", "X-CSRF-Token": ""})
	defer res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("code = %d body = %s, want 403", res.StatusCode, readBody(t, res))
	}
}

func assertPopulatedCSRFTokens(t *testing.T, path, body string) {
	t.Helper()
	matches := uiCSRFInputPattern.FindAllStringSubmatch(body, -1)
	for _, match := range matches {
		if strings.TrimSpace(match[1]) == "" {
			t.Fatalf("%s rendered an empty csrf_token; a construction site is missing uiSessionCSRFToken", path)
		}
	}
	// A page with a posting form but no token at all would slip past the loop
	// above, so check the two counts agree.
	forms := strings.Count(strings.ToLower(body), `method="post"`)
	if forms > len(matches) {
		t.Fatalf("%s has %d posting forms but only %d csrf_token fields", path, forms, len(matches))
	}
}

func (e *httpEnv) mustIssue(t *testing.T, title string) issueRef {
	t.Helper()
	code, body := e.do(t, http.MethodPost, e.projectPath()+"/issues", map[string]any{"title": title})
	if code != http.StatusCreated && code != http.StatusOK {
		t.Fatalf("create issue code = %d body = %s", code, body)
	}
	return decode[issueRef](t, body)
}

type issueRef struct {
	Identifier string `json:"identifier"`
}
