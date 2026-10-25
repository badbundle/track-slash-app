package store_test

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/store"
)

func timelineIDs(items []store.TimelineIssue) []uuid.UUID {
	ids := make([]uuid.UUID, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.Issue.ID)
	}
	return ids
}

func sameIDs(got, want []uuid.UUID) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestListTimelineIssues(t *testing.T) {
	t.Parallel()
	env := newInsightsEnv(t)
	now := time.Now().UTC().Truncate(time.Second)
	since := now.Add(-30 * 24 * time.Hour)
	due := model.DateFromTime(now.AddDate(0, 0, 10))

	shown := mustCreateSprint(t, env, "Shown", date(2026, 10, 1), date(2026, 10, 14))
	hidden := mustCreateSprint(t, env, "Hidden", date(2026, 11, 1), date(2026, 11, 14))
	gone := mustCreateSprint(t, env, "Deleted", date(2026, 9, 1), date(2026, 9, 14))

	// In the shown sprint, every issue is listed whatever its state, in the
	// order its work began: when it started, or else when it was filed.
	sprintTodo := mustCreateIssue(t, env, "Sprint to do")
	sprintDone := mustCreateIssue(t, env, "Sprint done")
	sprintStarted := mustCreateIssue(t, env, "Sprint started")
	sprintPrivate, err := env.store.CreateIssue(env.ctx, store.CreateIssueParams{ProjectID: env.projectID, Title: "Sprint private", Private: true})
	if err != nil {
		t.Fatalf("CreateIssue private: %v", err)
	}
	for _, id := range []uuid.UUID{sprintTodo.ID, sprintDone.ID, sprintStarted.ID, sprintPrivate.ID} {
		assignIssueToSprint(t, env, id, shown.ID)
	}
	setInsightIssueCreatedAt(t, env, sprintTodo.ID, now.Add(-5*24*time.Hour))
	setInsightIssueStatus(t, env, sprintDone.ID, model.StatusInProgress, now.Add(-9*24*time.Hour))
	setInsightIssueStatus(t, env, sprintDone.ID, model.StatusDone, now.Add(-2*24*time.Hour))
	setInsightIssueStatus(t, env, sprintStarted.ID, model.StatusInProgress, now.Add(-7*24*time.Hour))

	// A sprint the timeline doesn't list keeps its issues out of both groups.
	inHidden := mustCreateIssue(t, env, "In a sprint not listed")
	setIssueStatus(t, env, inHidden.ID, model.StatusInProgress)
	assignIssueToSprint(t, env, inHidden.ID, hidden.ID)

	// An issue left in a deleted sprint counts as in no sprint.
	inDeleted := mustCreateIssue(t, env, "In a deleted sprint")
	assignIssueToSprint(t, env, inDeleted.ID, gone.ID)
	setInsightIssueStatus(t, env, inDeleted.ID, model.StatusInProgress, now.Add(-20*24*time.Hour))
	if err := env.store.DeleteSprint(env.ctx, gone.ID); err != nil {
		t.Fatalf("DeleteSprint: %v", err)
	}

	// In no sprint, only work with a schedule is listed: started, due, or
	// completed within the range.
	outsideDue, err := env.store.CreateIssue(env.ctx, store.CreateIssueParams{ProjectID: env.projectID, Title: "Due soon", DueDate: &due})
	if err != nil {
		t.Fatalf("CreateIssue due: %v", err)
	}
	setInsightIssueCreatedAt(t, env, outsideDue.ID, now.Add(-3*24*time.Hour))
	outsideStarted := mustCreateIssue(t, env, "Started outside")
	setInsightIssueStatus(t, env, outsideStarted.ID, model.StatusInProgress, now.Add(-4*24*time.Hour))
	outsideDone := mustCreateIssue(t, env, "Done outside")
	setInsightIssueStatus(t, env, outsideDone.ID, model.StatusInProgress, now.Add(-12*24*time.Hour))
	setInsightIssueStatus(t, env, outsideDone.ID, model.StatusDone, now.Add(-24*time.Hour))
	outsideOld := mustCreateIssue(t, env, "Closed long ago")
	setInsightIssueStatus(t, env, outsideOld.ID, model.StatusClosed, now.Add(-60*24*time.Hour))
	setInsightIssueCreatedAt(t, env, outsideOld.ID, now.Add(-70*24*time.Hour))
	unrecorded := mustCreateIssue(t, env, "Imported done")
	if _, err := env.pool.Exec(env.ctx, `UPDATE issues SET status = 'done', created_at = $2 WHERE id = $1`, unrecorded.ID, now.Add(-6*24*time.Hour)); err != nil {
		t.Fatalf("write unrecorded done issue: %v", err)
	}
	unscheduled := mustCreateIssue(t, env, "Someday")
	_ = unscheduled

	// Never listed: deleted issues and sub-issues.
	deleted := mustCreateIssue(t, env, "Deleted")
	setIssueStatus(t, env, deleted.ID, model.StatusInProgress)
	if err := env.store.DeleteIssue(env.ctx, deleted.ID); err != nil {
		t.Fatalf("DeleteIssue: %v", err)
	}
	if _, err := env.store.CreateSubIssue(env.ctx, store.CreateSubIssueParams{ParentIssueID: outsideStarted.ID, Title: "Sub-issue", DueDate: &due}); err != nil {
		t.Fatalf("CreateSubIssue: %v", err)
	}

	params := store.ListTimelineIssuesParams{
		ProjectID:      env.projectID,
		SprintsEnabled: true,
		SprintIDs:      []uuid.UUID{shown.ID},
		Since:          &since,
		Limit:          10,
	}
	got, err := env.store.ListTimelineIssues(env.ctx, params)
	if err != nil {
		t.Fatalf("ListTimelineIssues: %v", err)
	}
	if want := []uuid.UUID{sprintDone.ID, sprintStarted.ID, sprintTodo.ID}; !sameIDs(timelineIDs(got.BySprint[shown.ID]), want) {
		t.Fatalf("shown sprint = %v, want %v", timelineIDs(got.BySprint[shown.ID]), want)
	}
	if len(got.BySprint) != 1 || got.SprintHasMore[shown.ID] {
		t.Fatalf("sprint groups = %v, has more %v", got.BySprint, got.SprintHasMore)
	}
	if want := []uuid.UUID{inDeleted.ID, outsideDone.ID, unrecorded.ID, outsideStarted.ID, outsideDue.ID}; !sameIDs(timelineIDs(got.Outside), want) || got.OutsideHasMore {
		t.Fatalf("outside = %v (has more %v), want %v", timelineIDs(got.Outside), got.OutsideHasMore, want)
	}
	if got.Unscheduled != 1 {
		t.Fatalf("Unscheduled = %d, want 1", got.Unscheduled)
	}

	// Each issue carries its first move to In progress, and while completed,
	// its last move to Done or Closed; an unrecorded completion is its filing.
	times := map[uuid.UUID]store.TimelineIssue{}
	for _, item := range append(got.BySprint[shown.ID], got.Outside...) {
		times[item.Issue.ID] = item
	}
	at := func(d time.Duration) *time.Time { t := now.Add(d); return &t }
	for _, tc := range []struct {
		id                 uuid.UUID
		started, completed *time.Time
	}{
		{id: sprintDone.ID, started: at(-9 * 24 * time.Hour), completed: at(-2 * 24 * time.Hour)},
		{id: sprintStarted.ID, started: at(-7 * 24 * time.Hour)},
		{id: sprintTodo.ID},
		{id: outsideDone.ID, started: at(-12 * 24 * time.Hour), completed: at(-24 * time.Hour)},
		{id: unrecorded.ID, completed: at(-6 * 24 * time.Hour)},
	} {
		item := times[tc.id]
		if !sameTime(item.StartedAt, tc.started) || !sameTime(item.CompletedAt, tc.completed) {
			t.Fatalf("%s started %v completed %v, want %v and %v", item.Issue.Title, item.StartedAt, item.CompletedAt, tc.started, tc.completed)
		}
		if item.Issue.Identifier == "" || item.Issue.Tags == nil {
			t.Fatalf("%s not fully loaded: %+v", item.Issue.Title, item.Issue)
		}
	}

	// Members see private issues; all time keeps old completions.
	params.IncludePrivate = true
	params.Since = nil
	got, err = env.store.ListTimelineIssues(env.ctx, params)
	if err != nil {
		t.Fatalf("ListTimelineIssues all time: %v", err)
	}
	if want := []uuid.UUID{sprintDone.ID, sprintStarted.ID, sprintTodo.ID, sprintPrivate.ID}; !sameIDs(timelineIDs(got.BySprint[shown.ID]), want) {
		t.Fatalf("shown sprint with private = %v, want %v", timelineIDs(got.BySprint[shown.ID]), want)
	}
	if want := []uuid.UUID{outsideOld.ID, inDeleted.ID, outsideDone.ID, unrecorded.ID, outsideStarted.ID, outsideDue.ID}; !sameIDs(timelineIDs(got.Outside), want) {
		t.Fatalf("outside all time = %v, want %v", timelineIDs(got.Outside), want)
	}

	// Each group is capped on its own.
	params.Limit = 2
	got, err = env.store.ListTimelineIssues(env.ctx, params)
	if err != nil {
		t.Fatalf("ListTimelineIssues capped: %v", err)
	}
	if len(got.BySprint[shown.ID]) != 2 || !got.SprintHasMore[shown.ID] || len(got.Outside) != 2 || !got.OutsideHasMore {
		t.Fatalf("capped groups: sprint %d (more %v), outside %d (more %v)", len(got.BySprint[shown.ID]), got.SprintHasMore[shown.ID], len(got.Outside), got.OutsideHasMore)
	}

	// With sprints off there is one group, and a sprint dates nothing: the
	// sprint's open To do issue is unscheduled, the rest are listed.
	got, err = env.store.ListTimelineIssues(env.ctx, store.ListTimelineIssuesParams{
		ProjectID: env.projectID,
		Since:     &since,
		Limit:     20,
	})
	if err != nil {
		t.Fatalf("ListTimelineIssues sprints off: %v", err)
	}
	if want := []uuid.UUID{inDeleted.ID, outsideDone.ID, sprintDone.ID, sprintStarted.ID, unrecorded.ID, outsideStarted.ID, outsideDue.ID, inHidden.ID}; len(got.BySprint) != 0 || !sameIDs(timelineIDs(got.Outside), want) {
		t.Fatalf("sprints off = %v (sprint groups %v), want %v", timelineIDs(got.Outside), got.BySprint, want)
	}
	if got.Unscheduled != 2 {
		t.Fatalf("Unscheduled with sprints off = %d, want 2", got.Unscheduled)
	}
}

func sameTime(got, want *time.Time) bool {
	if got == nil || want == nil {
		return got == nil && want == nil
	}
	return got.Equal(*want)
}
