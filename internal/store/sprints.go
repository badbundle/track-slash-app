package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/bradleymackey/track-slash/internal/model"
)

type CreateSprintParams struct {
	ProjectID uuid.UUID
	Name      string
	Goal      string
	StartDate *time.Time
	EndDate   *time.Time
}

type sprintScanner interface {
	Scan(dest ...any) error
}

func scanSprint(row sprintScanner) (model.Sprint, error) {
	var out model.Sprint
	var plannedOrder sql.NullInt64
	var startDate, endDate sql.NullTime
	err := row.Scan(
		&out.ID, &out.ProjectID, &out.Number, &out.Name, &out.Goal, &out.Status, &plannedOrder,
		&startDate, &endDate, &out.CompletedAt, &out.CreatedAt, &out.UpdatedAt,
	)
	if err != nil {
		return model.Sprint{}, err
	}
	out.Ref = model.SprintRef(out.Number)
	if plannedOrder.Valid {
		order := plannedOrder.Int64
		out.PlannedOrder = &order
	}
	if startDate.Valid {
		t := startDate.Time
		out.StartDate = &t
	}
	if endDate.Valid {
		t := endDate.Time
		out.EndDate = &t
	}
	return out, nil
}

func (s *Store) CreateSprint(ctx context.Context, p CreateSprintParams) (model.Sprint, error) {
	if err := validateSprintDateRange(p.StartDate, p.EndDate); err != nil {
		return model.Sprint{}, err
	}
	var out model.Sprint
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		var (
			projectID uuid.UUID
			number    int
		)
		if err := tx.QueryRow(ctx, `
			SELECT id, next_sprint_number FROM projects WHERE id = $1 AND deleted_at IS NULL FOR UPDATE
		`, p.ProjectID).Scan(&projectID, &number); err != nil {
			if isNoRows(err) {
				return fmt.Errorf("project not found: %w", ErrNotFound)
			}
			return err
		}

		var plannedOrder int64
		if err := tx.QueryRow(ctx, `
			SELECT COALESCE(MAX(planned_order), 0) + 1
			FROM sprints
			WHERE project_id = $1 AND status = 'planned' AND deleted_at IS NULL
		`, projectID).Scan(&plannedOrder); err != nil {
			return err
		}

		var err error
		out, err = scanSprint(tx.QueryRow(ctx, `
			INSERT INTO sprints (project_id, number, name, goal, start_date, end_date, planned_order)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			RETURNING id, project_id, number, name, goal, status, planned_order, start_date, end_date,
			          completed_at, created_at, updated_at
		`, projectID, number, p.Name, p.Goal, p.StartDate, p.EndDate, plannedOrder))
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `
			UPDATE projects SET next_sprint_number = next_sprint_number + 1, updated_at = now()
			WHERE id = $1
		`, projectID)
		if err != nil {
			return err
		}
		return appendProjectChangelog(ctx, tx, appendProjectChangelogParams{
			ProjectID:   out.ProjectID,
			Entity:      "sprint",
			Op:          "insert",
			EntityID:    out.ID,
			TargetRef:   changelogSprintRef(out),
			TargetTitle: out.Name,
			Summary:     fmt.Sprintf("Created sprint %s", out.Name),
			Details:     model.ProjectChangelogDetails{Preview: changelogPreview(out.Goal)},
		})
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			switch pgErr.Code {
			case "23503":
				return model.Sprint{}, fmt.Errorf("project not found: %w", ErrNotFound)
			case "23514":
				return model.Sprint{}, fmt.Errorf("end_date must be on or after start_date: %w", ErrConflict)
			}
		}
		return model.Sprint{}, err
	}
	return out, nil
}

func (s *Store) GetSprint(ctx context.Context, id uuid.UUID) (model.Sprint, error) {
	const q = `
		SELECT id, project_id, number, name, goal, status, planned_order, start_date, end_date,
		       completed_at, created_at, updated_at
		FROM sprints WHERE id = $1 AND deleted_at IS NULL
	`
	out, err := scanSprint(s.db.QueryRow(ctx, q, id))
	if err != nil {
		if isNoRows(err) {
			return model.Sprint{}, ErrNotFound
		}
		return model.Sprint{}, err
	}
	return out, nil
}

func (s *Store) GetSprintByProjectNumber(ctx context.Context, projectID uuid.UUID, number int) (model.Sprint, error) {
	const q = `
		SELECT id, project_id, number, name, goal, status, planned_order, start_date, end_date,
		       completed_at, created_at, updated_at
		FROM sprints
		WHERE project_id = $1 AND number = $2 AND deleted_at IS NULL
	`
	out, err := scanSprint(s.db.QueryRow(ctx, q, projectID, number))
	if err != nil {
		if isNoRows(err) {
			return model.Sprint{}, ErrNotFound
		}
		return model.Sprint{}, err
	}
	return out, nil
}

type ListSprintsParams struct {
	ProjectID uuid.UUID
	Status    model.SprintStatus // empty = all
	Sort      ListSprintsSort
	Cursor    *SprintsCursor
	Limit     int
}

type ListSprintsSort string

const (
	ListSprintsSortStartDate ListSprintsSort = ""
	ListSprintsSortCompleted ListSprintsSort = "completed"
)

// SprintsCursor keys off the active ordering. Planned lists use
// (planned_order, id), completed lists use (completed_at IS NULL,
// completed_at, id), and other lists use (start_date IS NULL, start_date,
// created_at, id).
type SprintsCursor struct {
	PlannedOrder int64      `json:"p,omitempty"`
	StartDate    *time.Time `json:"s,omitempty"`
	CompletedAt  *time.Time `json:"x,omitempty"`
	CreatedAt    time.Time  `json:"c"`
	ID           uuid.UUID  `json:"i"`
}

func (s *Store) ListSprints(ctx context.Context, p ListSprintsParams) ([]model.Sprint, bool, error) {
	args := []any{p.ProjectID}
	q := `
		SELECT id, project_id, number, name, goal, status, planned_order, start_date, end_date,
		       completed_at, created_at, updated_at
		FROM sprints
		WHERE project_id = $1 AND deleted_at IS NULL
	`
	if p.Status != "" {
		args = append(args, string(p.Status))
		q += fmt.Sprintf(" AND status = $%d", len(args))
	}
	if p.Cursor != nil && p.Sort == ListSprintsSortCompleted {
		if p.Cursor.CompletedAt == nil {
			args = append(args, p.Cursor.ID)
			q += fmt.Sprintf(" AND completed_at IS NULL AND id < $%d", len(args))
		} else {
			args = append(args, *p.Cursor.CompletedAt, p.Cursor.ID)
			q += fmt.Sprintf(" AND (completed_at IS NULL OR completed_at < $%d OR (completed_at = $%d AND id < $%d))",
				len(args)-1, len(args)-1, len(args))
		}
	} else if p.Cursor != nil && p.Status == model.SprintStatusPlanned {
		args = append(args, p.Cursor.PlannedOrder, p.Cursor.ID)
		q += fmt.Sprintf(" AND (planned_order, id) > ($%d, $%d)",
			len(args)-1, len(args))
	} else if p.Cursor != nil {
		if p.Cursor.StartDate == nil {
			args = append(args, p.Cursor.CreatedAt, p.Cursor.ID)
			q += fmt.Sprintf(" AND start_date IS NULL AND (created_at, id) > ($%d, $%d)",
				len(args)-1, len(args))
		} else {
			args = append(args, *p.Cursor.StartDate, p.Cursor.CreatedAt, p.Cursor.ID)
			q += fmt.Sprintf(" AND (start_date IS NULL OR start_date > $%d OR (start_date = $%d AND (created_at, id) > ($%d, $%d)))",
				len(args)-2, len(args)-2, len(args)-1, len(args))
		}
	}
	args = append(args, p.Limit+1)
	if p.Sort == ListSprintsSortCompleted {
		q += fmt.Sprintf(" ORDER BY completed_at DESC NULLS LAST, id DESC LIMIT $%d", len(args))
	} else if p.Status == model.SprintStatusPlanned {
		q += fmt.Sprintf(" ORDER BY planned_order ASC NULLS LAST, id ASC LIMIT $%d", len(args))
	} else {
		q += fmt.Sprintf(" ORDER BY start_date IS NULL ASC, start_date ASC, created_at ASC, id ASC LIMIT $%d", len(args))
	}

	rows, err := s.db.Query(ctx, q, args...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()

	out := make([]model.Sprint, 0, p.Limit)
	for rows.Next() {
		sp, err := scanSprint(rows)
		if err != nil {
			return nil, false, err
		}
		out = append(out, sp)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	hasMore := len(out) > p.Limit
	if hasMore {
		out = out[:p.Limit]
	}
	return out, hasMore, nil
}

type ListSprintSnapshotIssuesParams struct {
	ProjectID uuid.UUID
	SprintID  uuid.UUID
	Cursor    *IssuesCursor
	Limit     int
}

type CountSprintSnapshotIssuesByStatusParams struct {
	ProjectID uuid.UUID
	SprintIDs []uuid.UUID
}

func (s *Store) CountSprintSnapshotIssuesByStatus(ctx context.Context, p CountSprintSnapshotIssuesByStatusParams) (map[uuid.UUID]model.ProjectIssueStatusCounts, error) {
	out := make(map[uuid.UUID]model.ProjectIssueStatusCounts, len(p.SprintIDs))
	if len(p.SprintIDs) == 0 {
		return out, nil
	}
	rows, err := s.db.Query(ctx, `
		SELECT
			sis.sprint_id,
			COUNT(*)::INT,
			(COUNT(*) FILTER (WHERE sis.status = 'todo'))::INT,
			(COUNT(*) FILTER (WHERE sis.status = 'in_progress'))::INT,
			(COUNT(*) FILTER (WHERE sis.status = 'done'))::INT,
			(COUNT(*) FILTER (WHERE sis.status = 'closed'))::INT
		FROM sprint_issue_snapshots sis
		JOIN sprints sp ON sp.id = sis.sprint_id AND sp.project_id = sis.project_id
		JOIN projects p ON p.id = sis.project_id
		WHERE sis.project_id = $1 AND sis.sprint_id = ANY($2)
		  AND sp.deleted_at IS NULL AND p.deleted_at IS NULL
		GROUP BY sis.sprint_id
	`, p.ProjectID, p.SprintIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var sprintID uuid.UUID
		var counts model.ProjectIssueStatusCounts
		if err := rows.Scan(&sprintID, &counts.Total, &counts.Todo, &counts.InProgress, &counts.Done, &counts.Closed); err != nil {
			return nil, err
		}
		out[sprintID] = counts
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// ListSprintSnapshotIssues returns the current issue records for the immutable
// membership captured when a sprint completed. Soft-deleted issues remain in
// the history because deleting an issue does not erase its sprint membership.
func (s *Store) ListSprintSnapshotIssues(ctx context.Context, p ListSprintSnapshotIssuesParams) ([]model.Issue, bool, error) {
	args := []any{p.ProjectID, p.SprintID}
	q := `
		SELECT i.id, i.project_id, u.username, pr.key, i.number, i.title, i.description, i.status, i.close_reason, i.priority,
		       i.assignee_id, i.reporter_id, i.sprint_id, i.parent_issue_id, i.due_date, i.created_at, i.updated_at
		FROM sprint_issue_snapshots sis
		JOIN issues i ON i.id = sis.issue_id AND i.project_id = sis.project_id
		JOIN projects pr ON pr.id = i.project_id
		JOIN users u ON u.id = pr.owner_id
		WHERE sis.project_id = $1 AND sis.sprint_id = $2
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
		issue, err := scanIssue(rows)
		if err != nil {
			return nil, false, err
		}
		out = append(out, issue)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	hasMore := len(out) > p.Limit
	if hasMore {
		out = out[:p.Limit]
	}
	out, err = s.hydrateIssueTags(ctx, out)
	if err != nil {
		return nil, false, err
	}
	return out, hasMore, nil
}

type UpdateSprintParams struct {
	Name       *string
	Goal       *string
	StartDate  *time.Time
	EndDate    *time.Time
	ClearDates bool
	// Status: callers may only drive planned → active. Completion is
	// reserved for CompleteSprint so the rollover always runs atomically.
	Status *model.SprintStatus
}

func (s *Store) UpdateSprint(ctx context.Context, id uuid.UUID, p UpdateSprintParams) (model.Sprint, error) {
	var out model.Sprint
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		starting := p.Status != nil && *p.Status == model.SprintStatusActive
		sprintsEnabled := false
		if starting {
			// Starting a sprint takes the project row lock before the sprint
			// lock, the same order CreateSprint and ReorderPlannedSprints use.
			// SetProjectSprintsEnabled holds that lock while it checks for an
			// active sprint, so the two can never interleave.
			if err := tx.QueryRow(ctx, `
				SELECT p.sprints_enabled
				FROM sprints s
				JOIN projects p ON p.id = s.project_id
				WHERE s.id = $1 AND s.deleted_at IS NULL AND p.deleted_at IS NULL
				FOR UPDATE OF p
			`, id).Scan(&sprintsEnabled); err != nil {
				if isNoRows(err) {
					return ErrNotFound
				}
				// Defensive: a keyed lookup only fails here on a DB fault.
				return err
			}
		}
		before, err := scanSprint(tx.QueryRow(ctx, `
			SELECT id, project_id, number, name, goal, status, planned_order, start_date, end_date,
			       completed_at, created_at, updated_at
			FROM sprints
			WHERE id = $1 AND deleted_at IS NULL
			FOR UPDATE
		`, id))
		if err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err
		}
		current := before.Status
		if current == model.SprintStatusCompleted &&
			(p.Goal != nil || p.StartDate != nil || p.EndDate != nil || p.ClearDates || p.Status != nil) {
			return fmt.Errorf("completed sprint can only be renamed: %w", ErrConflict)
		}
		if p.Status != nil {
			if err := validateSprintTransition(current, *p.Status); err != nil {
				return err
			}
		}
		if starting && current == model.SprintStatusPlanned && !sprintsEnabled {
			return ErrSprintsDisabled
		}
		if p.ClearDates || p.StartDate != nil || p.EndDate != nil {
			startDate, endDate := sprintUpdatedDateRange(before, p)
			if err := validateSprintDateRange(startDate, endDate); err != nil {
				return err
			}
		}

		sets := []string{}
		args := []any{}
		i := 1
		if p.Name != nil {
			sets = append(sets, fmt.Sprintf("name = $%d", i))
			args = append(args, *p.Name)
			i++
		}
		if p.Goal != nil {
			sets = append(sets, fmt.Sprintf("goal = $%d", i))
			args = append(args, *p.Goal)
			i++
		}
		if p.ClearDates {
			sets = append(sets, "start_date = NULL", "end_date = NULL")
		} else if p.StartDate != nil {
			sets = append(sets, fmt.Sprintf("start_date = $%d", i))
			args = append(args, *p.StartDate)
			i++
		}
		if !p.ClearDates && p.EndDate != nil {
			sets = append(sets, fmt.Sprintf("end_date = $%d", i))
			args = append(args, *p.EndDate)
			i++
		}
		if p.Status != nil {
			sets = append(sets, fmt.Sprintf("status = $%d", i))
			args = append(args, string(*p.Status))
			i++
			if current == model.SprintStatusPlanned && *p.Status == model.SprintStatusActive {
				sets = append(sets, "planned_order = NULL")
			}
		}

		if len(sets) == 0 {
			out = before
			return nil
		}

		sets = append(sets, "updated_at = now()")
		args = append(args, id)
		q := fmt.Sprintf(`
			UPDATE sprints SET %s WHERE id = $%d AND deleted_at IS NULL
			RETURNING id, project_id, number, name, goal, status, planned_order, start_date, end_date,
			          completed_at, created_at, updated_at
	`, strings.Join(sets, ", "), i)

		out, err = scanSprint(tx.QueryRow(ctx, q, args...))
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) {
				switch pgErr.Code {
				case "23505":
					return fmt.Errorf("another sprint is already active in this project: %w", ErrConflict)
				case "23514":
					return fmt.Errorf("end_date must be on or after start_date: %w", ErrConflict)
				}
			}
			return err
		}
		changes := []model.ProjectChangelogChange{}
		changes = changelogAppendChange(changes, "name", "Name", before.Name, out.Name)
		changes = changelogAppendChange(changes, "goal", "Goal", changelogPreview(before.Goal), changelogPreview(out.Goal))
		changes = changelogAppendChange(changes, "status", "Status", changelogSprintStatusLabel(before.Status), changelogSprintStatusLabel(out.Status))
		changes = changelogAppendChange(changes, "start_date", "Start date", changelogSprintDateLabel(before.StartDate), changelogSprintDateLabel(out.StartDate))
		changes = changelogAppendChange(changes, "end_date", "End date", changelogSprintDateLabel(before.EndDate), changelogSprintDateLabel(out.EndDate))
		if len(changes) == 0 {
			return nil
		}
		return appendProjectChangelog(ctx, tx, appendProjectChangelogParams{
			ProjectID:   out.ProjectID,
			Entity:      "sprint",
			Op:          "update",
			EntityID:    out.ID,
			TargetRef:   changelogSprintRef(out),
			TargetTitle: out.Name,
			Summary:     fmt.Sprintf("Updated sprint %s", out.Name),
			Details:     model.ProjectChangelogDetails{Changes: changes},
		})
	})
	if err != nil {
		return model.Sprint{}, err
	}
	return out, nil
}

func validateSprintTransition(from, to model.SprintStatus) error {
	if from == to {
		return nil
	}
	if from == model.SprintStatusPlanned && to == model.SprintStatusActive {
		return nil
	}
	if to == model.SprintStatusCompleted {
		return fmt.Errorf("complete via POST /sprints/sprint-N/complete: %w", ErrConflict)
	}
	return fmt.Errorf("invalid sprint transition %s -> %s: %w", from, to, ErrConflict)
}

func sprintUpdatedDateRange(before model.Sprint, p UpdateSprintParams) (*time.Time, *time.Time) {
	if p.ClearDates {
		return nil, nil
	}
	startDate := before.StartDate
	endDate := before.EndDate
	if p.StartDate != nil {
		startDate = p.StartDate
	}
	if p.EndDate != nil {
		endDate = p.EndDate
	}
	return startDate, endDate
}

func validateSprintDateRange(startDate, endDate *time.Time) error {
	switch {
	case startDate == nil && endDate == nil:
		return nil
	case startDate == nil || endDate == nil:
		return fmt.Errorf("sprint dates must include both start_date and end_date: %w", ErrConflict)
	case endDate.Before(*startDate):
		return fmt.Errorf("end_date must be on or after start_date: %w", ErrConflict)
	default:
		return nil
	}
}

// CompleteSprint snapshots current issue membership and marks the sprint
// completed atomically. Non-terminal issues then roll into the next planned
// sprint when one exists, otherwise they fall back to the backlog;
// done-equivalent issues stay attached so historical velocity is preserved.
func (s *Store) CompleteSprint(ctx context.Context, id uuid.UUID) (model.Sprint, error) {
	var out model.Sprint
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		var projectID uuid.UUID
		var status model.SprintStatus
		err := tx.QueryRow(ctx, `
			SELECT project_id, status FROM sprints WHERE id = $1 AND deleted_at IS NULL FOR UPDATE
		`, id).Scan(&projectID, &status)
		if err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err
		}
		if status != model.SprintStatusActive {
			return fmt.Errorf("can only complete an active sprint (current: %s): %w", status, ErrConflict)
		}

		if _, err := tx.Exec(ctx, `
			INSERT INTO sprint_issue_snapshots (project_id, sprint_id, issue_id, status)
			SELECT $2, $1, sprint_issue.id, sprint_issue.status
			FROM (
				SELECT id, status
				FROM issues
				WHERE sprint_id = $1 AND deleted_at IS NULL
				FOR UPDATE
			) AS sprint_issue
		`, id, projectID); err != nil {
			return err
		}

		var nextSprintID uuid.UUID
		nextErr := tx.QueryRow(ctx, `
			SELECT id
			FROM sprints
			WHERE project_id = $1 AND status = 'planned' AND deleted_at IS NULL
			ORDER BY planned_order ASC NULLS LAST, id ASC
			LIMIT 1
			FOR UPDATE
		`, projectID).Scan(&nextSprintID)
		if nextErr != nil && !isNoRows(nextErr) {
			return nextErr
		}

		if isNoRows(nextErr) {
			_, err = tx.Exec(ctx, `
				UPDATE issues SET sprint_id = NULL, updated_at = now()
				WHERE sprint_id = $1 AND status NOT IN ('done', 'closed') AND deleted_at IS NULL
			`, id)
		} else {
			_, err = tx.Exec(ctx, `
				UPDATE issues SET sprint_id = $2, updated_at = now()
				WHERE sprint_id = $1 AND status NOT IN ('done', 'closed') AND deleted_at IS NULL
			`, id, nextSprintID)
		}
		if err != nil {
			return err
		}

		out, err = scanSprint(tx.QueryRow(ctx, `
			UPDATE sprints
			SET status = 'completed', planned_order = NULL, completed_at = now(), updated_at = now()
			WHERE id = $1 AND deleted_at IS NULL
			RETURNING id, project_id, number, name, goal, status, planned_order, start_date, end_date,
			          completed_at, created_at, updated_at
		`, id))
		if err != nil {
			return err
		}
		return appendProjectChangelog(ctx, tx, appendProjectChangelogParams{
			ProjectID:   out.ProjectID,
			Entity:      "sprint",
			Op:          "complete",
			EntityID:    out.ID,
			TargetRef:   changelogSprintRef(out),
			TargetTitle: out.Name,
			Summary:     fmt.Sprintf("Completed sprint %s", out.Name),
		})
	})
	if err != nil {
		return model.Sprint{}, err
	}
	return out, nil
}

func (s *Store) DeleteSprint(ctx context.Context, id uuid.UUID) error {
	return pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		before, err := scanSprint(tx.QueryRow(ctx, `
			SELECT id, project_id, number, name, goal, status, planned_order, start_date, end_date,
			       completed_at, created_at, updated_at
			FROM sprints
			WHERE id = $1 AND deleted_at IS NULL
			FOR UPDATE
		`, id))
		if err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err
		}
		if before.Status == model.SprintStatusActive || before.Status == model.SprintStatusCompleted {
			return fmt.Errorf("cannot delete %s sprint: %w", before.Status, ErrConflict)
		}
		tag, err := tx.Exec(ctx, `
			UPDATE sprints SET deleted_at = now(), updated_at = now()
			WHERE id = $1 AND deleted_at IS NULL AND status = 'planned'
		`, id)
		if err != nil {
			// Defensive: soft-delete has no expected FK/check mapping.
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return appendProjectChangelog(ctx, tx, appendProjectChangelogParams{
			ProjectID:   before.ProjectID,
			Entity:      "sprint",
			Op:          "delete",
			EntityID:    before.ID,
			TargetRef:   changelogSprintRef(before),
			TargetTitle: before.Name,
			Summary:     fmt.Sprintf("Deleted sprint %s", before.Name),
		})
	})
}

type ReorderPlannedSprintsParams struct {
	ProjectID uuid.UUID
	SprintIDs []uuid.UUID
}

func (s *Store) ReorderPlannedSprints(ctx context.Context, p ReorderPlannedSprintsParams) ([]model.Sprint, error) {
	var out []model.Sprint
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		var projectID uuid.UUID
		if err := tx.QueryRow(ctx, `
			SELECT id FROM projects WHERE id = $1 AND deleted_at IS NULL FOR UPDATE
		`, p.ProjectID).Scan(&projectID); err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err
		}

		rows, err := tx.Query(ctx, `
			SELECT id
			FROM sprints
			WHERE project_id = $1 AND status = 'planned' AND deleted_at IS NULL
			ORDER BY planned_order ASC NULLS LAST, id ASC
			FOR UPDATE
		`, projectID)
		if err != nil {
			return err
		}
		defer rows.Close()

		current := map[uuid.UUID]struct{}{}
		currentOrder := []uuid.UUID{}
		for rows.Next() {
			var id uuid.UUID
			if err := rows.Scan(&id); err != nil {
				return err
			}
			current[id] = struct{}{}
			currentOrder = append(currentOrder, id)
		}
		if err := rows.Err(); err != nil {
			return err
		}

		if len(p.SprintIDs) != len(current) {
			return fmt.Errorf("sprint_refs must include every planned sprint exactly once: %w", ErrConflict)
		}
		seen := map[uuid.UUID]struct{}{}
		for _, id := range p.SprintIDs {
			if _, ok := current[id]; !ok {
				return fmt.Errorf("sprint_refs must include only planned sprints from this project: %w", ErrConflict)
			}
			if _, dup := seen[id]; dup {
				return fmt.Errorf("sprint_refs must not contain duplicates: %w", ErrConflict)
			}
			seen[id] = struct{}{}
		}
		if len(p.SprintIDs) == 0 {
			out = []model.Sprint{}
			return nil
		}
		changedOrder := !sameUUIDOrder(currentOrder, p.SprintIDs)

		offset := int64(len(p.SprintIDs) + 1)
		if _, err := tx.Exec(ctx, `
			UPDATE sprints
			SET planned_order = COALESCE(planned_order, 0) + $2
			WHERE project_id = $1 AND status = 'planned' AND deleted_at IS NULL
		`, projectID, offset); err != nil {
			return err
		}

		for i, id := range p.SprintIDs {
			if _, err := tx.Exec(ctx, `
				UPDATE sprints
				SET planned_order = $2, updated_at = now()
				WHERE id = $1 AND project_id = $3 AND status = 'planned' AND deleted_at IS NULL
			`, id, int64(i+1), projectID); err != nil {
				return err
			}
		}

		rows, err = tx.Query(ctx, `
			SELECT id, project_id, number, name, goal, status, planned_order, start_date, end_date,
			       completed_at, created_at, updated_at
			FROM sprints
			WHERE project_id = $1 AND status = 'planned' AND deleted_at IS NULL
			ORDER BY planned_order ASC, id ASC
		`, projectID)
		if err != nil {
			return err
		}
		defer rows.Close()

		out = make([]model.Sprint, 0, len(p.SprintIDs))
		for rows.Next() {
			sp, err := scanSprint(rows)
			if err != nil {
				return err
			}
			out = append(out, sp)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if !changedOrder {
			return nil
		}
		return appendProjectChangelog(ctx, tx, appendProjectChangelogParams{
			ProjectID:   projectID,
			Entity:      "sprint",
			Op:          "reorder",
			EntityID:    projectID,
			TargetTitle: "Planned sprints",
			Summary:     "Reordered planned sprints",
		})
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func sameUUIDOrder(a, b []uuid.UUID) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
