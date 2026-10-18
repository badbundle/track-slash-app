package server_test

import (
	"net/http"
	"testing"

	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/store"
)

// clearableIssueFields are the issue fields an update can clear, each with
// the value that fills it in.
func clearableIssueFields(e *httpEnv, sprint model.Sprint) map[string]any {
	return map[string]any{
		"assignee_id": e.adminID.String(),
		"reporter_id": e.adminID.String(),
		"worker":      "agent",
		"sprint":      sprint.Ref,
		"repeat":      "daily",
		"due_date":    "2099-01-01",
	}
}

func requireIssueFieldsSet(t *testing.T, issue model.Issue, sprint model.Sprint, set bool) {
	t.Helper()
	fields := map[string]bool{
		"assignee_id": issue.AssigneeID != nil,
		"reporter_id": issue.ReporterID != nil,
		"worker":      issue.Worker != nil,
		"sprint":      issue.SprintID != nil && *issue.SprintID == sprint.ID,
		"repeat":      issue.Repeat != nil && issue.Repeat.Current,
		"due_date":    issue.DueDate != nil,
	}
	for field, got := range fields {
		if got != set {
			t.Fatalf("%s set = %v, want %v: %+v", field, got, set, issue)
		}
	}
}

func newClearableIssue(t *testing.T, e *httpEnv) (model.Issue, model.Sprint) {
	t.Helper()
	sprint, err := e.store.CreateSprint(e.ctx, store.CreateSprintParams{ProjectID: e.projectID, Name: "Clearable"})
	if err != nil {
		t.Fatalf("CreateSprint: %v", err)
	}
	issue, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{ProjectID: e.projectID, Title: "clearable"})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	code, body := e.do(t, http.MethodPatch, e.issuePath(issue), clearableIssueFields(e, sprint))
	if code != http.StatusOK {
		t.Fatalf("fill issue code = %d body = %s", code, body)
	}
	requireIssueFieldsSet(t, decode[model.Issue](t, body), sprint, true)
	return issue, sprint
}

// An explicit null clears a clearable field, as its clear_ flag does, while a
// field left out stays as it is.
func TestHTTPUpdateIssueNullClears(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	issue, sprint := newClearableIssue(t, e)

	code, body := e.do(t, http.MethodPatch, e.issuePath(issue), map[string]any{"title": "left alone"})
	if code != http.StatusOK {
		t.Fatalf("update title code = %d body = %s", code, body)
	}
	requireIssueFieldsSet(t, decode[model.Issue](t, body), sprint, true)

	nulls := map[string]any{}
	for field := range clearableIssueFields(e, sprint) {
		nulls[field] = nil
	}
	code, body = e.do(t, http.MethodPatch, e.issuePath(issue), nulls)
	if code != http.StatusOK {
		t.Fatalf("null update code = %d body = %s", code, body)
	}
	requireIssueFieldsSet(t, decode[model.Issue](t, body), sprint, false)
}

// MCP clients may send null for every optional argument they leave unset, so
// over MCP a null leaves the field as it is and only the clear_ flags clear.
func TestMCPUpdateIssueNullLeavesFieldsAlone(t *testing.T) {
	t.Parallel()
	e := newMCPHTTPEnv(t, nil)
	session := mcpConnect(t, e, e.authToken)
	issue, sprint := newClearableIssue(t, e)
	args := func(fields map[string]any) map[string]any {
		fields["owner"] = e.ownerUsername
		fields["issue"] = issue.Identifier
		return fields
	}

	out := mcpCall(t, e, session, "track_update_issue", args(map[string]any{"title": "left alone"}))
	requireIssueFieldsSet(t, decodeMCPField[model.Issue](t, out, "issue"), sprint, true)

	nulls := map[string]any{}
	for field := range clearableIssueFields(e, sprint) {
		nulls[field] = nil
	}
	out = mcpCall(t, e, session, "track_update_issue", args(nulls))
	requireIssueFieldsSet(t, decodeMCPField[model.Issue](t, out, "issue"), sprint, true)

	clears := map[string]any{}
	for _, flag := range []string{"clear_worker", "clear_assignee", "clear_reporter", "clear_sprint", "clear_due_date", "clear_repeat"} {
		clears[flag] = true
	}
	out = mcpCall(t, e, session, "track_update_issue", args(clears))
	requireIssueFieldsSet(t, decodeMCPField[model.Issue](t, out, "issue"), sprint, false)
}
