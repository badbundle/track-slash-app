package store_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/store"
	"github.com/bradleymackey/track-slash/internal/testutil"
)

// newSprintModeEnv is newSprintsEnv without the fixture opt-in: the project is
// exactly what CreateProjectForUser produced, so it starts with sprints off.
func newSprintModeEnv(t *testing.T) *sprintsTestEnv {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	db := testutil.NewMigratedDatabase(t)
	s := store.New(db.Pool)
	owner, err := s.CreateOrUpdateAdminUser(ctx, "mode-"+uniqueProjectKey(t)+"@example.com", "Owner")
	if err != nil {
		t.Fatalf("CreateOrUpdateAdminUser: %v", err)
	}
	proj, err := s.CreateProjectForUser(ctx, owner.ID, uniqueProjectKey(t), "sprint-mode-test", "")
	if err != nil {
		t.Fatalf("CreateProjectForUser: %v", err)
	}
	return &sprintsTestEnv{ctx: ctx, pool: db.Pool, store: s, projectID: proj.ID}
}

func mustSetSprintsEnabled(t *testing.T, env *sprintsTestEnv, enabled bool) model.Project {
	t.Helper()
	project, err := env.store.SetProjectSprintsEnabled(env.ctx, env.projectID, enabled)
	if err != nil {
		t.Fatalf("SetProjectSprintsEnabled(%t): %v", enabled, err)
	}
	if project.SprintsEnabled != enabled {
		t.Fatalf("SetProjectSprintsEnabled(%t) returned SprintsEnabled = %t", enabled, project.SprintsEnabled)
	}
	return project
}

func mustProjectSprintsEnabled(t *testing.T, env *sprintsTestEnv) bool {
	t.Helper()
	project, err := env.store.GetProject(env.ctx, env.projectID)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	return project.SprintsEnabled
}

func mustSprintStatus(t *testing.T, env *sprintsTestEnv, id uuid.UUID) model.SprintStatus {
	t.Helper()
	sprint, err := env.store.GetSprint(env.ctx, id)
	if err != nil {
		t.Fatalf("GetSprint: %v", err)
	}
	return sprint.Status
}

func sprintModeChangelog(t *testing.T, env *sprintsTestEnv) []model.ProjectChangelogEntry {
	t.Helper()
	entries, _, err := env.store.ListProjectChangelog(env.ctx, store.ListProjectChangelogParams{ProjectID: env.projectID, Limit: 100})
	if err != nil {
		t.Fatalf("ListProjectChangelog: %v", err)
	}
	var out []model.ProjectChangelogEntry
	for _, entry := range entries {
		for _, change := range entry.Details.Changes {
			if change.Field == "sprints_enabled" {
				out = append(out, entry)
				break
			}
		}
	}
	return out
}

func TestNewProjectsStartWithSprintsDisabled(t *testing.T) {
	t.Parallel()
	env := newSprintModeEnv(t)

	if mustProjectSprintsEnabled(t, env) {
		t.Fatal("new project SprintsEnabled = true, want false")
	}
	projects, _, err := env.store.ListProjects(env.ctx, store.ListProjectsParams{Limit: 10})
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(projects) != 1 || projects[0].SprintsEnabled {
		t.Fatalf("ListProjects = %+v, want one project with sprints disabled", projects)
	}
}

func TestSetProjectSprintsEnabledRecordsEachChangeOnce(t *testing.T) {
	t.Parallel()
	env := newSprintModeEnv(t)

	enabled := mustSetSprintsEnabled(t, env, true)
	if !mustProjectSprintsEnabled(t, env) {
		t.Fatal("GetProject after enable: SprintsEnabled = false")
	}
	// Repeating the current value is a no-op, not a second changelog entry.
	again := mustSetSprintsEnabled(t, env, true)
	if !again.UpdatedAt.Equal(enabled.UpdatedAt) {
		t.Fatalf("no-op enable touched updated_at: %s -> %s", enabled.UpdatedAt, again.UpdatedAt)
	}
	mustSetSprintsEnabled(t, env, false)
	mustSetSprintsEnabled(t, env, false)
	if mustProjectSprintsEnabled(t, env) {
		t.Fatal("GetProject after disable: SprintsEnabled = true")
	}

	entries := sprintModeChangelog(t, env)
	if len(entries) != 2 {
		t.Fatalf("sprint mode changelog entries = %d, want 2: %+v", len(entries), entries)
	}
	disabled, enabledEntry := entries[0], entries[1]
	for _, check := range []struct {
		entry    model.ProjectChangelogEntry
		summary  string
		from, to string
	}{
		{entry: enabledEntry, summary: "Enabled sprints for project " + enabled.Key, from: "Disabled", to: "Enabled"},
		{entry: disabled, summary: "Disabled sprints for project " + enabled.Key, from: "Enabled", to: "Disabled"},
	} {
		if check.entry.Entity != "project" || check.entry.Op != "update" || check.entry.EntityID != env.projectID {
			t.Fatalf("changelog entry identity = %s/%s/%s", check.entry.Entity, check.entry.Op, check.entry.EntityID)
		}
		if check.entry.Summary != check.summary {
			t.Fatalf("changelog summary = %q, want %q", check.entry.Summary, check.summary)
		}
		change := check.entry.Details.Changes[0]
		if change.Label != "Sprints" || change.From != check.from || change.To != check.to {
			t.Fatalf("changelog change = %+v, want Sprints %s -> %s", change, check.from, check.to)
		}
	}
}

func TestSetProjectSprintsEnabledMissingProject(t *testing.T) {
	t.Parallel()
	env := newSprintModeEnv(t)

	if _, err := env.store.SetProjectSprintsEnabled(env.ctx, uuid.New(), true); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("SetProjectSprintsEnabled(missing) err = %v, want ErrNotFound", err)
	}
	if err := env.store.DeleteProject(env.ctx, env.projectID); err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}
	if _, err := env.store.SetProjectSprintsEnabled(env.ctx, env.projectID, true); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("SetProjectSprintsEnabled(deleted) err = %v, want ErrNotFound", err)
	}
}

func TestDisablingSprintsIsBlockedByTheActiveSprint(t *testing.T) {
	t.Parallel()
	env := newSprintModeEnv(t)
	mustSetSprintsEnabled(t, env, true)
	sprint := mustCreateSprint(t, env, "Running", date(2026, 9, 1), date(2026, 9, 14))
	mustActivate(t, env, sprint.ID)

	_, err := env.store.SetProjectSprintsEnabled(env.ctx, env.projectID, false)
	if !errors.Is(err, store.ErrActiveSprintBlocksSprintsOff) || !errors.Is(err, store.ErrConflict) {
		t.Fatalf("disable with active sprint err = %v, want ErrActiveSprintBlocksSprintsOff wrapping ErrConflict", err)
	}
	if !mustProjectSprintsEnabled(t, env) {
		t.Fatal("rejected disable still turned sprints off")
	}
	if got := len(sprintModeChangelog(t, env)); got != 1 {
		t.Fatalf("rejected disable left %d sprint mode changelog entries, want only the enable", got)
	}

	if _, err := env.store.CompleteSprint(env.ctx, sprint.ID); err != nil {
		t.Fatalf("CompleteSprint: %v", err)
	}
	mustSetSprintsEnabled(t, env, false)
}

func TestStartingASprintIsRejectedWhileSprintsAreDisabled(t *testing.T) {
	t.Parallel()
	env := newSprintModeEnv(t)
	sprint := mustCreateSprint(t, env, "Waiting", date(2026, 9, 1), date(2026, 9, 14))

	active := model.SprintStatusActive
	name := "Renamed while starting"
	_, err := env.store.UpdateSprint(env.ctx, sprint.ID, store.UpdateSprintParams{Name: &name, Status: &active})
	if !errors.Is(err, store.ErrSprintsDisabled) || !errors.Is(err, store.ErrConflict) {
		t.Fatalf("start while disabled err = %v, want ErrSprintsDisabled wrapping ErrConflict", err)
	}
	got, err := env.store.GetSprint(env.ctx, sprint.ID)
	if err != nil {
		t.Fatalf("GetSprint: %v", err)
	}
	if got.Status != model.SprintStatusPlanned || got.Name != sprint.Name {
		t.Fatalf("rejected start changed the sprint: %+v", got)
	}

	// Keeping a planned sprint planned is not a start and stays allowed.
	planned := model.SprintStatusPlanned
	if _, err := env.store.UpdateSprint(env.ctx, sprint.ID, store.UpdateSprintParams{Status: &planned}); err != nil {
		t.Fatalf("planned -> planned while disabled: %v", err)
	}

	mustSetSprintsEnabled(t, env, true)
	mustActivate(t, env, sprint.ID)
	if got := mustSprintStatus(t, env, sprint.ID); got != model.SprintStatusActive {
		t.Fatalf("status after enabling and starting = %s, want active", got)
	}
}

func TestStartingAMissingSprintIsNotFound(t *testing.T) {
	t.Parallel()
	env := newSprintModeEnv(t)

	active := model.SprintStatusActive
	if _, err := env.store.UpdateSprint(env.ctx, uuid.New(), store.UpdateSprintParams{Status: &active}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("start missing sprint err = %v, want ErrNotFound", err)
	}
}

func TestPlannedSprintsStayWorkableWhileSprintsAreDisabled(t *testing.T) {
	t.Parallel()
	env := newSprintModeEnv(t)

	first := mustCreateSprint(t, env, "First", date(2026, 10, 1), date(2026, 10, 14))
	second, err := env.store.CreateSprint(env.ctx, store.CreateSprintParams{ProjectID: env.projectID, Name: "Second"})
	if err != nil {
		t.Fatalf("CreateSprint undated: %v", err)
	}
	goal := "Refined while sprints are off"
	start, end := date(2026, 11, 1), date(2026, 11, 14)
	if _, err := env.store.UpdateSprint(env.ctx, second.ID, store.UpdateSprintParams{Goal: &goal, StartDate: &start, EndDate: &end}); err != nil {
		t.Fatalf("UpdateSprint planned fields: %v", err)
	}
	reordered, err := env.store.ReorderPlannedSprints(env.ctx, store.ReorderPlannedSprintsParams{
		ProjectID: env.projectID,
		SprintIDs: []uuid.UUID{second.ID, first.ID},
	})
	if err != nil {
		t.Fatalf("ReorderPlannedSprints: %v", err)
	}
	if len(reordered) != 2 || reordered[0].ID != second.ID {
		t.Fatalf("reordered = %+v, want second first", reordered)
	}
	issue := mustCreateIssue(t, env, "Scheduled while sprints are off")
	assignIssueToSprint(t, env, issue.ID, second.ID)
	if err := env.store.DeleteSprint(env.ctx, first.ID); err != nil {
		t.Fatalf("DeleteSprint planned: %v", err)
	}
	if mustProjectSprintsEnabled(t, env) {
		t.Fatal("planning work turned sprints on")
	}
}

// Without sprint mode an issue is picked up and finished on its own. Finishing
// it leaves it attached to the planned sprint it was scheduled into.
func TestIssueInPlannedSprintCompletesWhileSprintsAreDisabled(t *testing.T) {
	t.Parallel()
	env := newSprintModeEnv(t)
	sprint := mustCreateSprint(t, env, "Later", date(2026, 10, 1), date(2026, 10, 14))
	issue := mustCreateIssue(t, env, "Pick me up now")
	assignIssueToSprint(t, env, issue.ID, sprint.ID)

	setIssueStatus(t, env, issue.ID, model.StatusInProgress)
	setIssueStatus(t, env, issue.ID, model.StatusDone)

	got, err := env.store.GetIssue(env.ctx, issue.ID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if got.Status != model.StatusDone {
		t.Fatalf("issue status = %s, want done", got.Status)
	}
	if got.SprintID == nil || *got.SprintID != sprint.ID {
		t.Fatalf("completed issue sprint = %v, want still %s", got.SprintID, sprint.ID)
	}
	if status := mustSprintStatus(t, env, sprint.ID); status != model.SprintStatusPlanned {
		t.Fatalf("sprint status = %s, want planned", status)
	}
}

type sprintModeSnapshot struct {
	sprints         []model.Sprint
	completedIssues []uuid.UUID
	plannedIssues   map[uuid.UUID][]uuid.UUID
}

func captureSprintModeSnapshot(t *testing.T, env *sprintsTestEnv, completedID uuid.UUID) sprintModeSnapshot {
	t.Helper()
	sprints, _, err := env.store.ListSprints(env.ctx, store.ListSprintsParams{ProjectID: env.projectID, Limit: 50})
	if err != nil {
		t.Fatalf("ListSprints: %v", err)
	}
	history, _, err := env.store.ListSprintSnapshotIssues(env.ctx, store.ListSprintSnapshotIssuesParams{
		ProjectID: env.projectID,
		SprintID:  completedID,
		Limit:     50,
	})
	if err != nil {
		t.Fatalf("ListSprintSnapshotIssues: %v", err)
	}
	out := sprintModeSnapshot{sprints: sprints, plannedIssues: map[uuid.UUID][]uuid.UUID{}}
	for _, issue := range history {
		out.completedIssues = append(out.completedIssues, issue.ID)
	}
	for _, sprint := range sprints {
		if sprint.Status != model.SprintStatusPlanned {
			continue
		}
		issues, _, err := env.store.ListIssues(env.ctx, store.ListIssuesParams{ProjectID: env.projectID, SprintID: &sprint.ID, Limit: 50})
		if err != nil {
			t.Fatalf("ListIssues sprint: %v", err)
		}
		for _, issue := range issues {
			out.plannedIssues[sprint.ID] = append(out.plannedIssues[sprint.ID], issue.ID)
		}
	}
	return out
}

func requireSameSprintModeSnapshot(t *testing.T, label string, want, got sprintModeSnapshot) {
	t.Helper()
	if len(got.sprints) != len(want.sprints) {
		t.Fatalf("%s: %d sprints, want %d", label, len(got.sprints), len(want.sprints))
	}
	for i := range want.sprints {
		w, g := want.sprints[i], got.sprints[i]
		samePlannedOrder := (w.PlannedOrder == nil) == (g.PlannedOrder == nil) && (w.PlannedOrder == nil || *w.PlannedOrder == *g.PlannedOrder)
		sameCompletedAt := (w.CompletedAt == nil) == (g.CompletedAt == nil) && (w.CompletedAt == nil || w.CompletedAt.Equal(*g.CompletedAt))
		if w.ID != g.ID || w.Status != g.Status || w.Name != g.Name || !samePlannedOrder || !sameCompletedAt || !w.UpdatedAt.Equal(g.UpdatedAt) {
			t.Fatalf("%s: sprint %d = %+v, want %+v", label, i, g, w)
		}
	}
	if len(got.completedIssues) != len(want.completedIssues) {
		t.Fatalf("%s: completed sprint history = %v, want %v", label, got.completedIssues, want.completedIssues)
	}
	for i := range want.completedIssues {
		if got.completedIssues[i] != want.completedIssues[i] {
			t.Fatalf("%s: completed sprint history = %v, want %v", label, got.completedIssues, want.completedIssues)
		}
	}
	for sprintID, issues := range want.plannedIssues {
		if len(got.plannedIssues[sprintID]) != len(issues) {
			t.Fatalf("%s: planned sprint %s issues = %v, want %v", label, sprintID, got.plannedIssues[sprintID], issues)
		}
		for i := range issues {
			if got.plannedIssues[sprintID][i] != issues[i] {
				t.Fatalf("%s: planned sprint %s issues = %v, want %v", label, sprintID, got.plannedIssues[sprintID], issues)
			}
		}
	}
}

func TestTogglingSprintsLeavesPlannedAndCompletedSprintsUntouched(t *testing.T) {
	t.Parallel()
	env := newSprintModeEnv(t)
	mustSetSprintsEnabled(t, env, true)

	completed := mustCreateSprint(t, env, "Shipped", date(2026, 8, 1), date(2026, 8, 14))
	shipped := mustCreateIssue(t, env, "Shipped work")
	assignIssueToSprint(t, env, shipped.ID, completed.ID)
	mustActivate(t, env, completed.ID)
	setIssueStatus(t, env, shipped.ID, model.StatusDone)
	if _, err := env.store.CompleteSprint(env.ctx, completed.ID); err != nil {
		t.Fatalf("CompleteSprint: %v", err)
	}
	next := mustCreateSprint(t, env, "Next", date(2026, 9, 1), date(2026, 9, 14))
	after := mustCreateSprint(t, env, "After", date(2026, 9, 15), date(2026, 9, 28))
	if _, err := env.store.ReorderPlannedSprints(env.ctx, store.ReorderPlannedSprintsParams{
		ProjectID: env.projectID,
		SprintIDs: []uuid.UUID{after.ID, next.ID},
	}); err != nil {
		t.Fatalf("ReorderPlannedSprints: %v", err)
	}
	queued := mustCreateIssue(t, env, "Queued work")
	assignIssueToSprint(t, env, queued.ID, next.ID)

	before := captureSprintModeSnapshot(t, env, completed.ID)
	if len(before.completedIssues) != 1 || len(before.plannedIssues[next.ID]) != 1 {
		t.Fatalf("fixture snapshot = %+v", before)
	}
	mustSetSprintsEnabled(t, env, false)
	requireSameSprintModeSnapshot(t, "after disabling", before, captureSprintModeSnapshot(t, env, completed.ID))
	mustSetSprintsEnabled(t, env, true)
	requireSameSprintModeSnapshot(t, "after re-enabling", before, captureSprintModeSnapshot(t, env, completed.ID))
}

// Turning sprints off and starting a sprint both take the project row lock, so
// whichever commits first wins and the other sees its result. Neither order may
// leave a running sprint in a project that has sprints disabled.
func TestDisablingSprintsAndStartingASprintNeverBothSucceed(t *testing.T) {
	t.Parallel()
	env := newSprintModeEnv(t)
	mustSetSprintsEnabled(t, env, true)

	for round := 0; round < 6; round++ {
		sprint := mustCreateSprint(t, env, "Race", date(2026, 9, 1), date(2026, 9, 14))
		var disableErr, startErr error
		var wg sync.WaitGroup
		release := make(chan struct{})
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-release
			_, disableErr = env.store.SetProjectSprintsEnabled(env.ctx, env.projectID, false)
		}()
		go func() {
			defer wg.Done()
			<-release
			active := model.SprintStatusActive
			_, startErr = env.store.UpdateSprint(env.ctx, sprint.ID, store.UpdateSprintParams{Status: &active})
		}()
		close(release)
		wg.Wait()

		enabled := mustProjectSprintsEnabled(t, env)
		status := mustSprintStatus(t, env, sprint.ID)
		switch {
		case disableErr == nil && startErr == nil:
			t.Fatalf("round %d: disable and start both succeeded (enabled=%t status=%s)", round, enabled, status)
		case disableErr == nil:
			if !errors.Is(startErr, store.ErrSprintsDisabled) || enabled || status != model.SprintStatusPlanned {
				t.Fatalf("round %d: disable won but start err = %v, enabled = %t, status = %s", round, startErr, enabled, status)
			}
			if err := env.store.DeleteSprint(env.ctx, sprint.ID); err != nil {
				t.Fatalf("round %d: DeleteSprint: %v", round, err)
			}
			mustSetSprintsEnabled(t, env, true)
		case startErr == nil:
			if !errors.Is(disableErr, store.ErrActiveSprintBlocksSprintsOff) || !enabled || status != model.SprintStatusActive {
				t.Fatalf("round %d: start won but disable err = %v, enabled = %t, status = %s", round, disableErr, enabled, status)
			}
			if _, err := env.store.CompleteSprint(env.ctx, sprint.ID); err != nil {
				t.Fatalf("round %d: CompleteSprint: %v", round, err)
			}
		default:
			t.Fatalf("round %d: both failed: disable = %v, start = %v", round, disableErr, startErr)
		}
	}
}
