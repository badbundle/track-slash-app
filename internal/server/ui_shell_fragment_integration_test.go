package server_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// The reported symptom: after creating a token, two sidebars appeared side by
// side. tokens.html posts with hx-target="#main", and #main is a sibling of the
// sidebar inside .app-shell, so a whole-document response puts a second header,
// sidebar, and #main inside the existing #main.
func TestCreatingATokenOverHTMXDoesNotDuplicateTheSidebar(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	_, session := e.mustProjectMemberToken(t, "htmx-token")

	form := url.Values{"name": {"from htmx"}}
	res := e.uiDoNoRedirectWithHeaders(t, http.MethodPost, "/tokens", session,
		strings.NewReader(form.Encode()), map[string]string{"HX-Request": "true", "Origin": e.ts.URL})
	body := readBody(t, res)
	res.Body.Close()

	if res.StatusCode != http.StatusOK {
		t.Fatalf("code = %d body = %s", res.StatusCode, body)
	}
	if !strings.Contains(body, "Copy this token now.") {
		t.Fatalf("htmx response lost the one-time token: %s", body)
	}
	assertSwappableFragment(t, "POST /tokens", body)
}

// The same guarantee for every htmx request that reaches the shell renderer,
// not just the one that was reported.
func TestHTMXResponsesAreFragmentsNotDocuments(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	_, session := e.mustProjectMemberToken(t, "htmx-fragment")
	issue := e.mustIssue(t, "htmx fragment issue")

	for _, path := range []string{
		"/",
		"/me",
		"/me/all",
		"/projects",
		"/projects/new",
		"/issues/new",
		"/settings/profile",
		"/settings/login",
		"/settings/notifications",
		"/tokens",
		e.projectPath() + "/sprint",
		e.projectPath() + "/about",
		e.projectPath() + "/sprints",
		e.projectPath() + "/all",
		e.projectPath() + "/context",
		e.projectPath() + "/tags",
		e.projectPath() + "/deleted",
		"/" + e.ownerUsername + "/issues/" + issue.Identifier,
		"/nope",
	} {
		t.Run(path, func(t *testing.T) {
			res := e.uiDoNoRedirectWithHeaders(t, http.MethodGet, path, session, nil, map[string]string{
				"HX-Request": "true",
			})
			body := readBody(t, res)
			res.Body.Close()
			if res.StatusCode >= 500 {
				t.Fatalf("code = %d body = %s", res.StatusCode, body)
			}
			assertSwappableFragment(t, path, body)
		})
	}
}

// Plain navigations must still receive the whole document, sidebar included.
func TestPlainNavigationStillRendersTheWholeShell(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	_, session := e.mustProjectMemberToken(t, "shell-navigation")

	body := e.uiGet(t, "/tokens", session)
	for _, want := range []string{"<!doctype html>", `id="app-sidebar"`, `<main id="main"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("navigation response missing %q", want)
		}
	}
}

func assertSwappableFragment(t *testing.T, label, body string) {
	t.Helper()
	for _, unwanted := range []string{"<!doctype html>", "<html", `id="app-sidebar"`, `<main id="main"`} {
		if strings.Contains(strings.ToLower(body), strings.ToLower(unwanted)) {
			t.Fatalf("%s answered htmx with %q, which htmx would swap inside #main: %s", label, unwanted, body)
		}
	}
}
