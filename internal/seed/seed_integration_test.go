package seed_test

import (
	"context"
	"testing"
	"time"

	"github.com/bradleymackey/track-slash/internal/seed"
	"github.com/bradleymackey/track-slash/internal/store"
	"github.com/bradleymackey/track-slash/internal/testutil"
)

func TestSeedCreatesSubIssues(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	db := testutil.NewMigratedDatabase(t)
	st := store.New(db.Pool)

	summary, err := seed.Run(ctx, st, seed.Options{
		Username:      "seedsub" + time.Now().Format("150405000000"),
		Password:      "correct-horse-battery",
		Name:          "Seed Sub-Issues",
		ProjectPrefix: "S" + time.Now().Format("04050"),
		Now:           time.Date(2026, 6, 6, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("seed.Run: %v", err)
	}

	totalSubIssues := 0
	for _, project := range summary.Projects {
		if !project.Seeded {
			t.Fatalf("project %s was not seeded: %+v", project.Project.Key, project)
		}
		if project.SubIssuesCreated == 0 {
			t.Fatalf("project %s SubIssuesCreated = 0", project.Project.Key)
		}
		// Demo projects seed an active sprint, so they run in sprint mode.
		stored, err := st.GetProject(ctx, project.Project.ID)
		if err != nil {
			t.Fatalf("GetProject %s: %v", project.Project.Key, err)
		}
		if !project.Project.SprintsEnabled || !stored.SprintsEnabled {
			t.Fatalf("project %s sprints enabled = summary %t, stored %t; want both true", project.Project.Key, project.Project.SprintsEnabled, stored.SprintsEnabled)
		}
		totalSubIssues += project.SubIssuesCreated

		all, _, err := st.ListIssues(ctx, store.ListIssuesParams{
			ProjectID:        project.Project.ID,
			Limit:            200,
			IncludeSubIssues: true,
		})
		if err != nil {
			t.Fatalf("ListIssues include sub-issues: %v", err)
		}
		foundChild := false
		for _, issue := range all {
			if issue.ParentIssueID != nil {
				foundChild = true
				break
			}
		}
		if !foundChild {
			t.Fatalf("project %s had no persisted child issues", project.Project.Key)
		}
	}
	if totalSubIssues == 0 {
		t.Fatal("total SubIssuesCreated = 0")
	}
}
