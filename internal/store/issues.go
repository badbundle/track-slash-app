package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/bradleymackey/track-slash/internal/model"
)

type CreateIssueParams struct {
	ProjectID   uuid.UUID
	Title       string
	Description string
	Priority    model.IssuePriority
	Worker      *model.IssueWorker
	Private     bool
	AssigneeID  *uuid.UUID
	ReporterID  *uuid.UUID
	DueDate     *model.Date
	// Repeat makes the new issue the first repetition of a series.
	Repeat *IssueRepeatSetting
}

type CreateSubIssueParams struct {
	ParentIssueID uuid.UUID
	Title         string
	Description   string
	Priority      model.IssuePriority
	Worker        *model.IssueWorker
	// Private is forced on under a private parent, so a sub-issue never
	// starts out more visible than the issue it belongs to.
	Private    bool
	AssigneeID *uuid.UUID
	ReporterID *uuid.UUID
	DueDate    *model.Date
	// Repeat makes the new sub-issue the first repetition of a series.
	Repeat *IssueRepeatSetting
}

type issueScanner interface {
	Scan(dest ...any) error
}

func scanIssue(row issueScanner) (model.Issue, error) {
	var iss model.Issue
	var dueDate *time.Time
	err := row.Scan(
		&iss.ID, &iss.ProjectID, &iss.OwnerUsername, &iss.ProjectKey, &iss.Number,
		&iss.Title, &iss.Description, &iss.Status, &iss.CloseReason, &iss.Priority, &iss.Worker, &iss.Private, &iss.AssigneeID, &iss.ReporterID,
		&iss.SprintID, &iss.ParentIssueID, &dueDate, &iss.CreatedAt, &iss.UpdatedAt,
	)
	if err != nil {
		return model.Issue{}, err
	}
	setIssueDueDate(&iss, dueDate)
	iss.Identifier = fmt.Sprintf("%s-%d", iss.ProjectKey, iss.Number)
	return iss, nil
}

func setIssueDueDate(iss *model.Issue, dueDate *time.Time) {
	if dueDate == nil {
		iss.DueDate = nil
		return
	}
	d := model.DateFromTime(*dueDate)
	iss.DueDate = &d
}

func issueDueDateValue(d *model.Date) any {
	if d == nil {
		return nil
	}
	return d.Time()
}

func issueWorkerValue(worker *model.IssueWorker) any {
	if worker == nil {
		return nil
	}
	return string(*worker)
}

func issuePriorityOrDefault(priority model.IssuePriority) model.IssuePriority {
	if priority == "" {
		return model.PriorityP2
	}
	return priority
}

// MaxIssueDescriptionRunes matches the limit on project context and
// whiteboard bodies. A description is rendered on every view of its issue,
// including anonymous views of a public project.
const MaxIssueDescriptionRunes = 100_000

// ErrIssueDescriptionTooLong rejects a description over MaxIssueDescriptionRunes.
var ErrIssueDescriptionTooLong = fmt.Errorf("description max 100000 chars: %w", ErrConflict)

func validateIssueDescription(description string) error {
	if utf8.RuneCountInString(description) > MaxIssueDescriptionRunes {
		return ErrIssueDescriptionTooLong
	}
	return nil
}

func (s *Store) CreateIssue(ctx context.Context, p CreateIssueParams) (model.Issue, error) {
	if err := validateIssueDescription(p.Description); err != nil {
		return model.Issue{}, err
	}
	var out model.Issue
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		var (
			number        int
			projectKey    string
			ownerUsername string
		)
		err := tx.QueryRow(ctx, `
			SELECT p.next_issue_number, p.key, u.username
			FROM projects p
			JOIN users u ON u.id = p.owner_id
			WHERE p.id = $1 AND p.deleted_at IS NULL AND u.deleted_at IS NULL
			FOR UPDATE OF p
		`, p.ProjectID).Scan(&number, &projectKey, &ownerUsername)
		if err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err
		}
		if err := validateIssuePeople(ctx, tx, p.ProjectID, p.AssigneeID, p.ReporterID); err != nil {
			return err
		}

		priority := issuePriorityOrDefault(p.Priority)
		var dueDate *time.Time
		err = tx.QueryRow(ctx, `
			INSERT INTO issues (project_id, number, title, description, priority, worker, private, assignee_id, reporter_id, due_date)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
			RETURNING id, project_id, number, title, description, status, close_reason, priority, worker, private,
			          assignee_id, reporter_id, sprint_id, parent_issue_id, due_date, created_at, updated_at
		`, p.ProjectID, number, p.Title, p.Description, string(priority), issueWorkerValue(p.Worker), p.Private, p.AssigneeID, p.ReporterID, issueDueDateValue(p.DueDate)).
			Scan(&out.ID, &out.ProjectID, &out.Number, &out.Title, &out.Description, &out.Status, &out.CloseReason, &out.Priority, &out.Worker, &out.Private,
				&out.AssigneeID, &out.ReporterID, &out.SprintID, &out.ParentIssueID, &dueDate, &out.CreatedAt, &out.UpdatedAt)
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23503" {
				return fmt.Errorf("invalid assignee/reporter: %w", ErrNotFound)
			}
			return err
		}
		setIssueDueDate(&out, dueDate)

		_, err = tx.Exec(ctx, `
			UPDATE projects
			SET next_issue_number = next_issue_number + 1,
			    updated_at = now()
			WHERE id = $1
		`, p.ProjectID)
		if err != nil {
			return err
		}

		out.ProjectKey = projectKey
		out.OwnerUsername = ownerUsername
		out.Identifier = fmt.Sprintf("%s-%d", projectKey, out.Number)
		details := model.ProjectChangelogDetails{}
		if p.Repeat != nil {
			change, err := applyIssueRepeat(ctx, tx, out, *p.Repeat, s.clock())
			if err != nil {
				return err
			}
			out.DueDate = change.DueDate
			details.Changes = changelogAppendChange(details.Changes, "repeat", "Repeat", change.From, change.To)
		}
		if preview := changelogPreview(out.Description); preview != "" {
			details.Preview = preview
		}
		details.PushNotification = pushNotificationChangelogData(out.AssigneeID, pushMentionUsernames(out.Description))
		if err := appendProjectChangelog(ctx, tx, appendProjectChangelogParams{
			ProjectID:   out.ProjectID,
			Entity:      "issue",
			Op:          "insert",
			EntityID:    out.ID,
			IssueID:     &out.ID,
			TargetRef:   out.Identifier,
			TargetTitle: out.Title,
			Summary:     changelogIssueSummary(out, "Created issue"),
			Details:     details,
		}); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return model.Issue{}, err
	}
	return s.hydrateIssue(ctx, out)
}

func (s *Store) CreateSubIssue(ctx context.Context, p CreateSubIssueParams) (model.Issue, error) {
	if err := validateIssueDescription(p.Description); err != nil {
		return model.Issue{}, err
	}
	var out model.Issue
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		var (
			number        int
			projectKey    string
			ownerUsername string
			projectID     uuid.UUID
			parentIssueID *uuid.UUID
			parentPrivate bool
		)
		// The project goes first, as it does when completing the parent.
		if err := lockIssueProject(ctx, tx, p.ParentIssueID); err != nil {
			return err
		}
		err := tx.QueryRow(ctx, `
			SELECT pr.next_issue_number, pr.key, u.username, i.project_id, i.parent_issue_id, i.private
			FROM issues i
			JOIN projects pr ON pr.id = i.project_id
			JOIN users u ON u.id = pr.owner_id
			WHERE i.id = $1 AND i.deleted_at IS NULL AND pr.deleted_at IS NULL
			  AND u.deleted_at IS NULL
			FOR UPDATE OF i
		`, p.ParentIssueID).Scan(&number, &projectKey, &ownerUsername, &projectID, &parentIssueID, &parentPrivate)
		if err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err
		}
		if parentIssueID != nil {
			return fmt.Errorf("sub-issues cannot have sub-issues: %w", ErrConflict)
		}
		if err := validateIssuePeople(ctx, tx, projectID, p.AssigneeID, p.ReporterID); err != nil {
			return err
		}

		priority := issuePriorityOrDefault(p.Priority)
		var dueDate *time.Time
		err = tx.QueryRow(ctx, `
			INSERT INTO issues (project_id, number, title, description, priority, worker, private, assignee_id, reporter_id, parent_issue_id, due_date)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
			RETURNING id, project_id, number, title, description, status, close_reason, priority, worker, private,
			          assignee_id, reporter_id, sprint_id, parent_issue_id, due_date, created_at, updated_at
		`, projectID, number, p.Title, p.Description, string(priority), issueWorkerValue(p.Worker), p.Private || parentPrivate, p.AssigneeID, p.ReporterID, p.ParentIssueID, issueDueDateValue(p.DueDate)).
			Scan(&out.ID, &out.ProjectID, &out.Number, &out.Title, &out.Description, &out.Status, &out.CloseReason, &out.Priority, &out.Worker, &out.Private,
				&out.AssigneeID, &out.ReporterID, &out.SprintID, &out.ParentIssueID, &dueDate, &out.CreatedAt, &out.UpdatedAt)
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23503" {
				return fmt.Errorf("invalid assignee/reporter: %w", ErrNotFound)
			}
			return err
		}
		setIssueDueDate(&out, dueDate)

		_, err = tx.Exec(ctx, `
			UPDATE projects
			SET next_issue_number = next_issue_number + 1,
			    updated_at = now()
			WHERE id = $1
		`, projectID)
		if err != nil {
			return err
		}

		out.ProjectKey = projectKey
		out.OwnerUsername = ownerUsername
		out.Identifier = fmt.Sprintf("%s-%d", projectKey, out.Number)
		details := model.ProjectChangelogDetails{}
		if p.Repeat != nil {
			change, err := applyIssueRepeat(ctx, tx, out, *p.Repeat, s.clock())
			if err != nil {
				return err
			}
			out.DueDate = change.DueDate
			details.Changes = changelogAppendChange(details.Changes, "repeat", "Repeat", change.From, change.To)
		}
		if preview := changelogPreview(out.Description); preview != "" {
			details.Preview = preview
		}
		details.PushNotification = pushNotificationChangelogData(out.AssigneeID, pushMentionUsernames(out.Description))
		if err := appendProjectChangelog(ctx, tx, appendProjectChangelogParams{
			ProjectID:     out.ProjectID,
			Entity:        "issue",
			Op:            "insert",
			EntityID:      out.ID,
			IssueID:       &out.ID,
			ParentIssueID: out.ParentIssueID,
			TargetRef:     out.Identifier,
			TargetTitle:   out.Title,
			Summary:       changelogIssueSummary(out, "Created sub-issue"),
			Details:       details,
		}); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return model.Issue{}, err
	}
	return s.hydrateIssue(ctx, out)
}

func (s *Store) GetIssue(ctx context.Context, id uuid.UUID) (model.Issue, error) {
	const q = `
		SELECT i.id, i.project_id, u.username, p.key, i.number, i.title, i.description, i.status, i.close_reason, i.priority, i.worker, i.private,
		       i.assignee_id, i.reporter_id, i.sprint_id, i.parent_issue_id, i.due_date, i.created_at, i.updated_at
		FROM issues i
		JOIN projects p ON p.id = i.project_id
		JOIN users u ON u.id = p.owner_id
		WHERE i.id = $1 AND i.deleted_at IS NULL AND p.deleted_at IS NULL AND u.deleted_at IS NULL
	`
	iss, err := scanIssue(s.db.QueryRow(ctx, q, id))
	if err != nil {
		if isNoRows(err) {
			return model.Issue{}, ErrNotFound
		}
		return model.Issue{}, err
	}
	return s.hydrateIssue(ctx, iss)
}

func getIssueForChangelog(ctx context.Context, q changelogQueryer, id uuid.UUID, includeDeleted bool) (model.Issue, error) {
	deletedClause := "i.deleted_at IS NULL"
	if includeDeleted {
		deletedClause = "TRUE"
	}
	query := fmt.Sprintf(`
		SELECT i.id, i.project_id, u.username, p.key, i.number, i.title, i.description, i.status, i.close_reason, i.priority, i.worker, i.private,
		       i.assignee_id, i.reporter_id, i.sprint_id, i.parent_issue_id, i.due_date, i.created_at, i.updated_at
		FROM issues i
		JOIN projects p ON p.id = i.project_id
		JOIN users u ON u.id = p.owner_id
		WHERE i.id = $1 AND %s AND p.deleted_at IS NULL AND u.deleted_at IS NULL
		FOR UPDATE OF i
	`, deletedClause)
	return scanIssue(q.QueryRow(ctx, query, id))
}

func issueChangelogChanges(ctx context.Context, q changelogQueryer, before, after model.Issue) []model.ProjectChangelogChange {
	changes := []model.ProjectChangelogChange{}
	changes = changelogAppendChange(changes, "title", "Title", before.Title, after.Title)
	changes = changelogAppendChange(changes, "description", "Description", changelogPreview(before.Description), changelogPreview(after.Description))
	changes = changelogAppendChange(changes, "status", "Status", changelogStatusLabel(before.Status), changelogStatusLabel(after.Status))
	changes = changelogAppendChange(changes, "close_reason", "Close reason", changelogCloseReasonLabel(before.CloseReason), changelogCloseReasonLabel(after.CloseReason))
	changes = changelogAppendChange(changes, "priority", "Priority", string(before.Priority), string(after.Priority))
	changes = changelogAppendChange(changes, "worker", "Worker", changelogWorkerLabel(before.Worker), changelogWorkerLabel(after.Worker))
	changes = changelogAppendChange(changes, "private", "Private", changelogYesNo(before.Private), changelogYesNo(after.Private))
	changes = changelogAppendChange(changes, "assignee", "Assignee", changelogUserLabel(ctx, q, before.AssigneeID), changelogUserLabel(ctx, q, after.AssigneeID))
	changes = changelogAppendChange(changes, "reporter", "Reporter", changelogUserLabel(ctx, q, before.ReporterID), changelogUserLabel(ctx, q, after.ReporterID))
	changes = changelogAppendChange(changes, "sprint", "Sprint", changelogSprintLabel(ctx, q, before.SprintID), changelogSprintLabel(ctx, q, after.SprintID))
	changes = changelogAppendChange(changes, "due_date", "Due date", changelogDateLabel(before.DueDate), changelogDateLabel(after.DueDate))
	return changes
}

func (s *Store) GetIssueByOwnerKeyNumber(ctx context.Context, ownerUsername, projectKey string, number int) (model.Issue, error) {
	const q = `
		SELECT i.id, i.project_id, u.username, p.key, i.number, i.title, i.description, i.status, i.close_reason, i.priority, i.worker, i.private,
		       i.assignee_id, i.reporter_id, i.sprint_id, i.parent_issue_id, i.due_date, i.created_at, i.updated_at
		FROM issues i
		JOIN projects p ON p.id = i.project_id
		JOIN users u ON u.id = p.owner_id
		WHERE u.username = $1 AND p.key = $2 AND i.number = $3
		  AND i.deleted_at IS NULL AND p.deleted_at IS NULL AND u.deleted_at IS NULL
	`
	iss, err := scanIssue(s.db.QueryRow(ctx, q, ownerUsername, projectKey, number))
	if err != nil {
		if isNoRows(err) {
			return model.Issue{}, ErrNotFound
		}
		return model.Issue{}, err
	}
	return s.hydrateIssue(ctx, iss)
}

func (s *Store) GetDeletedIssueByOwnerKeyNumber(ctx context.Context, ownerUsername, projectKey string, number int) (model.Issue, error) {
	const q = `
		SELECT i.id, i.project_id, u.username, p.key, i.number, i.title, i.description, i.status, i.close_reason, i.priority, i.worker, i.private,
		       i.assignee_id, i.reporter_id, i.sprint_id, i.parent_issue_id, i.due_date, i.created_at, i.updated_at
		FROM issues i
		JOIN projects p ON p.id = i.project_id
		JOIN users u ON u.id = p.owner_id
		WHERE u.username = $1 AND p.key = $2 AND i.number = $3
		  AND i.deleted_at IS NOT NULL AND p.deleted_at IS NULL AND u.deleted_at IS NULL
	`
	iss, err := scanIssue(s.db.QueryRow(ctx, q, ownerUsername, projectKey, number))
	if err != nil {
		if isNoRows(err) {
			return model.Issue{}, ErrNotFound
		}
		return model.Issue{}, err
	}
	return s.hydrateIssue(ctx, iss)
}

type ListIssuesParams struct {
	ProjectID uuid.UUID
	Status    model.Status // empty = all
	// Statuses filters to issues in any supplied status. Empty = all.
	Statuses []model.Status
	// Priorities filters to issues in any supplied priority. Empty = all.
	Priorities []model.IssuePriority
	// Workers filters to issues marked for any supplied worker. Empty = all.
	Workers []model.IssueWorker
	// UnsetWorker also matches issues with no worker. With no Workers, it
	// matches only those.
	UnsetWorker bool
	// AssigneeIDs filters to issues assigned to any supplied users. Empty = all.
	AssigneeIDs []uuid.UUID
	// ReporterID filters to issues the user reported. Nil = all.
	ReporterID *uuid.UUID
	// TagNames filters to issues tagged with any supplied tag names. Empty = all.
	TagNames []string
	// SprintID filters by sprint. Backlog == true means "WHERE sprint_id IS NULL"
	// and SprintID is ignored. Both nil/false → no sprint filter.
	SprintID  *uuid.UUID
	Backlog   bool
	Cursor    *IssuesCursor
	Limit     int
	Sort      ListIssuesSort
	Direction ListIssuesSortDirection
	// IncludeSubIssues is reserved for explicit parent/child traversal. Product
	// issue collections keep the default top-level-only shape.
	IncludeSubIssues bool
	// IncludePrivate lists private issues too. Set it only for a reader who
	// may see them: a project member, the owner or a site admin.
	IncludePrivate bool
	// Private filters to private (true) or non-private (false) issues. Nil =
	// both, subject to IncludePrivate.
	Private *bool
}

type ListIssuesSort string

const (
	ListIssuesSortNumber   ListIssuesSort = ""
	ListIssuesSortCreated  ListIssuesSort = "created"
	ListIssuesSortUpdated  ListIssuesSort = "updated"
	ListIssuesSortStatus   ListIssuesSort = "status"
	ListIssuesSortPriority ListIssuesSort = "priority"
	ListIssuesSortDueDate  ListIssuesSort = "due"
)

type ListIssuesSortDirection string

const (
	ListIssuesSortAscending  ListIssuesSortDirection = "asc"
	ListIssuesSortDescending ListIssuesSortDirection = "desc"
)

// IssuesCursor includes the sort key plus Number. Number is monotonic per
// project, so it is the stable tie-breaker for every issue listing sort.
type IssuesCursor struct {
	Number    int                 `json:"n,omitempty"`
	CreatedAt time.Time           `json:"ca,omitempty"`
	UpdatedAt time.Time           `json:"ua,omitempty"`
	Status    model.Status        `json:"s,omitempty"`
	Priority  model.IssuePriority `json:"p,omitempty"`
	DueDate   *model.Date         `json:"d,omitempty"`
}

// ListIssuesByIDs returns the issues matching the supplied id set, in no
// guaranteed order. Missing ids are silently skipped — callers diff against
// their request to detect deletions. Designed for batched WebSocket-driven
// refetches; ids should be capped by the caller.
func (s *Store) ListIssuesByIDs(ctx context.Context, ids []uuid.UUID) ([]model.Issue, error) {
	if len(ids) == 0 {
		return []model.Issue{}, nil
	}
	const q = `
		SELECT i.id, i.project_id, u.username, p.key, i.number, i.title, i.description, i.status, i.close_reason, i.priority, i.worker, i.private,
		       i.assignee_id, i.reporter_id, i.sprint_id, i.parent_issue_id, i.due_date, i.created_at, i.updated_at
		FROM issues i
		JOIN projects p ON p.id = i.project_id
		JOIN users u ON u.id = p.owner_id
		WHERE i.id = ANY($1) AND i.deleted_at IS NULL AND p.deleted_at IS NULL AND u.deleted_at IS NULL
	`
	rows, err := s.db.Query(ctx, q, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]model.Issue, 0, len(ids))
	for rows.Next() {
		iss, err := scanIssue(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, iss)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return s.hydrateIssues(ctx, out)
}

func (s *Store) ListIssues(ctx context.Context, p ListIssuesParams) ([]model.Issue, bool, error) {
	args := []any{p.ProjectID}
	q := `
		SELECT i.id, i.project_id, u.username, pr.key, i.number, i.title, i.description, i.status, i.close_reason, i.priority, i.worker, i.private,
		       i.assignee_id, i.reporter_id, i.sprint_id, i.parent_issue_id, i.due_date, i.created_at, i.updated_at
		FROM issues i
		JOIN projects pr ON pr.id = i.project_id
		JOIN users u ON u.id = pr.owner_id
		WHERE i.project_id = $1 AND i.deleted_at IS NULL AND pr.deleted_at IS NULL AND u.deleted_at IS NULL
	`
	if !p.IncludeSubIssues {
		q += " AND i.parent_issue_id IS NULL"
	}
	if !p.IncludePrivate {
		q += " AND NOT i.private"
	}
	if p.Private != nil {
		args = append(args, *p.Private)
		q += fmt.Sprintf(" AND i.private = $%d", len(args))
	}
	statuses := issueStatusFilters(p.Status, p.Statuses)
	if len(statuses) > 0 {
		args = append(args, statuses)
		q += fmt.Sprintf(" AND i.status = ANY($%d::issue_status[])", len(args))
	}
	priorities := issuePriorityFilters(p.Priorities)
	if len(priorities) > 0 {
		args = append(args, priorities)
		q += fmt.Sprintf(" AND i.priority = ANY($%d::issue_priority[])", len(args))
	}
	q = appendIssueWorkerFilter(q, p.Workers, p.UnsetWorker, &args)
	if len(p.AssigneeIDs) > 0 {
		args = append(args, p.AssigneeIDs)
		q += fmt.Sprintf(" AND i.assignee_id = ANY($%d)", len(args))
	}
	if p.ReporterID != nil {
		args = append(args, *p.ReporterID)
		q += fmt.Sprintf(" AND i.reporter_id = $%d", len(args))
	}
	tagNames, err := normalizeIssueTagFilters(p.TagNames)
	if err != nil {
		return nil, false, fmt.Errorf("%s: %w", err.Error(), ErrConflict)
	}
	if len(tagNames) > 0 {
		args = append(args, tagNames)
		q += fmt.Sprintf(`
			AND EXISTS (
				SELECT 1
				FROM issue_tag_links itl
				JOIN issue_tags it ON it.id = itl.tag_id
				WHERE itl.issue_id = i.id AND it.name = ANY($%d)
			)
		`, len(args))
	}
	switch {
	case p.Backlog:
		q += " AND i.sprint_id IS NULL"
	case p.SprintID != nil:
		args = append(args, *p.SprintID)
		q += fmt.Sprintf(" AND i.sprint_id = $%d", len(args))
	}
	q = appendIssueCursor(q, p, &args)
	args = append(args, p.Limit+1)
	q += fmt.Sprintf(" %s LIMIT $%d", issueOrderClause(p.Sort, p.Direction), len(args))

	rows, err := s.db.Query(ctx, q, args...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()

	out := make([]model.Issue, 0, p.Limit)
	for rows.Next() {
		iss, err := scanIssue(rows)
		if err != nil {
			return nil, false, err
		}
		out = append(out, iss)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	hasMore := len(out) > p.Limit
	if hasMore {
		out = out[:p.Limit]
	}
	out, err = s.hydrateIssues(ctx, out)
	if err != nil {
		return nil, false, err
	}
	return out, hasMore, nil
}

func issueStatusFilters(status model.Status, statuses []model.Status) []string {
	out := make([]string, 0, len(statuses)+1)
	seen := map[model.Status]struct{}{}
	if status != "" {
		seen[status] = struct{}{}
		out = append(out, string(status))
	}
	for _, current := range statuses {
		if current == "" {
			continue
		}
		if _, ok := seen[current]; ok {
			continue
		}
		seen[current] = struct{}{}
		out = append(out, string(current))
	}
	return out
}

func issuePriorityFilters(priorities []model.IssuePriority) []string {
	out := make([]string, 0, len(priorities))
	seen := map[model.IssuePriority]struct{}{}
	for _, current := range priorities {
		if current == "" {
			continue
		}
		if _, ok := seen[current]; ok {
			continue
		}
		seen[current] = struct{}{}
		out = append(out, string(current))
	}
	return out
}

func appendIssueWorkerFilter(q string, workers []model.IssueWorker, unset bool, args *[]any) string {
	values := make([]string, 0, len(workers))
	seen := map[model.IssueWorker]struct{}{}
	for _, worker := range workers {
		if _, ok := seen[worker]; ok {
			continue
		}
		seen[worker] = struct{}{}
		values = append(values, string(worker))
	}
	switch {
	case len(values) == 0 && !unset:
		return q
	case len(values) == 0:
		return q + " AND i.worker IS NULL"
	}
	*args = append(*args, values)
	match := fmt.Sprintf("i.worker = ANY($%d::issue_worker[])", len(*args))
	if unset {
		return q + " AND (" + match + " OR i.worker IS NULL)"
	}
	return q + " AND " + match
}

func appendIssueCursor(q string, p ListIssuesParams, args *[]any) string {
	if p.Cursor == nil {
		return q
	}
	direction := issueSortDirection(p.Sort, p.Direction)
	op := ">"
	if direction == ListIssuesSortDescending {
		op = "<"
	}
	switch p.Sort {
	case ListIssuesSortCreated:
		*args = append(*args, p.Cursor.CreatedAt, p.Cursor.Number)
		createdAtArg := len(*args) - 1
		numberArg := len(*args)
		return q + fmt.Sprintf(" AND (i.created_at %s $%d OR (i.created_at = $%d AND i.number %s $%d))", op, createdAtArg, createdAtArg, op, numberArg)
	case ListIssuesSortUpdated:
		*args = append(*args, p.Cursor.UpdatedAt, p.Cursor.Number)
		updatedAtArg := len(*args) - 1
		numberArg := len(*args)
		return q + fmt.Sprintf(" AND (i.updated_at %s $%d OR (i.updated_at = $%d AND i.number %s $%d))", op, updatedAtArg, updatedAtArg, op, numberArg)
	case ListIssuesSortStatus:
		rank := issueStatusSortRank(p.Cursor.Status)
		*args = append(*args, rank, p.Cursor.Number)
		rankArg := len(*args) - 1
		numberArg := len(*args)
		rankExpr := issueStatusRankSQL()
		return q + fmt.Sprintf(" AND ((%s) %s $%d OR ((%s) = $%d AND i.number %s $%d))", rankExpr, op, rankArg, rankExpr, rankArg, op, numberArg)
	case ListIssuesSortPriority:
		rank := issuePrioritySortRank(p.Cursor.Priority)
		*args = append(*args, rank, p.Cursor.Number)
		rankArg := len(*args) - 1
		numberArg := len(*args)
		rankExpr := issuePriorityRankSQL()
		return q + fmt.Sprintf(" AND ((%s) %s $%d OR ((%s) = $%d AND i.number %s $%d))", rankExpr, op, rankArg, rankExpr, rankArg, op, numberArg)
	case ListIssuesSortDueDate:
		return appendIssueDueDateCursor(q, p.Cursor, args, direction)
	default:
		*args = append(*args, p.Cursor.Number)
		return q + fmt.Sprintf(" AND i.number %s $%d", op, len(*args))
	}
}

func appendIssueDueDateCursor(q string, cursor *IssuesCursor, args *[]any, direction ListIssuesSortDirection) string {
	op := ">"
	if direction == ListIssuesSortDescending {
		op = "<"
	}
	if cursor.DueDate == nil {
		*args = append(*args, cursor.Number)
		return q + fmt.Sprintf(" AND i.due_date IS NULL AND i.number %s $%d", op, len(*args))
	}
	*args = append(*args, issueDueDateValue(cursor.DueDate), cursor.Number)
	dueDateArg := len(*args) - 1
	numberArg := len(*args)
	return q + fmt.Sprintf(" AND (i.due_date IS NULL OR i.due_date %s $%d OR (i.due_date = $%d AND i.number %s $%d))", op, dueDateArg, dueDateArg, op, numberArg)
}

func issueOrderClause(sort ListIssuesSort, direction ListIssuesSortDirection) string {
	dir := "ASC"
	if issueSortDirection(sort, direction) == ListIssuesSortDescending {
		dir = "DESC"
	}
	switch sort {
	case ListIssuesSortCreated:
		return "ORDER BY i.created_at " + dir + ", i.number " + dir
	case ListIssuesSortUpdated:
		return "ORDER BY i.updated_at " + dir + ", i.number " + dir
	case ListIssuesSortStatus:
		return "ORDER BY " + issueStatusRankSQL() + " " + dir + ", i.number " + dir
	case ListIssuesSortPriority:
		return "ORDER BY " + issuePriorityRankSQL() + " " + dir + ", i.number " + dir
	case ListIssuesSortDueDate:
		return "ORDER BY i.due_date IS NULL ASC, i.due_date " + dir + ", i.number " + dir
	default:
		return "ORDER BY i.number " + dir
	}
}

func issueSortDirection(sort ListIssuesSort, direction ListIssuesSortDirection) ListIssuesSortDirection {
	switch direction {
	case ListIssuesSortAscending, ListIssuesSortDescending:
		return direction
	}
	switch sort {
	case ListIssuesSortCreated, ListIssuesSortUpdated:
		return ListIssuesSortDescending
	default:
		return ListIssuesSortAscending
	}
}

func issueStatusRankSQL() string {
	return "CASE i.status WHEN 'todo' THEN 0 WHEN 'in_progress' THEN 1 WHEN 'done' THEN 2 WHEN 'closed' THEN 3 ELSE 4 END"
}

func issuePriorityRankSQL() string {
	return "CASE i.priority WHEN 'P0' THEN 0 WHEN 'P1' THEN 1 WHEN 'P2' THEN 2 WHEN 'P3' THEN 3 WHEN 'P4' THEN 4 ELSE 5 END"
}

func issueStatusSortRank(status model.Status) int {
	switch status {
	case model.StatusTodo:
		return 0
	case model.StatusInProgress:
		return 1
	case model.StatusDone:
		return 2
	case model.StatusClosed:
		return 3
	default:
		return 4
	}
}

func issuePrioritySortRank(priority model.IssuePriority) int {
	switch priority {
	case model.PriorityP0:
		return 0
	case model.PriorityP1:
		return 1
	case model.PriorityP2:
		return 2
	case model.PriorityP3:
		return 3
	case model.PriorityP4:
		return 4
	default:
		return 5
	}
}

type ListDeletedIssuesParams struct {
	ProjectID uuid.UUID
	Cursor    *IssuesCursor
	Limit     int
}

// ListDeletedIssues lists a project's deleted issues. A sub-issue is listed
// only while its parent is live: one whose parent is deleted too comes back
// when the parent is restored.
func (s *Store) ListDeletedIssues(ctx context.Context, p ListDeletedIssuesParams) ([]model.Issue, bool, error) {
	args := []any{p.ProjectID}
	q := `
		SELECT i.id, i.project_id, u.username, pr.key, i.number, i.title, i.description, i.status, i.close_reason, i.priority, i.worker, i.private,
		       i.assignee_id, i.reporter_id, i.sprint_id, i.parent_issue_id, i.due_date, i.created_at, i.updated_at
		FROM issues i
		JOIN projects pr ON pr.id = i.project_id
		JOIN users u ON u.id = pr.owner_id
		LEFT JOIN issues parent ON parent.id = i.parent_issue_id
		WHERE i.project_id = $1 AND i.deleted_at IS NOT NULL
		  AND (i.parent_issue_id IS NULL OR parent.deleted_at IS NULL)
		  AND pr.deleted_at IS NULL AND u.deleted_at IS NULL
	`
	if p.Cursor != nil {
		args = append(args, p.Cursor.Number)
		q += fmt.Sprintf(" AND i.number > $%d", len(args))
	}
	args = append(args, p.Limit+1)
	q += fmt.Sprintf(" ORDER BY i.number ASC LIMIT $%d", len(args))

	rows, err := s.db.Query(ctx, q, args...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()

	out := make([]model.Issue, 0, p.Limit)
	for rows.Next() {
		iss, err := scanIssue(rows)
		if err != nil {
			return nil, false, err
		}
		out = append(out, iss)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	hasMore := len(out) > p.Limit
	if hasMore {
		out = out[:p.Limit]
	}
	out, err = s.hydrateIssues(ctx, out)
	if err != nil {
		return nil, false, err
	}
	return out, hasMore, nil
}

type ListSubIssuesForIssueParams struct {
	ParentIssueID uuid.UUID
	Cursor        *IssuesCursor
	Limit         int
	// IncludePrivate lists private sub-issues too; see ListIssuesParams.
	IncludePrivate bool
}

type SubIssueProgress struct {
	Total     int
	Completed int
}

// ListSubIssueProgress counts each parent's sub-issues. includePrivate counts
// private sub-issues too; see ListIssuesParams.IncludePrivate.
func (s *Store) ListSubIssueProgress(ctx context.Context, parentIssueIDs []uuid.UUID, includePrivate bool) (map[uuid.UUID]SubIssueProgress, error) {
	out := make(map[uuid.UUID]SubIssueProgress, len(parentIssueIDs))
	if len(parentIssueIDs) == 0 {
		return out, nil
	}

	rows, err := s.db.Query(ctx, `
		SELECT parent_issue_id,
		       COUNT(*)::int,
		       COUNT(*) FILTER (WHERE status IN ('done', 'closed'))::int
		FROM issues
		WHERE parent_issue_id = ANY($1) AND deleted_at IS NULL AND ($2 OR NOT private)
		GROUP BY parent_issue_id
	`, parentIssueIDs, includePrivate)
	if err != nil {
		// Defensive: aggregate query failures require a database/runtime fault.
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var parentIssueID uuid.UUID
		var progress SubIssueProgress
		if err := rows.Scan(&parentIssueID, &progress.Total, &progress.Completed); err != nil {
			// Defensive: selected columns match the aggregate result fields.
			return nil, err
		}
		out[parentIssueID] = progress
	}
	if err := rows.Err(); err != nil {
		// Defensive: post-scan failures require a database/runtime fault.
		return nil, err
	}
	return out, nil
}

func (s *Store) ListSubIssuesForIssue(ctx context.Context, p ListSubIssuesForIssueParams) ([]model.Issue, bool, error) {
	var parentIssueID *uuid.UUID
	if err := s.db.QueryRow(ctx, `
		SELECT i.parent_issue_id
		FROM issues i
		JOIN projects pr ON pr.id = i.project_id
		WHERE i.id = $1 AND i.deleted_at IS NULL AND pr.deleted_at IS NULL
	`, p.ParentIssueID).Scan(&parentIssueID); err != nil {
		if isNoRows(err) {
			return nil, false, ErrNotFound
		}
		// Defensive: only no-rows has a domain mapping here.
		return nil, false, err
	}
	if parentIssueID != nil {
		return nil, false, fmt.Errorf("sub-issues cannot have sub-issues: %w", ErrConflict)
	}

	args := []any{p.ParentIssueID}
	q := `
		SELECT i.id, i.project_id, u.username, pr.key, i.number, i.title, i.description, i.status, i.close_reason, i.priority, i.worker, i.private,
		       i.assignee_id, i.reporter_id, i.sprint_id, i.parent_issue_id, i.due_date, i.created_at, i.updated_at
		FROM issues i
		JOIN projects pr ON pr.id = i.project_id
		JOIN users u ON u.id = pr.owner_id
		WHERE i.parent_issue_id = $1 AND i.deleted_at IS NULL AND pr.deleted_at IS NULL AND u.deleted_at IS NULL
	`
	if !p.IncludePrivate {
		q += " AND NOT i.private"
	}
	if p.Cursor != nil {
		args = append(args, p.Cursor.Number)
		q += fmt.Sprintf(" AND i.number > $%d", len(args))
	}
	args = append(args, p.Limit+1)
	q += fmt.Sprintf(" ORDER BY i.number ASC LIMIT $%d", len(args))

	rows, err := s.db.Query(ctx, q, args...)
	if err != nil {
		// Defensive: list query has no expected constraint mapping.
		return nil, false, err
	}
	defer rows.Close()

	out := make([]model.Issue, 0, p.Limit)
	for rows.Next() {
		iss, err := scanIssue(rows)
		if err != nil {
			// Defensive: selected columns match model fields.
			return nil, false, err
		}
		out = append(out, iss)
	}
	if err := rows.Err(); err != nil {
		// Defensive: scan/query failures after setup are DB/runtime faults.
		return nil, false, err
	}
	hasMore := len(out) > p.Limit
	if hasMore {
		out = out[:p.Limit]
	}
	out, err = s.hydrateIssues(ctx, out)
	if err != nil {
		return nil, false, err
	}
	return out, hasMore, nil
}

type UpdateIssueParams struct {
	Title         *string
	Description   *string
	Status        *model.Status
	CloseReason   *model.IssueCloseReason
	Priority      *model.IssuePriority
	Worker        *model.IssueWorker
	ClearWorker   bool
	Private       *bool
	AssigneeID    *uuid.UUID
	ClearAssignee bool
	ReporterID    *uuid.UUID
	ClearReporter bool
	SprintID      *uuid.UUID
	ClearSprint   bool
	DueDate       *model.Date
	ClearDueDate  bool
	// Repeat makes the issue repeat, or changes how it repeats; ClearRepeat
	// stops it repeating and wins over Repeat.
	Repeat      *IssueRepeatSetting
	ClearRepeat bool
}

func (s *Store) UpdateIssue(ctx context.Context, id uuid.UUID, p UpdateIssueParams) (model.Issue, error) {
	if p.Description != nil {
		if err := validateIssueDescription(*p.Description); err != nil {
			return model.Issue{}, err
		}
	}
	sets := []string{}
	args := []any{}
	i := 1
	if p.Title != nil {
		sets = append(sets, fmt.Sprintf("title = $%d", i))
		args = append(args, *p.Title)
		i++
	}
	if p.Description != nil {
		sets = append(sets, fmt.Sprintf("description = $%d", i))
		args = append(args, *p.Description)
		i++
	}
	if p.Status != nil {
		sets = append(sets, fmt.Sprintf("status = $%d", i))
		args = append(args, string(*p.Status))
		i++
		if *p.Status != model.StatusClosed {
			sets = append(sets, "close_reason = NULL")
		}
	}
	if p.CloseReason != nil {
		sets = append(sets, fmt.Sprintf("close_reason = $%d", i))
		args = append(args, string(*p.CloseReason))
		i++
	}
	if p.Priority != nil {
		sets = append(sets, fmt.Sprintf("priority = $%d", i))
		args = append(args, string(*p.Priority))
		i++
	}
	if p.ClearWorker {
		sets = append(sets, "worker = NULL")
	} else if p.Worker != nil {
		sets = append(sets, fmt.Sprintf("worker = $%d", i))
		args = append(args, string(*p.Worker))
		i++
	}
	if p.Private != nil {
		sets = append(sets, fmt.Sprintf("private = $%d", i))
		args = append(args, *p.Private)
		i++
	}
	if p.ClearDueDate {
		sets = append(sets, "due_date = NULL")
	} else if p.DueDate != nil {
		sets = append(sets, fmt.Sprintf("due_date = $%d", i))
		args = append(args, issueDueDateValue(p.DueDate))
		i++
	}
	if p.ClearAssignee {
		sets = append(sets, "assignee_id = NULL")
	} else if p.AssigneeID != nil {
		sets = append(sets, fmt.Sprintf("assignee_id = $%d", i))
		args = append(args, *p.AssigneeID)
		i++
	}
	if p.ClearReporter {
		sets = append(sets, "reporter_id = NULL")
	} else if p.ReporterID != nil {
		sets = append(sets, fmt.Sprintf("reporter_id = $%d", i))
		args = append(args, *p.ReporterID)
		i++
	}
	if p.ClearSprint {
		sets = append(sets, "sprint_id = NULL")
	} else if p.SprintID != nil {
		sets = append(sets, fmt.Sprintf("sprint_id = $%d", i))
		args = append(args, *p.SprintID)
		i++
	}

	changesRepeat := p.ClearRepeat || p.Repeat != nil
	if len(sets) == 0 && !changesRepeat {
		return s.GetIssue(ctx, id)
	}

	sets = append(sets, "updated_at = now()")
	args = append(args, id)
	q := fmt.Sprintf(`
		UPDATE issues SET %s WHERE id = $%d AND deleted_at IS NULL
	`, strings.Join(sets, ", "), i)

	editSprint := p.ClearSprint || (p.SprintID != nil && !p.ClearSprint)
	validateCloseReason := p.Status != nil || p.CloseReason != nil

	var out model.Issue
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		if p.Status != nil && p.Status.CountsAsDone() {
			if err := lockIssueProject(ctx, tx, id); err != nil {
				return err
			}
		}
		before, err := getIssueForChangelog(ctx, tx, id, false)
		if err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err
		}
		issueProject := before.ProjectID
		parentIssueID := before.ParentIssueID
		issueStatus := before.Status
		var assigneeID *uuid.UUID
		if !p.ClearAssignee {
			assigneeID = p.AssigneeID
		}
		var reporterID *uuid.UUID
		if !p.ClearReporter {
			reporterID = p.ReporterID
		}
		if err := validateIssuePeople(ctx, tx, issueProject, assigneeID, reporterID); err != nil {
			return err
		}
		if validateCloseReason {
			if p.Status != nil && !p.Status.Valid() {
				return fmt.Errorf("invalid issue status: %w", ErrConflict)
			}
			if p.CloseReason != nil && !p.CloseReason.Valid() {
				return fmt.Errorf("invalid close reason: %w", ErrConflict)
			}
			effectiveStatus := issueStatus
			if p.Status != nil {
				effectiveStatus = *p.Status
			}
			if p.Status != nil && issueStatus != model.StatusClosed && *p.Status == model.StatusClosed && p.CloseReason == nil {
				return fmt.Errorf("close reason required: %w", ErrConflict)
			}
			if p.CloseReason != nil && effectiveStatus != model.StatusClosed {
				return fmt.Errorf("close reason only applies to closed issues: %w", ErrConflict)
			}
		}
		if p.Private != nil && !*p.Private && parentIssueID != nil {
			var parentPrivate bool
			// FOR SHARE waits for a concurrent change to the parent, so a
			// parent going private can't race a sub-issue going public.
			if err := tx.QueryRow(ctx, `SELECT private FROM issues WHERE id = $1 FOR SHARE`, *parentIssueID).Scan(&parentPrivate); err != nil {
				return err // defensive: the parent is a foreign key of a live issue
			}
			if parentPrivate {
				return ErrSubIssueOfPrivateIssue
			}
		}
		if editSprint {
			if issueStatus.CountsAsDone() || (p.Status != nil && p.Status.CountsAsDone()) {
				return fmt.Errorf("cannot edit sprint for completed issue: %w", ErrConflict)
			}
			if parentIssueID != nil {
				return fmt.Errorf("sub-issues cannot be assigned to sprints: %w", ErrConflict)
			}
		}
		if p.SprintID != nil && !p.ClearSprint {
			var sprintProject uuid.UUID
			var sprintStatus model.SprintStatus
			if err := tx.QueryRow(ctx, `SELECT project_id, status FROM sprints WHERE id = $1 AND deleted_at IS NULL FOR SHARE`, *p.SprintID).Scan(&sprintProject, &sprintStatus); err != nil {
				if isNoRows(err) {
					return fmt.Errorf("sprint not found: %w", ErrConflict)
				}
				return err
			}
			if issueProject != sprintProject {
				return fmt.Errorf("sprint belongs to a different project: %w", ErrConflict)
			}
			if sprintStatus == model.SprintStatusCompleted {
				return fmt.Errorf("cannot assign issue to completed sprint: %w", ErrConflict)
			}
		}
		// The repeat changes first, so a due date in the same request wins
		// over the schedule's, and an issue made to repeat and completed in
		// one request still creates its next repetition.
		var repeatChange issueRepeatChange
		if p.ClearRepeat {
			if repeatChange, err = stopIssueRepeat(ctx, tx, id); err != nil {
				return err
			}
		} else if p.Repeat != nil {
			effectiveStatus := issueStatus
			if p.Status != nil {
				effectiveStatus = *p.Status
			}
			if issueStatus.CountsAsDone() && effectiveStatus.CountsAsDone() {
				return ErrRepeatOnCompletedIssue
			}
			if repeatChange, err = applyIssueRepeat(ctx, tx, before, *p.Repeat, s.clock()); err != nil {
				return err
			}
		}
		tag, err := tx.Exec(ctx, q, args...)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		after, err := getIssueForChangelog(ctx, tx, id, false)
		if err != nil {
			return err
		}
		out = after
		if !before.Private && after.Private {
			if err := makeSubIssuesPrivate(ctx, tx, after.ID); err != nil {
				return err
			}
		}
		if !before.Status.CountsAsDone() && after.Status.CountsAsDone() {
			if _, err := advanceIssueRepeat(ctx, tx, after, s.clock()); err != nil {
				return err
			}
		}
		changes := issueChangelogChanges(ctx, tx, before, after)
		changes = changelogAppendChange(changes, "repeat", "Repeat", repeatChange.From, repeatChange.To)
		if len(changes) == 0 {
			return nil
		}
		targetRef, targetTitle := changelogTarget(after)
		details := model.ProjectChangelogDetails{Changes: changes}
		var assignedID *uuid.UUID
		if !sameUUIDPtr(before.AssigneeID, after.AssigneeID) {
			assignedID = after.AssigneeID
		}
		details.PushNotification = pushNotificationChangelogData(
			assignedID,
			pushNewMentionUsernames(before.Description, after.Description),
		)
		return appendProjectChangelog(ctx, tx, appendProjectChangelogParams{
			ProjectID:     after.ProjectID,
			Entity:        "issue",
			Op:            "update",
			EntityID:      after.ID,
			IssueID:       &after.ID,
			ParentIssueID: after.ParentIssueID,
			TargetRef:     targetRef,
			TargetTitle:   targetTitle,
			Summary:       changelogIssueSummary(after, "Updated issue"),
			Details:       details,
		})
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			switch pgErr.Code {
			case "23503":
				return model.Issue{}, fmt.Errorf("invalid assignee, reporter, or sprint: %w", ErrConflict)
			case "23514":
				return model.Issue{}, fmt.Errorf("invalid issue close reason state: %w", ErrConflict)
			}
		}
		return model.Issue{}, err
	}
	return s.hydrateIssue(ctx, out)
}

// ErrSubIssueOfPrivateIssue refuses to make public a sub-issue whose parent is
// private: a sub-issue is never more visible than the issue it belongs to.
var ErrSubIssueOfPrivateIssue = fmt.Errorf("a sub-issue of a private issue stays private: %w", ErrConflict)

// makeSubIssuesPrivate makes every sub-issue of a newly private issue private
// too, deleted ones included so a restore can't bring one back public, and
// records the change on each live one.
func makeSubIssuesPrivate(ctx context.Context, tx pgx.Tx, parentID uuid.UUID) error {
	rows, err := tx.Query(ctx, `
		UPDATE issues SET private = true, updated_at = now()
		WHERE parent_issue_id = $1 AND NOT private
		RETURNING id, deleted_at IS NULL
	`, parentID)
	if err != nil {
		return err
	}
	var live []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		var isLive bool
		if err := rows.Scan(&id, &isLive); err != nil {
			rows.Close()
			return err
		}
		if isLive {
			live = append(live, id)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range live {
		child, err := getIssueForChangelog(ctx, tx, id, false)
		if err != nil {
			return err
		}
		targetRef, targetTitle := changelogTarget(child)
		if err := appendProjectChangelog(ctx, tx, appendProjectChangelogParams{
			ProjectID:     child.ProjectID,
			Entity:        "issue",
			Op:            "update",
			EntityID:      child.ID,
			IssueID:       &child.ID,
			ParentIssueID: child.ParentIssueID,
			TargetRef:     targetRef,
			TargetTitle:   targetTitle,
			Summary:       changelogIssueSummary(child, "Updated issue"),
			Details: model.ProjectChangelogDetails{Changes: []model.ProjectChangelogChange{
				changelogChange("private", "Private", changelogYesNo(false), changelogYesNo(true)),
			}},
		}); err != nil {
			return err
		}
	}
	return nil
}

func issueProjectMemberExists(ctx context.Context, tx pgx.Tx, projectID, userID uuid.UUID) (bool, error) {
	var ok bool
	err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM projects p
			JOIN users u ON u.id = $2 AND u.deleted_at IS NULL
			LEFT JOIN project_members pm ON pm.project_id = p.id AND pm.user_id = u.id
			WHERE p.id = $1
			  AND p.deleted_at IS NULL
			  AND (p.owner_id = u.id OR pm.role = 'member')
		)
	`, projectID, userID).Scan(&ok)
	return ok, err
}

func issueProjectReporterExists(ctx context.Context, tx pgx.Tx, projectID, userID uuid.UUID) (bool, error) {
	var ok bool
	err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM projects p
			JOIN users u ON u.id = $2 AND u.deleted_at IS NULL
			LEFT JOIN project_members pm ON pm.project_id = p.id AND pm.user_id = u.id
			WHERE p.id = $1
			  AND p.deleted_at IS NULL
			  AND NOT EXISTS (
			      SELECT 1 FROM project_user_blocks b
			      WHERE b.project_id = p.id AND b.user_id = u.id
			  )
			  AND (
			      p.owner_id = u.id
			      OR pm.role = 'member'
			      OR (pm.user_id IS NULL AND p.access_mode IN ('public_issues', 'helpdesk'))
			  )
		)
	`, projectID, userID).Scan(&ok)
	return ok, err
}

func validateIssuePeople(ctx context.Context, tx pgx.Tx, projectID uuid.UUID, assigneeID, reporterID *uuid.UUID) error {
	if assigneeID != nil {
		ok, err := issueProjectMemberExists(ctx, tx, projectID, *assigneeID)
		if err != nil {
			return err
		}
		if !ok {
			return ErrNotFound
		}
	}
	if reporterID != nil {
		ok, err := issueProjectReporterExists(ctx, tx, projectID, *reporterID)
		if err != nil {
			return err
		}
		if !ok {
			return ErrNotFound
		}
	}
	return nil
}

func (s *Store) DeleteIssue(ctx context.Context, id uuid.UUID) error {
	return pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		before, err := getIssueForChangelog(ctx, tx, id, false)
		if err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err
		}
		tag, err := tx.Exec(ctx, `
			UPDATE issues SET deleted_at = now(), updated_at = now()
			WHERE (id = $1 OR parent_issue_id = $1) AND deleted_at IS NULL
		`, id)
		if err != nil {
			// Defensive: soft-delete has no expected FK/check mapping.
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		targetRef, targetTitle := changelogTarget(before)
		return appendProjectChangelog(ctx, tx, appendProjectChangelogParams{
			ProjectID:     before.ProjectID,
			Entity:        "issue",
			Op:            "delete",
			EntityID:      before.ID,
			IssueID:       &before.ID,
			ParentIssueID: before.ParentIssueID,
			TargetRef:     targetRef,
			TargetTitle:   targetTitle,
			Summary:       changelogIssueSummary(before, "Deleted issue"),
		})
	})
}

func (s *Store) RestoreIssue(ctx context.Context, id uuid.UUID) (model.Issue, error) {
	var out model.Issue
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		before, err := getIssueForChangelog(ctx, tx, id, true)
		if err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err
		}
		// An unfinished issue whose sprint was deleted or completed while it
		// was gone comes back to the backlog, as it would have gone there or
		// rolled over had it been live.
		tag, err := tx.Exec(ctx, `
			WITH target AS (
				SELECT id, parent_issue_id
				FROM issues
				WHERE id = $1 AND deleted_at IS NOT NULL
			),
			restore_ids AS (
				SELECT id FROM target
				UNION
				SELECT parent_issue_id FROM target WHERE parent_issue_id IS NOT NULL
				UNION
				SELECT i.id
				FROM issues i
				JOIN target t ON i.parent_issue_id = t.id
			)
			UPDATE issues i
			SET deleted_at = NULL, updated_at = now(),
			    sprint_id = CASE
			        WHEN i.status NOT IN ('done', 'closed') AND EXISTS (
			            SELECT 1 FROM sprints s
			            WHERE s.id = i.sprint_id AND (s.deleted_at IS NOT NULL OR s.status = 'completed')
			        ) THEN NULL
			        ELSE i.sprint_id
			    END
			WHERE i.id IN (SELECT id FROM restore_ids) AND i.deleted_at IS NOT NULL
		`, id)
		if err != nil {
			// Defensive: restore has no expected FK/check mapping.
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		out, err = getIssueForChangelog(ctx, tx, id, false)
		if err != nil {
			return err
		}
		targetRef, targetTitle := changelogTarget(out)
		return appendProjectChangelog(ctx, tx, appendProjectChangelogParams{
			ProjectID:     out.ProjectID,
			Entity:        "issue",
			Op:            "restore",
			EntityID:      out.ID,
			IssueID:       &out.ID,
			ParentIssueID: out.ParentIssueID,
			TargetRef:     targetRef,
			TargetTitle:   targetTitle,
			Summary:       changelogIssueSummary(before, "Restored issue"),
		})
	})
	if err != nil {
		return model.Issue{}, err
	}
	return s.hydrateIssue(ctx, out)
}
