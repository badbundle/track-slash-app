package server_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/store"
)

// progressIssue creates an issue at priority and moves it through statuses in
// order, the way someone working it would.
func progressIssue(t *testing.T, e *httpEnv, title string, priority model.IssuePriority, statuses ...model.Status) model.Issue {
	t.Helper()
	issue, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{ProjectID: e.projectID, Title: title, Priority: priority})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	for _, status := range statuses {
		params := store.UpdateIssueParams{Status: &status}
		if status == model.StatusClosed {
			reason := model.CloseReasonWontDo
			params.CloseReason = &reason
		}
		if _, err := e.store.UpdateIssue(e.ctx, issue.ID, params); err != nil {
			t.Fatalf("UpdateIssue %s: %v", status, err)
		}
	}
	return issue
}

// backdateCompletion moves an issue's latest update, its move to Done, back by
// age, so it reads as completed that long ago.
func backdateCompletion(t *testing.T, e *httpEnv, issueID uuid.UUID, age time.Duration) {
	t.Helper()
	tag, err := e.pool.Exec(e.ctx, `
		UPDATE project_changelog_entries SET created_at = now() - make_interval(secs => $2)
		WHERE id = (
			SELECT id FROM project_changelog_entries
			WHERE issue_id = $1 AND op = 'update'
			ORDER BY created_at DESC, id DESC
			LIMIT 1
		)
	`, issueID, age.Seconds())
	if err != nil || tag.RowsAffected() != 1 {
		t.Fatalf("backdate completion: rows %d err %v", tag.RowsAffected(), err)
	}
}

type progressFixture struct {
	urgent, later, justDone, doneYesterday, doneLastWeek, todo, closed model.Issue
}

func newProgressFixture(t *testing.T, e *httpEnv) progressFixture {
	t.Helper()
	f := progressFixture{
		later:         progressIssue(t, e, "Progress later work", model.PriorityP2, model.StatusInProgress),
		urgent:        progressIssue(t, e, "Progress urgent work", model.PriorityP0, model.StatusInProgress),
		justDone:      progressIssue(t, e, "Progress just done", model.PriorityP2, model.StatusInProgress, model.StatusDone),
		doneYesterday: progressIssue(t, e, "Progress done yesterday", model.PriorityP2, model.StatusDone),
		doneLastWeek:  progressIssue(t, e, "Progress done ten days ago", model.PriorityP2, model.StatusDone),
		todo:          progressIssue(t, e, "Progress not started", model.PriorityP0),
		closed:        progressIssue(t, e, "Progress won't do", model.PriorityP0, model.StatusClosed),
	}
	backdateCompletion(t, e, f.doneYesterday.ID, 30*time.Hour)
	backdateCompletion(t, e, f.doneLastWeek.ID, 10*24*time.Hour)
	return f
}

func requireProgressOrder(t *testing.T, name string, got []uuid.UUID, want ...model.Issue) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s = %v, want %d issues", name, got, len(want))
	}
	for i, issue := range want {
		if got[i] != issue.ID {
			t.Fatalf("%s[%d] = %s, want %s (%s)", name, i, got[i], issue.ID, issue.Title)
		}
	}
}

func progressOf(p model.ProjectProgress) (inProgress, completed []uuid.UUID) {
	for _, issue := range p.InProgress {
		inProgress = append(inProgress, issue.ID)
	}
	for _, issue := range p.RecentlyCompleted {
		completed = append(completed, issue.ID)
	}
	return inProgress, completed
}

func TestProjectProgressAPI(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	_, memberToken := e.mustProjectMemberToken(t, "progress-api-member")
	f := newProgressFixture(t, e)
	path := e.projectPath() + "/progress"

	code, body := e.doWithToken(t, memberToken, http.MethodGet, path, nil)
	if code != http.StatusOK {
		t.Fatalf("progress code = %d body = %s", code, body)
	}
	progress := decode[model.ProjectProgress](t, body)
	if progress.CompletedWithin != model.CompletionWindowWeek || progress.InProgressHasMore || progress.RecentlyCompletedHasMore {
		t.Fatalf("default progress window %q more %t/%t", progress.CompletedWithin, progress.InProgressHasMore, progress.RecentlyCompletedHasMore)
	}
	if age := time.Since(progress.CompletedSince); age < 7*24*time.Hour-time.Minute || age > 7*24*time.Hour+time.Minute {
		t.Fatalf("completed_since = %s, want about 7 days ago", progress.CompletedSince)
	}
	inProgress, completed := progressOf(progress)
	// Highest priority first; most recently completed first.
	requireProgressOrder(t, "in_progress", inProgress, f.urgent, f.later)
	requireProgressOrder(t, "recently_completed", completed, f.justDone, f.doneYesterday)
	if age := time.Since(progress.RecentlyCompleted[1].CompletedAt); age < 29*time.Hour || age > 31*time.Hour {
		t.Fatalf("done yesterday completed_at = %s, want about 30 hours ago", progress.RecentlyCompleted[1].CompletedAt)
	}
	if !strings.Contains(string(body), `"completed_at":`) || !strings.Contains(string(body), `"identifier":"`+f.justDone.Identifier+`"`) {
		t.Fatalf("progress JSON does not flatten completed issues: %s", body)
	}

	for _, tt := range []struct {
		window string
		want   model.CompletionWindow
		done   []model.Issue
	}{
		{window: "1d", want: model.CompletionWindowDay, done: []model.Issue{f.justDone}},
		{window: "14D", want: model.CompletionWindowTwoWeeks, done: []model.Issue{f.justDone, f.doneYesterday, f.doneLastWeek}},
	} {
		code, body := e.do(t, http.MethodGet, path+"?completed_within="+tt.window, nil)
		if code != http.StatusOK {
			t.Fatalf("%s progress code = %d body = %s", tt.window, code, body)
		}
		progress := decode[model.ProjectProgress](t, body)
		_, completed := progressOf(progress)
		if progress.CompletedWithin != tt.want {
			t.Fatalf("%s progress window = %q", tt.window, progress.CompletedWithin)
		}
		requireProgressOrder(t, tt.window+" recently_completed", completed, tt.done...)
	}

	for _, tt := range []struct {
		name  string
		token string
		path  string
		want  int
	}{
		{name: "invalid window", token: e.authToken, path: path + "?completed_within=2w", want: http.StatusBadRequest},
		{name: "bad owner", token: e.authToken, path: "/bad!/projects/" + e.projKey + "/progress", want: http.StatusBadRequest},
		{name: "missing project", token: e.authToken, path: "/" + e.ownerUsername + "/projects/" + uniqueProjectKey(t) + "/progress", want: http.StatusNotFound},
		{name: "anonymous private", path: path, want: http.StatusUnauthorized},
	} {
		if code, body := e.doWithToken(t, tt.token, http.MethodGet, tt.path, nil); code != tt.want {
			t.Fatalf("%s code = %d body = %s, want %d", tt.name, code, body, tt.want)
		}
	}
	_, deniedToken := e.mustUserToken(t, "progress-api-denied")
	if code, _ := e.doWithToken(t, deniedToken, http.MethodGet, path, nil); code != http.StatusForbidden {
		t.Fatalf("non-member progress code = %d", code)
	}
	if _, err := e.store.UpdateProjectAccessSettings(e.ctx, e.projectID, model.ProjectAccessSettings{IsPublic: true}); err != nil {
		t.Fatalf("UpdateProjectAccessSettings: %v", err)
	}
	if code, body := e.doUnauth(t, http.MethodGet, path, nil); code != http.StatusOK {
		t.Fatalf("anonymous public progress code = %d body = %s", code, body)
	}
}

func TestMCPGetProjectProgress(t *testing.T) {
	t.Parallel()
	e := newMCPHTTPEnv(t, nil)
	f := newProgressFixture(t, e)
	session := mcpConnect(t, e, e.authToken)
	args := func(extra map[string]any) map[string]any {
		out := map[string]any{"owner": e.ownerUsername, "key": e.projKey}
		for k, v := range extra {
			out[k] = v
		}
		return out
	}

	out := mcpCall(t, e, session, "track_get_project_progress", args(map[string]any{"completed_within": "14d"}))
	progress := decodeMCPField[model.ProjectProgress](t, out, "progress")
	inProgress, completed := progressOf(progress)
	if progress.CompletedWithin != model.CompletionWindowTwoWeeks {
		t.Fatalf("MCP progress window = %q", progress.CompletedWithin)
	}
	requireProgressOrder(t, "MCP in_progress", inProgress, f.urgent, f.later)
	requireProgressOrder(t, "MCP recently_completed", completed, f.justDone, f.doneYesterday, f.doneLastWeek)

	out = mcpCall(t, e, session, "track_get_project_progress", args(nil))
	if progress := decodeMCPField[model.ProjectProgress](t, out, "progress"); progress.CompletedWithin != model.DefaultCompletionWindow {
		t.Fatalf("MCP default progress window = %q", progress.CompletedWithin)
	}

	requireMCPErrorCode(t, mcpCallExpectError(t, e, session, "track_get_project_progress", args(map[string]any{"completed_within": "2w"})), "validation_error")
	_, outsiderToken := e.mustUserToken(t, "progress-mcp-outsider")
	outsider := mcpConnect(t, e, outsiderToken)
	requireMCPErrorCode(t, mcpCallExpectError(t, e, outsider, "track_get_project_progress", args(nil)), "forbidden")
}

func TestUIProjectInProgressView(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	e.mustSetFixtureSprintsEnabled(t, false)
	readonly, readonlyToken := e.mustUserToken(t, "ui-progress-readonly")
	if _, err := e.store.SetProjectMemberRole(e.ctx, e.projectID, readonly.ID, model.ProjectMemberRoleReadonly); err != nil {
		t.Fatalf("SetProjectMemberRole: %v", err)
	}
	f := newProgressFixture(t, e)
	path := e.projectPath() + "/progress"

	body := e.uiGet(t, path, readonlyToken)
	inProgress := progressSection(t, body, "data-progress-in-progress")
	completed := progressSection(t, body, "data-progress-recently-completed")
	requireMarkupOrderForTest(t, inProgress, f.urgent.Title, f.later.Title)
	requireMarkupOrderForTest(t, completed, f.justDone.Title, f.doneYesterday.Title)
	for _, notWant := range []string{f.doneLastWeek.Title, f.todo.Title, f.closed.Title, f.justDone.Title} {
		if strings.Contains(inProgress, notWant) {
			t.Fatalf("In progress lists %q: %s", notWant, inProgress)
		}
	}
	for _, notWant := range []string{f.doneLastWeek.Title, f.todo.Title, f.closed.Title, f.urgent.Title} {
		if strings.Contains(completed, notWant) {
			t.Fatalf("Recently completed lists %q: %s", notWant, completed)
		}
	}
	if got := strings.Count(completed, "data-completed-at"); got != 2 {
		t.Fatalf("recently completed rows showing completion time = %d, want 2: %s", got, completed)
	}
	if strings.Contains(inProgress, "data-completed-at") {
		t.Fatalf("in progress rows should not show a completion time: %s", inProgress)
	}
	for _, want := range []string{
		`aria-label="Completed within"`, `aria-current="true" class="whitespace-nowrap`, ">7 days</a>",
		`href="` + path + `?completed_within=14d" hx-get="` + path + `/panel?completed_within=14d"`,
		`href="` + path + `" hx-get="` + path + `/panel"`,
		`href="` + path + `?completed_within=1d"`, `href="` + path + `?completed_within=30d"`,
		">In progress<",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("In progress view missing %q: %s", want, body)
		}
	}
	// In progress takes Planned's place ahead of All, and neither sprint view
	// is linked.
	tabs := progressSection(t, body, `aria-label="Project views"`)
	requireMarkupOrderForTest(t, tabs, `href="`+path+`"`, `href="`+e.projectPath()+`/all"`)
	for _, notWant := range []string{`href="` + e.projectPath() + `/planned"`, `href="` + e.projectPath() + `/sprint"`} {
		if strings.Contains(body, notWant) {
			t.Fatalf("project without sprints still links %q", notWant)
		}
	}

	wider := e.uiGet(t, path+"?completed_within=14d", readonlyToken)
	requireMarkupOrderForTest(t, progressSection(t, wider, "data-progress-recently-completed"), f.doneYesterday.Title, f.doneLastWeek.Title)
	if !strings.Contains(wider, `aria-current="true" class="whitespace-nowrap`) || !strings.Contains(wider, ">14 days</a>") {
		t.Fatalf("14-day window not marked current: %s", wider)
	}

	res := e.uiDoNoRedirect(t, http.MethodGet, path+"?completed_within=forever", readonlyToken, nil)
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid window code = %d, want 400", res.StatusCode)
	}

	// A project with nothing going on says so in each section.
	empty, err := e.store.CreateProjectForUser(e.ctx, e.adminID, uniqueProjectKey(t), "Quiet project", "")
	if err != nil {
		t.Fatalf("CreateProjectForUser: %v", err)
	}
	quiet := e.uiGet(t, "/"+e.ownerUsername+"/projects/"+empty.Key+"/progress", e.authToken)
	for _, want := range []string{"No issues in progress.", "Nothing completed in the last 7 days."} {
		if !strings.Contains(quiet, want) {
			t.Fatalf("empty In progress view missing %q: %s", want, quiet)
		}
	}
}

func progressSection(t *testing.T, body, marker string) string {
	t.Helper()
	start := strings.Index(body, marker)
	if start < 0 {
		t.Fatalf("missing %q: %s", marker, body)
	}
	end := strings.Index(body[start:], "</section>")
	if strings.HasPrefix(marker, "aria-label") {
		end = strings.Index(body[start:], "</nav>")
	}
	if end < 0 {
		t.Fatalf("unterminated %q: %s", marker, body)
	}
	return body[start : start+end]
}

func requireMarkupOrderForTest(t *testing.T, body, first, second string) {
	t.Helper()
	firstIndex := strings.Index(body, first)
	secondIndex := strings.Index(body, second)
	if firstIndex < 0 || secondIndex < 0 || firstIndex > secondIndex {
		t.Fatalf("%q should render before %q: %s", first, second, body)
	}
}
