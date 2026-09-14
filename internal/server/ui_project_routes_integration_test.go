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

func TestUIIssueListsLinkToIssueDetail(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	user, token := e.mustProjectMemberToken(t, "ui-issue-links")
	dueDate, err := model.ParseDate("2099-06-24")
	if err != nil {
		t.Fatalf("ParseDate: %v", err)
	}
	issue, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{
		ProjectID:  e.projectID,
		Title:      "linked from lists",
		AssigneeID: &user.ID,
		DueDate:    &dueDate,
	})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	sp, err := e.store.CreateSprint(e.ctx, store.CreateSprintParams{
		ProjectID: e.projectID,
		Name:      "Linked Active Sprint",
		StartDate: datePtr(time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)),
		EndDate:   datePtr(time.Date(2026, 6, 14, 0, 0, 0, 0, time.UTC)),
	})
	if err != nil {
		t.Fatalf("CreateSprint: %v", err)
	}
	active := model.SprintStatusActive
	if _, err := e.store.UpdateSprint(e.ctx, sp.ID, store.UpdateSprintParams{Status: &active}); err != nil {
		t.Fatalf("UpdateSprint active: %v", err)
	}
	if _, err := e.store.UpdateIssue(e.ctx, issue.ID, store.UpdateIssueParams{SprintID: &sp.ID}); err != nil {
		t.Fatalf("assign sprint: %v", err)
	}
	wantHref := `href="` + e.issuePath(issue) + `"`
	wantHXGet := `hx-get="` + e.issuePath(issue) + `/panel"`
	wantHXPush := `hx-push-url="` + e.issuePath(issue) + `"`

	for _, path := range []string{e.projectPath() + "/all", "/me", "/me/all"} {
		body := e.uiGet(t, path, token)
		for _, want := range []string{wantHref, wantHXGet, wantHXPush} {
			if !strings.Contains(body, want) {
				t.Fatalf("%s missing issue link %q: %s", path, want, body)
			}
		}
		if strings.Contains(body, `data-main-view`) {
			t.Fatalf("%s contains legacy sidebar state marker: %s", path, body)
		}
		if !strings.Contains(body, "Jun 24") {
			t.Fatalf("%s missing due date badge: %s", path, body)
		}
	}
}

func TestUIProjectRoutesRedirectAndRejectOldGlobals(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	_, token := e.mustProjectMemberToken(t, "ui-routes")

	res := e.uiDoNoRedirect(t, http.MethodGet, e.projectPath(), token, nil)
	defer res.Body.Close()
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("project root code = %d body = %s", res.StatusCode, readBody(t, res))
	}
	if loc := res.Header.Get("Location"); loc != e.projectPath()+"/sprint" {
		t.Fatalf("project root Location = %q", loc)
	}

	res = e.uiDoNoRedirect(t, http.MethodGet, "/projects/"+e.projectID.String(), token, nil)
	defer res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("old project UUID route code = %d body = %s", res.StatusCode, readBody(t, res))
	}

	for _, path := range []string{"/sprint", "/sprint/panel", "/backlog", "/backlog/panel"} {
		res := e.uiDoNoRedirect(t, http.MethodGet, path, token, nil)
		defer res.Body.Close()
		if res.StatusCode != http.StatusNotFound {
			t.Fatalf("%s code = %d body = %s", path, res.StatusCode, readBody(t, res))
		}
	}
}

func TestUIProjectChildRoutesRequireAccess(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	_, token := e.mustUserToken(t, "ui-no-project")
	sp, err := e.store.CreateSprint(e.ctx, store.CreateSprintParams{
		ProjectID: e.projectID,
		Name:      "Denied Sprint",
		StartDate: datePtr(time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)),
		EndDate:   datePtr(time.Date(2026, 6, 14, 0, 0, 0, 0, time.UTC)),
	})
	if err != nil {
		t.Fatalf("CreateSprint: %v", err)
	}
	issue, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{ProjectID: e.projectID, Title: "denied sprint issue"})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	for _, path := range []string{
		e.projectPath() + "/about",
		e.projectPath() + "/about/panel",
		e.projectPath() + "/name/edit?view=about",
		e.projectPath() + "/description/edit",
		e.projectPath() + "/sprint",
		e.projectPath() + "/sprint/panel",
		e.projectPath() + "/planned",
		e.projectPath() + "/planned/panel",
		e.projectPath() + "/sprints",
		e.projectPath() + "/sprints/panel",
		e.projectPath() + "/sprints/page",
		e.projectPath() + "/sprints/new",
		e.projectPath() + "/sprints/" + sp.Ref + "/history/issues",
		e.projectPath() + "/sprints/" + sp.Ref + "/edit",
		e.projectPath() + "/sprints/" + sp.Ref + "/issues/new",
		e.projectPath() + "/all",
		e.projectPath() + "/all/panel",
		e.projectPath() + "/all/page",
		e.projectPath() + "/backlog",
		e.projectPath() + "/backlog/panel",
		e.projectPath() + "/issues/new",
		e.projectPath() + "/issues/new/panel",
		e.projectPath() + "/deleted",
		e.projectPath() + "/deleted/panel",
		e.projectPath() + "/delete",
	} {
		res := e.uiDoNoRedirect(t, http.MethodGet, path, token, nil)
		defer res.Body.Close()
		if res.StatusCode != http.StatusForbidden {
			t.Fatalf("%s code = %d body = %s", path, res.StatusCode, readBody(t, res))
		}
	}
	res := e.uiDoNoRedirect(t, http.MethodPost, e.projectPath()+"/issues", token, strings.NewReader(url.Values{"title": {"denied"}}.Encode()))
	defer res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("create issue denied code = %d body = %s", res.StatusCode, readBody(t, res))
	}
	for _, item := range []struct {
		path string
		form url.Values
	}{
		{path: e.projectPath() + "/name", form: url.Values{"view": {"about"}, "name": {"Denied"}}},
		{path: e.projectPath() + "/description", form: url.Values{"description": {"denied"}}},
		{path: e.projectPath() + "/favorite", form: url.Values{"view": {"about"}}},
		{path: e.projectPath() + "/delete", form: url.Values{"key": {e.projKey}}},
		{path: e.projectPath() + "/sprints", form: url.Values{"name": {"Denied"}, "start_date": {"2026-06-01"}, "end_date": {"2026-06-14"}}},
		{path: e.projectPath() + "/sprints/" + sp.Ref, form: url.Values{"name": {"Denied"}, "start_date": {"2026-06-01"}, "end_date": {"2026-06-14"}}},
		{path: e.projectPath() + "/sprints/" + sp.Ref + "/activate", form: url.Values{}},
		{path: e.projectPath() + "/sprints/" + sp.Ref + "/complete", form: url.Values{}},
		{path: e.projectPath() + "/sprints/" + sp.Ref + "/delete", form: url.Values{}},
		{path: e.projectPath() + "/sprints/" + sp.Ref + "/move-up", form: url.Values{}},
		{path: e.projectPath() + "/sprints/" + sp.Ref + "/move-down", form: url.Values{}},
		{path: e.projectPath() + "/sprints/" + sp.Ref + "/issues", form: url.Values{"issue": {issue.Identifier}}},
	} {
		res := e.uiDoNoRedirect(t, http.MethodPost, item.path, token, strings.NewReader(item.form.Encode()))
		defer res.Body.Close()
		if res.StatusCode != http.StatusForbidden {
			t.Fatalf("%s denied code = %d body = %s", item.path, res.StatusCode, readBody(t, res))
		}
	}
	res = e.uiDoNoRedirect(t, http.MethodPost, "/issues", token, strings.NewReader(url.Values{"project_id": {e.projectID.String()}, "title": {"denied"}}.Encode()))
	defer res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("global create issue denied code = %d body = %s", res.StatusCode, readBody(t, res))
	}

	res = e.uiDoNoRedirect(t, http.MethodGet, e.projectPath()+"/issues/new", "", nil)
	defer res.Body.Close()
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("unauth new issue code = %d body = %s", res.StatusCode, readBody(t, res))
	}
	if loc := res.Header.Get("Location"); loc != "/login?next="+url.QueryEscape(e.projectPath()+"/issues/new") {
		t.Fatalf("new issue Location = %q", loc)
	}
	res = e.uiDoNoRedirect(t, http.MethodGet, "/issues/new", "", nil)
	defer res.Body.Close()
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("unauth global new issue code = %d body = %s", res.StatusCode, readBody(t, res))
	}
	if loc := res.Header.Get("Location"); loc != "/login?next="+url.QueryEscape("/issues/new") {
		t.Fatalf("global new issue Location = %q", loc)
	}
}

func TestUIProjectSprintHistory(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	user, token := e.mustProjectMemberToken(t, "ui-sprint-history")
	olderAt := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	newestAt := olderAt.Add(48 * time.Hour)
	older := createCompletedSprintAtFor(t, e, e.projectID, "Older completed sprint", sprintTestDate(2026, 8, 1), sprintTestDate(2026, 8, 14), &olderAt)
	newest := createCompletedSprintAtFor(t, e, e.projectID, "Newest completed sprint", sprintTestDate(2026, 6, 1), sprintTestDate(2026, 6, 14), &newestAt)
	planned, err := e.store.CreateSprint(e.ctx, store.CreateSprintParams{ProjectID: e.projectID, Name: "Still planned"})
	if err != nil {
		t.Fatalf("CreateSprint planned: %v", err)
	}
	active, err := e.store.CreateSprint(e.ctx, store.CreateSprintParams{ProjectID: e.projectID, Name: "Still active"})
	if err != nil {
		t.Fatalf("CreateSprint active: %v", err)
	}
	activeStatus := model.SprintStatusActive
	if _, err := e.store.UpdateSprint(e.ctx, active.ID, store.UpdateSprintParams{Status: &activeStatus}); err != nil {
		t.Fatalf("activate sprint: %v", err)
	}

	otherProject, err := e.store.CreateProject(e.ctx, uniqueProjectKey(t), "Other sprint history project", "")
	if err != nil {
		t.Fatalf("CreateProject other: %v", err)
	}
	if _, err := e.store.GrantProjectAccess(e.ctx, otherProject.ID, user.ID); err != nil {
		t.Fatalf("GrantProjectAccess other: %v", err)
	}
	otherAt := newestAt.Add(24 * time.Hour)
	other := createCompletedSprintAtFor(t, e, otherProject.ID, "Other project completed sprint", sprintTestDate(2026, 5, 1), sprintTestDate(2026, 5, 14), &otherAt)

	body := e.uiGet(t, e.projectPath()+"/sprints", token)
	for _, want := range []string{
		"Sprint history",
		`data-lucide="archive"`,
		`href="` + e.projectPath() + `/sprints"`,
		`hx-get="` + e.projectPath() + `/sprints/panel"`,
		`aria-current="page"`,
		newest.Name,
		older.Name,
		`data-sprint-ref`,
		`>` + newest.Ref + `</span>`,
		`>` + older.Ref + `</span>`,
		"Jun 1-Jun 14, 2026",
		`Completed <time datetime="2026-07-03T10:00:00Z" data-local-time title="Jul 3, 2026 10:00 UTC">Jul 3, 2026 10:00 UTC</time>`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("sprint history missing %q: %s", want, body)
		}
	}
	if got := strings.Count(body, `data-sprint-ref`); got != 2 {
		t.Fatalf("historical sprint ref badges = %d, want 2: %s", got, body)
	}
	requireMarkupOrder(t, body, newest.Name, older.Name)
	requireMarkupOrder(t, body, `href="`+e.projectPath()+`/sprints"`, `href="`+e.projectPath()+`/changelog"`)
	for _, notWant := range []string{planned.Name, active.Name, other.Name} {
		if strings.Contains(body, notWant) {
			t.Fatalf("sprint history included %q: %s", notWant, body)
		}
	}

	panelBody := e.uiGet(t, e.projectPath()+"/sprints/panel", token)
	if !strings.Contains(panelBody, newest.Name) || strings.Contains(panelBody, "<!doctype html>") {
		t.Fatalf("sprint history panel response = %s", panelBody)
	}
	emptyIssuesBody := e.uiGet(t, e.projectPath()+"/sprints/"+newest.Ref+"/history/issues", token)
	if !strings.Contains(emptyIssuesBody, "No issues were included in this sprint.") {
		t.Fatalf("empty sprint history issues response = %s", emptyIssuesBody)
	}

	res := e.uiDoNoRedirect(t, http.MethodGet, e.projectPath()+"/sprints/page?cursor=bad", token, nil)
	defer res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad cursor code = %d body = %s", res.StatusCode, readBody(t, res))
	}

	res = e.uiDoNoRedirect(t, http.MethodGet, "/bad!/projects/INVALID/sprints/page", token, nil)
	defer res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad project route code = %d body = %s", res.StatusCode, readBody(t, res))
	}
}

func TestUIProjectSprintHistoryPagination(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	_, token := e.mustProjectMemberToken(t, "ui-sprint-history-pages")
	if _, err := e.pool.Exec(e.ctx, `
		INSERT INTO sprints (project_id, number, name, status, start_date, end_date, completed_at)
		SELECT $1, 1000 + n, 'History page ' || lpad(n::text, 2, '0'), 'completed',
		       DATE '2026-01-01', DATE '2026-01-14', $2::timestamptz - (n * INTERVAL '1 hour')
		FROM generate_series(1, 51) AS n
	`, e.projectID, time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("insert sprint history pages: %v", err)
	}

	body := e.uiGet(t, e.projectPath()+"/sprints", token)
	if !strings.Contains(body, "History page 01") || !strings.Contains(body, "History page 50") || strings.Contains(body, "History page 51") {
		t.Fatalf("sprint history first page = %s", body)
	}
	historyCard := `<section class="overflow-hidden rounded-lg border border-slate-200 bg-white dark:border-slate-800 dark:bg-slate-900">`
	if got := strings.Count(body, historyCard); got != 50 {
		t.Fatalf("sprint history first-page card count = %d, want 50: %s", got, body)
	}
	if !strings.Contains(body, `hx-target="#project-sprint-history-more"`) || !strings.Contains(body, `hx-swap="outerHTML"`) {
		t.Fatalf("sprint history load-more replacement target missing: %s", body)
	}
	marker := `hx-get="` + e.projectPath() + `/sprints/page?cursor=`
	start := strings.Index(body, marker)
	if start < 0 {
		t.Fatalf("sprint history load-more URL missing: %s", body)
	}
	rest := body[start+len(marker):]
	end := strings.Index(rest, `"`)
	if end < 0 {
		t.Fatalf("sprint history load-more cursor malformed: %s", body)
	}
	pageBody := e.uiGet(t, e.projectPath()+"/sprints/page?cursor="+rest[:end], token)
	if !strings.Contains(pageBody, "History page 51") || strings.Contains(pageBody, "project-sprint-history-more") || strings.Count(pageBody, historyCard) != 1 {
		t.Fatalf("sprint history second page = %s", pageBody)
	}
}

func TestUIProjectSprintHistoryExpandsSnapshotIssues(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	_, token := e.mustProjectMemberToken(t, "ui-sprint-history-issues")
	sprint, err := e.store.CreateSprint(e.ctx, store.CreateSprintParams{
		ProjectID: e.projectID,
		Name:      "Completed with captured work",
		Goal:      "Original **sprint direction**",
	})
	if err != nil {
		t.Fatalf("CreateSprint: %v", err)
	}
	next, err := e.store.CreateSprint(e.ctx, store.CreateSprintParams{ProjectID: e.projectID, Name: "Next sprint"})
	if err != nil {
		t.Fatalf("CreateSprint next: %v", err)
	}
	active := model.SprintStatusActive
	if _, err := e.store.UpdateSprint(e.ctx, sprint.ID, store.UpdateSprintParams{Status: &active}); err != nil {
		t.Fatalf("activate sprint: %v", err)
	}
	todo := e.mustCreateIssue(t, "unfinished snapshot issue")
	done := e.mustCreateIssue(t, "done snapshot issue")
	backlog := e.mustCreateIssue(t, "never in completed sprint")
	for _, issue := range []model.Issue{todo, done} {
		if _, err := e.store.UpdateIssue(e.ctx, issue.ID, store.UpdateIssueParams{SprintID: &sprint.ID}); err != nil {
			t.Fatalf("assign %s: %v", issue.Title, err)
		}
	}
	doneStatus := model.StatusDone
	if _, err := e.store.UpdateIssue(e.ctx, done.ID, store.UpdateIssueParams{Status: &doneStatus}); err != nil {
		t.Fatalf("complete issue: %v", err)
	}
	completed, err := e.store.CompleteSprint(e.ctx, sprint.ID)
	if err != nil {
		t.Fatalf("CompleteSprint: %v", err)
	}
	moved, err := e.store.GetIssue(e.ctx, todo.ID)
	if err != nil {
		t.Fatalf("GetIssue moved: %v", err)
	}
	if moved.SprintID == nil || *moved.SprintID != next.ID {
		t.Fatalf("unfinished issue sprint = %v, want next sprint %s", moved.SprintID, next.ID)
	}
	if _, err := e.store.UpdateIssue(e.ctx, todo.ID, store.UpdateIssueParams{Status: &doneStatus}); err != nil {
		t.Fatalf("update issue after sprint completion: %v", err)
	}

	historyBody := e.uiGet(t, e.projectPath()+"/sprints", token)
	issuesPath := e.projectPath() + "/sprints/" + completed.Ref + "/history/issues"
	descriptionPath := e.projectPath() + "/sprints/" + completed.Ref + "/description"
	for _, want := range []string{
		`aria-label="Show issues"`,
		`data-disclosure-label>Show issues</span>`,
		`aria-controls="completed-sprint-` + completed.Ref + `-issues"`,
		`hx-get="` + issuesPath + `"`,
		`hx-trigger="click once"`,
		`aria-label="Done: 1"`,
		`aria-label="Cancelled: 0"`,
		"Original <strong>sprint direction</strong>",
		`hx-get="` + descriptionPath + `?expanded=1"`,
		`hx-target="#sprint-` + completed.Ref + `-description"`,
		"See more",
	} {
		if !strings.Contains(historyBody, want) {
			t.Fatalf("sprint history disclosure missing %q: %s", want, historyBody)
		}
	}
	for _, issue := range []model.Issue{todo, done, backlog} {
		if strings.Contains(historyBody, issue.Title) {
			t.Fatalf("sprint history eagerly rendered issue %q: %s", issue.Title, historyBody)
		}
	}
	if strings.Contains(historyBody, "Original **sprint direction**") {
		t.Fatalf("sprint history rendered Markdown source: %s", historyBody)
	}
	for _, notWant := range []string{`aria-label="To do:`, `aria-label="In progress:`, `aria-label="Closed:`} {
		if strings.Contains(historyBody, notWant) {
			t.Fatalf("sprint history included hidden outcome count %q: %s", notWant, historyBody)
		}
	}
	expandedDescriptionBody := e.uiGet(t, descriptionPath+"?expanded=1", token)
	for _, want := range []string{"Original <strong>sprint direction</strong>", "See less", `hx-target="#sprint-` + completed.Ref + `-description"`} {
		if !strings.Contains(expandedDescriptionBody, want) {
			t.Fatalf("expanded sprint history description missing %q: %s", want, expandedDescriptionBody)
		}
	}

	issuesBody := e.uiGet(t, issuesPath, token)
	if strings.Contains(issuesBody, "sprint direction") {
		t.Fatalf("snapshot issue response included sprint description: %s", issuesBody)
	}
	for _, issue := range []model.Issue{todo, done} {
		for _, want := range []string{issue.Title, `href="` + e.issuePath(issue) + `"`, `hx-get="` + e.issuePath(issue) + `/panel"`} {
			if !strings.Contains(issuesBody, want) {
				t.Fatalf("snapshot issue response missing %q: %s", want, issuesBody)
			}
		}
	}
	if strings.Contains(issuesBody, backlog.Title) {
		t.Fatalf("snapshot issue response included backlog issue: %s", issuesBody)
	}

	res := e.uiDoNoRedirect(t, http.MethodGet, issuesPath+"?cursor=bad", token, nil)
	defer res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad snapshot cursor code = %d body = %s", res.StatusCode, readBody(t, res))
	}

	res = e.uiDoNoRedirect(t, http.MethodGet, e.projectPath()+"/sprints/"+next.Ref+"/history/issues", token, nil)
	defer res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("planned snapshot code = %d body = %s", res.StatusCode, readBody(t, res))
	}
}

func TestUIProjectSprintHistoryIssuePagination(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	_, token := e.mustProjectMemberToken(t, "ui-sprint-history-issue-pages")
	sprint := createCompletedSprintAtFor(t, e, e.projectID, "Completed issue pages", sprintTestDate(2026, 6, 1), sprintTestDate(2026, 6, 14), datePtr(time.Now()))
	if _, err := e.pool.Exec(e.ctx, `
		WITH inserted AS (
			INSERT INTO issues (project_id, number, title)
			SELECT $1, 2000 + n, 'Snapshot issue ' || lpad(n::text, 2, '0')
			FROM generate_series(1, 51) AS n
			RETURNING id, project_id
		)
		INSERT INTO sprint_issue_snapshots (project_id, sprint_id, issue_id, status)
		SELECT project_id, $2, id, 'todo' FROM inserted
	`, e.projectID, sprint.ID); err != nil {
		t.Fatalf("insert sprint issue snapshots: %v", err)
	}

	issuesPath := e.projectPath() + "/sprints/" + sprint.Ref + "/history/issues"
	body := e.uiGet(t, issuesPath, token)
	if !strings.Contains(body, "Snapshot issue 01") || !strings.Contains(body, "Snapshot issue 50") || strings.Contains(body, "Snapshot issue 51") {
		t.Fatalf("snapshot issues first page = %s", body)
	}
	marker := `hx-get="` + issuesPath + `?cursor=`
	start := strings.Index(body, marker)
	if start < 0 {
		t.Fatalf("snapshot issue load-more URL missing: %s", body)
	}
	rest := body[start+len(marker):]
	end := strings.Index(rest, `"`)
	if end < 0 {
		t.Fatalf("snapshot issue load-more cursor malformed: %s", body)
	}
	pageBody := e.uiGet(t, issuesPath+"?cursor="+rest[:end], token)
	if !strings.Contains(pageBody, "Snapshot issue 51") || strings.Contains(pageBody, "Load more issues") {
		t.Fatalf("snapshot issues second page = %s", pageBody)
	}
}

func TestUIRendersProjectSprintEmptyState(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	_, token := e.mustProjectMemberToken(t, "ui-empty")
	body := e.uiGet(t, e.projectPath()+"/sprint", token)
	if !strings.Contains(body, "No active sprint.") {
		t.Fatalf("body missing no-active-sprint state: %s", body)
	}
	for _, notWant := range []string{`aria-label="Issue controls"`, "No active sprint issues."} {
		if strings.Contains(body, notWant) {
			t.Fatalf("empty sprint body included %q: %s", notWant, body)
		}
	}
}

func TestUIProjectSprintHidesIssueUIWithOnlyInactiveSprints(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	_, token := e.mustProjectMemberToken(t, "ui-inactive-sprints")

	planned, err := e.store.CreateSprint(e.ctx, store.CreateSprintParams{ProjectID: e.projectID, Name: "Future Sprint"})
	if err != nil {
		t.Fatalf("CreateSprint planned: %v", err)
	}
	completed, err := e.store.CreateSprint(e.ctx, store.CreateSprintParams{ProjectID: e.projectID, Name: "Completed Sprint"})
	if err != nil {
		t.Fatalf("CreateSprint completed: %v", err)
	}
	active := model.SprintStatusActive
	if _, err := e.store.UpdateSprint(e.ctx, completed.ID, store.UpdateSprintParams{Status: &active}); err != nil {
		t.Fatalf("UpdateSprint active: %v", err)
	}
	if _, err := e.store.CompleteSprint(e.ctx, completed.ID); err != nil {
		t.Fatalf("CompleteSprint: %v", err)
	}

	body := e.uiGet(t, e.projectPath()+"/sprint", token)
	if !strings.Contains(body, "No active sprint.") {
		t.Fatalf("inactive-only sprint body missing guidance: %s", body)
	}
	for _, notWant := range []string{`aria-label="Issue controls"`, "No active sprint issues."} {
		if strings.Contains(body, notWant) {
			t.Fatalf("inactive-only sprint body included %q: %s", notWant, body)
		}
	}

	plannedBody := e.uiGet(t, e.projectPath()+"/planned", token)
	if !strings.Contains(plannedBody, planned.Name) {
		t.Fatalf("planned view missing inactive sprint: %s", plannedBody)
	}
	historyBody := e.uiGet(t, e.projectPath()+"/sprints", token)
	if !strings.Contains(historyBody, completed.Name) {
		t.Fatalf("sprint history missing completed sprint: %s", historyBody)
	}
}

func TestUIProjectSprintDoesNotIncludeBacklog(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	_, token := e.mustProjectMemberToken(t, "ui-sprint")
	dueDate, err := model.ParseDate("2099-06-24")
	if err != nil {
		t.Fatalf("ParseDate: %v", err)
	}
	sp, err := e.store.CreateSprint(e.ctx, store.CreateSprintParams{
		ProjectID: e.projectID,
		Name:      "Active UI Sprint",
		StartDate: datePtr(time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)),
		EndDate:   datePtr(time.Date(2026, 6, 14, 0, 0, 0, 0, time.UTC)),
	})
	if err != nil {
		t.Fatalf("CreateSprint: %v", err)
	}
	active := model.SprintStatusActive
	if _, err := e.store.UpdateSprint(e.ctx, sp.ID, store.UpdateSprintParams{Status: &active}); err != nil {
		t.Fatalf("UpdateSprint active: %v", err)
	}
	emptyBody := e.uiGet(t, e.projectPath()+"/sprint", token)
	for _, want := range []string{`aria-label="Issue controls"`, "Nothing here."} {
		if !strings.Contains(emptyBody, want) {
			t.Fatalf("empty active sprint body missing %q: %s", want, emptyBody)
		}
	}
	inSprint, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{ProjectID: e.projectID, Title: "issue inside active sprint", DueDate: &dueDate})
	if err != nil {
		t.Fatalf("CreateIssue in sprint: %v", err)
	}
	if _, err := e.store.UpdateIssue(e.ctx, inSprint.ID, store.UpdateIssueParams{SprintID: &sp.ID}); err != nil {
		t.Fatalf("assign issue: %v", err)
	}
	if _, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{ProjectID: e.projectID, Title: "issue still in backlog"}); err != nil {
		t.Fatalf("CreateIssue backlog: %v", err)
	}

	body := e.uiGet(t, e.projectPath()+"/sprint", token)
	if !strings.Contains(body, "issue inside active sprint") {
		t.Fatalf("sprint body missing sprint issue: %s", body)
	}
	if !strings.Contains(body, "Jun 24") {
		t.Fatalf("sprint body missing issue due date: %s", body)
	}
	if strings.Contains(body, "issue still in backlog") {
		t.Fatalf("sprint body included backlog issue: %s", body)
	}
}
