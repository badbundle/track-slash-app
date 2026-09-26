package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/bradleymackey/track-slash/internal/model"
)

const whiteboardPageEntity = "whiteboard_page"

type CreateWhiteboardPageParams struct {
	ProjectID   uuid.UUID
	Title       string
	Body        string
	CreatedByID uuid.UUID
}

type UpdateWhiteboardPageParams struct {
	ID          uuid.UUID
	Title       *string
	Body        *string
	UpdatedByID uuid.UUID
}

// WhiteboardPagesCursor pages through the most-recently-updated-first order.
type WhiteboardPagesCursor struct {
	UpdatedAt time.Time `json:"t"`
	ID        uuid.UUID `json:"i"`
}

type ListWhiteboardPagesParams struct {
	ProjectID uuid.UUID
	Cursor    *WhiteboardPagesCursor
	Limit     int
}

const whiteboardPageColumns = `
	wp.id, wp.project_id, wp.number, wp.title, wp.body,
	wp.created_by_id, wp.updated_by_id, wp.created_at, wp.updated_at
`

type whiteboardPageScanner interface {
	Scan(dest ...any) error
}

func scanWhiteboardPage(row whiteboardPageScanner) (model.WhiteboardPage, error) {
	var out model.WhiteboardPage
	if err := row.Scan(
		&out.ID, &out.ProjectID, &out.Number, &out.Title, &out.Body,
		&out.CreatedByID, &out.UpdatedByID, &out.CreatedAt, &out.UpdatedAt,
	); err != nil {
		return model.WhiteboardPage{}, err
	}
	out.Ref = model.WhiteboardPageRef(out.Number)
	return out, nil
}

func scanWhiteboardPageSummary(row whiteboardPageScanner) (model.WhiteboardPageSummary, error) {
	var out model.WhiteboardPageSummary
	if err := row.Scan(
		&out.ID, &out.ProjectID, &out.Number, &out.Title,
		&out.CreatedByID, &out.UpdatedByID, &out.CreatedAt, &out.UpdatedAt,
	); err != nil {
		return model.WhiteboardPageSummary{}, err // defensive: columns match the fixed SELECT list
	}
	out.Ref = model.WhiteboardPageRef(out.Number)
	return out, nil
}

// CreateWhiteboardPage adds a page to a live project and allocates its
// project-scoped whiteboard-N ref under the project row lock.
func (s *Store) CreateWhiteboardPage(ctx context.Context, p CreateWhiteboardPageParams) (model.WhiteboardPage, error) {
	var out model.WhiteboardPage
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		var number int
		if err := tx.QueryRow(ctx, `
			SELECT next_whiteboard_page_number
			FROM projects
			WHERE id = $1 AND deleted_at IS NULL
			FOR UPDATE
		`, p.ProjectID).Scan(&number); err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err // defensive: DB outage past the no-rows branch
		}

		var err error
		out, err = scanWhiteboardPage(tx.QueryRow(ctx, `
			INSERT INTO whiteboard_pages AS wp (project_id, number, title, body, created_by_id, updated_by_id)
			VALUES ($1, $2, $3, $4, $5, $5)
			RETURNING `+whiteboardPageColumns,
			p.ProjectID, number, p.Title, p.Body, p.CreatedByID))
		if err != nil {
			return mapWhiteboardPageWriteError(err)
		}

		if _, err := tx.Exec(ctx, `
			UPDATE projects
			SET next_whiteboard_page_number = next_whiteboard_page_number + 1
			WHERE id = $1
		`, p.ProjectID); err != nil {
			return err // defensive: project row was locked above
		}
		return appendProjectChangelog(ctx, tx, appendProjectChangelogParams{
			ProjectID:   out.ProjectID,
			Entity:      whiteboardPageEntity,
			Op:          "insert",
			EntityID:    out.ID,
			TargetRef:   out.Ref,
			TargetTitle: out.Title,
			Summary:     fmt.Sprintf("Created whiteboard page %s", out.Title),
			Details:     model.ProjectChangelogDetails{Preview: changelogPreview(out.Body)},
		})
	})
	if err != nil {
		return model.WhiteboardPage{}, err
	}
	return out, nil
}

// GetWhiteboardPageByProjectNumber returns a live page by its whiteboard-N
// number. Deleted pages, and pages of deleted projects, are not found.
func (s *Store) GetWhiteboardPageByProjectNumber(ctx context.Context, projectID uuid.UUID, number int) (model.WhiteboardPage, error) {
	out, err := scanWhiteboardPage(s.db.QueryRow(ctx, `
		SELECT `+whiteboardPageColumns+`
		FROM whiteboard_pages wp
		JOIN projects p ON p.id = wp.project_id
		WHERE wp.project_id = $1 AND wp.number = $2
		  AND wp.deleted_at IS NULL AND p.deleted_at IS NULL
	`, projectID, number))
	if err != nil {
		if isNoRows(err) {
			return model.WhiteboardPage{}, ErrNotFound
		}
		return model.WhiteboardPage{}, err // defensive: DB outage past the no-rows branch
	}
	return out, nil
}

// ListWhiteboardPages lists live pages most recently updated first, without
// their bodies.
func (s *Store) ListWhiteboardPages(ctx context.Context, p ListWhiteboardPagesParams) ([]model.WhiteboardPageSummary, bool, error) {
	if _, err := s.GetProject(ctx, p.ProjectID); err != nil {
		return nil, false, err
	}
	args := []any{p.ProjectID}
	q := `
		SELECT wp.id, wp.project_id, wp.number, wp.title,
		       wp.created_by_id, wp.updated_by_id, wp.created_at, wp.updated_at
		FROM whiteboard_pages wp
		WHERE wp.project_id = $1 AND wp.deleted_at IS NULL
	`
	if p.Cursor != nil {
		args = append(args, p.Cursor.UpdatedAt, p.Cursor.ID)
		q += fmt.Sprintf(" AND (wp.updated_at, wp.id) < ($%d, $%d)", len(args)-1, len(args))
	}
	args = append(args, p.Limit+1)
	q += fmt.Sprintf(" ORDER BY wp.updated_at DESC, wp.id DESC LIMIT $%d", len(args))

	rows, err := s.db.Query(ctx, q, args...)
	if err != nil {
		return nil, false, err // defensive: the project lookup above already reached the DB
	}
	defer rows.Close()

	out := make([]model.WhiteboardPageSummary, 0, p.Limit)
	for rows.Next() {
		item, err := scanWhiteboardPageSummary(rows)
		if err != nil {
			return nil, false, err // defensive: see scanWhiteboardPageSummary
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err // defensive: connection lost mid-read
	}
	hasMore := len(out) > p.Limit
	if hasMore {
		out = out[:p.Limit]
	}
	return out, hasMore, nil
}

// UpdateWhiteboardPage changes a live page's title and/or body. A save that
// changes nothing leaves the page, its place in the list, and the changelog
// untouched.
func (s *Store) UpdateWhiteboardPage(ctx context.Context, p UpdateWhiteboardPageParams) (model.WhiteboardPage, error) {
	var out model.WhiteboardPage
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		before, err := scanWhiteboardPage(tx.QueryRow(ctx, `
			SELECT `+whiteboardPageColumns+`
			FROM whiteboard_pages wp
			JOIN projects p ON p.id = wp.project_id
			WHERE wp.id = $1 AND wp.deleted_at IS NULL AND p.deleted_at IS NULL
			FOR UPDATE OF wp
		`, p.ID))
		if err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err // defensive: DB outage past the no-rows branch
		}
		titleChanged := p.Title != nil && *p.Title != before.Title
		bodyChanged := p.Body != nil && *p.Body != before.Body
		if !titleChanged && !bodyChanged {
			out = before
			return nil
		}
		changes := []model.ProjectChangelogChange{}
		if titleChanged {
			changes = append(changes, changelogChange("title", "Title", before.Title, *p.Title))
		}
		if bodyChanged {
			changes = append(changes, changelogChange("body", "Body", changelogPreview(before.Body), changelogPreview(*p.Body)))
		}

		out, err = scanWhiteboardPage(tx.QueryRow(ctx, `
			UPDATE whiteboard_pages wp
			SET title = COALESCE($2, title),
			    body = COALESCE($3, body),
			    updated_by_id = $4,
			    updated_at = GREATEST(clock_timestamp(), wp.updated_at + interval '1 microsecond')
			WHERE wp.id = $1
			RETURNING `+whiteboardPageColumns,
			p.ID, p.Title, p.Body, p.UpdatedByID))
		if err != nil {
			return mapWhiteboardPageWriteError(err)
		}
		return appendProjectChangelog(ctx, tx, appendProjectChangelogParams{
			ProjectID:   out.ProjectID,
			Entity:      whiteboardPageEntity,
			Op:          "update",
			EntityID:    out.ID,
			TargetRef:   out.Ref,
			TargetTitle: out.Title,
			Summary:     fmt.Sprintf("Updated whiteboard page %s", out.Title),
			Details:     model.ProjectChangelogDetails{Changes: changes},
		})
	})
	if err != nil {
		return model.WhiteboardPage{}, err
	}
	return out, nil
}

// DeleteWhiteboardPage soft-deletes a live page. Its number stays allocated so
// the ref is never handed to a different page.
func (s *Store) DeleteWhiteboardPage(ctx context.Context, id uuid.UUID) error {
	return pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		deleted, err := scanWhiteboardPage(tx.QueryRow(ctx, `
			UPDATE whiteboard_pages wp
			SET deleted_at = now()
			FROM projects p
			WHERE wp.id = $1 AND wp.deleted_at IS NULL
			  AND p.id = wp.project_id AND p.deleted_at IS NULL
			RETURNING `+whiteboardPageColumns, id))
		if err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err // defensive: DB outage past the no-rows branch
		}
		return appendProjectChangelog(ctx, tx, appendProjectChangelogParams{
			ProjectID:   deleted.ProjectID,
			Entity:      whiteboardPageEntity,
			Op:          "delete",
			EntityID:    deleted.ID,
			TargetRef:   deleted.Ref,
			TargetTitle: deleted.Title,
			Summary:     fmt.Sprintf("Deleted whiteboard page %s", deleted.Title),
		})
	})
}

// ProjectIDForWhiteboardPage resolves a live page's project for realtime topic
// authorization.
func (s *Store) ProjectIDForWhiteboardPage(ctx context.Context, id uuid.UUID) (uuid.UUID, error) {
	return s.lookupProjectID(ctx, `
		SELECT wp.project_id
		FROM whiteboard_pages wp
		JOIN projects p ON p.id = wp.project_id
		WHERE wp.id = $1 AND wp.deleted_at IS NULL AND p.deleted_at IS NULL
	`, id)
}

func mapWhiteboardPageWriteError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23503":
			return fmt.Errorf("project or user not found: %w", ErrNotFound)
		case "23514":
			return fmt.Errorf("title/body outside allowed length: %w", ErrConflict)
		}
	}
	return err // defensive: non-pg or unmapped pg error
}
