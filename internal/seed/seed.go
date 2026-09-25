package seed

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/store"
)

var (
	projectKeyRe = regexp.MustCompile(`^[A-Z][A-Z0-9]{1,9}$`)
	keyPrefixRe  = regexp.MustCompile(`^[A-Z][A-Z0-9]{0,5}$`)
)

type Options struct {
	Username      string
	Password      string
	Name          string
	ProjectPrefix string
	Now           time.Time
}

type Summary struct {
	User        model.User
	CreatedUser bool
	Projects    []ProjectSummary
}

type ProjectSummary struct {
	Project          model.Project
	CreatedProject   bool
	Seeded           bool
	ExistingContent  bool
	IssuesCreated    int
	SubIssuesCreated int
	SprintsCreated   int
	CommentsCreated  int
	LinksCreated     int
}

func Run(ctx context.Context, st *store.Store, opts Options) (Summary, error) {
	opts, err := normalizeOptions(opts)
	if err != nil {
		return Summary{}, err
	}

	user, createdUser, err := ensureAccount(ctx, st, opts)
	if err != nil {
		return Summary{}, err
	}

	out := Summary{User: user, CreatedUser: createdUser}
	for _, def := range demoProjects(opts.ProjectPrefix, opts.Now) {
		project, createdProject, err := ensureProject(ctx, st, user.ID, def)
		if err != nil {
			return Summary{}, err
		}

		projectSummary := ProjectSummary{
			Project:        project,
			CreatedProject: createdProject,
		}
		hasContent, err := projectHasContent(ctx, st, project.ID)
		if err != nil {
			return Summary{}, err
		}
		if hasContent {
			projectSummary.ExistingContent = true
			out.Projects = append(out.Projects, projectSummary)
			continue
		}

		if err := seedProject(ctx, st, user.ID, project, def, &projectSummary); err != nil {
			return Summary{}, err
		}
		projectSummary.Seeded = true
		out.Projects = append(out.Projects, projectSummary)
	}

	return out, nil
}

func normalizeOptions(opts Options) (Options, error) {
	username, err := store.NormalizeUsername(opts.Username)
	if err != nil {
		return Options{}, fmt.Errorf("username: %w", err)
	}
	opts.Username = username

	if err := store.ValidatePassword(opts.Password); err != nil {
		return Options{}, fmt.Errorf("password: %w", err)
	}

	opts.Name = strings.TrimSpace(opts.Name)
	if opts.Name == "" {
		opts.Name = opts.Username
	}

	opts.ProjectPrefix = strings.ToUpper(strings.TrimSpace(opts.ProjectPrefix))
	if opts.ProjectPrefix == "" {
		opts.ProjectPrefix = "DEMO"
	}
	if !keyPrefixRe.MatchString(opts.ProjectPrefix) {
		return Options{}, errors.New("project prefix must be 1-6 chars: A-Z, 0-9, starting with A-Z")
	}

	if opts.Now.IsZero() {
		opts.Now = time.Now()
	}
	opts.Now = dateOnly(opts.Now)

	for _, def := range demoProjects(opts.ProjectPrefix, opts.Now) {
		if !projectKeyRe.MatchString(def.Key) {
			return Options{}, fmt.Errorf("project key %q must match ^[A-Z][A-Z0-9]{1,9}$", def.Key)
		}
	}

	return opts, nil
}

func ensureAccount(ctx context.Context, st *store.Store, opts Options) (model.User, bool, error) {
	user, err := st.CreateAccount(ctx, store.CreateAccountParams{
		Username: opts.Username,
		Password: opts.Password,
		Name:     opts.Name,
	})
	if err == nil {
		return user, true, nil
	}
	if !errors.Is(err, store.ErrConflict) {
		return model.User{}, false, err
	}

	user, authErr := st.AuthenticatePassword(ctx, opts.Username, opts.Password)
	if authErr != nil {
		return model.User{}, false, fmt.Errorf("account %q already exists but password did not authenticate: %w", opts.Username, authErr)
	}
	return user, false, nil
}

func ensureProject(ctx context.Context, st *store.Store, userID uuid.UUID, def projectDefinition) (model.Project, bool, error) {
	var cursor *store.ProjectsCursor
	for {
		projects, hasMore, err := st.ListProjects(ctx, store.ListProjectsParams{
			Cursor:        cursor,
			Limit:         100,
			VisibleToUser: &userID,
		})
		if err != nil {
			return model.Project{}, false, err
		}
		for _, project := range projects {
			if project.Key == def.Key {
				return project, false, nil
			}
		}
		if !hasMore {
			break
		}
		last := projects[len(projects)-1]
		cursor = &store.ProjectsCursor{CreatedAt: last.CreatedAt, ID: last.ID}
	}

	project, err := st.CreateProjectForUser(ctx, userID, def.Key, def.Name, def.Description)
	if err != nil {
		if errors.Is(err, store.ErrConflict) {
			return model.Project{}, false, fmt.Errorf("project key %q already exists but is not visible to seeded user: %w", def.Key, err)
		}
		return model.Project{}, false, err
	}
	return project, true, nil
}

func projectHasContent(ctx context.Context, st *store.Store, projectID uuid.UUID) (bool, error) {
	issues, _, err := st.ListIssues(ctx, store.ListIssuesParams{ProjectID: projectID, Limit: 1})
	if err != nil {
		return false, err
	}
	if len(issues) > 0 {
		return true, nil
	}

	sprints, _, err := st.ListSprints(ctx, store.ListSprintsParams{ProjectID: projectID, Limit: 1})
	if err != nil {
		return false, err
	}
	return len(sprints) > 0, nil
}

func seedProject(ctx context.Context, st *store.Store, userID uuid.UUID, project model.Project, def projectDefinition, summary *ProjectSummary) error {
	issueByKey := map[string]model.Issue{}

	// Every demo project runs in sprint mode: it seeds completed, active and
	// planned sprints, and sprints can only start once the mode is on.
	project, err := st.SetProjectSprintsEnabled(ctx, project.ID, true)
	if err != nil {
		return err
	}
	summary.Project = project

	completed, err := createSprint(ctx, st, project.ID, def.CompletedSprint, summary)
	if err != nil {
		return err
	}
	if err := activateSprint(ctx, st, completed.ID); err != nil {
		return err
	}
	for _, issue := range def.CompletedSprint.Issues {
		issue.Status = model.StatusDone
		if err := createIssue(ctx, st, userID, project.ID, &completed.ID, issue, issueByKey, summary); err != nil {
			return err
		}
	}
	if _, err := st.CompleteSprint(ctx, completed.ID); err != nil {
		return err
	}

	active, err := createSprint(ctx, st, project.ID, def.ActiveSprint, summary)
	if err != nil {
		return err
	}
	if err := activateSprint(ctx, st, active.ID); err != nil {
		return err
	}
	for _, issue := range def.ActiveSprint.Issues {
		if err := createIssue(ctx, st, userID, project.ID, &active.ID, issue, issueByKey, summary); err != nil {
			return err
		}
	}

	planned, err := createSprint(ctx, st, project.ID, def.PlannedSprint, summary)
	if err != nil {
		return err
	}
	for _, issue := range def.PlannedSprint.Issues {
		if err := createIssue(ctx, st, userID, project.ID, &planned.ID, issue, issueByKey, summary); err != nil {
			return err
		}
	}

	for _, issue := range def.BacklogIssues {
		if err := createIssue(ctx, st, userID, project.ID, nil, issue, issueByKey, summary); err != nil {
			return err
		}
	}

	for _, link := range def.Links {
		source, ok := issueByKey[link.SourceKey]
		if !ok {
			return fmt.Errorf("missing source issue key %q", link.SourceKey)
		}
		target, ok := issueByKey[link.TargetKey]
		if !ok {
			return fmt.Errorf("missing target issue key %q", link.TargetKey)
		}
		if _, err := st.CreateIssueLink(ctx, store.CreateIssueLinkParams{
			SourceID: source.ID,
			TargetID: target.ID,
			LinkType: link.Type,
		}); err != nil {
			return err
		}
		summary.LinksCreated++
	}

	return nil
}

func createSprint(ctx context.Context, st *store.Store, projectID uuid.UUID, sprint sprintDefinition, summary *ProjectSummary) (model.Sprint, error) {
	created, err := st.CreateSprint(ctx, store.CreateSprintParams{
		ProjectID: projectID,
		Name:      sprint.Name,
		Goal:      sprint.Goal,
		StartDate: seedTimePtr(sprint.StartDate),
		EndDate:   seedTimePtr(sprint.EndDate),
	})
	if err != nil {
		return model.Sprint{}, err
	}
	summary.SprintsCreated++
	return created, nil
}

func seedTimePtr(t time.Time) *time.Time {
	return &t
}

func activateSprint(ctx context.Context, st *store.Store, sprintID uuid.UUID) error {
	status := model.SprintStatusActive
	_, err := st.UpdateSprint(ctx, sprintID, store.UpdateSprintParams{Status: &status})
	return err
}

func createIssue(
	ctx context.Context,
	st *store.Store,
	userID uuid.UUID,
	projectID uuid.UUID,
	sprintID *uuid.UUID,
	seed issueDefinition,
	issueByKey map[string]model.Issue,
	summary *ProjectSummary,
) error {
	if _, exists := issueByKey[seed.Key]; exists {
		return fmt.Errorf("duplicate issue seed key %q", seed.Key)
	}
	created, err := st.CreateIssue(ctx, store.CreateIssueParams{
		ProjectID:   projectID,
		Title:       seed.Title,
		Description: seed.Description,
		Priority:    issuePriorityOrDefault(seed.Priority),
		AssigneeID:  &userID,
		ReporterID:  &userID,
	})
	if err != nil {
		return err
	}

	if sprintID != nil {
		created, err = st.UpdateIssue(ctx, created.ID, store.UpdateIssueParams{SprintID: sprintID})
		if err != nil {
			return err
		}
	}
	if seed.Status != "" && seed.Status != model.StatusTodo {
		params := store.UpdateIssueParams{Status: &seed.Status}
		if seed.Status == model.StatusClosed {
			reason := model.CloseReasonWontDo
			params.CloseReason = &reason
		}
		created, err = st.UpdateIssue(ctx, created.ID, params)
		if err != nil {
			return err
		}
	}

	for _, body := range seed.Comments {
		if _, err := st.CreateComment(ctx, store.CreateCommentParams{
			IssueID:  created.ID,
			AuthorID: userID,
			Body:     body,
		}); err != nil {
			return err
		}
		summary.CommentsCreated++
	}

	issueByKey[seed.Key] = created
	summary.IssuesCreated++
	for _, subIssue := range seed.SubIssues {
		if err := createSubIssue(ctx, st, userID, created.ID, subIssue, issueByKey, summary); err != nil {
			return err
		}
	}
	return nil
}

func createSubIssue(
	ctx context.Context,
	st *store.Store,
	userID uuid.UUID,
	parentIssueID uuid.UUID,
	seed issueDefinition,
	issueByKey map[string]model.Issue,
	summary *ProjectSummary,
) error {
	if _, exists := issueByKey[seed.Key]; exists {
		return fmt.Errorf("duplicate issue seed key %q", seed.Key)
	}
	if len(seed.SubIssues) > 0 {
		return fmt.Errorf("sub-issue seed key %q cannot define nested sub-issues", seed.Key)
	}
	created, err := st.CreateSubIssue(ctx, store.CreateSubIssueParams{
		ParentIssueID: parentIssueID,
		Title:         seed.Title,
		Description:   seed.Description,
		Priority:      issuePriorityOrDefault(seed.Priority),
		AssigneeID:    &userID,
		ReporterID:    &userID,
	})
	if err != nil {
		return err
	}

	if seed.Status != "" && seed.Status != model.StatusTodo {
		params := store.UpdateIssueParams{Status: &seed.Status}
		if seed.Status == model.StatusClosed {
			reason := model.CloseReasonWontDo
			params.CloseReason = &reason
		}
		created, err = st.UpdateIssue(ctx, created.ID, params)
		if err != nil {
			return err
		}
	}

	for _, body := range seed.Comments {
		if _, err := st.CreateComment(ctx, store.CreateCommentParams{
			IssueID:  created.ID,
			AuthorID: userID,
			Body:     body,
		}); err != nil {
			return err
		}
		summary.CommentsCreated++
	}

	issueByKey[seed.Key] = created
	summary.SubIssuesCreated++
	return nil
}

type projectDefinition struct {
	Key             string
	Name            string
	Description     string
	CompletedSprint sprintDefinition
	ActiveSprint    sprintDefinition
	PlannedSprint   sprintDefinition
	BacklogIssues   []issueDefinition
	Links           []linkDefinition
}

type sprintDefinition struct {
	Name      string
	Goal      string
	StartDate time.Time
	EndDate   time.Time
	Issues    []issueDefinition
}

type issueDefinition struct {
	Key         string
	Title       string
	Description string
	Status      model.Status
	Priority    model.IssuePriority
	Comments    []string
	SubIssues   []issueDefinition
}

type linkDefinition struct {
	SourceKey string
	TargetKey string
	Type      model.LinkType
}

func issuePriorityOrDefault(priority model.IssuePriority) model.IssuePriority {
	if priority == "" {
		return model.PriorityP2
	}
	return priority
}

func demoProjects(prefix string, now time.Time) []projectDefinition {
	return []projectDefinition{
		coreWorkflowProject(prefix+"CORE", now),
		mobileCompanionProject(prefix+"APP", now),
		operationsDeskProject(prefix+"OPS", now),
	}
}

func coreWorkflowProject(key string, now time.Time) projectDefinition {
	return projectDefinition{
		Key:         key,
		Name:        "Core Workflow",
		Description: "Issue tracking primitives, realtime updates, and workflow policy for product teams.",
		CompletedSprint: sprintDefinition{
			Name:      "Sprint 22 - Board Calm",
			Goal:      "Remove noisy board states before expanding planning workflows.",
			StartDate: now.AddDate(0, 0, -28),
			EndDate:   now.AddDate(0, 0, -15),
			Issues: []issueDefinition{
				{
					Key:         "core-filter-refresh",
					Title:       "Keep board filters stable across refreshes",
					Description: "Saved status filters reset after hard refresh, making triage feel unreliable during standup.",
					Comments: []string{
						"Reproduced with status + assignee filters on the project board.",
						"Fixed by hydrating filters before the first issue fetch.",
					},
				},
				{
					Key:         "core-column-moves",
					Title:       "Persist column moves without losing selection",
					Description: "Moving an issue between columns should preserve active selection and avoid a second fetch.",
					Comments: []string{
						"Selection loss was caused by optimistic state being replaced by the broadcast payload.",
					},
				},
				{
					Key:         "core-realtime-dup",
					Title:       "Close duplicate realtime event delivery",
					Description: "Some listeners receive duplicate issue update events when reconnecting after laptop sleep.",
					Priority:    model.PriorityP1,
					Comments: []string{
						"Added reconnect test coverage around topic fanout.",
						"Confirmed duplicate stream is gone in local Postgres listener run.",
					},
				},
			},
		},
		ActiveSprint: sprintDefinition{
			Name:      "Sprint 23 - Planning Loop",
			Goal:      "Make sprint planning fast enough for daily use.",
			StartDate: now.AddDate(0, 0, -7),
			EndDate:   now.AddDate(0, 0, 6),
			Issues: []issueDefinition{
				{
					Key:         "core-capacity",
					Title:       "Show sprint capacity before start",
					Description: "Planning view needs a quick count of todo, in-progress, and done work before a sprint is activated.",
					Status:      model.StatusInProgress,
					Priority:    model.PriorityP0,
					Comments: []string{
						"Counts can come from existing list endpoints for now.",
					},
					SubIssues: []issueDefinition{
						{
							Key:         "core-capacity-status-counts",
							Title:       "Count issues by status for planned sprint",
							Description: "Return todo, in-progress, and done counts for the selected planned sprint.",
							Status:      model.StatusDone,
							Comments: []string{
								"Use the existing issue status enum; no new capacity state.",
							},
						},
						{
							Key:         "core-capacity-empty-state",
							Title:       "Render empty capacity state",
							Description: "Show a compact zero-state when a planned sprint has no issues yet.",
							Status:      model.StatusInProgress,
						},
					},
				},
				{
					Key:         "core-token-last-used",
					Title:       "Expose token last-used state in settings",
					Description: "Users need confidence that old API tokens are inactive before rotating credentials.",
					Status:      model.StatusTodo,
					Priority:    model.PriorityP3,
					Comments: []string{
						"Display null last_used_at as Never used.",
					},
				},
				{
					Key:         "core-rollover",
					Title:       "Rollover unfinished work when completing sprint",
					Description: "Completing a sprint should keep done work attached and move unfinished work back to backlog.",
					Status:      model.StatusDone,
					Comments: []string{
						"Store behavior is in place; UI needs a clear completion affordance.",
					},
				},
			},
		},
		PlannedSprint: sprintDefinition{
			Name:      "Sprint 24 - Import Readiness",
			Goal:      "Prepare clean primitives for importing work from other trackers.",
			StartDate: now.AddDate(0, 0, 7),
			EndDate:   now.AddDate(0, 0, 20),
			Issues: []issueDefinition{
				{
					Key:         "core-import-priority",
					Title:       "Map imported priorities to workflow lanes",
					Description: "Importer needs a deterministic policy for priority fields that do not exist in track-slash yet.",
					Comments: []string{
						"Keep mapping visible in import summary instead of hiding it in logs.",
					},
				},
				{
					Key:         "core-attachment-contract",
					Title:       "Draft attachment metadata contract",
					Description: "Define how external attachments are represented before adding blob storage.",
					Comments: []string{
						"Manifesto says extra services must earn their place; metadata first.",
					},
				},
			},
		},
		BacklogIssues: []issueDefinition{
			{
				Key:         "core-saved-search",
				Title:       "Add saved issue search endpoint",
				Description: "Teams want reusable filters for triage without adding dashboard surface area.",
				Comments: []string{
					"API-first endpoint can ship before frontend polish.",
				},
			},
			{
				Key:         "core-permission-audit",
				Title:       "Capture audit trail for permission changes",
				Description: "Project membership grants and revokes need a lightweight audit trail for administrators.",
				Status:      model.StatusInProgress,
				Comments: []string{
					"Need to decide whether audit records are realtime entities.",
				},
			},
			{
				Key:         "core-stale-sync",
				Title:       "Remove stale board sync retry path",
				Description: "Old retry path predates websocket batching and now overlaps with realtime recovery.",
				Priority:    model.PriorityP4,
				Comments: []string{
					"Candidate duplicate of realtime delivery cleanup.",
				},
			},
		},
		Links: []linkDefinition{
			{SourceKey: "core-capacity", TargetKey: "core-rollover", Type: model.LinkTypeRelatesTo},
			{SourceKey: "core-import-priority", TargetKey: "core-attachment-contract", Type: model.LinkTypeBlocks},
			{SourceKey: "core-stale-sync", TargetKey: "core-realtime-dup", Type: model.LinkTypeDuplicates},
		},
	}
}

func mobileCompanionProject(key string, now time.Time) projectDefinition {
	return projectDefinition{
		Key:         key,
		Name:        "Mobile Companion",
		Description: "Small-screen planning and field updates for people away from the desk.",
		CompletedSprint: sprintDefinition{
			Name:      "Sprint 10 - Offline Notes",
			Goal:      "Make field notes safe to capture when connectivity is bad.",
			StartDate: now.AddDate(0, 0, -27),
			EndDate:   now.AddDate(0, 0, -14),
			Issues: []issueDefinition{
				{
					Key:         "app-note-drafts",
					Title:       "Save issue note drafts locally",
					Description: "Comment drafts should survive tab close until the next successful submit.",
					Comments: []string{
						"Local draft key includes issue id and user id.",
					},
				},
				{
					Key:         "app-touch-targets",
					Title:       "Increase board touch targets",
					Description: "Compact issue rows are too easy to miss on phones.",
					Comments: []string{
						"Keep desktop density unchanged.",
					},
				},
			},
		},
		ActiveSprint: sprintDefinition{
			Name:      "Sprint 11 - Fast Triage",
			Goal:      "Let mobile users update status and comments without opening every detail view.",
			StartDate: now.AddDate(0, 0, -6),
			EndDate:   now.AddDate(0, 0, 7),
			Issues: []issueDefinition{
				{
					Key:         "app-quick-status",
					Title:       "Add quick status controls on mobile cards",
					Description: "Mobile cards should expose todo, in-progress, and done controls without layout jumps.",
					Status:      model.StatusInProgress,
					Comments: []string{
						"Use the same status enum as the API, no mobile-only states.",
					},
				},
				{
					Key:         "app-comment-sheet",
					Title:       "Use a bottom sheet for comment composer",
					Description: "Comment creation should not push the entire issue detail view off-screen.",
					Status:      model.StatusTodo,
					Comments: []string{
						"Composer needs visible submit and cancel states.",
					},
					SubIssues: []issueDefinition{
						{
							Key:         "app-comment-sheet-focus",
							Title:       "Keep keyboard focus inside composer sheet",
							Description: "Opening the composer should focus the textarea and keep tab order inside the sheet.",
							Status:      model.StatusInProgress,
						},
						{
							Key:         "app-comment-sheet-submit",
							Title:       "Submit comment without closing on validation error",
							Description: "Validation errors should leave the draft visible and focused.",
						},
					},
				},
				{
					Key:         "app-session-renew",
					Title:       "Recover cleanly from expired mobile sessions",
					Description: "Expired sessions should return to login and preserve the intended destination.",
					Status:      model.StatusDone,
					Comments: []string{
						"Session path now carries next parameter through login.",
					},
				},
			},
		},
		PlannedSprint: sprintDefinition{
			Name:      "Sprint 12 - Notification Polish",
			Goal:      "Make realtime updates legible without stealing focus.",
			StartDate: now.AddDate(0, 0, 8),
			EndDate:   now.AddDate(0, 0, 21),
			Issues: []issueDefinition{
				{
					Key:         "app-passive-toast",
					Title:       "Show passive toast for remote issue updates",
					Description: "Users need a quiet signal when someone else updates the issue they are viewing.",
					Comments: []string{
						"Avoid modal interruption during editing.",
					},
				},
				{
					Key:         "app-merge-drafts",
					Title:       "Merge remote comments with local drafts",
					Description: "Incoming comments should appear without discarding an unsent draft.",
					Comments: []string{
						"Needs conflict-free ordering by created_at and id.",
					},
				},
			},
		},
		BacklogIssues: []issueDefinition{
			{
				Key:         "app-dark-mode",
				Title:       "Tune dark mode contrast on issue detail",
				Description: "Several secondary labels are too dim in dark mode on mobile OLED screens.",
				Comments: []string{
					"Check comment timestamps and token labels together.",
				},
			},
			{
				Key:         "app-share-link",
				Title:       "Add share link action for issue identifiers",
				Description: "Mobile users need a quick way to copy a stable issue link into chat.",
				Status:      model.StatusInProgress,
				Comments: []string{
					"Use identifier in label, canonical URL in clipboard value.",
				},
			},
		},
		Links: []linkDefinition{
			{SourceKey: "app-comment-sheet", TargetKey: "app-merge-drafts", Type: model.LinkTypeBlocks},
			{SourceKey: "app-passive-toast", TargetKey: "app-quick-status", Type: model.LinkTypeRelatesTo},
			{SourceKey: "app-share-link", TargetKey: "app-session-renew", Type: model.LinkTypeRelatesTo},
		},
	}
}

func operationsDeskProject(key string, now time.Time) projectDefinition {
	return projectDefinition{
		Key:         key,
		Name:        "Operations Desk",
		Description: "Migration, support, and reliability work that keeps the tracker easy to run.",
		CompletedSprint: sprintDefinition{
			Name:      "Sprint 5 - Migration Safety",
			Goal:      "Make local and CI database setup predictable.",
			StartDate: now.AddDate(0, 0, -29),
			EndDate:   now.AddDate(0, 0, -16),
			Issues: []issueDefinition{
				{
					Key:         "ops-test-db",
					Title:       "Document test database bootstrap",
					Description: "Developers need one command path for Postgres, migrations, and integration tests.",
					Comments: []string{
						"Makefile now creates the test database if missing.",
					},
				},
				{
					Key:         "ops-migration-markers",
					Title:       "Audit goose statement markers",
					Description: "Migrations should keep logical blocks wrapped so down migrations stay readable.",
					Comments: []string{
						"Checked existing multi-statement migrations for StatementBegin usage.",
					},
				},
			},
		},
		ActiveSprint: sprintDefinition{
			Name:      "Sprint 6 - Support Signals",
			Goal:      "Expose enough operational context to debug user reports quickly.",
			StartDate: now.AddDate(0, 0, -5),
			EndDate:   now.AddDate(0, 0, 8),
			Issues: []issueDefinition{
				{
					Key:         "ops-ci-flake-log",
					Title:       "Collect failing CI job summaries",
					Description: "Support needs a compact view of failed checks before opening full logs.",
					Status:      model.StatusInProgress,
					Comments: []string{
						"Start with job name, conclusion, and failure URL.",
					},
				},
				{
					Key:         "ops-seed-data",
					Title:       "Keep demo seed data compiling with store API",
					Description: "Seed data should exercise project, sprint, issue, comment, and link creation through Go code.",
					Status:      model.StatusDone,
					Comments: []string{
						"This issue is intentionally meta so demo boards show repo maintenance work.",
					},
				},
				{
					Key:         "ops-healthcheck",
					Title:       "Add database health check response details",
					Description: "Operators should see whether track-slash can reach Postgres without checking logs first.",
					Status:      model.StatusTodo,
					Comments: []string{
						"Keep response small and avoid leaking connection strings.",
					},
				},
			},
		},
		PlannedSprint: sprintDefinition{
			Name:      "Sprint 7 - Release Routine",
			Goal:      "Make routine releases boring and inspectable.",
			StartDate: now.AddDate(0, 0, 9),
			EndDate:   now.AddDate(0, 0, 22),
			Issues: []issueDefinition{
				{
					Key:         "ops-release-notes",
					Title:       "Generate release notes from closed issues",
					Description: "Release notes should be assembled from issue titles and links, then edited by humans.",
					Comments: []string{
						"Prefer exportable text over hidden generated summaries.",
					},
				},
				{
					Key:         "ops-backup-restore",
					Title:       "Practice restore from latest backup",
					Description: "Backup confidence requires a documented restore run, not just scheduled dumps.",
					Comments: []string{
						"Record database size and elapsed restore time.",
					},
				},
			},
		},
		BacklogIssues: []issueDefinition{
			{
				Key:         "ops-rate-limit",
				Title:       "Define API rate-limit policy",
				Description: "Public API needs clear limits once integrations begin polling.",
				Comments: []string{
					"Keep policy explicit before adding enforcement code.",
				},
			},
			{
				Key:         "ops-admin-guide",
				Title:       "Write first-run admin guide",
				Description: "New admins need a short path from empty database to first project and users.",
				Status:      model.StatusInProgress,
				Comments: []string{
					"Include bootstrap token, account creation, and project membership.",
				},
				SubIssues: []issueDefinition{
					{
						Key:         "ops-admin-guide-bootstrap",
						Title:       "Document bootstrap admin account",
						Description: "Explain how the initial admin account is created and rotated.",
						Status:      model.StatusDone,
					},
					{
						Key:         "ops-admin-guide-members",
						Title:       "Add project member walkthrough",
						Description: "Show the minimum steps to grant and revoke project access.",
					},
				},
			},
		},
		Links: []linkDefinition{
			{SourceKey: "ops-release-notes", TargetKey: "ops-ci-flake-log", Type: model.LinkTypeRelatesTo},
			{SourceKey: "ops-backup-restore", TargetKey: "ops-healthcheck", Type: model.LinkTypeBlocks},
			{SourceKey: "ops-admin-guide", TargetKey: "ops-seed-data", Type: model.LinkTypeRelatesTo},
		},
	}
}

func dateOnly(t time.Time) time.Time {
	utc := t.UTC()
	return time.Date(utc.Year(), utc.Month(), utc.Day(), 0, 0, 0, 0, time.UTC)
}
