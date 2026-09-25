package server_test

import (
	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/store"
	"github.com/google/uuid"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestUINewIssueCreatesIssueWithAllFieldsAndDefaultReporter(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	reporter, token := e.mustProjectMemberToken(t, "ui-new-issue")
	assignee, _ := e.mustProjectMemberToken(t, "ui-new-issue-assignee")
	searchProject, err := e.store.CreateProjectForUser(e.ctx, reporter.ID, uniqueProjectKey(t), "Search Target Project", "")
	if err != nil {
		t.Fatalf("CreateProjectForUser search target: %v", err)
	}

	body := e.uiGet(t, "/issues/new", token)
	for _, want := range []string{
		"New issue",
		"Choose a project",
		`data-sidebar-view=""`,
		`method="post" action="/issues"`,
		`hx-post="/issues"`,
		`id="issue-description" name="description" rows="5" data-autogrow-textarea placeholder="Describe the issue: context, steps to reproduce, acceptance criteria… Markdown is supported."`,
		`id="new-issue-project-form" method="get" action="/issues/new/panel"`,
		`id="issue-project" name="project"`,
		`type="hidden" name="project_id" value=""`,
		`hx-get="/issues/new/projects"`,
		`hx-target="#new-issue-project-options"`,
		`hx-swap="outerHTML"`,
		`data-search`,
		`data-search-collapsible`,
		`data-search-clear-target="project_id"`,
		`data-search-input`,
		`hx-trigger="input changed delay:300ms"`,
		`hx-push-url="false"`,
		`hx-include="#new-issue-project-form"`,
		`id="new-issue-project-options"`,
		`data-search-options hidden role="listbox" aria-label="Project suggestions"`,
		`data-search-option`,
		`data-target-name="project_id"`,
		`data-target-value="` + e.projectID.String() + `"`,
		e.projKey + " - http-test",
		searchProject.Key + " - Search Target Project",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("global new issue page missing %q: %s", want, body)
		}
	}
	if strings.Contains(body, `@`+assignee.Username) {
		t.Fatalf("global new issue page should wait for project before member suggestions: %s", body)
	}

	body = e.uiGet(t, "/issues/new/projects?project="+url.QueryEscape("search target"), token)
	for _, want := range []string{
		`id="new-issue-project-options"`,
		`data-search-options role="listbox" aria-label="Project suggestions"`,
		`data-target-value="` + searchProject.ID.String() + `"`,
		searchProject.Key + " - Search Target Project",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("project search panel missing %q: %s", want, body)
		}
	}
	if strings.Contains(body, e.projKey+" - http-test") || strings.Contains(body, `data-target-value="`+e.projectID.String()+`"`) {
		t.Fatalf("project search did not filter unrelated project: %s", body)
	}
	body = e.uiGet(t, "/issues/new/projects?project="+url.QueryEscape("missing"), token)
	for _, want := range []string{
		`id="new-issue-project-options"`,
		`data-search-options role="listbox" aria-label="Project suggestions"`,
		`No projects found.`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("empty project search options missing %q: %s", want, body)
		}
	}

	body = e.uiGet(t, e.projectPath()+"/issues/new", token)
	for _, want := range []string{
		"New issue",
		"Create issue",
		`data-sidebar-view="project" data-sidebar-project-id="` + e.projectID.String() + `"`,
		`type="hidden" name="project_scoped" value="true"`,
		`method="post" action="/issues"`,
		`hx-post="/issues"`,
		`hx-push-url="false"`,
		`id="issue-project" name="project" value="` + e.projKey + ` - http-test"`,
		`type="hidden" name="project_id" value="` + e.projectID.String() + `"`,
		`id="issue-title"`,
		`id="issue-description"`,
		`role="listbox" aria-labelledby="issue-priority-label" data-priority-picker`,
		`type="radio" name="priority" value="P2" checked`,
		`aria-label="Priority P2"`,
		`data-checkbox-reveal`,
		`id="issue-due-date-toggle" type="checkbox" data-checkbox-reveal-toggle aria-controls="issue-due-date-field" aria-expanded="false"`,
		`id="issue-due-date-field" data-checkbox-reveal-panel hidden`,
		`id="issue-due-date" type="date" name="due_date" value="" disabled`,
		`name="assignee"`,
		`name="reporter"`,
		`@` + assignee.Username,
		`@` + reporter.Username,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("new issue page missing %q: %s", want, body)
		}
	}
	// Metadata fields share one left-aligned column, each labelled above its
	// control, with Assignee directly below Reporter and Due date last.
	if !strings.Contains(body, `data-new-issue-fields class="mt-4 space-y-4 sm:max-w-xs"`) || strings.Contains(body, "sm:grid-cols-3") {
		t.Fatalf("new issue metadata fields should stack in one column: %s", body)
	}
	for _, pair := range [][2]string{
		{`id="issue-description"`, `id="issue-priority-label">Priority</span>`},
		{`id="issue-priority-label">Priority</span>`, `for="issue-reporter">Reporter</label>`},
		{`for="issue-reporter">Reporter</label>`, `for="issue-assignee">Assignee</label>`},
		{`for="issue-assignee">Assignee</label>`, `id="issue-due-date-label">Due date</span>`},
		{`id="issue-due-date-label">Due date</span>`, `aria-describedby="issue-due-date-label"`},
		{`aria-describedby="issue-due-date-label"`, "<span>Set a due date</span>"},
	} {
		requireMarkupOrder(t, body, pair[0], pair[1])
	}
	if err := e.store.FavoriteProject(e.ctx, reporter.ID, e.projectID); err != nil {
		t.Fatalf("FavoriteProject: %v", err)
	}
	validationRes := e.uiDoNoRedirect(t, http.MethodPost, "/issues", token, strings.NewReader(url.Values{
		"project_id":     {e.projectID.String()},
		"project_scoped": {"true"},
	}.Encode()))
	defer validationRes.Body.Close()
	body = readBody(t, validationRes)
	if validationRes.StatusCode != http.StatusOK || !strings.Contains(body, "Title required, max 200 chars.") || !strings.Contains(body, `name="project_scoped" value="true"`) {
		t.Fatalf("project-scoped validation code = %d body = %s", validationRes.StatusCode, body)
	}
	requireActiveSidebarDestination(t, body, `data-sidebar-project-id="`+e.projectID.String()+`"`)

	body = e.uiGet(t, "/issues/new/panel?project_id="+url.QueryEscape(e.projectID.String())+"&title=Kept", token)
	for _, want := range []string{
		`id="issue-project" name="project" value="` + e.projKey + ` - http-test"`,
		`type="hidden" name="project_id" value="` + e.projectID.String() + `"`,
		`name="title" value="Kept"`,
		`@` + assignee.Username,
		`@` + reporter.Username,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("project-selected new issue panel missing %q: %s", want, body)
		}
	}

	form := url.Values{
		"project_id":  {e.projectID.String()},
		"title":       {"all fields issue"},
		"description": {"full body from ui"},
		"priority":    {string(model.PriorityP1)},
		"due_date":    {"2099-06-24"},
		"assignee":    {"@" + assignee.Username},
		"reporter":    {"@" + reporter.Username},
	}
	res := e.uiDoNoRedirectWithHeaders(t, http.MethodPost, "/issues", token, strings.NewReader(form.Encode()), map[string]string{"HX-Request": "true"})
	defer res.Body.Close()
	body = readBody(t, res)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("create issue code = %d body = %s", res.StatusCode, body)
	}

	issues, _, err := e.store.ListIssues(e.ctx, store.ListIssuesParams{ProjectID: e.projectID, Limit: 10})
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	var created model.Issue
	for _, issue := range issues {
		if issue.Title == "all fields issue" {
			created = issue
			break
		}
	}
	if created.ID == uuid.Nil {
		t.Fatalf("created issue not found: %+v", issues)
	}
	if push := res.Header.Get("HX-Push-Url"); push != e.issuePath(created) {
		t.Fatalf("HX-Push-Url = %q, want %q", push, e.issuePath(created))
	}
	if created.Description != "full body from ui" || created.Priority != model.PriorityP1 || created.DueDate == nil || created.DueDate.String() != "2099-06-24" {
		t.Fatalf("created fields = %+v", created)
	}
	if created.AssigneeID == nil || *created.AssigneeID != assignee.ID || created.ReporterID == nil || *created.ReporterID != reporter.ID {
		t.Fatalf("created people = assignee %v reporter %v", created.AssigneeID, created.ReporterID)
	}
	for _, want := range []string{"all fields issue", "full body from ui", assignee.Name, reporter.Name, "Jun 24"} {
		if !strings.Contains(body, want) {
			t.Fatalf("created issue panel missing %q: %s", want, body)
		}
	}

	defaultForm := url.Values{"project_id": {e.projectID.String()}, "title": {"default reporter issue"}}
	res = e.uiDoNoRedirect(t, http.MethodPost, "/issues", token, strings.NewReader(defaultForm.Encode()))
	defer res.Body.Close()
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("default create code = %d body = %s", res.StatusCode, readBody(t, res))
	}
	issues, _, err = e.store.ListIssues(e.ctx, store.ListIssuesParams{ProjectID: e.projectID, Limit: 10})
	if err != nil {
		t.Fatalf("ListIssues default: %v", err)
	}
	var defaulted model.Issue
	for _, issue := range issues {
		if issue.Title == "default reporter issue" {
			defaulted = issue
			break
		}
	}
	if defaulted.ID == uuid.Nil {
		t.Fatalf("default reporter issue not found: %+v", issues)
	}
	if loc := res.Header.Get("Location"); loc != e.issuePath(defaulted) {
		t.Fatalf("Location = %q, want %q", loc, e.issuePath(defaulted))
	}
	if defaulted.ReporterID == nil || *defaulted.ReporterID != reporter.ID || defaulted.Priority != model.PriorityP2 || defaulted.AssigneeID != nil || defaulted.DueDate != nil {
		t.Fatalf("defaulted issue = %+v", defaulted)
	}
}

func TestUINewIssueValidationPreservesFormValues(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	user, token := e.mustProjectMemberToken(t, "ui-new-issue-errors")
	other, _ := e.mustUserToken(t, "ui-new-issue-other")

	base := url.Values{
		"project_id":  {e.projectID.String()},
		"title":       {"Draft issue"},
		"description": {"Draft body"},
		"priority":    {string(model.PriorityP1)},
		"due_date":    {"2099-06-24"},
		"assignee":    {"@" + user.Username},
		"reporter":    {"@" + user.Username},
	}
	clone := func(in url.Values) url.Values {
		out := url.Values{}
		for key, values := range in {
			out[key] = append([]string(nil), values...)
		}
		return out
	}
	cases := []struct {
		name      string
		mutate    func(url.Values)
		wantError string
		want      []string
	}{
		{
			name:      "blank project",
			mutate:    func(v url.Values) { v.Del("project_id") },
			wantError: "Choose a project.",
			want:      []string{`id="issue-project" name="project"`, `type="hidden" name="project_id" value=""`},
		},
		{
			name:      "blank title",
			mutate:    func(v url.Values) { v.Set("title", "   ") },
			wantError: "Title required, max 200 chars.",
			want:      []string{`name="title" value="   "`},
		},
		{
			name:      "long title",
			mutate:    func(v url.Values) { v.Set("title", strings.Repeat("x", 201)) },
			wantError: "Title required, max 200 chars.",
		},
		{
			name:      "bad priority",
			mutate:    func(v url.Values) { v.Set("priority", "P5") },
			wantError: "Invalid priority.",
		},
		{
			name:      "bad due date",
			mutate:    func(v url.Values) { v.Set("due_date", "tomorrow") },
			wantError: "Use YYYY-MM-DD.",
			want:      []string{`name="due_date" value="tomorrow"`},
		},
		{
			name:      "unknown assignee",
			mutate:    func(v url.Values) { v.Set("assignee", "@missinguser") },
			wantError: "Choose a user.",
			want:      []string{`name="assignee" value="@missinguser"`},
		},
		{
			name:      "forbidden reporter",
			mutate:    func(v url.Values) { v.Set("reporter", "@"+other.Username) },
			wantError: "Reporter must be you.",
			want:      []string{`name="reporter" value="@` + other.Username + `"`},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			form := clone(base)
			tc.mutate(form)
			res := e.uiDoNoRedirect(t, http.MethodPost, "/issues", token, strings.NewReader(form.Encode()))
			defer res.Body.Close()
			body := readBody(t, res)
			if res.StatusCode != http.StatusOK {
				t.Fatalf("code = %d body = %s", res.StatusCode, body)
			}
			for _, want := range append([]string{
				"New issue",
				tc.wantError,
				"Draft body",
				`name="assignee"`,
				`name="reporter"`,
			}, tc.want...) {
				if !strings.Contains(body, want) {
					t.Fatalf("%s response missing %q: %s", tc.name, want, body)
				}
			}
		})
	}
	issues, _, err := e.store.ListIssues(e.ctx, store.ListIssuesParams{ProjectID: e.projectID, Limit: 10})
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	if len(issues) != 0 {
		t.Fatalf("validation created issues: %+v", issues)
	}
}
