package store_test

import (
	"errors"
	"testing"
	"time"

	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/store"
	"github.com/google/uuid"
)

func TestGetProjectCompletionHistoryReplaysLifecycleEvents(t *testing.T) {
	t.Parallel()
	env := newSprintsEnv(t)
	now := time.Date(2026, 4, 20, 12, 0, 0, 0, time.UTC)
	start := time.Date(2026, 2, 2, 0, 0, 0, 0, time.UTC)

	issueA := mustCreateIssue(t, env, "completion history A")
	setCompletionIssueCreatedAt(t, env, issueA.ID, start)
	setCompletionIssueStatus(t, env, issueA.ID, model.StatusDone, start.AddDate(0, 0, 7))
	setCompletionIssueStatus(t, env, issueA.ID, model.StatusTodo, start.AddDate(0, 0, 14))

	issueB := mustCreateIssue(t, env, "completion history B")
	setCompletionIssueCreatedAt(t, env, issueB.ID, start.AddDate(0, 0, 14))
	setCompletionIssueStatus(t, env, issueA.ID, model.StatusDone, start.AddDate(0, 0, 21))
	if err := env.store.DeleteIssue(env.ctx, issueB.ID); err != nil {
		t.Fatalf("DeleteIssue: %v", err)
	}
	setLatestCompletionEventAt(t, env, issueB.ID, "delete", start.AddDate(0, 0, 28))
	if _, err := env.store.RestoreIssue(env.ctx, issueB.ID); err != nil {
		t.Fatalf("RestoreIssue: %v", err)
	}
	setLatestCompletionEventAt(t, env, issueB.ID, "restore", start.AddDate(0, 0, 35))

	history, err := env.store.GetProjectCompletionHistory(env.ctx, store.ProjectCompletionHistoryParams{ProjectID: env.projectID, Now: now})
	if err != nil {
		t.Fatalf("GetProjectCompletionHistory: %v", err)
	}
	if history.ProjectID != env.projectID || !history.Start.Equal(start) || !history.End.Equal(now) || len(history.Points) != 12 {
		t.Fatalf("history metadata = %+v", history)
	}
	for i, want := range []struct {
		total     int
		completed int
		rate      float64
	}{
		{total: 1, completed: 0, rate: 0},
		{total: 1, completed: 1, rate: 100},
		{total: 2, completed: 0, rate: 0},
		{total: 2, completed: 1, rate: 50},
		{total: 1, completed: 1, rate: 100},
		{total: 2, completed: 1, rate: 50},
	} {
		got := history.Points[i]
		if got.Total != want.total || got.Completed != want.completed || got.Rate != want.rate {
			t.Fatalf("point %d = %+v, want total=%d completed=%d rate=%.0f", i, got, want.total, want.completed, want.rate)
		}
	}
	last := history.Points[len(history.Points)-1]
	if last.Total != 2 || last.Completed != 1 || last.Rate != 50 || !last.AsOf.Equal(now) {
		t.Fatalf("current point = %+v", last)
	}
}

// This is the one test that exercises the default-clock path, and it holds no
// issues on purpose: with nothing to count, a skewed database clock cannot move
// the result. Adding an issue here would reintroduce that flake.
func TestGetProjectCompletionHistoryEmptyDefaultAndMissingProject(t *testing.T) {
	t.Parallel()
	env := newSprintsEnv(t)
	history, err := env.store.GetProjectCompletionHistory(env.ctx, store.ProjectCompletionHistoryParams{ProjectID: env.projectID})
	if err != nil {
		t.Fatalf("GetProjectCompletionHistory: %v", err)
	}
	if len(history.Points) != 12 || history.End.IsZero() {
		t.Fatalf("default history = %+v", history)
	}
	for i, point := range history.Points {
		if point.Total != 0 || point.Completed != 0 || point.Rate != 0 {
			t.Fatalf("empty point %d = %+v", i, point)
		}
	}
	if _, err := env.store.GetProjectCompletionHistory(env.ctx, store.ProjectCompletionHistoryParams{ProjectID: uuid.New()}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing project err = %v, want ErrNotFound", err)
	}
}

// Every timestamp here is pinned. Letting the issue keep its Postgres-written
// created_at while the history defaulted to the Go process clock made this fail
// whenever the database ran even microseconds ahead: the issue then sorted after
// the final point's AsOf and was dropped from the count entirely.
func TestGetProjectCompletionHistoryCountsClosedAsCompleted(t *testing.T) {
	t.Parallel()
	env := newSprintsEnv(t)
	now := time.Date(2026, 4, 20, 12, 0, 0, 0, time.UTC)
	created := now.AddDate(0, 0, -14)

	issue := mustCreateIssue(t, env, "closed completion history")
	setCompletionIssueCreatedAt(t, env, issue.ID, created)
	closed := model.StatusClosed
	reason := model.CloseReasonWontDo
	if _, err := env.store.UpdateIssue(env.ctx, issue.ID, store.UpdateIssueParams{Status: &closed, CloseReason: &reason}); err != nil {
		t.Fatalf("UpdateIssue closed: %v", err)
	}
	setLatestCompletionEventAt(t, env, issue.ID, "update", created.AddDate(0, 0, 7))

	history, err := env.store.GetProjectCompletionHistory(env.ctx, store.ProjectCompletionHistoryParams{ProjectID: env.projectID, Now: now})
	if err != nil {
		t.Fatalf("GetProjectCompletionHistory: %v", err)
	}
	last := history.Points[len(history.Points)-1]
	if last.Total != 1 || last.Completed != 1 || last.Rate != 100 {
		t.Fatalf("closed current point = %+v", last)
	}
}

func setCompletionIssueCreatedAt(t *testing.T, env *sprintsTestEnv, issueID uuid.UUID, at time.Time) {
	t.Helper()
	if _, err := env.pool.Exec(env.ctx, `UPDATE issues SET created_at = $1 WHERE id = $2`, at, issueID); err != nil {
		t.Fatalf("set issue created_at: %v", err)
	}
}

func setCompletionIssueStatus(t *testing.T, env *sprintsTestEnv, issueID uuid.UUID, status model.Status, at time.Time) {
	t.Helper()
	if _, err := env.store.UpdateIssue(env.ctx, issueID, store.UpdateIssueParams{Status: &status}); err != nil {
		t.Fatalf("UpdateIssue %s: %v", status, err)
	}
	setLatestCompletionEventAt(t, env, issueID, "update", at)
}

func setLatestCompletionEventAt(t *testing.T, env *sprintsTestEnv, issueID uuid.UUID, op string, at time.Time) {
	t.Helper()
	var eventID uuid.UUID
	if err := env.pool.QueryRow(env.ctx, `
		SELECT id FROM project_changelog_entries
		WHERE issue_id = $1 AND op = $2
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`, issueID, op).Scan(&eventID); err != nil {
		t.Fatalf("find latest %s event: %v", op, err)
	}
	if _, err := env.pool.Exec(env.ctx, `UPDATE project_changelog_entries SET created_at = $1 WHERE id = $2`, at, eventID); err != nil {
		t.Fatalf("set %s event created_at: %v", op, err)
	}
}
