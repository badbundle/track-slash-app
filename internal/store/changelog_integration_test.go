package store_test

import (
	"strings"
	"testing"

	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/store"
)

func TestProjectChangelogRecordsActorChangesAndPagination(t *testing.T) {
	t.Parallel()
	env := newSprintsEnv(t)
	project, err := env.store.GetProject(env.ctx, env.projectID)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	issue := mustCreateIssue(t, env, "original")

	renamed := "renamed"
	if _, err := env.store.UpdateIssue(store.WithActor(env.ctx, project.OwnerID), issue.ID, store.UpdateIssueParams{Title: &renamed}); err != nil {
		t.Fatalf("UpdateIssue rename: %v", err)
	}
	if _, err := env.store.UpdateIssue(store.WithActor(env.ctx, project.OwnerID), issue.ID, store.UpdateIssueParams{Title: &renamed}); err != nil {
		t.Fatalf("UpdateIssue no-op: %v", err)
	}

	entries, hasMore, err := env.store.ListProjectChangelog(env.ctx, store.ListProjectChangelogParams{
		ProjectID: env.projectID,
		Limit:     20,
	})
	if err != nil {
		t.Fatalf("ListProjectChangelog: %v", err)
	}
	if hasMore {
		t.Fatalf("hasMore = true, want false")
	}
	var updates, creates int
	var updateEntry model.ProjectChangelogEntry
	for _, entry := range entries {
		if strings.HasPrefix(entry.Summary, "Updated issue ") {
			updates++
			updateEntry = entry
			if entry.Actor == nil || entry.Actor.ID != project.OwnerID {
				t.Fatalf("update actor = %+v, want owner %s", entry.Actor, project.OwnerID)
			}
			if entry.TargetRef != issue.Identifier || entry.TargetTitle != renamed {
				t.Fatalf("update target = %s/%s, want %s/%s", entry.TargetRef, entry.TargetTitle, issue.Identifier, renamed)
			}
			if len(entry.Details.Changes) != 1 || entry.Details.Changes[0] != (model.ProjectChangelogChange{Field: "title", Label: "Title", From: "original", To: renamed}) {
				t.Fatalf("update changes = %+v", entry.Details.Changes)
			}
		}
		if strings.HasPrefix(entry.Summary, "Created issue ") {
			creates++
			if entry.Actor != nil || entry.ActorID != nil {
				t.Fatalf("create actor = %+v/%v, want nil", entry.Actor, entry.ActorID)
			}
		}
	}
	if updates != 1 {
		t.Fatalf("update entries = %d, want 1; entries=%+v", updates, entries)
	}
	gotProjectID, err := env.store.ProjectIDForProjectChangelog(env.ctx, updateEntry.ID)
	if err != nil {
		t.Fatalf("ProjectIDForProjectChangelog: %v", err)
	}
	if gotProjectID != env.projectID {
		t.Fatalf("ProjectIDForProjectChangelog = %s, want %s", gotProjectID, env.projectID)
	}
	if creates != 1 {
		t.Fatalf("create entries = %d, want 1; entries=%+v", creates, entries)
	}

	first, more, err := env.store.ListProjectChangelog(env.ctx, store.ListProjectChangelogParams{
		ProjectID: env.projectID,
		Limit:     1,
	})
	if err != nil {
		t.Fatalf("ListProjectChangelog page1: %v", err)
	}
	if len(first) != 1 || !more {
		t.Fatalf("page1 len/more = %d/%v, want 1/true", len(first), more)
	}
	second, _, err := env.store.ListProjectChangelog(env.ctx, store.ListProjectChangelogParams{
		ProjectID: env.projectID,
		Limit:     1,
		Cursor: &store.ProjectChangelogCursor{
			CreatedAt: first[0].CreatedAt,
			ID:        first[0].ID,
		},
	})
	if err != nil {
		t.Fatalf("ListProjectChangelog page2: %v", err)
	}
	if len(second) != 1 || second[0].ID == first[0].ID {
		t.Fatalf("page2 = %+v after page1 %+v", second, first)
	}
}

// Comment visibility changes, issue-scoped context lookups and the realtime
// trigger find a changelog entity's entries by (entity, entity_id) alone.
// With sequential scans ruled out, each of those lookups must reach the
// entries through the index made for them, not a scan of the table.
func TestProjectChangelogEntityLookupsUseIndex(t *testing.T) {
	t.Parallel()
	env := newSprintsEnv(t)
	tx, err := env.pool.Begin(env.ctx)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	defer tx.Rollback(env.ctx)
	if _, err := tx.Exec(env.ctx, `SET LOCAL enable_seqscan = off`); err != nil {
		t.Fatalf("disable seqscan: %v", err)
	}
	for _, q := range []string{
		`UPDATE project_changelog_entries SET members_only = true
		 WHERE entity = 'comment' AND entity_id = '00000000-0000-0000-0000-000000000001' AND members_only <> true`,
		`SELECT issue_id FROM project_changelog_entries
		 WHERE entity = 'project_context' AND entity_id = '00000000-0000-0000-0000-000000000001' AND issue_id IS NOT NULL`,
		`SELECT 1 FROM project_changelog_entries c JOIN issues i ON i.id = c.issue_id
		 WHERE c.entity = 'project_context' AND c.entity_id = '00000000-0000-0000-0000-000000000001' AND i.private`,
	} {
		rows, err := tx.Query(env.ctx, "EXPLAIN "+q)
		if err != nil {
			t.Fatalf("EXPLAIN: %v", err)
		}
		var plan strings.Builder
		for rows.Next() {
			var line string
			if err := rows.Scan(&line); err != nil {
				t.Fatalf("scan plan: %v", err)
			}
			plan.WriteString(line + "\n")
		}
		rows.Close()
		if !strings.Contains(plan.String(), "idx_project_changelog_entity") {
			t.Fatalf("plan for %q does not use the entity index:\n%s", q, plan.String())
		}
	}
}
