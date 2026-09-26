package store_test

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/store"
)

type insightFlowWant struct {
	todo, inProgress, done, cancelled int
}

type insightThroughputWant struct {
	created, resolved, reopened int
}

// Every timestamp is pinned so the day buckets are exact. The fixture walks
// one issue through start, completion, reopening, and completion again, and
// places events on bucket boundaries to prove a change at midnight lands in
// the day it starts.
func TestGetProjectInsightsFlowThroughputAndCycleTime(t *testing.T) {
	t.Parallel()
	env := newInsightsEnv(t)
	now := time.Date(2026, 4, 20, 12, 0, 0, 0, time.UTC)
	at := func(day, hour int) time.Time { return time.Date(2026, 4, day, hour, 0, 0, 0, time.UTC) }

	a := mustCreateIssue(t, env, "reopened then finished")
	setInsightIssueCreatedAt(t, env, a.ID, at(8, 10))
	setInsightIssueStatus(t, env, a.ID, model.StatusInProgress, at(9, 10))
	setInsightIssueStatus(t, env, a.ID, model.StatusDone, at(10, 10))
	setInsightIssueStatus(t, env, a.ID, model.StatusTodo, at(12, 10))
	setInsightIssueStatus(t, env, a.ID, model.StatusDone, at(14, 10))

	b := mustCreateIssue(t, env, "created at midnight then cancelled")
	setInsightIssueCreatedAt(t, env, b.ID, at(9, 0))
	setInsightIssueStatus(t, env, b.ID, model.StatusClosed, at(11, 23).Add(59*time.Minute+59*time.Second))

	c := mustCreateIssue(t, env, "older open issue")
	setInsightIssueCreatedAt(t, env, c.ID, at(1, 9))

	d := mustCreateIssue(t, env, "deleted issue")
	setInsightIssueCreatedAt(t, env, d.ID, at(15, 9))
	if err := env.store.DeleteIssue(env.ctx, d.ID); err != nil {
		t.Fatalf("DeleteIssue: %v", err)
	}

	e := mustCreateIssue(t, env, "started exactly at midnight")
	setInsightIssueCreatedAt(t, env, e.ID, at(16, 9))
	setInsightIssueStatus(t, env, e.ID, model.StatusInProgress, at(17, 0))

	f := mustCreateIssue(t, env, "finished without starting")
	setInsightIssueCreatedAt(t, env, f.ID, at(2, 9))
	setInsightIssueStatus(t, env, f.ID, model.StatusDone, at(13, 10))

	g := mustCreateIssue(t, env, "one day cycle")
	setInsightIssueCreatedAt(t, env, g.ID, at(1, 9))
	setInsightIssueStatus(t, env, g.ID, model.StatusInProgress, at(18, 0))
	setInsightIssueStatus(t, env, g.ID, model.StatusDone, at(19, 0))

	// A title edit is a changelog update without a status change; it must not
	// disturb the timeline.
	title := "one day cycle, renamed"
	if _, err := env.store.UpdateIssue(env.ctx, g.ID, store.UpdateIssueParams{Title: &title}); err != nil {
		t.Fatalf("UpdateIssue title: %v", err)
	}
	setLatestInsightEventAt(t, env, g.ID, "update", at(19, 6))

	insights, err := env.store.GetProjectInsights(env.ctx, store.ProjectInsightsParams{
		ProjectID: env.projectID,
		Range:     model.InsightRangeTwoWeeks,
		Now:       now,
	})
	if err != nil {
		t.Fatalf("GetProjectInsights: %v", err)
	}
	if insights.Range != model.InsightRangeTwoWeeks || insights.Bucket != model.InsightBucketDay ||
		!insights.Start.Equal(at(7, 0)) || !insights.End.Equal(now) || len(insights.Flow) != 14 || len(insights.Throughput) != 14 {
		t.Fatalf("insights window = range %s bucket %s start %s end %s flow %d throughput %d",
			insights.Range, insights.Bucket, insights.Start, insights.End, len(insights.Flow), len(insights.Throughput))
	}

	wantFlow := []insightFlowWant{
		{todo: 3},                                       // Apr 7: C, F, G
		{todo: 4},                                       // Apr 8: A created
		{todo: 4, inProgress: 1},                        // Apr 9: A started, B created at 00:00
		{todo: 4, done: 1},                              // Apr 10: A done
		{todo: 3, done: 1, cancelled: 1},                // Apr 11: B cancelled at 23:59:59
		{todo: 4, cancelled: 1},                         // Apr 12: A reopened
		{todo: 3, done: 1, cancelled: 1},                // Apr 13: F done without starting
		{todo: 2, done: 2, cancelled: 1},                // Apr 14: A done again
		{todo: 2, done: 2, cancelled: 1},                // Apr 15: D deleted, so never counted
		{todo: 3, done: 2, cancelled: 1},                // Apr 16: E created
		{todo: 2, inProgress: 1, done: 2, cancelled: 1}, // Apr 17: E started at 00:00
		{todo: 1, inProgress: 2, done: 2, cancelled: 1}, // Apr 18: G started at 00:00
		{todo: 1, inProgress: 1, done: 3, cancelled: 1}, // Apr 19: G done at 00:00
		{todo: 1, inProgress: 1, done: 3, cancelled: 1}, // Apr 20 as of noon
	}
	for i, want := range wantFlow {
		got := insights.Flow[i]
		if got.Todo != want.todo || got.InProgress != want.inProgress || got.Done != want.done || got.Cancelled != want.cancelled {
			t.Fatalf("flow[%d] (%s) = todo %d in progress %d done %d cancelled %d, want %+v",
				i, got.PeriodStart.Format("Jan 2"), got.Todo, got.InProgress, got.Done, got.Cancelled, want)
		}
		if got.Scope != want.todo+want.inProgress+want.done || got.Started != want.inProgress+want.done || got.Completed != want.done {
			t.Fatalf("flow[%d] burn-up = scope %d started %d completed %d", i, got.Scope, got.Started, got.Completed)
		}
		if !got.PeriodStart.Equal(at(7+i, 0)) {
			t.Fatalf("flow[%d] period start = %s", i, got.PeriodStart)
		}
	}
	if last := insights.Flow[len(insights.Flow)-1]; !last.PeriodEnd.Equal(now) {
		t.Fatalf("current period end = %s, want now", last.PeriodEnd)
	}

	wantThroughput := map[int]insightThroughputWant{
		8:  {created: 1},
		9:  {created: 1},
		10: {resolved: 1},
		11: {resolved: 1},
		12: {reopened: 1},
		13: {resolved: 1},
		14: {resolved: 1},
		16: {created: 1},
		19: {resolved: 1},
	}
	for i, got := range insights.Throughput {
		want := wantThroughput[7+i]
		if got.Created != want.created || got.Resolved != want.resolved || got.Reopened != want.reopened {
			t.Fatalf("throughput Apr %d = %+v, want %+v", 7+i, got, want)
		}
	}

	cycle := insights.CycleTime
	if len(cycle.Issues) != 2 || cycle.NotStarted != 1 || cycle.Truncated {
		t.Fatalf("cycle time = %+v", cycle)
	}
	first, second := cycle.Issues[0], cycle.Issues[1]
	if first.IssueID != a.ID || first.Identifier != a.Identifier || first.Number != a.Number || first.Title != a.Title ||
		!first.StartedAt.Equal(at(9, 10)) || !first.CompletedAt.Equal(at(14, 10)) || first.DurationHours != 120 {
		t.Fatalf("reopened cycle = %+v", first)
	}
	if second.IssueID != g.ID || second.DurationHours != 24 || second.Title != title {
		t.Fatalf("one-day cycle = %+v", second)
	}
	if cycle.MedianHours != 72 || math.Abs(cycle.P85Hours-105.6) > 1e-9 {
		t.Fatalf("cycle percentiles = median %v p85 %v", cycle.MedianHours, cycle.P85Hours)
	}
	if insights.Sprints.Count != 0 || len(insights.Sprints.Velocity) != 0 || len(insights.Sprints.Options) != 0 || insights.Sprints.Burnup != nil {
		t.Fatalf("sprints without sprints = %+v", insights.Sprints)
	}
}

func TestGetProjectInsightsSprintVelocityAndBurnup(t *testing.T) {
	t.Parallel()
	env := newInsightsEnv(t)
	now := time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC)
	at := func(day, hour int) time.Time { return time.Date(2026, 3, day, hour, 0, 0, 0, time.UTC) }

	first := mustCreateSprint(t, env, "First", date(2026, 3, 2), date(2026, 3, 6))
	second := mustCreateSprint(t, env, "Second", date(2026, 3, 7), date(2026, 3, 12))
	x := mustCreateIssue(t, env, "committed and done")
	y := mustCreateIssue(t, env, "committed and carried")
	z := mustCreateIssue(t, env, "added mid-sprint")
	for _, issue := range []model.Issue{x, y, z} {
		setInsightIssueCreatedAt(t, env, issue.ID, at(1, 8))
	}
	assignIssueToSprint(t, env, x.ID, first.ID)
	assignIssueToSprint(t, env, y.ID, first.ID)
	setInsightMembershipTimes(t, env, first.ID, at(1, 9), nil)
	mustActivate(t, env, first.ID)
	setLatestSprintEventAt(t, env, first.ID, at(2, 9))
	assignIssueToSprint(t, env, z.ID, first.ID)
	setInsightMembershipTimesFor(t, env, first.ID, z.ID, at(3, 12), nil)
	setInsightIssueStatus(t, env, x.ID, model.StatusDone, at(4, 10))
	if _, err := env.store.CompleteSprint(env.ctx, first.ID); err != nil {
		t.Fatalf("CompleteSprint: %v", err)
	}
	completedAt := at(6, 15)
	if _, err := env.pool.Exec(env.ctx, `UPDATE sprints SET completed_at = $1 WHERE id = $2`, completedAt, first.ID); err != nil {
		t.Fatalf("pin completed_at: %v", err)
	}
	for _, issue := range []model.Issue{y, z} {
		setInsightMembershipTimesFor(t, env, first.ID, issue.ID, membershipAddedAt(t, env, first.ID, issue.ID), &completedAt)
		setInsightMembershipTimesFor(t, env, second.ID, issue.ID, completedAt, nil)
	}
	mustActivate(t, env, second.ID)
	setLatestSprintEventAt(t, env, second.ID, at(7, 9))

	insights, err := env.store.GetProjectInsights(env.ctx, store.ProjectInsightsParams{
		ProjectID: env.projectID,
		Range:     model.InsightRangeThirtyDays,
		Now:       now,
	})
	if err != nil {
		t.Fatalf("GetProjectInsights: %v", err)
	}
	sprints := insights.Sprints
	if sprints.Count != 2 || len(sprints.Velocity) != 1 {
		t.Fatalf("sprints = %+v", sprints)
	}
	v := sprints.Velocity[0]
	if v.SprintID != first.ID || v.Ref != first.Ref || v.Name != "First" || !v.CompletedAt.Equal(completedAt) ||
		v.StartedAt == nil || !v.StartedAt.Equal(at(2, 9)) || v.Committed == nil || *v.Committed != 2 ||
		v.Total != 3 || v.Done != 1 || v.Cancelled != 0 || v.CarriedOver != 2 {
		t.Fatalf("velocity = %+v committed=%v", v, v.Committed)
	}
	if len(sprints.Options) != 2 || sprints.Options[0].SprintID != second.ID || sprints.Options[0].Status != model.SprintStatusActive ||
		sprints.Options[1].SprintID != first.ID || sprints.Options[1].Status != model.SprintStatusCompleted {
		t.Fatalf("sprint options = %+v", sprints.Options)
	}

	active := sprints.Burnup
	if active == nil || active.SprintID != second.ID || active.Status != model.SprintStatusActive || active.Estimated ||
		!active.Start.Equal(at(7, 0)) || !active.End.Equal(at(13, 0)) || active.Days != 6 || len(active.Points) != 3 {
		t.Fatalf("active burn-up = %+v", active)
	}
	for i, point := range active.Points {
		if point.Scope != 2 || point.Completed != 0 {
			t.Fatalf("active burn-up point %d = %+v", i, point)
		}
	}
	if !active.Points[2].PeriodEnd.Equal(now) {
		t.Fatalf("active burn-up last point end = %s", active.Points[2].PeriodEnd)
	}

	insights, err = env.store.GetProjectInsights(env.ctx, store.ProjectInsightsParams{
		ProjectID: env.projectID,
		Range:     model.InsightRangeThirtyDays,
		SprintID:  &first.ID,
		Now:       now,
	})
	if err != nil {
		t.Fatalf("GetProjectInsights first sprint: %v", err)
	}
	done := insights.Sprints.Burnup
	if done == nil || done.SprintID != first.ID || done.Status != model.SprintStatusCompleted ||
		!done.Start.Equal(at(2, 0)) || !done.End.Equal(at(7, 0)) || done.Days != 5 || len(done.Points) != 5 {
		t.Fatalf("completed burn-up = %+v", done)
	}
	for i, want := range []struct{ scope, started, completed int }{
		{scope: 2},                           // Mar 2: X and Y committed
		{scope: 3},                           // Mar 3: Z added at noon
		{scope: 3, started: 1, completed: 1}, // Mar 4: X done
		{scope: 3, started: 1, completed: 1}, // Mar 5
		{scope: 3, started: 1, completed: 1}, // Mar 6 at completion: carried issues still count
	} {
		got := done.Points[i]
		if got.Scope != want.scope || got.Started != want.started || got.Completed != want.completed {
			t.Fatalf("completed burn-up point %d = %+v, want %+v", i, got, want)
		}
	}
	if !done.Points[4].PeriodEnd.Equal(completedAt) {
		t.Fatalf("completed burn-up last point end = %s", done.Points[4].PeriodEnd)
	}

	// Membership rows the migration had to estimate make commitment unknown.
	if _, err := env.pool.Exec(env.ctx, `UPDATE sprint_issue_memberships SET backfilled = true WHERE sprint_id = $1`, first.ID); err != nil {
		t.Fatalf("mark backfilled: %v", err)
	}
	insights, err = env.store.GetProjectInsights(env.ctx, store.ProjectInsightsParams{
		ProjectID: env.projectID,
		Range:     model.InsightRangeThirtyDays,
		SprintID:  &first.ID,
		Now:       now,
	})
	if err != nil {
		t.Fatalf("GetProjectInsights backfilled: %v", err)
	}
	if insights.Sprints.Velocity[0].Committed != nil || !insights.Sprints.Burnup.Estimated {
		t.Fatalf("backfilled sprint = committed %v estimated %v", insights.Sprints.Velocity[0].Committed, insights.Sprints.Burnup.Estimated)
	}
}

func TestGetProjectInsightsSprintFallbacks(t *testing.T) {
	t.Parallel()
	env := newInsightsEnv(t)
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)

	planned := mustCreateSprint(t, env, "Planned", date(2026, 5, 25), date(2026, 5, 29))
	insights, err := env.store.GetProjectInsights(env.ctx, store.ProjectInsightsParams{ProjectID: env.projectID, Now: now})
	if err != nil {
		t.Fatalf("GetProjectInsights planned only: %v", err)
	}
	if insights.Range != model.DefaultInsightRange || insights.Sprints.Count != 1 || len(insights.Sprints.Options) != 0 || insights.Sprints.Burnup != nil {
		t.Fatalf("planned-only sprints = %+v", insights.Sprints)
	}
	if _, err := env.store.GetProjectInsights(env.ctx, store.ProjectInsightsParams{ProjectID: env.projectID, SprintID: &planned.ID, Now: now}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("planned sprint burn-up err = %v, want ErrNotFound", err)
	}

	// A sprint activated before the changelog existed has no activation row:
	// commitment is unknown and the burn-up starts on its planned start date.
	undated := mustCreateSprint(t, env, "Legacy", date(2026, 5, 11), date(2026, 5, 15))
	mustActivate(t, env, undated.ID)
	if _, err := env.pool.Exec(env.ctx, `DELETE FROM project_changelog_entries WHERE entity = 'sprint' AND entity_id = $1 AND op = 'update'`, undated.ID); err != nil {
		t.Fatalf("drop activation row: %v", err)
	}
	if _, err := env.store.CompleteSprint(env.ctx, undated.ID); err != nil {
		t.Fatalf("CompleteSprint: %v", err)
	}
	if _, err := env.pool.Exec(env.ctx, `UPDATE sprints SET completed_at = $1 WHERE id = $2`, time.Date(2026, 5, 15, 17, 0, 0, 0, time.UTC), undated.ID); err != nil {
		t.Fatalf("pin completed_at: %v", err)
	}
	insights, err = env.store.GetProjectInsights(env.ctx, store.ProjectInsightsParams{ProjectID: env.projectID, Range: model.InsightRangeTwoWeeks, Now: now})
	if err != nil {
		t.Fatalf("GetProjectInsights legacy: %v", err)
	}
	if len(insights.Sprints.Velocity) != 1 || insights.Sprints.Velocity[0].Committed != nil || insights.Sprints.Velocity[0].StartedAt != nil {
		t.Fatalf("legacy velocity = %+v", insights.Sprints.Velocity)
	}
	burnup := insights.Sprints.Burnup
	if burnup == nil || burnup.SprintID != undated.ID || !burnup.Start.Equal(date(2026, 5, 11)) || burnup.Days != 5 || len(burnup.Points) != 5 {
		t.Fatalf("legacy burn-up = %+v", burnup)
	}

	// Outside the range, the completed sprint drops out of velocity and no
	// burn-up is chosen by default.
	insights, err = env.store.GetProjectInsights(env.ctx, store.ProjectInsightsParams{
		ProjectID: env.projectID,
		Range:     model.InsightRangeTwoWeeks,
		Now:       now.AddDate(0, 1, 0),
	})
	if err != nil {
		t.Fatalf("GetProjectInsights later: %v", err)
	}
	if len(insights.Sprints.Velocity) != 0 || insights.Sprints.Burnup != nil || insights.Sprints.Count != 2 {
		t.Fatalf("later sprints = %+v", insights.Sprints)
	}

	// An active sprint whose planned start is still ahead has a window but
	// no points yet.
	mustActivate(t, env, planned.ID)
	if _, err := env.pool.Exec(env.ctx, `DELETE FROM project_changelog_entries WHERE entity = 'sprint' AND entity_id = $1 AND op = 'update'`, planned.ID); err != nil {
		t.Fatalf("drop activation row: %v", err)
	}
	insights, err = env.store.GetProjectInsights(env.ctx, store.ProjectInsightsParams{ProjectID: env.projectID, Now: now})
	if err != nil {
		t.Fatalf("GetProjectInsights future sprint: %v", err)
	}
	future := insights.Sprints.Burnup
	if future == nil || future.SprintID != planned.ID || future.Status != model.SprintStatusActive || len(future.Points) != 0 ||
		!future.Start.Equal(date(2026, 5, 25)) || future.Days != 5 {
		t.Fatalf("future sprint burn-up = %+v", future)
	}
}

func TestGetProjectInsightsCycleTimeTruncates(t *testing.T) {
	t.Parallel()
	env := newInsightsEnv(t)
	now := time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC)
	var key string
	if err := env.pool.QueryRow(env.ctx, `SELECT key FROM projects WHERE id = $1`, env.projectID).Scan(&key); err != nil {
		t.Fatalf("project key: %v", err)
	}
	// Bulk rows keep this fast: 501 Done issues, each started an hour before
	// it finished, all inside the last two weeks.
	if _, err := env.pool.Exec(env.ctx, `
		WITH created AS (
			INSERT INTO issues (project_id, number, title, status, created_at)
			SELECT $1, n, 'bulk ' || n, 'done', $2::timestamptz - interval '10 days'
			FROM generate_series(1, 501) AS n
			RETURNING id, number
		)
		INSERT INTO project_changelog_entries (project_id, entity, op, entity_id, issue_id, summary, details, created_at)
		SELECT $1, 'issue', 'update', c.id, c.id, 'Updated issue',
		       jsonb_build_object('changes', jsonb_build_array(jsonb_build_object('field', 'status', 'label', 'Status', 'from', s.from_label, 'to', s.to_label))),
		       $2::timestamptz - interval '5 days' + (c.number * interval '1 minute') + s.offset_hours * interval '1 hour'
		FROM created c
		CROSS JOIN (VALUES ('To do', 'In progress', 0), ('In progress', 'Done', 1)) AS s(from_label, to_label, offset_hours)
	`, env.projectID, now); err != nil {
		t.Fatalf("bulk insert: %v", err)
	}
	insights, err := env.store.GetProjectInsights(env.ctx, store.ProjectInsightsParams{ProjectID: env.projectID, Range: model.InsightRangeTwoWeeks, Now: now})
	if err != nil {
		t.Fatalf("GetProjectInsights: %v", err)
	}
	cycle := insights.CycleTime
	if !cycle.Truncated || len(cycle.Issues) != 500 || cycle.MedianHours != 1 || cycle.P85Hours != 1 {
		t.Fatalf("truncated cycle = truncated %v issues %d median %v p85 %v", cycle.Truncated, len(cycle.Issues), cycle.MedianHours, cycle.P85Hours)
	}
	if first := cycle.Issues[0]; first.Number != 2 || first.Identifier != key+"-2" {
		t.Fatalf("oldest kept issue = %+v, want the second-oldest completion", first)
	}
}

func TestGetProjectInsightsAllRangeAndErrors(t *testing.T) {
	t.Parallel()
	env := newInsightsEnv(t)
	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	issue := mustCreateIssue(t, env, "old issue")
	setInsightIssueCreatedAt(t, env, issue.ID, time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC))

	insights, err := env.store.GetProjectInsights(env.ctx, store.ProjectInsightsParams{ProjectID: env.projectID, Range: model.InsightRangeAll, Now: now})
	if err != nil {
		t.Fatalf("GetProjectInsights all: %v", err)
	}
	if insights.Bucket != model.InsightBucketWeek || !insights.Start.Equal(time.Date(2026, 6, 29, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("all range = bucket %s start %s", insights.Bucket, insights.Start)
	}
	if first := insights.Flow[0]; first.Todo != 1 || insights.Throughput[0].Created != 1 {
		t.Fatalf("all range first period = %+v %+v", first, insights.Throughput[0])
	}

	if _, err := env.store.GetProjectInsights(env.ctx, store.ProjectInsightsParams{ProjectID: env.projectID, Range: "7d", Now: now}); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("invalid range err = %v, want ErrConflict", err)
	}
	if _, err := env.store.GetProjectInsights(env.ctx, store.ProjectInsightsParams{ProjectID: uuid.New()}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing project err = %v, want ErrNotFound", err)
	}
	missing := uuid.New()
	if _, err := env.store.GetProjectInsights(env.ctx, store.ProjectInsightsParams{ProjectID: env.projectID, SprintID: &missing, Now: now}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("sprint without sprints err = %v, want ErrNotFound", err)
	}
}

// The default clock path is only exercised on an empty project, so a skewed
// database clock cannot move the result.
func TestGetProjectInsightsDefaultClockEmptyProject(t *testing.T) {
	t.Parallel()
	env := newInsightsEnv(t)
	insights, err := env.store.GetProjectInsights(env.ctx, store.ProjectInsightsParams{ProjectID: env.projectID})
	if err != nil {
		t.Fatalf("GetProjectInsights: %v", err)
	}
	if insights.End.IsZero() || len(insights.Flow) == 0 || len(insights.CycleTime.Issues) != 0 {
		t.Fatalf("empty insights = %+v", insights)
	}
	for i, point := range insights.Flow {
		if point.Scope != 0 || insights.Throughput[i].Created != 0 {
			t.Fatalf("empty point %d = %+v", i, point)
		}
	}
}

// newInsightsEnv gives insight fixtures, which pin many timestamps, more room
// than the shared 30 second budget on a loaded machine.
func newInsightsEnv(t *testing.T) *sprintsTestEnv {
	t.Helper()
	env := newSprintsEnv(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)
	env.ctx = ctx
	return env
}

func setInsightIssueCreatedAt(t *testing.T, env *sprintsTestEnv, issueID uuid.UUID, at time.Time) {
	t.Helper()
	if _, err := env.pool.Exec(env.ctx, `UPDATE issues SET created_at = $1 WHERE id = $2`, at, issueID); err != nil {
		t.Fatalf("set issue created_at: %v", err)
	}
}

func setInsightIssueStatus(t *testing.T, env *sprintsTestEnv, issueID uuid.UUID, status model.Status, at time.Time) {
	t.Helper()
	setIssueStatus(t, env, issueID, status)
	setLatestInsightEventAt(t, env, issueID, "update", at)
}

func setLatestInsightEventAt(t *testing.T, env *sprintsTestEnv, issueID uuid.UUID, op string, at time.Time) {
	t.Helper()
	tag, err := env.pool.Exec(env.ctx, `
		UPDATE project_changelog_entries SET created_at = $3
		WHERE id = (
			SELECT id FROM project_changelog_entries
			WHERE issue_id = $1 AND op = $2 AND created_at > now() - interval '1 hour'
			ORDER BY created_at DESC, id DESC
			LIMIT 1
		)
	`, issueID, op, at)
	if err != nil || tag.RowsAffected() != 1 {
		t.Fatalf("pin latest %s event: rows %d err %v", op, tag.RowsAffected(), err)
	}
}

func setLatestSprintEventAt(t *testing.T, env *sprintsTestEnv, sprintID uuid.UUID, at time.Time) {
	t.Helper()
	tag, err := env.pool.Exec(env.ctx, `
		UPDATE project_changelog_entries SET created_at = $2
		WHERE id = (
			SELECT id FROM project_changelog_entries
			WHERE entity = 'sprint' AND entity_id = $1 AND op = 'update'
			ORDER BY created_at DESC, id DESC
			LIMIT 1
		)
	`, sprintID, at)
	if err != nil || tag.RowsAffected() != 1 {
		t.Fatalf("pin sprint event: rows %d err %v", tag.RowsAffected(), err)
	}
}

func setInsightMembershipTimes(t *testing.T, env *sprintsTestEnv, sprintID uuid.UUID, added time.Time, removed *time.Time) {
	t.Helper()
	if _, err := env.pool.Exec(env.ctx, `
		UPDATE sprint_issue_memberships SET added_at = $2, removed_at = $3 WHERE sprint_id = $1
	`, sprintID, added, removed); err != nil {
		t.Fatalf("pin memberships: %v", err)
	}
}

func setInsightMembershipTimesFor(t *testing.T, env *sprintsTestEnv, sprintID, issueID uuid.UUID, added time.Time, removed *time.Time) {
	t.Helper()
	tag, err := env.pool.Exec(env.ctx, `
		UPDATE sprint_issue_memberships SET added_at = $3, removed_at = $4 WHERE sprint_id = $1 AND issue_id = $2
	`, sprintID, issueID, added, removed)
	if err != nil || tag.RowsAffected() != 1 {
		t.Fatalf("pin membership: rows %d err %v", tag.RowsAffected(), err)
	}
}

func membershipAddedAt(t *testing.T, env *sprintsTestEnv, sprintID, issueID uuid.UUID) time.Time {
	t.Helper()
	var added time.Time
	if err := env.pool.QueryRow(env.ctx, `
		SELECT added_at FROM sprint_issue_memberships WHERE sprint_id = $1 AND issue_id = $2
	`, sprintID, issueID).Scan(&added); err != nil {
		t.Fatalf("membership added_at: %v", err)
	}
	return added
}
