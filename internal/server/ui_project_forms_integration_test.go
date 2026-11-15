package server_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/store"
)

func TestUIProjectFormsList(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	member, _ := e.mustProjectMemberToken(t, "forms-list-assignee")
	issue := e.mustCreateIssue(t, "Linked issue")

	// Nothing yet: the menu item is there for a member, the list is empty.
	path := e.projectPath() + "/forms"
	body := e.uiGet(t, path, e.authToken)
	requireContains(t, "empty list", body, "No forms yet. Agents create forms when they need a decision from a person.",
		`data-project-forms`, `aria-label="Form status"`, ">Open</a>", ">Submitted</a>", ">Done</a>", ">All</a>",
		`<span>Forms</span>`, `<i data-lucide="clipboard-list"`)
	requireOmits(t, "empty list", body, "No forms match this filter.", "data-form-summary-row")
	requireContains(t, "breadcrumb", body, `Forms`)

	open := e.mustIssueForm(t, issue, member.ID, "Open one")
	submitted := e.mustIssueForm(t, issue, e.adminID, "Submitted one")
	e.mustSubmitIssueForm(t, submitted)
	acked := e.mustIssueForm(t, issue, e.adminID, "Acknowledged one")
	e.mustSubmitIssueForm(t, acked)
	if _, err := e.store.AcknowledgeForm(e.ctx, acked.ID); err != nil {
		t.Fatalf("AcknowledgeForm: %v", err)
	}
	cancelled := e.mustIssueForm(t, issue, member.ID, "Cancelled one")
	if _, err := e.store.CancelForm(e.ctx, cancelled.ID); err != nil {
		t.Fatalf("CancelForm: %v", err)
	}

	titles := []string{"Open one", "Submitted one", "Acknowledged one", "Cancelled one"}
	for query, want := range map[string][]string{
		"":                  titles,
		"?status=all":       titles,
		"?status=open":      {"Open one"},
		"?status=submitted": {"Submitted one"},
		"?status=done":      {"Acknowledged one", "Cancelled one"},
		"?status=bogus":     titles,
	} {
		for _, suffix := range []string{"", "/panel"} {
			body := e.uiGet(t, path+suffix+query, e.authToken)
			if got := strings.Count(body, "data-form-summary-row"); got != len(want) {
				t.Fatalf("GET %s%s rows = %d, want %d: %s", suffix, query, got, len(want), body)
			}
			for _, title := range titles {
				if contains := strings.Contains(body, title); contains != containsString(want, title) {
					t.Fatalf("GET %s%s has %q = %v", suffix, query, title, contains)
				}
			}
		}
	}

	// Newest first, with the status badge, the assignee's avatar and name, and a link to the form.
	body = e.uiGet(t, path, e.authToken)
	if strings.Index(body, "Cancelled one") > strings.Index(body, "Open one") {
		t.Fatalf("forms are not newest first: %s", body)
	}
	requireContains(t, "rows", body,
		`data-form-status="open"`, `data-form-status="submitted"`, `data-form-status="acknowledged"`, `data-form-status="cancelled"`,
		`hx-get="`+e.uiFormPath(open)+`/panel"`, `href="`+e.uiFormPath(open)+`"`, "data-form-row-assignee",
		`title="`+member.Name+`"`, `title="`+uiAdminName(t, e)+`"`)
	requireOmits(t, "rows", body, member.Email)
	// The active filter is marked and the others link to theirs.
	body = e.uiGet(t, path+"?status=done", e.authToken)
	requireContains(t, "filter", body, `href="`+path+`?status=open"`, `hx-get="`+path+`/panel?status=open"`, `aria-current="true"`)

	// A filter with nothing in it says so.
	if _, err := e.store.CancelForm(e.ctx, open.ID); err != nil {
		t.Fatalf("CancelForm: %v", err)
	}
	body = e.uiGet(t, path+"?status=open", e.authToken)
	requireContains(t, "empty filter", body, "No forms match this filter.")
	requireOmits(t, "empty filter", body, "No forms yet.", "data-form-summary-row")

	// The form page's breadcrumb links to the list.
	body = e.uiGet(t, e.uiFormPath(open), e.authToken)
	requireContains(t, "form breadcrumb", body, `href="`+path+`"`, `hx-get="`+path+`/panel"`)
}

func uiAdminName(t *testing.T, e *httpEnv) string {
	t.Helper()
	user, err := e.store.GetUser(e.ctx, e.adminID)
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	return user.Name
}

func containsString(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}

func TestUIProjectFormsPagination(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	for i := 1; i <= 52; i++ {
		e.mustUIForm(t, e.adminID, func(p *store.CreateFormParams) { p.Title = fmt.Sprintf("Batch form %02d", i) })
	}
	path := e.projectPath() + "/forms"
	body := e.uiGet(t, path, e.authToken)
	if got := strings.Count(body, "data-form-summary-row"); got != 50 {
		t.Fatalf("first run rows = %d, want 50", got)
	}
	requireContains(t, "first run", body, "Batch form 52", "Batch form 03", "Load more", `hx-get="`+path+`/page?cursor=`)
	requireOmits(t, "first run", body, "Batch form 02")
	start := strings.Index(body, `hx-get="`+path+`/page?cursor=`) + len(`hx-get="`)
	more := strings.ReplaceAll(body[start:start+strings.Index(body[start:], `"`)], "&amp;", "&")
	rest := e.uiGet(t, more, e.authToken)
	if got := strings.Count(rest, "data-form-summary-row"); got != 2 {
		t.Fatalf("second run rows = %d, want 2", got)
	}
	requireContains(t, "second run", rest, "Batch form 02", "Batch form 01")
	requireOmits(t, "second run", rest, "Load more", "Batch form 03")

	// The cursor keeps the filter.
	filtered := e.uiGet(t, path+"?status=open", e.authToken)
	requireContains(t, "filtered run", filtered, `/forms/page?status=open&amp;cursor=`)
	res := e.uiDoNoRedirect(t, http.MethodGet, path+"/page?cursor=%21%21", e.authToken, nil)
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad cursor code = %d", res.StatusCode)
	}
}

func TestUIProjectFormsAreTheMembers(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	e.mustUIForm(t, e.adminID, nil)
	e.makeProjectPublic(t)
	path := e.projectPath() + "/forms"
	_, outsiderToken := e.mustUserToken(t, "forms-list-outsider")

	for name, token := range map[string]string{"anonymous": "", "outsider": outsiderToken} {
		for _, target := range []string{path, path + "/panel", path + "/page", path + "?status=open"} {
			res := e.uiDoNoRedirect(t, http.MethodGet, target, token, nil)
			body := readBody(t, res)
			res.Body.Close()
			if res.StatusCode != http.StatusNotFound || !strings.Contains(body, "Page not found") || res.Header.Get("Location") != "" {
				t.Fatalf("%s GET %s = %d %s", name, target, res.StatusCode, body)
			}
			requireOmits(t, name+" "+target, body, "Release decisions", "data-form-summary-row")
		}
		// The public project page is open to them, without the menu item.
		body := e.uiGet(t, e.projectPath()+"/all", token)
		requireOmits(t, name+" project page", body, `<span>Forms</span>`, `/forms"`)
	}
	// A member sees the item in the menu on every view that has it.
	requireContains(t, "member menu", e.uiGet(t, e.projectPath()+"/changelog", e.authToken), `<span>Forms</span>`, `hx-push-url="`+path+`"`)
	// A read-only member reads the list too.
	readonly, readonlyToken := e.mustUserToken(t, "forms-list-readonly")
	if _, err := e.store.SetProjectMemberRole(e.ctx, e.projectID, readonly.ID, model.ProjectMemberRoleReadonly); err != nil {
		t.Fatalf("SetProjectMemberRole: %v", err)
	}
	requireContains(t, "read-only list", e.uiGet(t, path, readonlyToken), "Release decisions")
}
