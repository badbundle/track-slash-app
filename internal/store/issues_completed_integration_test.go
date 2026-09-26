package store_test

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/store"
)

func TestListRecentlyCompletedIssues(t *testing.T) {
	t.Parallel()
	env := newInsightsEnv(t)
	now := time.Now().UTC().Truncate(time.Second)
	since := now.Add(-7 * 24 * time.Hour)

	recent := mustCreateIssue(t, env, "Done yesterday")
	setInsightIssueStatus(t, env, recent.ID, model.StatusDone, now.Add(-24*time.Hour))

	newest := mustCreateIssue(t, env, "Done an hour ago")
	setInsightIssueStatus(t, env, newest.ID, model.StatusDone, now.Add(-time.Hour))

	// Reopened and finished again: the last move to Done is what counts.
	reopened := mustCreateIssue(t, env, "Reopened")
	setInsightIssueStatus(t, env, reopened.ID, model.StatusDone, now.Add(-20*24*time.Hour))
	setIssueStatus(t, env, reopened.ID, model.StatusInProgress)
	setInsightIssueStatus(t, env, reopened.ID, model.StatusDone, now.Add(-3*24*time.Hour))

	// Finished long ago but edited since: updated_at is recent, the completion
	// is not.
	old := mustCreateIssue(t, env, "Done weeks ago")
	setInsightIssueStatus(t, env, old.ID, model.StatusDone, now.Add(-20*24*time.Hour))
	title := "Done weeks ago, retitled"
	if _, err := env.store.UpdateIssue(env.ctx, old.ID, store.UpdateIssueParams{Title: &title}); err != nil {
		t.Fatalf("retitle: %v", err)
	}

	// Closing counts as completing, whether by hand or automatically when the
	// issue is marked a duplicate.
	wontDo := mustCreateIssue(t, env, "Won't do")
	setInsightIssueStatus(t, env, wontDo.ID, model.StatusClosed, now.Add(-5*time.Hour))
	duplicate := mustCreateIssue(t, env, "Duplicate report")
	original := mustCreateIssue(t, env, "Original report")
	if _, err := env.store.CreateIssueLink(env.ctx, store.CreateIssueLinkParams{SourceID: duplicate.ID, TargetID: original.ID, LinkType: model.LinkTypeDuplicates}); err != nil {
		t.Fatalf("CreateIssueLink: %v", err)
	}
	setLatestInsightEventAt(t, env, duplicate.ID, "update", now.Add(-6*time.Hour))
	closedLongAgo := mustCreateIssue(t, env, "Closed weeks ago")
	setInsightIssueStatus(t, env, closedLongAgo.ID, model.StatusClosed, now.Add(-20*24*time.Hour))

	// Rows written without a changelog entry fall back to their creation time.
	unrecordedRecent := mustCreateIssue(t, env, "Imported recently")
	unrecordedOld := mustCreateIssue(t, env, "Imported long ago")
	for _, tc := range []struct {
		id      uuid.UUID
		created time.Time
	}{
		{id: unrecordedRecent.ID, created: now.Add(-2 * 24 * time.Hour)},
		{id: unrecordedOld.ID, created: now.Add(-40 * 24 * time.Hour)},
	} {
		if _, err := env.pool.Exec(env.ctx, `UPDATE issues SET status = 'done', created_at = $2 WHERE id = $1`, tc.id, tc.created); err != nil {
			t.Fatalf("write unrecorded done issue: %v", err)
		}
	}

	// None of these are recently completed top-level issues.
	inProgress := mustCreateIssue(t, env, "Still going")
	setIssueStatus(t, env, inProgress.ID, model.StatusInProgress)
	deleted := mustCreateIssue(t, env, "Done then deleted")
	setIssueStatus(t, env, deleted.ID, model.StatusDone)
	if err := env.store.DeleteIssue(env.ctx, deleted.ID); err != nil {
		t.Fatalf("DeleteIssue: %v", err)
	}
	sub, err := env.store.CreateSubIssue(env.ctx, store.CreateSubIssueParams{ParentIssueID: inProgress.ID, Title: "Sub-issue"})
	if err != nil {
		t.Fatalf("CreateSubIssue: %v", err)
	}
	setIssueStatus(t, env, sub.ID, model.StatusDone)

	got, hasMore, err := env.store.ListRecentlyCompletedIssues(env.ctx, store.ListRecentlyCompletedIssuesParams{
		ProjectID: env.projectID,
		Since:     since,
		Limit:     10,
	})
	if err != nil {
		t.Fatalf("ListRecentlyCompletedIssues: %v", err)
	}
	if hasMore {
		t.Fatal("hasMore = true with room to spare")
	}
	want := []struct {
		id          uuid.UUID
		completedAt time.Time
	}{
		{id: newest.ID, completedAt: now.Add(-time.Hour)},
		{id: wontDo.ID, completedAt: now.Add(-5 * time.Hour)},
		{id: duplicate.ID, completedAt: now.Add(-6 * time.Hour)},
		{id: recent.ID, completedAt: now.Add(-24 * time.Hour)},
		{id: unrecordedRecent.ID, completedAt: now.Add(-2 * 24 * time.Hour)},
		{id: reopened.ID, completedAt: now.Add(-3 * 24 * time.Hour)},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d issues, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i].ID != w.id || !got[i].CompletedAt.Equal(w.completedAt) {
			t.Fatalf("issue %d = %s completed %s, want %s completed %s", i, got[i].Identifier, got[i].CompletedAt, w.id, w.completedAt)
		}
		if !got[i].Status.CountsAsDone() || got[i].Identifier == "" || got[i].Tags == nil {
			t.Fatalf("issue %d not fully loaded: %+v", i, got[i])
		}
	}

	page, hasMore, err := env.store.ListRecentlyCompletedIssues(env.ctx, store.ListRecentlyCompletedIssuesParams{
		ProjectID: env.projectID,
		Since:     since,
		Limit:     2,
	})
	if err != nil {
		t.Fatalf("ListRecentlyCompletedIssues limited: %v", err)
	}
	if !hasMore || len(page) != 2 || page[0].ID != newest.ID || page[1].ID != wontDo.ID {
		t.Fatalf("limited page = %+v hasMore %t, want the two newest and more", page, hasMore)
	}

	narrow, _, err := env.store.ListRecentlyCompletedIssues(env.ctx, store.ListRecentlyCompletedIssuesParams{
		ProjectID: env.projectID,
		Since:     now.Add(-2 * time.Hour),
		Limit:     10,
	})
	if err != nil {
		t.Fatalf("ListRecentlyCompletedIssues narrow: %v", err)
	}
	if len(narrow) != 1 || narrow[0].ID != newest.ID {
		t.Fatalf("two-hour window = %+v, want only the issue done an hour ago", narrow)
	}
}
