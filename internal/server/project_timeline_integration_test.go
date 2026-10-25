package server_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/store"
)

type timelineFixture struct {
	oldSprint, doneSprint, activeSprint, plannedSprint model.Sprint
	oldDone, sprintDone, started, private, dueSoon     model.Issue
	unplanned, outsideStarted, outsideOld, someday     model.Issue
}

func timelineSprint(t *testing.T, e *httpEnv, name string, start, end *time.Time) model.Sprint {
	t.Helper()
	sprint, err := e.store.CreateSprint(e.ctx, store.CreateSprintParams{ProjectID: e.projectID, Name: name, StartDate: start, EndDate: end})
	if err != nil {
		t.Fatalf("CreateSprint %s: %v", name, err)
	}
	return sprint
}

func timelineSetSprintStatus(t *testing.T, e *httpEnv, id uuid.UUID, status model.SprintStatus) {
	t.Helper()
	if _, err := e.store.UpdateSprint(e.ctx, id, store.UpdateSprintParams{Status: &status}); err != nil {
		t.Fatalf("UpdateSprint %s: %v", status, err)
	}
}

func timelineCompleteSprint(t *testing.T, e *httpEnv, id uuid.UUID, ago time.Duration) {
	t.Helper()
	if _, err := e.store.CompleteSprint(e.ctx, id); err != nil {
		t.Fatalf("CompleteSprint: %v", err)
	}
	if _, err := e.pool.Exec(e.ctx, `UPDATE sprints SET completed_at = now() - make_interval(secs => $2) WHERE id = $1`, id, ago.Seconds()); err != nil {
		t.Fatalf("backdate sprint completion: %v", err)
	}
}

func timelineAssign(t *testing.T, e *httpEnv, issueID, sprintID uuid.UUID) {
	t.Helper()
	if _, err := e.store.UpdateIssue(e.ctx, issueID, store.UpdateIssueParams{SprintID: &sprintID}); err != nil {
		t.Fatalf("assign sprint: %v", err)
	}
}

func timelineSetStatus(t *testing.T, e *httpEnv, issueID uuid.UUID, status model.Status) {
	t.Helper()
	if _, err := e.store.UpdateIssue(e.ctx, issueID, store.UpdateIssueParams{Status: &status}); err != nil {
		t.Fatalf("UpdateIssue %s: %v", status, err)
	}
}

// newTimelineFixture builds a sprint project with a completed sprint outside
// the default 30-day range, one inside it, the active sprint, a planned
// sprint without dates, and work in no sprint.
func newTimelineFixture(t *testing.T, e *httpEnv) timelineFixture {
	t.Helper()
	today := time.Now().UTC().Truncate(24 * time.Hour)
	day := func(n int) *time.Time { d := today.AddDate(0, 0, n); return &d }
	var f timelineFixture

	f.oldSprint = timelineSprint(t, e, "Timeline long ago", day(-80), day(-66))
	f.oldDone = progressIssue(t, e, "Timeline old work", model.PriorityP2)
	timelineAssign(t, e, f.oldDone.ID, f.oldSprint.ID)
	timelineSetSprintStatus(t, e, f.oldSprint.ID, model.SprintStatusActive)
	timelineSetStatus(t, e, f.oldDone.ID, model.StatusDone)
	backdateCompletion(t, e, f.oldDone.ID, 70*24*time.Hour)
	timelineCompleteSprint(t, e, f.oldSprint.ID, 65*24*time.Hour)

	f.doneSprint = timelineSprint(t, e, "Timeline last sprint", day(-34), day(-20))
	f.sprintDone = progressIssue(t, e, "Timeline finished in sprint", model.PriorityP2)
	timelineAssign(t, e, f.sprintDone.ID, f.doneSprint.ID)
	timelineSetSprintStatus(t, e, f.doneSprint.ID, model.SprintStatusActive)
	timelineSetStatus(t, e, f.sprintDone.ID, model.StatusDone)
	backdateCompletion(t, e, f.sprintDone.ID, 22*24*time.Hour)
	timelineCompleteSprint(t, e, f.doneSprint.ID, 20*24*time.Hour)

	f.activeSprint = timelineSprint(t, e, "Timeline this sprint", day(-3), day(11))
	f.plannedSprint = timelineSprint(t, e, "Timeline someday sprint", nil, nil)
	due := model.DateFromTime(*day(5))
	f.started = progressIssue(t, e, "Timeline started", model.PriorityP2)
	f.dueSoon = progressIssue(t, e, "Timeline due soon", model.PriorityP2)
	if _, err := e.store.UpdateIssue(e.ctx, f.dueSoon.ID, store.UpdateIssueParams{DueDate: &due}); err != nil {
		t.Fatalf("set due date: %v", err)
	}
	private, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{ProjectID: e.projectID, Title: "Timeline private", Private: true})
	if err != nil {
		t.Fatalf("CreateIssue private: %v", err)
	}
	f.private = private
	for _, issue := range []model.Issue{f.started, f.dueSoon, f.private} {
		timelineAssign(t, e, issue.ID, f.activeSprint.ID)
	}
	timelineSetSprintStatus(t, e, f.activeSprint.ID, model.SprintStatusActive)
	timelineSetStatus(t, e, f.started.ID, model.StatusInProgress)
	backdateCompletion(t, e, f.started.ID, 2*24*time.Hour)
	f.unplanned = progressIssue(t, e, "Timeline undated plan", model.PriorityP2)
	timelineAssign(t, e, f.unplanned.ID, f.plannedSprint.ID)

	f.outsideStarted = progressIssue(t, e, "Timeline outside started", model.PriorityP2, model.StatusInProgress)
	f.outsideOld = progressIssue(t, e, "Timeline outside done long ago", model.PriorityP2, model.StatusDone)
	backdateCompletion(t, e, f.outsideOld.ID, 40*24*time.Hour)
	f.someday = progressIssue(t, e, "Timeline outside someday", model.PriorityP2)
	return f
}

// timelineSprintsOff completes the fixture's active sprint, which moves its
// unfinished work on, then turns sprints off.
func timelineSprintsOff(t *testing.T, e *httpEnv, f timelineFixture) {
	t.Helper()
	if _, err := e.store.CompleteSprint(e.ctx, f.activeSprint.ID); err != nil {
		t.Fatalf("CompleteSprint: %v", err)
	}
	e.mustSetFixtureSprintsEnabled(t, false)
}

func timelineGroupIDs(t *testing.T, group model.TimelineGroup) []uuid.UUID {
	t.Helper()
	ids := []uuid.UUID{}
	for _, item := range group.Items {
		ids = append(ids, item.ID)
	}
	return ids
}

func requireTimelineGroups(t *testing.T, timeline model.ProjectTimeline, want ...*model.Sprint) {
	t.Helper()
	if len(timeline.Groups) != len(want) {
		t.Fatalf("timeline has %d groups, want %d: %+v", len(timeline.Groups), len(want), timeline.Groups)
	}
	for i, sprint := range want {
		got := timeline.Groups[i].Sprint
		if (got == nil) != (sprint == nil) || (got != nil && got.ID != sprint.ID) {
			t.Fatalf("group %d sprint = %+v, want %+v", i, got, sprint)
		}
	}
}

func TestProjectTimelineAPI(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	_, memberToken := e.mustProjectMemberToken(t, "timeline-api-member")
	f := newTimelineFixture(t, e)
	path := e.projectPath() + "/timeline"
	today := model.DateFromTime(time.Now().UTC())

	code, body := e.doWithToken(t, memberToken, http.MethodGet, path, nil)
	if code != http.StatusOK {
		t.Fatalf("timeline code = %d body = %s", code, body)
	}
	timeline := decode[model.ProjectTimeline](t, body)
	if timeline.Range != model.InsightRangeThirtyDays || timeline.Since == nil || *timeline.Since != today.AddDays(-30) || timeline.Today != today || !timeline.SprintsEnabled {
		t.Fatalf("timeline header = %+v", timeline)
	}
	// Completed sprints in range, the active sprint, planned sprints, then
	// the work in no sprint.
	requireTimelineGroups(t, timeline, &f.doneSprint, &f.activeSprint, &f.plannedSprint, nil)
	requireProgressOrder(t, "last sprint", timelineGroupIDs(t, timeline.Groups[0]), f.sprintDone)
	// A group runs in the order its bars begin: unstarted work begins when
	// the sprint does, before work started since.
	requireProgressOrder(t, "active sprint", timelineGroupIDs(t, timeline.Groups[1]), f.dueSoon, f.private, f.started)
	requireProgressOrder(t, "planned sprint", timelineGroupIDs(t, timeline.Groups[2]), f.unplanned)
	requireProgressOrder(t, "no sprint", timelineGroupIDs(t, timeline.Groups[3]), f.outsideStarted)
	if timeline.Unscheduled != 1 || timeline.MoreCompletedSprints || timeline.MorePlannedSprints {
		t.Fatalf("unscheduled %d, more sprints %v/%v", timeline.Unscheduled, timeline.MoreCompletedSprints, timeline.MorePlannedSprints)
	}

	// Bars: completed work ends when it was completed, started work when it
	// started, unstarted work in the active sprint at its due date or the
	// sprint's end, and the undated plan has no bar.
	done := timeline.Groups[0].Items[0]
	if done.CompletedAt == nil || done.End == nil || *done.End != model.DateFromTime(*done.CompletedAt) || *done.End != today.AddDays(-22) {
		t.Fatalf("sprint work bar = %+v", done)
	}
	started := timeline.Groups[1].Items[2]
	if started.StartedAt == nil || *started.Start != today.AddDays(-2) || *started.End != today.AddDays(11) {
		t.Fatalf("started bar = %v..%v", started.Start, started.End)
	}
	dueSoon := timeline.Groups[1].Items[0]
	if *dueSoon.Start != today.AddDays(-3) || *dueSoon.End != today.AddDays(5) {
		t.Fatalf("due soon bar = %v..%v", dueSoon.Start, dueSoon.End)
	}
	if plan := timeline.Groups[2].Items[0]; plan.Start != nil || plan.End != nil {
		t.Fatalf("undated plan bar = %v..%v", plan.Start, plan.End)
	}
	if timeline.From != *timeline.Since || timeline.To != today.AddDays(11) {
		t.Fatalf("frame = %s..%s", timeline.From, timeline.To)
	}
	if !strings.Contains(string(body), `"identifier":"`+f.started.Identifier+`"`) || !strings.Contains(string(body), `"start":"`) {
		t.Fatalf("timeline JSON does not flatten its issues: %s", body)
	}

	// All time reaches the old sprint and the old work in no sprint.
	code, body = e.doWithToken(t, memberToken, http.MethodGet, path+"?range=ALL", nil)
	if code != http.StatusOK {
		t.Fatalf("all-time timeline code = %d body = %s", code, body)
	}
	all := decode[model.ProjectTimeline](t, body)
	if all.Range != model.InsightRangeAll || all.Since != nil {
		t.Fatalf("all-time range %q since %v", all.Range, all.Since)
	}
	requireTimelineGroups(t, all, &f.oldSprint, &f.doneSprint, &f.activeSprint, &f.plannedSprint, nil)
	requireProgressOrder(t, "all-time no sprint", timelineGroupIDs(t, all.Groups[4]), f.outsideOld, f.outsideStarted)
	if all.From != today.AddDays(-80) {
		t.Fatalf("all-time frame starts %s, want the old sprint's start", all.From)
	}

	// Two weeks leaves the last sprint out.
	code, body = e.doWithToken(t, memberToken, http.MethodGet, path+"?range=2w", nil)
	if code != http.StatusOK {
		t.Fatalf("2w timeline code = %d body = %s", code, body)
	}
	requireTimelineGroups(t, decode[model.ProjectTimeline](t, body), &f.activeSprint, &f.plannedSprint, nil)

	for _, tt := range []struct {
		name  string
		token string
		path  string
		want  int
	}{
		{name: "invalid range", token: e.authToken, path: path + "?range=1y", want: http.StatusBadRequest},
		{name: "missing project", token: e.authToken, path: "/" + e.ownerUsername + "/projects/" + uniqueProjectKey(t) + "/timeline", want: http.StatusNotFound},
		{name: "anonymous private", path: path, want: http.StatusUnauthorized},
	} {
		if code, body := e.doWithToken(t, tt.token, http.MethodGet, tt.path, nil); code != tt.want {
			t.Fatalf("%s code = %d body = %s, want %d", tt.name, code, body, tt.want)
		}
	}
	_, deniedToken := e.mustUserToken(t, "timeline-api-denied")
	if code, _ := e.doWithToken(t, deniedToken, http.MethodGet, path, nil); code != http.StatusForbidden {
		t.Fatalf("non-member timeline code = %d", code)
	}

	// A public project's timeline is open to everyone, without its private
	// issues.
	if _, err := e.store.UpdateProjectAccessMode(e.ctx, e.projectID, model.ProjectAccessPublic); err != nil {
		t.Fatalf("UpdateProjectAccessMode: %v", err)
	}
	code, body = e.doUnauth(t, http.MethodGet, path, nil)
	if code != http.StatusOK {
		t.Fatalf("anonymous public timeline code = %d body = %s", code, body)
	}
	requireProgressOrder(t, "public active sprint", timelineGroupIDs(t, decode[model.ProjectTimeline](t, body).Groups[1]), f.dueSoon, f.started)

	// With sprints off there is one group and sprints date nothing: the
	// undated plan and the sprint's unstarted work without a due date join
	// the unscheduled count.
	timelineSprintsOff(t, e, f)
	code, body = e.doWithToken(t, memberToken, http.MethodGet, path, nil)
	if code != http.StatusOK {
		t.Fatalf("sprints-off timeline code = %d body = %s", code, body)
	}
	off := decode[model.ProjectTimeline](t, body)
	requireTimelineGroups(t, off, nil)
	requireProgressOrder(t, "sprints off", timelineGroupIDs(t, off.Groups[0]), f.sprintDone, f.started, f.dueSoon, f.outsideStarted)
	if off.SprintsEnabled || off.Unscheduled != 3 {
		t.Fatalf("sprints off: enabled %v, unscheduled %d", off.SprintsEnabled, off.Unscheduled)
	}
}

// A timeline lists the twelve most recent completed sprints in range and the
// next twelve planned sprints, and says when there are more.
func TestProjectTimelineCapsSprints(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	for i := 0; i < 13; i++ {
		sprint := timelineSprint(t, e, fmt.Sprintf("Timeline done %02d", i), nil, nil)
		timelineSetSprintStatus(t, e, sprint.ID, model.SprintStatusActive)
		timelineCompleteSprint(t, e, sprint.ID, time.Duration(13-i)*time.Hour)
	}
	for i := 0; i < 13; i++ {
		timelineSprint(t, e, fmt.Sprintf("Timeline plan %02d", i), nil, nil)
	}
	code, body := e.do(t, http.MethodGet, e.projectPath()+"/timeline", nil)
	if code != http.StatusOK {
		t.Fatalf("timeline code = %d body = %s", code, body)
	}
	timeline := decode[model.ProjectTimeline](t, body)
	if !timeline.MoreCompletedSprints || !timeline.MorePlannedSprints || len(timeline.Groups) != 24 {
		t.Fatalf("more %v/%v with %d groups", timeline.MoreCompletedSprints, timeline.MorePlannedSprints, len(timeline.Groups))
	}
	// The oldest completed sprint is the one left off, and the rest run
	// oldest first; the planned sprints follow in their planned order.
	if first, last := timeline.Groups[0].Sprint.Name, timeline.Groups[11].Sprint.Name; first != "Timeline done 01" || last != "Timeline done 12" {
		t.Fatalf("completed sprints run %q..%q", first, last)
	}
	if first, last := timeline.Groups[12].Sprint.Name, timeline.Groups[23].Sprint.Name; first != "Timeline plan 00" || last != "Timeline plan 11" {
		t.Fatalf("planned sprints run %q..%q", first, last)
	}
}

func TestMCPGetProjectTimeline(t *testing.T) {
	t.Parallel()
	e := newMCPHTTPEnv(t, nil)
	f := newTimelineFixture(t, e)
	session := mcpConnect(t, e, e.authToken)
	args := func(extra map[string]any) map[string]any {
		out := map[string]any{"owner": e.ownerUsername, "key": e.projKey}
		for k, v := range extra {
			out[k] = v
		}
		return out
	}

	out := mcpCall(t, e, session, "track_get_project_timeline", args(nil))
	timeline := decodeMCPField[model.ProjectTimeline](t, out, "timeline")
	if timeline.Range != model.DefaultInsightRange {
		t.Fatalf("MCP default timeline range = %q", timeline.Range)
	}
	requireTimelineGroups(t, timeline, &f.doneSprint, &f.activeSprint, &f.plannedSprint, nil)
	requireProgressOrder(t, "MCP active sprint", timelineGroupIDs(t, timeline.Groups[1]), f.dueSoon, f.private, f.started)

	out = mcpCall(t, e, session, "track_get_project_timeline", args(map[string]any{"range": "all"}))
	requireTimelineGroups(t, decodeMCPField[model.ProjectTimeline](t, out, "timeline"), &f.oldSprint, &f.doneSprint, &f.activeSprint, &f.plannedSprint, nil)

	requireMCPErrorCode(t, mcpCallExpectError(t, e, session, "track_get_project_timeline", args(map[string]any{"range": "1y"})), "validation_error")
	_, outsiderToken := e.mustUserToken(t, "timeline-mcp-outsider")
	outsider := mcpConnect(t, e, outsiderToken)
	requireMCPErrorCode(t, mcpCallExpectError(t, e, outsider, "track_get_project_timeline", args(nil)), "forbidden")
}

func TestUIProjectTimelineView(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	readonly, readonlyToken := e.mustUserToken(t, "ui-timeline-readonly")
	if _, err := e.store.SetProjectMemberRole(e.ctx, e.projectID, readonly.ID, model.ProjectMemberRoleReadonly); err != nil {
		t.Fatalf("SetProjectMemberRole: %v", err)
	}
	f := newTimelineFixture(t, e)
	path := e.projectPath() + "/timeline"

	body := e.uiGet(t, path, readonlyToken)
	for _, want := range []string{
		`data-project-timeline`, ">Timeline</h2>", `aria-label="Date range"`, ">30 days</a>",
		`href="` + path + `?range=2w" hx-get="` + path + `/panel?range=2w"`, `href="` + path + `" hx-get="` + path + `/panel"`,
		`data-timeline-group="` + f.doneSprint.Ref + `"`, `data-timeline-group="` + f.activeSprint.Ref + `"`, `data-timeline-group="` + f.plannedSprint.Ref + `"`, `data-timeline-group="none"`,
		">Completed</span>", ">Active</span>", ">Planned</span>", ">Not in a sprint</h3>", ">Sprint</li>",
		`data-timeline-row="` + f.started.Identifier + `"`, `data-timeline-row="` + f.private.Identifier + `"`, `data-timeline-bar`, `data-timeline-due`, `data-timeline-today`, `data-timeline-window`,
		"No dates", "1 open issue has no due date or sprint, so it isn&#39;t drawn.",
		`aria-current="page"`, "<span>Timeline</span>",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("timeline view missing %q: %s", want, body)
		}
	}
	requireMarkupOrderForTest(t, body, `data-timeline-group="`+f.doneSprint.Ref+`"`, `data-timeline-group="`+f.activeSprint.Ref+`"`)
	requireMarkupOrderForTest(t, body, `data-timeline-group="`+f.plannedSprint.Ref+`"`, `data-timeline-group="none"`)
	requireMarkupOrderForTest(t, body, `data-timeline-row="`+f.dueSoon.Identifier+`"`, `data-timeline-row="`+f.started.Identifier+`"`)
	for _, notWant := range []string{f.oldDone.Title, f.outsideOld.Title, f.someday.Title} {
		if strings.Contains(body, notWant) {
			t.Fatalf("30-day timeline shows %q: %s", notWant, body)
		}
	}

	// The project actions menu links to it from every project view, and the
	// panel route answers htmx with the view alone.
	if other := e.uiGet(t, e.projectPath()+"/all", readonlyToken); !strings.Contains(other, `href="`+path+`" hx-get="`+path+`/panel"`) {
		t.Fatalf("project actions menu does not link the timeline: %s", other)
	}
	panel := e.uiGet(t, path+"/panel?range=all", readonlyToken)
	if !strings.Contains(panel, f.oldDone.Title) || !strings.Contains(panel, `aria-current="true" class="whitespace-nowrap`) || strings.Contains(panel, "<html") {
		t.Fatalf("all-time timeline panel = %s", panel)
	}

	res := e.uiDoNoRedirect(t, http.MethodGet, path+"?range=forever", readonlyToken, nil)
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid range code = %d, want 400", res.StatusCode)
	}

	// With sprints off the one group has no heading and the key has no
	// sprint swatch.
	timelineSprintsOff(t, e, f)
	off := e.uiGet(t, path, readonlyToken)
	if !strings.Contains(off, `data-timeline-group="none" aria-label="Issues"`) || strings.Contains(off, ">Not in a sprint</h3>") || strings.Contains(off, ">Sprint</li>") || strings.Contains(off, `data-timeline-window`) {
		t.Fatalf("sprints-off timeline = %s", off)
	}

	// A project with nothing scheduled says so.
	empty, err := e.store.CreateProjectForUser(e.ctx, e.adminID, uniqueProjectKey(t), "Quiet timeline", "")
	if err != nil {
		t.Fatalf("CreateProjectForUser: %v", err)
	}
	quiet := e.uiGet(t, "/"+e.ownerUsername+"/projects/"+empty.Key+"/timeline", e.authToken)
	if !strings.Contains(quiet, "data-timeline-empty") || !strings.Contains(quiet, "Nothing to show in this range.") || strings.Contains(quiet, "data-timeline-row") {
		t.Fatalf("empty timeline = %s", quiet)
	}
}
