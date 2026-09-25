package server_test

import (
	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/store"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestUIEditPriorityUpdatesIssuePanel(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	_, token := e.mustProjectMemberToken(t, "ui-priority")
	issue, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{
		ProjectID: e.projectID,
		Title:     "priority target issue",
		Priority:  model.PriorityP3,
	})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	edit := e.uiGet(t, e.issuePath(issue)+"/priority/edit", token)
	for _, want := range []string{
		"priority target issue",
		`role="listbox" aria-label="Issue priority"`,
		`method="post" action="` + e.issuePath(issue) + `/priority"`,
		`hx-post="` + e.issuePath(issue) + `/priority"`,
		`hx-push-url="false"`,
		`name="priority" value="P0"`,
		`name="priority" value="P1"`,
		`name="priority" value="P2"`,
		`name="priority" value="P3"`,
		`name="priority" value="P4"`,
		`hx-get="` + e.issuePath(issue) + `/panel"`,
		`aria-label="Priority P3"`,
		`bg-yellow-500`,
		`flex flex-wrap items-center gap-2`,
		`opacity-100`,
		`opacity-40 hover:opacity-80`,
		`data-lucide="corner-up-left"`,
		`href="` + e.projectPath() + `/all"`,
		`hx-get="` + e.projectPath() + `/all/panel"`,
	} {
		if !strings.Contains(edit, want) {
			t.Fatalf("priority edit response missing %q: %s", want, edit)
		}
	}
	if strings.Contains(edit, `title="Change priority"`) ||
		strings.Contains(edit, `title="Cancel priority change"`) ||
		strings.Contains(edit, `aria-label="Cancel priority change"`) ||
		strings.Contains(edit, `aria-expanded="true"`) ||
		strings.Contains(edit, `data-lucide="chevron-up"`) ||
		strings.Contains(edit, `opacity-100 ring-2 ring-indigo-500`) {
		t.Fatalf("priority edit response has native tooltip state: %s", edit)
	}

	form := url.Values{"priority": {string(model.PriorityP0)}}
	res := e.uiDoNoRedirect(t, http.MethodPost, e.issuePath(issue)+"/priority", token, strings.NewReader(form.Encode()))
	defer res.Body.Close()
	body := readBody(t, res)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("update priority code = %d body = %s", res.StatusCode, body)
	}
	if !strings.Contains(body, `aria-label="Priority P0"`) || strings.Contains(body, `role="listbox"`) {
		t.Fatalf("update priority response did not return read mode with new priority: %s", body)
	}
	updated, err := e.store.GetIssue(e.ctx, issue.ID)
	if err != nil {
		t.Fatalf("GetIssue after priority update: %v", err)
	}
	if updated.Priority != model.PriorityP0 {
		t.Fatalf("Priority = %q, want %q", updated.Priority, model.PriorityP0)
	}

	badPriority := url.Values{"priority": {"p0"}}
	res = e.uiDoNoRedirect(t, http.MethodPost, e.issuePath(issue)+"/priority", token, strings.NewReader(badPriority.Encode()))
	defer res.Body.Close()
	body = readBody(t, res)
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad priority code = %d body = %s", res.StatusCode, body)
	}
	if !strings.Contains(body, "invalid priority") {
		t.Fatalf("bad priority response missing validation error: %s", body)
	}
	updated, err = e.store.GetIssue(e.ctx, issue.ID)
	if err != nil {
		t.Fatalf("GetIssue after bad priority: %v", err)
	}
	if updated.Priority != model.PriorityP0 {
		t.Fatalf("bad priority changed Priority = %q, want %q", updated.Priority, model.PriorityP0)
	}

	res = e.uiDoNoRedirect(t, http.MethodPost, e.issuePath(issue)+"/priority", token, strings.NewReader("%zz"))
	defer res.Body.Close()
	body = readBody(t, res)
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad form priority code = %d body = %s", res.StatusCode, body)
	}
	if !strings.Contains(body, "unable to read form") {
		t.Fatalf("bad form priority response missing parse error: %s", body)
	}
	updated, err = e.store.GetIssue(e.ctx, issue.ID)
	if err != nil {
		t.Fatalf("GetIssue after bad form priority: %v", err)
	}
	if updated.Priority != model.PriorityP0 {
		t.Fatalf("bad form changed Priority = %q, want %q", updated.Priority, model.PriorityP0)
	}
}

func TestUIEditTitleUpdatesIssuePanel(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	_, token := e.mustProjectMemberToken(t, "ui-title")
	issue, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{
		ProjectID: e.projectID,
		Title:     "title target issue",
	})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	edit := e.uiGet(t, e.issuePath(issue)+"/title/edit", token)
	for _, want := range []string{
		"title target issue",
		`method="post" action="` + e.issuePath(issue) + `/title"`,
		`hx-post="` + e.issuePath(issue) + `/title"`,
		`hx-push-url="false"`,
		`name="title" value="title target issue"`,
		`aria-label="Issue title"`,
		`aria-label="Save title"`,
		`aria-label="Cancel editing title"`,
		`hx-get="` + e.issuePath(issue) + `/panel"`,
	} {
		if !strings.Contains(edit, want) {
			t.Fatalf("title edit response missing %q: %s", want, edit)
		}
	}
	if strings.Contains(edit, `<input disabled`) || strings.Contains(edit, `title="Save title"`) {
		t.Fatalf("title edit response has disabled/editor tooltip state: %s", edit)
	}

	form := url.Values{"title": {"  renamed title issue  "}}
	res := e.uiDoNoRedirectWithHeaders(t, http.MethodPost, e.issuePath(issue)+"/title", token, strings.NewReader(form.Encode()), map[string]string{
		"HX-Current-URL": e.ts.URL + e.issuePath(issue),
		"HX-Request":     "true",
	})
	defer res.Body.Close()
	body := readBody(t, res)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("update title code = %d body = %s", res.StatusCode, body)
	}
	if push := res.Header.Get("HX-Push-Url"); push != "" {
		t.Fatalf("title update HX-Push-Url = %q, want empty", push)
	}
	if replace := res.Header.Get("HX-Replace-Url"); replace != "" {
		t.Fatalf("title update HX-Replace-Url = %q, want empty", replace)
	}
	if !strings.Contains(body, "renamed title issue") || strings.Contains(body, "title target issue") || strings.Contains(body, `name="title"`) {
		t.Fatalf("update title response did not return read mode with new title: %s", body)
	}
	updated, err := e.store.GetIssue(e.ctx, issue.ID)
	if err != nil {
		t.Fatalf("GetIssue after update: %v", err)
	}
	if updated.Title != "renamed title issue" {
		t.Fatalf("Title = %q, want renamed title issue", updated.Title)
	}

	blank := url.Values{"title": {" \n\t "}}
	res = e.uiDoNoRedirect(t, http.MethodPost, e.issuePath(issue)+"/title", token, strings.NewReader(blank.Encode()))
	defer res.Body.Close()
	body = readBody(t, res)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("blank title code = %d body = %s", res.StatusCode, body)
	}
	if !strings.Contains(body, "Title required, max 200 chars.") || !strings.Contains(body, `name="title"`) {
		t.Fatalf("blank title response missing error/editor: %s", body)
	}
	updated, err = e.store.GetIssue(e.ctx, issue.ID)
	if err != nil {
		t.Fatalf("GetIssue after blank title: %v", err)
	}
	if updated.Title != "renamed title issue" {
		t.Fatalf("blank title changed Title = %q, want renamed title issue", updated.Title)
	}

	tooLong := url.Values{"title": {strings.Repeat("x", 201)}}
	res = e.uiDoNoRedirect(t, http.MethodPost, e.issuePath(issue)+"/title", token, strings.NewReader(tooLong.Encode()))
	defer res.Body.Close()
	body = readBody(t, res)
	if res.StatusCode != http.StatusOK || !strings.Contains(body, "Title required, max 200 chars.") {
		t.Fatalf("long title code = %d body = %s", res.StatusCode, body)
	}
	updated, err = e.store.GetIssue(e.ctx, issue.ID)
	if err != nil {
		t.Fatalf("GetIssue after long title: %v", err)
	}
	if updated.Title != "renamed title issue" {
		t.Fatalf("long title changed Title = %q, want renamed title issue", updated.Title)
	}

	res = e.uiDoNoRedirect(t, http.MethodPost, e.issuePath(issue)+"/title", token, strings.NewReader("%zz"))
	defer res.Body.Close()
	body = readBody(t, res)
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad form title code = %d body = %s", res.StatusCode, body)
	}
	if !strings.Contains(body, "unable to read form") {
		t.Fatalf("bad form title response missing parse error: %s", body)
	}
	updated, err = e.store.GetIssue(e.ctx, issue.ID)
	if err != nil {
		t.Fatalf("GetIssue after bad form: %v", err)
	}
	if updated.Title != "renamed title issue" {
		t.Fatalf("bad form changed Title = %q, want renamed title issue", updated.Title)
	}
}

func TestUIEditDescriptionUpdatesAndClearsIssuePanel(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	_, token := e.mustProjectMemberToken(t, "ui-description")
	issue, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{
		ProjectID:   e.projectID,
		Title:       "description target issue",
		Description: "old description",
	})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	edit := e.uiGet(t, e.issuePath(issue)+"/description/edit", token)
	for _, want := range []string{
		"description target issue",
		`method="post" action="` + e.issuePath(issue) + `/description"`,
		`hx-post="` + e.issuePath(issue) + `/description"`,
		`hx-push-url="false"`,
		`name="description"`,
		`placeholder="Describe the issue: context, steps to reproduce, acceptance criteria… Markdown is supported."`,
		`aria-label="Save description"`,
		`aria-label="Cancel editing description"`,
		`hx-get="` + e.issuePath(issue) + `/panel"`,
		"old description",
	} {
		if !strings.Contains(edit, want) {
			t.Fatalf("description edit response missing %q: %s", want, edit)
		}
	}
	if strings.Contains(edit, `<textarea disabled`) || strings.Contains(edit, `title="Save description"`) {
		t.Fatalf("description edit response has disabled/editor tooltip state: %s", edit)
	}

	form := url.Values{"description": {"new description"}}
	res := e.uiDoNoRedirectWithHeaders(t, http.MethodPost, e.issuePath(issue)+"/description", token, strings.NewReader(form.Encode()), map[string]string{
		"HX-Current-URL": e.ts.URL + e.issuePath(issue),
		"HX-Request":     "true",
	})
	defer res.Body.Close()
	body := readBody(t, res)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("update description code = %d body = %s", res.StatusCode, body)
	}
	if push := res.Header.Get("HX-Push-Url"); push != "" {
		t.Fatalf("description update HX-Push-Url = %q, want empty", push)
	}
	if replace := res.Header.Get("HX-Replace-Url"); replace != "" {
		t.Fatalf("description update HX-Replace-Url = %q, want empty", replace)
	}
	if !strings.Contains(body, "new description") || strings.Contains(body, "old description") || strings.Contains(body, `name="description"`) {
		t.Fatalf("update description response did not return read mode with new body: %s", body)
	}
	updated, err := e.store.GetIssue(e.ctx, issue.ID)
	if err != nil {
		t.Fatalf("GetIssue after update: %v", err)
	}
	if updated.Description != "new description" {
		t.Fatalf("Description = %q, want new description", updated.Description)
	}

	res = e.uiDoNoRedirect(t, http.MethodPost, e.issuePath(issue)+"/description", token, strings.NewReader("%zz"))
	defer res.Body.Close()
	body = readBody(t, res)
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad form description code = %d body = %s", res.StatusCode, body)
	}
	if !strings.Contains(body, "unable to read form") {
		t.Fatalf("bad form description response missing parse error: %s", body)
	}
	updated, err = e.store.GetIssue(e.ctx, issue.ID)
	if err != nil {
		t.Fatalf("GetIssue after bad form: %v", err)
	}
	if updated.Description != "new description" {
		t.Fatalf("bad form changed Description = %q, want new description", updated.Description)
	}

	blank := url.Values{"description": {" \n\t "}}
	res = e.uiDoNoRedirect(t, http.MethodPost, e.issuePath(issue)+"/description", token, strings.NewReader(blank.Encode()))
	defer res.Body.Close()
	body = readBody(t, res)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("blank description code = %d body = %s", res.StatusCode, body)
	}
	if !strings.Contains(body, "No description.") || strings.Contains(body, "new description") {
		t.Fatalf("blank description response missing empty state: %s", body)
	}
	updated, err = e.store.GetIssue(e.ctx, issue.ID)
	if err != nil {
		t.Fatalf("GetIssue after blank update: %v", err)
	}
	if updated.Description != "" {
		t.Fatalf("blank Description = %q, want empty string", updated.Description)
	}
}

func TestUIEditIssuePeopleUpdatesAndClearsIssuePanel(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	user, token := e.mustProjectMemberToken(t, "ui-people")
	target, err := e.store.CreateUser(e.ctx, "ui-person-"+uniqueProjectKey(t)+"@example.com", "UI Person")
	if err != nil {
		t.Fatalf("CreateUser target: %v", err)
	}
	nonMember, err := e.store.CreateUser(e.ctx, "ui-person-outsider-"+uniqueProjectKey(t)+"@example.com", "UI Person Outsider")
	if err != nil {
		t.Fatalf("CreateUser nonmember: %v", err)
	}
	if _, err := e.store.GrantProjectAccess(e.ctx, e.projectID, target.ID); err != nil {
		t.Fatalf("GrantProjectAccess target: %v", err)
	}
	issue, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{
		ProjectID:  e.projectID,
		Title:      "people target issue",
		ReporterID: &user.ID,
	})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	editAssignee := e.uiGet(t, e.issuePath(issue)+"/assignee/edit", token)
	for _, want := range []string{
		`method="post" action="` + e.issuePath(issue) + `/assignee"`,
		`hx-post="` + e.issuePath(issue) + `/assignee"`,
		`hx-push-url="false"`,
		`name="assignee"`,
		`data-search`,
		`data-search-input`,
		`role="listbox" aria-label="Assignee suggestions"`,
		`data-search-option`,
		`aria-label="Save assignee"`,
		`aria-label="Cancel editing assignee"`,
		`hx-get="` + e.issuePath(issue) + `/panel"`,
		`data-value="@` + target.Username + `"`,
		`data-search-text="@` + target.Username,
		target.Name,
	} {
		if !strings.Contains(editAssignee, want) {
			t.Fatalf("assignee edit missing %q: %s", want, editAssignee)
		}
	}
	if strings.Contains(editAssignee, `aria-label="Edit assignee" class="grid h-7 w-7 shrink-0 cursor-not-allowed`) || strings.Contains(editAssignee, `title="Save assignee"`) {
		t.Fatalf("assignee edit has disabled or tooltip state: %s", editAssignee)
	}

	form := url.Values{"assignee": {"@" + target.Username}}
	res := e.uiDoNoRedirect(t, http.MethodPost, e.issuePath(issue)+"/assignee", token, strings.NewReader(form.Encode()))
	defer res.Body.Close()
	body := readBody(t, res)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("update assignee code = %d body = %s", res.StatusCode, body)
	}
	if !strings.Contains(body, target.Name) || strings.Contains(body, `name="assignee"`) {
		t.Fatalf("assignee update did not return read mode with target: %s", body)
	}
	updated, err := e.store.GetIssue(e.ctx, issue.ID)
	if err != nil {
		t.Fatalf("GetIssue after assignee update: %v", err)
	}
	if updated.AssigneeID == nil || *updated.AssigneeID != target.ID {
		t.Fatalf("AssigneeID = %v, want %s", updated.AssigneeID, target.ID)
	}

	res = e.uiDoNoRedirect(t, http.MethodPost, e.issuePath(issue)+"/assignee", token, strings.NewReader(url.Values{"assignee": {" "}}.Encode()))
	defer res.Body.Close()
	body = readBody(t, res)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("clear assignee code = %d body = %s", res.StatusCode, body)
	}
	if !strings.Contains(body, "Unassigned") || strings.Contains(body, target.Name) {
		t.Fatalf("clear assignee response missing empty state: %s", body)
	}
	updated, err = e.store.GetIssue(e.ctx, issue.ID)
	if err != nil {
		t.Fatalf("GetIssue after assignee clear: %v", err)
	}
	if updated.AssigneeID != nil {
		t.Fatalf("AssigneeID after clear = %v, want nil", updated.AssigneeID)
	}

	res = e.uiDoNoRedirect(t, http.MethodPost, e.issuePath(issue)+"/assignee", token, strings.NewReader(url.Values{"assignee": {"@" + nonMember.Username}}.Encode()))
	defer res.Body.Close()
	body = readBody(t, res)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("invalid assignee code = %d body = %s", res.StatusCode, body)
	}
	if !strings.Contains(body, "Choose a project member.") || !strings.Contains(body, `name="assignee"`) {
		t.Fatalf("invalid assignee did not rerender edit mode: %s", body)
	}

	res = e.uiDoNoRedirect(t, http.MethodPost, e.issuePath(issue)+"/assignee", token, strings.NewReader("%zz"))
	defer res.Body.Close()
	body = readBody(t, res)
	if res.StatusCode != http.StatusBadRequest || !strings.Contains(body, "unable to read form") {
		t.Fatalf("bad assignee form code = %d body = %s", res.StatusCode, body)
	}

	editReporter := e.uiGet(t, e.issuePath(issue)+"/reporter/edit", token)
	for _, want := range []string{
		`method="post" action="` + e.issuePath(issue) + `/reporter"`,
		`hx-post="` + e.issuePath(issue) + `/reporter"`,
		`hx-push-url="false"`,
		`name="reporter"`,
		`value="@` + user.Username + `"`,
		`value="@` + target.Username + `"`,
		`aria-label="Save reporter"`,
		`aria-label="Cancel editing reporter"`,
	} {
		if !strings.Contains(editReporter, want) {
			t.Fatalf("reporter edit missing %q: %s", want, editReporter)
		}
	}

	res = e.uiDoNoRedirect(t, http.MethodPost, e.issuePath(issue)+"/reporter", token, strings.NewReader(url.Values{"reporter": {"@" + target.Username}}.Encode()))
	defer res.Body.Close()
	body = readBody(t, res)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("update reporter code = %d body = %s", res.StatusCode, body)
	}
	if !strings.Contains(body, target.Name) || strings.Contains(body, `name="reporter"`) {
		t.Fatalf("reporter update did not return read mode with target: %s", body)
	}
	updated, err = e.store.GetIssue(e.ctx, issue.ID)
	if err != nil {
		t.Fatalf("GetIssue after reporter update: %v", err)
	}
	if updated.ReporterID == nil || *updated.ReporterID != target.ID {
		t.Fatalf("ReporterID = %v, want %s", updated.ReporterID, target.ID)
	}

	res = e.uiDoNoRedirect(t, http.MethodPost, e.issuePath(issue)+"/reporter", token, strings.NewReader(url.Values{"reporter": {""}}.Encode()))
	defer res.Body.Close()
	body = readBody(t, res)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("clear reporter code = %d body = %s", res.StatusCode, body)
	}
	if !strings.Contains(body, "No reporter") || strings.Contains(body, target.Name) {
		t.Fatalf("clear reporter response missing empty state: %s", body)
	}
	updated, err = e.store.GetIssue(e.ctx, issue.ID)
	if err != nil {
		t.Fatalf("GetIssue after reporter clear: %v", err)
	}
	if updated.ReporterID != nil {
		t.Fatalf("ReporterID after clear = %v, want nil", updated.ReporterID)
	}

	res = e.uiDoNoRedirect(t, http.MethodPost, e.issuePath(issue)+"/reporter", token, strings.NewReader(url.Values{"reporter": {"@" + nonMember.Username}}.Encode()))
	defer res.Body.Close()
	body = readBody(t, res)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("invalid reporter code = %d body = %s", res.StatusCode, body)
	}
	if !strings.Contains(body, "Choose a project member.") || !strings.Contains(body, `name="reporter"`) {
		t.Fatalf("invalid reporter did not rerender edit mode: %s", body)
	}

	res = e.uiDoNoRedirect(t, http.MethodPost, e.issuePath(issue)+"/reporter", token, strings.NewReader("%zz"))
	defer res.Body.Close()
	body = readBody(t, res)
	if res.StatusCode != http.StatusBadRequest || !strings.Contains(body, "unable to read form") {
		t.Fatalf("bad reporter form code = %d body = %s", res.StatusCode, body)
	}
}

func TestUIEditIssueSprintUpdatesClearsAndValidates(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	_, token := e.mustProjectMemberToken(t, "ui-sprint-edit")
	past, err := e.store.CreateSprint(e.ctx, store.CreateSprintParams{
		ProjectID: e.projectID,
		Name:      "Past Sprint",
		StartDate: datePtr(time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)),
		EndDate:   datePtr(time.Date(2026, 5, 14, 0, 0, 0, 0, time.UTC)),
	})
	if err != nil {
		t.Fatalf("CreateSprint past: %v", err)
	}
	activeStatus := model.SprintStatusActive
	if _, err := e.store.UpdateSprint(e.ctx, past.ID, store.UpdateSprintParams{Status: &activeStatus}); err != nil {
		t.Fatalf("activate past: %v", err)
	}
	if _, err := e.store.CompleteSprint(e.ctx, past.ID); err != nil {
		t.Fatalf("complete past: %v", err)
	}
	active, err := e.store.CreateSprint(e.ctx, store.CreateSprintParams{
		ProjectID: e.projectID,
		Name:      "Active Sprint",
		StartDate: datePtr(time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)),
		EndDate:   datePtr(time.Date(2026, 6, 14, 0, 0, 0, 0, time.UTC)),
	})
	if err != nil {
		t.Fatalf("CreateSprint active: %v", err)
	}
	if _, err := e.store.UpdateSprint(e.ctx, active.ID, store.UpdateSprintParams{Status: &activeStatus}); err != nil {
		t.Fatalf("activate current: %v", err)
	}
	planned, err := e.store.CreateSprint(e.ctx, store.CreateSprintParams{
		ProjectID: e.projectID,
		Name:      "Future Sprint",
		StartDate: datePtr(time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)),
		EndDate:   datePtr(time.Date(2026, 6, 28, 0, 0, 0, 0, time.UTC)),
	})
	if err != nil {
		t.Fatalf("CreateSprint planned: %v", err)
	}
	issue, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{
		ProjectID: e.projectID,
		Title:     "sprint target issue",
	})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	edit := e.uiGet(t, e.issuePath(issue)+"/sprint/edit", token)
	for _, want := range []string{
		`method="post" action="` + e.issuePath(issue) + `/sprint"`,
		`hx-post="` + e.issuePath(issue) + `/sprint"`,
		`hx-push-url="false"`,
		`name="sprint"`,
		`data-search`,
		`data-search-input`,
		`role="listbox" aria-label="Sprint suggestions"`,
		`data-search-option`,
		`aria-label="Save sprint"`,
		`aria-label="Cancel editing sprint"`,
		`data-value="` + active.Ref + `"`,
		`data-search-text="` + active.Ref,
		"Active Sprint",
		`data-value="` + planned.Ref + `"`,
		"Future Sprint",
	} {
		if !strings.Contains(edit, want) {
			t.Fatalf("sprint edit missing %q: %s", want, edit)
		}
	}
	activeIndex := strings.Index(edit, `value="`+active.Ref+`"`)
	plannedIndex := strings.Index(edit, `value="`+planned.Ref+`"`)
	if activeIndex < 0 || plannedIndex < 0 || activeIndex > plannedIndex {
		t.Fatalf("active option should render before planned option: %s", edit)
	}
	if strings.Contains(edit, "Past Sprint") || strings.Contains(edit, past.Ref) {
		t.Fatalf("sprint edit included completed sprint: %s", edit)
	}

	res := e.uiDoNoRedirect(t, http.MethodPost, e.issuePath(issue)+"/sprint", token, strings.NewReader(url.Values{"sprint": {planned.Ref}}.Encode()))
	defer res.Body.Close()
	body := readBody(t, res)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("set sprint code = %d body = %s", res.StatusCode, body)
	}
	if !strings.Contains(body, "Future Sprint") || strings.Contains(body, `name="sprint"`) {
		t.Fatalf("set sprint did not return read mode with planned sprint: %s", body)
	}
	updated, err := e.store.GetIssue(e.ctx, issue.ID)
	if err != nil {
		t.Fatalf("GetIssue after set: %v", err)
	}
	if updated.SprintID == nil || *updated.SprintID != planned.ID {
		t.Fatalf("SprintID = %v, want %s", updated.SprintID, planned.ID)
	}

	res = e.uiDoNoRedirect(t, http.MethodPost, e.issuePath(issue)+"/sprint", token, strings.NewReader(url.Values{"sprint": {" "}}.Encode()))
	defer res.Body.Close()
	body = readBody(t, res)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("clear sprint code = %d body = %s", res.StatusCode, body)
	}
	if !strings.Contains(body, `class="min-w-0 truncate text-slate-900 dark:text-slate-100">None</span>`) {
		t.Fatalf("clear sprint did not show none: %s", body)
	}
	updated, err = e.store.GetIssue(e.ctx, issue.ID)
	if err != nil {
		t.Fatalf("GetIssue after clear: %v", err)
	}
	if updated.SprintID != nil {
		t.Fatalf("SprintID = %v, want nil", updated.SprintID)
	}

	res = e.uiDoNoRedirect(t, http.MethodPost, e.issuePath(issue)+"/sprint", token, strings.NewReader(url.Values{"sprint": {"not-a-sprint"}}.Encode()))
	defer res.Body.Close()
	body = readBody(t, res)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("invalid sprint code = %d body = %s", res.StatusCode, body)
	}
	if !strings.Contains(body, "Choose an active or planned sprint.") || !strings.Contains(body, `value="not-a-sprint"`) {
		t.Fatalf("invalid sprint response missing inline error/input: %s", body)
	}
}

func TestUIIssueSprintDoneReadOnlyAndPostRejected(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	_, token := e.mustProjectMemberToken(t, "ui-sprint-done")
	current, err := e.store.CreateSprint(e.ctx, store.CreateSprintParams{
		ProjectID: e.projectID,
		Name:      "Current Sprint",
		StartDate: datePtr(time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)),
		EndDate:   datePtr(time.Date(2026, 6, 14, 0, 0, 0, 0, time.UTC)),
	})
	if err != nil {
		t.Fatalf("CreateSprint current: %v", err)
	}
	next, err := e.store.CreateSprint(e.ctx, store.CreateSprintParams{
		ProjectID: e.projectID,
		Name:      "Next Sprint",
		StartDate: datePtr(time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)),
		EndDate:   datePtr(time.Date(2026, 6, 28, 0, 0, 0, 0, time.UTC)),
	})
	if err != nil {
		t.Fatalf("CreateSprint next: %v", err)
	}
	issue, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{
		ProjectID: e.projectID,
		Title:     "done sprint issue",
	})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	done := model.StatusDone
	if _, err := e.store.UpdateIssue(e.ctx, issue.ID, store.UpdateIssueParams{SprintID: &current.ID}); err != nil {
		t.Fatalf("assign current: %v", err)
	}
	if _, err := e.store.UpdateIssue(e.ctx, issue.ID, store.UpdateIssueParams{Status: &done}); err != nil {
		t.Fatalf("mark done: %v", err)
	}

	body := e.uiGet(t, e.issuePath(issue), token)
	for _, want := range []string{
		"Current Sprint",
		`aria-label="Edit sprint"`,
		"disabled",
		"cursor-not-allowed",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("done detail missing %q: %s", want, body)
		}
	}
	if strings.Contains(body, `/sprint/edit`) || strings.Contains(body, `name="sprint"`) {
		t.Fatalf("done detail included editable sprint controls: %s", body)
	}

	res := e.uiDoNoRedirect(t, http.MethodPost, e.issuePath(issue)+"/sprint", token, strings.NewReader(url.Values{"sprint": {next.Ref}}.Encode()))
	defer res.Body.Close()
	body = readBody(t, res)
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("done sprint post code = %d body = %s", res.StatusCode, body)
	}
	updated, err := e.store.GetIssue(e.ctx, issue.ID)
	if err != nil {
		t.Fatalf("GetIssue after rejected post: %v", err)
	}
	if updated.SprintID == nil || *updated.SprintID != current.ID {
		t.Fatalf("SprintID = %v, want %s", updated.SprintID, current.ID)
	}

	closedIssue, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{
		ProjectID: e.projectID,
		Title:     "closed sprint issue",
	})
	if err != nil {
		t.Fatalf("CreateIssue closed: %v", err)
	}
	closed := model.StatusClosed
	closedReason := model.CloseReasonWontDo
	if _, err := e.store.UpdateIssue(e.ctx, closedIssue.ID, store.UpdateIssueParams{SprintID: &current.ID}); err != nil {
		t.Fatalf("assign closed current: %v", err)
	}
	if _, err := e.store.UpdateIssue(e.ctx, closedIssue.ID, store.UpdateIssueParams{Status: &closed, CloseReason: &closedReason}); err != nil {
		t.Fatalf("mark closed: %v", err)
	}
	body = e.uiGet(t, e.issuePath(closedIssue), token)
	if !strings.Contains(body, "Current Sprint") || !strings.Contains(body, "cursor-not-allowed") || strings.Contains(body, `/sprint/edit`) {
		t.Fatalf("closed detail did not render read-only sprint: %s", body)
	}
	res = e.uiDoNoRedirect(t, http.MethodPost, e.issuePath(closedIssue)+"/sprint", token, strings.NewReader(url.Values{"sprint": {next.Ref}}.Encode()))
	defer res.Body.Close()
	body = readBody(t, res)
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("closed sprint post code = %d body = %s", res.StatusCode, body)
	}
	updated, err = e.store.GetIssue(e.ctx, closedIssue.ID)
	if err != nil {
		t.Fatalf("GetIssue closed after rejected post: %v", err)
	}
	if updated.SprintID == nil || *updated.SprintID != current.ID {
		t.Fatalf("closed SprintID = %v, want %s", updated.SprintID, current.ID)
	}
}
