package server_test

import (
	"encoding/base64"
	"net/http"
	"testing"
)

// Ref numbers are INT columns. A ref or cursor whose number is past that range
// is refused the same way as any other bad ref or cursor, never sent on to
// the database to fail there with an internal error.
func TestOutOfRangeRefsAndCursorsAreRefused(t *testing.T) {
	t.Parallel()
	e := newMCPHTTPEnv(t, nil)
	_, token := e.mustProjectMemberToken(t, "ref-range")
	const tooBig = "2147483648"
	bigCursor := base64.RawURLEncoding.EncodeToString([]byte(`{"n":` + tooBig + `}`))
	issueBase := "/" + e.ownerUsername + "/issues/" + e.projKey + "-"

	for _, tc := range []struct{ bad, malformed string }{
		{issueBase + tooBig, issueBase + "0"},
		{e.projectPath() + "/links/link-" + tooBig, e.projectPath() + "/links/link-0"},
		{e.projectIssuesPath() + "?cursor=" + bigCursor, e.projectIssuesPath() + "?cursor=not-base64!!!"},
		{e.projectPath() + "/tags?cursor=" + bigCursor, e.projectPath() + "/tags?cursor=not-base64!!!"},
	} {
		wantCode, wantBody := e.do(t, http.MethodGet, tc.malformed, nil)
		code, body := e.do(t, http.MethodGet, tc.bad, nil)
		if code != http.StatusBadRequest || code != wantCode {
			t.Fatalf("GET %s code = %d body = %s, want %d like %s", tc.bad, code, body, wantCode, wantBody)
		}
	}

	res := e.uiDoNoRedirect(t, http.MethodGet, issueBase+tooBig, token, nil)
	body := readBody(t, res)
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("UI out-of-range issue code = %d body = %s", res.StatusCode, body)
	}
	res = e.uiDoNoRedirect(t, http.MethodGet, e.projectPath()+"/sprints/sprint-"+tooBig+"/description", token, nil)
	body = readBody(t, res)
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("UI out-of-range sprint code = %d body = %s", res.StatusCode, body)
	}

	session := mcpConnect(t, e, e.authToken)
	for _, call := range []struct {
		tool string
		args map[string]any
	}{
		{"track_get_issue", map[string]any{"owner": e.ownerUsername, "issue": e.projKey + "-" + tooBig}},
		{"track_get_sprint", map[string]any{"owner": e.ownerUsername, "key": e.projKey, "sprint": "sprint-" + tooBig}},
		{"track_list_issues", map[string]any{"owner": e.ownerUsername, "key": e.projKey, "cursor": bigCursor}},
		{"track_list_objects", map[string]any{"owner": e.ownerUsername, "key": e.projKey, "cursor": bigCursor}},
	} {
		requireMCPErrorCode(t, mcpCallExpectError(t, e, session, call.tool, call.args), "validation_error")
	}
}
