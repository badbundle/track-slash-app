package server_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/store"
)

// uiOptionalFormDefinition is a form with one optional question, so a test can
// submit it without answering.
func uiOptionalFormDefinition() model.FormDefinition {
	no := false
	return model.FormDefinition{Sections: []model.FormSection{{
		Questions: []model.FormQuestion{{ID: "note", Type: model.FormQuestionText, Label: "Any note?", Required: &no}},
	}}}
}

// mustIssueForm makes a form on an issue, addressed to assignee.
func (e *httpEnv) mustIssueForm(t *testing.T, issue model.Issue, assignee uuid.UUID, title string) model.Form {
	t.Helper()
	return e.mustUIForm(t, assignee, func(p *store.CreateFormParams) {
		p.IssueID = &issue.ID
		p.Title = title
		p.Definition = uiOptionalFormDefinition()
	})
}

func (e *httpEnv) mustSubmitIssueForm(t *testing.T, form model.Form) {
	t.Helper()
	if _, err := e.store.SubmitForm(e.ctx, store.SubmitFormParams{FormID: form.ID, AssigneeID: form.AssigneeID, Via: model.FormSubmitWeb}); err != nil {
		t.Fatalf("SubmitForm: %v", err)
	}
}

func TestUIIssueFormNotice(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	member, _ := e.mustProjectMemberToken(t, "issue-form-assignee")
	issue := e.mustCreateIssue(t, "Needs a decision")
	other := e.mustCreateIssue(t, "No forms here")

	// An issue without forms has neither the notice nor the row.
	body := e.uiGet(t, e.issuePath(other), e.authToken)
	requireOmits(t, "issue without forms", body, "data-issue-form-notice", "data-issue-forms-row")

	mine := e.mustIssueForm(t, issue, e.adminID, "Pick the region")
	theirs := e.mustIssueForm(t, issue, member.ID, "Approve the budget")
	body = e.uiGet(t, e.issuePath(issue), e.authToken)
	requireContains(t, "issue page", body,
		"data-issue-form-notice", "Waiting on answers:", "Pick the region", "Approve the budget",
		`hx-get="`+e.uiFormPath(mine)+`/panel"`, `href="`+e.uiFormPath(theirs)+`"`,
		" · waiting on "+member.Name, "clipboard-list", "data-issue-forms-row")
	if got := strings.Count(body, "data-issue-form-notice-item"); got != 2 {
		t.Fatalf("notice lists %d forms, want 2", got)
	}
	// The viewer's own form carries no suffix.
	if got := strings.Count(body, " · waiting on "); got != 1 {
		t.Fatalf("notice has %d waiting-on suffixes, want 1", got)
	}
	if got := strings.Count(body, "data-issue-form-line"); got != 2 {
		t.Fatalf("Details row lists %d forms, want 2", got)
	}
	// The other issue still has none.
	requireOmits(t, "other issue", e.uiGet(t, e.issuePath(other), e.authToken), "Pick the region", "data-issue-forms-row")

	// Submitting one leaves the other in the notice, and the row keeps both.
	e.mustSubmitIssueForm(t, mine)
	body = e.uiGet(t, e.issuePath(issue), e.authToken)
	requireContains(t, "after submit", body, "Approve the budget", `data-form-status="submitted"`)
	if got := strings.Count(body, "data-issue-form-notice-item"); got != 1 {
		t.Fatalf("notice lists %d forms after submit, want 1", got)
	}
	requireOmits(t, "after submit", strings.SplitN(strings.SplitN(body, "data-issue-form-notice", 2)[1], "</div>", 2)[0], "Pick the region")

	// Cancelling the last open form takes the notice away; the row stays.
	if _, err := e.store.CancelForm(e.ctx, theirs.ID); err != nil {
		t.Fatalf("CancelForm: %v", err)
	}
	body = e.uiGet(t, e.issuePath(issue), e.authToken)
	requireOmits(t, "no open forms", body, "data-issue-form-notice", "Waiting on answers:")
	requireContains(t, "no open forms", body, "data-issue-forms-row", `data-form-status="cancelled"`, `data-form-status="submitted"`)
	// The panel twin carries the same row.
	requireContains(t, "panel", e.uiGet(t, e.issuePath(issue)+"/panel", e.authToken), "data-issue-forms-row")
}

func TestUIIssueFormsRowCap(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	issue := e.mustCreateIssue(t, "Many forms")
	for i := 1; i <= 12; i++ {
		e.mustIssueForm(t, issue, e.adminID, fmt.Sprintf("Question set %02d", i))
	}
	body := e.uiGet(t, e.issuePath(issue), e.authToken)
	if got := strings.Count(body, "data-issue-form-line"); got != 10 {
		t.Fatalf("Details row lists %d forms, want 10", got)
	}
	// Newest first: the ten newest show, the two oldest are left out of the row.
	row := strings.SplitN(body, "data-issue-forms-row", 2)[1]
	requireContains(t, "row", row, "Question set 12", "Question set 03", "and 2 more", `data-issue-forms-more href="`+e.projectPath()+`/forms"`, ">12</span>")
	requireOmits(t, "row", row, "Question set 02", "Question set 01")
	// The notice still names every open form.
	if got := strings.Count(body, "data-issue-form-notice-item"); got != 12 {
		t.Fatalf("notice lists %d forms, want 12", got)
	}
}

func TestUIIssueFormsStayAwayFromNonMembers(t *testing.T) {
	t.Parallel()
	const title = "Quietly ask the customer"
	check := func(t *testing.T, what, body string) {
		t.Helper()
		requireOmits(t, what, body, title, "form-1", "Waiting on answers", "data-issue-form-notice", "data-issue-forms-row", "clipboard-list")
	}

	t.Run("public viewer", func(t *testing.T) {
		t.Parallel()
		e := newHTTPEnv(t)
		e.makeProjectPublic(t)
		issue := e.mustCreateIssue(t, "A public issue")
		e.mustIssueForm(t, issue, e.adminID, title)
		member := e.uiGet(t, e.issuePath(issue), e.authToken)
		requireContains(t, "member", member, title)
		for _, token := range []string{"", e.mustSignedInOutsider(t)} {
			check(t, "public issue page", e.uiGet(t, e.issuePath(issue), token))
			check(t, "public issue panel", e.uiGet(t, e.issuePath(issue)+"/panel", token))
		}
	})

	t.Run("help-desk reporter", func(t *testing.T) {
		t.Parallel()
		e := newHTTPEnv(t)
		f := newHelpDeskFixture(t, e)
		e.mustIssueForm(t, f.aliceIssue, e.adminID, title)
		body := e.uiGet(t, e.issuePath(f.aliceIssue), f.aliceToken)
		requireContains(t, "reporter view", body, "Alice cannot sign in")
		check(t, "help-desk reporter page", body)
		check(t, "help-desk reporter panel", e.uiGet(t, e.issuePath(f.aliceIssue)+"/panel", f.aliceToken))
	})

	t.Run("outside reporter of a private issue", func(t *testing.T) {
		t.Parallel()
		e := newHTTPEnv(t)
		if _, err := e.store.UpdateProjectAccessMode(e.ctx, e.projectID, model.ProjectAccessPublicIssues); err != nil {
			t.Fatalf("UpdateProjectAccessMode: %v", err)
		}
		reporter, token := e.mustUserToken(t, "private-reporter")
		issue, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{ProjectID: e.projectID, Title: "My private report", Private: true, ReporterID: &reporter.ID})
		if err != nil {
			t.Fatalf("CreateIssue: %v", err)
		}
		e.mustIssueForm(t, issue, e.adminID, title)
		body := e.uiGet(t, e.issuePath(issue), token)
		requireContains(t, "reporter view", body, "My private report")
		check(t, "private reporter page", body)
		check(t, "private reporter panel", e.uiGet(t, e.issuePath(issue)+"/panel", token))
	})
}

func (e *httpEnv) mustSignedInOutsider(t *testing.T) string {
	t.Helper()
	_, token := e.mustUserToken(t, "forms-outsider")
	return token
}

func TestIssueOpenFormsInAPIAndMCP(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	issue := e.mustCreateIssue(t, "Has forms")
	quiet := e.mustCreateIssue(t, "No forms")
	first := e.mustIssueForm(t, issue, e.adminID, "First")
	second := e.mustIssueForm(t, issue, e.adminID, "Second")
	done := e.mustIssueForm(t, issue, e.adminID, "Third")
	e.mustSubmitIssueForm(t, done)
	readonly, readonlyToken := e.mustUserToken(t, "open-forms-readonly")
	if _, err := e.store.SetProjectMemberRole(e.ctx, e.projectID, readonly.ID, model.ProjectMemberRoleReadonly); err != nil {
		t.Fatalf("SetProjectMemberRole: %v", err)
	}

	openForms := func(raw []byte) ([]string, bool) {
		t.Helper()
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			t.Fatalf("unmarshal issue: %v body=%s", err, raw)
		}
		value, ok := fields["open_forms"]
		if !ok {
			return nil, false
		}
		var refs []string
		if err := json.Unmarshal(value, &refs); err != nil || refs == nil {
			t.Fatalf("open_forms = %s (%v)", value, err)
		}
		return refs, true
	}
	want := []string{second.Ref, first.Ref}
	same := func(got []string) bool { return strings.Join(got, ",") == strings.Join(want, ",") }

	// REST: a member gets the open refs, newest first, and an empty list when none.
	code, body := e.do(t, http.MethodGet, e.issuePath(issue), nil)
	if refs, ok := openForms(body); code != http.StatusOK || !ok || !same(refs) {
		t.Fatalf("member GET issue = %d %s", code, body)
	}
	code, body = e.do(t, http.MethodGet, e.issuePath(quiet), nil)
	if refs, ok := openForms(body); code != http.StatusOK || !ok || len(refs) != 0 || !strings.Contains(string(body), `"open_forms":[]`) {
		t.Fatalf("member GET issue without forms = %d %s", code, body)
	}
	code, body = e.doWithToken(t, readonlyToken, http.MethodGet, e.issuePath(issue), nil)
	if _, ok := openForms(body); code != http.StatusOK || !ok {
		t.Fatalf("read-only member GET issue = %d %s", code, body)
	}
	// Lists never carry it.
	code, body = e.do(t, http.MethodGet, e.projectIssuesPath(), nil)
	if code != http.StatusOK || strings.Contains(string(body), "open_forms") {
		t.Fatalf("issue list = %d %s", code, body)
	}

	// MCP agrees for a member, and its list and batch tools leave it out.
	session := mcpConnect(t, e, e.authToken)
	out := mcpCall(t, e, session, "track_get_issue", mcpIssueArgs(e, issue))
	if refs, ok := openForms(out["issue"]); !ok || !same(refs) {
		t.Fatalf("MCP member issue = %s", out["issue"])
	}
	for tool, args := range map[string]map[string]any{
		"track_list_issues":      {"owner": e.ownerUsername, "key": e.projKey},
		"track_batch_get_issues": {"owner": e.ownerUsername, "refs": []string{issue.Identifier}},
	} {
		raw, _ := json.Marshal(mcpCall(t, e, session, tool, args))
		if strings.Contains(string(raw), "open_forms") {
			t.Fatalf("MCP %s carries open_forms: %s", tool, raw)
		}
	}

	// A reader who is not a member never gets the field.
	e.makeProjectPublic(t)
	_, outsiderToken := e.mustUserToken(t, "open-forms-outsider")
	for name, token := range map[string]string{"anonymous": "", "outsider": outsiderToken} {
		code, body := e.doWithToken(t, token, http.MethodGet, e.issuePath(issue), nil)
		if _, ok := openForms(body); code != http.StatusOK || ok || strings.Contains(string(body), "open_forms") {
			t.Fatalf("%s GET issue = %d %s", name, code, body)
		}
	}
	out = mcpCall(t, e, mcpConnect(t, e, outsiderToken), "track_get_issue", mcpIssueArgs(e, issue))
	if _, ok := openForms(out["issue"]); ok {
		t.Fatalf("MCP outsider issue carries open_forms: %s", out["issue"])
	}

	// Neither kind of reporter gets it.
	hd := newHTTPEnv(t)
	f := newHelpDeskFixture(t, hd)
	hd.mustIssueForm(t, f.aliceIssue, hd.adminID, "Ask Alice")
	code, body = hd.doWithToken(t, f.aliceToken, http.MethodGet, hd.issuePath(f.aliceIssue), nil)
	if _, ok := openForms(body); code != http.StatusOK || ok {
		t.Fatalf("help-desk reporter GET issue = %d %s", code, body)
	}
	out = mcpCall(t, hd, mcpConnect(t, hd, f.aliceToken), "track_get_issue", mcpIssueArgs(hd, f.aliceIssue))
	if _, ok := openForms(out["issue"]); ok {
		t.Fatalf("MCP help-desk reporter issue carries open_forms: %s", out["issue"])
	}

	pe := newHTTPEnv(t)
	if _, err := pe.store.UpdateProjectAccessMode(pe.ctx, pe.projectID, model.ProjectAccessPublicIssues); err != nil {
		t.Fatalf("UpdateProjectAccessMode: %v", err)
	}
	reporter, reporterToken := pe.mustUserToken(t, "open-forms-private-reporter")
	secret, err := pe.store.CreateIssue(pe.ctx, store.CreateIssueParams{ProjectID: pe.projectID, Title: "Private", Private: true, ReporterID: &reporter.ID})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	pe.mustIssueForm(t, secret, pe.adminID, "Ask privately")
	code, body = pe.doWithToken(t, reporterToken, http.MethodGet, pe.issuePath(secret), nil)
	if _, ok := openForms(body); code != http.StatusOK || ok {
		t.Fatalf("private reporter GET issue = %d %s", code, body)
	}
	out = mcpCall(t, pe, mcpConnect(t, pe, reporterToken), "track_get_issue", mcpIssueArgs(pe, secret))
	if _, ok := openForms(out["issue"]); ok {
		t.Fatalf("MCP private reporter issue carries open_forms: %s", out["issue"])
	}
}

func mcpIssueArgs(e *httpEnv, issue model.Issue) map[string]any {
	return map[string]any{"owner": e.ownerUsername, "issue": issue.Identifier}
}
