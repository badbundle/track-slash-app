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

type CreateCommentParams struct {
	IssueID  uuid.UUID
	AuthorID uuid.UUID
	Body     string
	// Visibility defaults to the project's DefaultCommentVisibility when empty.
	Visibility model.CommentVisibility
}

type UpdateCommentParams struct {
	ID       uuid.UUID
	AuthorID uuid.UUID
	Body     string
	// Visibility leaves the comment's visibility unchanged when nil.
	Visibility *model.CommentVisibility
}

// ErrInvalidCommentVisibility rejects a visibility other than shared or members.
var ErrInvalidCommentVisibility = fmt.Errorf("visibility must be shared or members: %w", ErrConflict)

// commentColumns is the column list scanComment reads, in order.
const commentColumns = `id, issue_id, number, author_id, body, visibility, created_at, updated_at`

type DeleteCommentParams struct {
	ID       uuid.UUID
	AuthorID uuid.UUID
}

type commentScanner interface {
	Scan(dest ...any) error
}

func scanComment(row commentScanner) (model.Comment, error) {
	var out model.Comment
	err := row.Scan(&out.ID, &out.IssueID, &out.Number, &out.AuthorID, &out.Body, &out.Visibility, &out.CreatedAt, &out.UpdatedAt)
	if err != nil {
		return model.Comment{}, err
	}
	out.Ref = model.CommentRef(out.Number)
	if out.UpdatedAt.After(out.CreatedAt) {
		editedAt := out.UpdatedAt
		out.EditedAt = &editedAt
	}
	return out, nil
}

func (s *Store) CreateComment(ctx context.Context, p CreateCommentParams) (model.Comment, error) {
	if p.Visibility != "" && !p.Visibility.Valid() {
		return model.Comment{}, ErrInvalidCommentVisibility
	}
	var out model.Comment
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		var number int
		var accessMode model.ProjectAccessMode
		if err := tx.QueryRow(ctx, `
			SELECT i.next_comment_number, p.access_mode
			FROM issues i
			JOIN projects p ON p.id = i.project_id
			WHERE i.id = $1 AND i.deleted_at IS NULL
			FOR UPDATE OF i
		`, p.IssueID).Scan(&number, &accessMode); err != nil {
			if isNoRows(err) {
				return fmt.Errorf("issue not found: %w", ErrNotFound)
			}
			return err
		}
		visibility := p.Visibility
		if visibility == "" {
			visibility = model.DefaultCommentVisibility(accessMode)
		}
		var err error
		out, err = scanComment(tx.QueryRow(ctx, `
			INSERT INTO comments (issue_id, number, author_id, body, visibility)
			VALUES ($1, $2, $3, $4, $5)
			RETURNING `+commentColumns, p.IssueID, number, p.AuthorID, p.Body, visibility))
		if err != nil {
			return err
		}
		issue, err := getIssueForChangelog(ctx, tx, p.IssueID, false)
		if err != nil {
			return err
		}
		targetRef, targetTitle := changelogTarget(issue)
		if err := appendProjectChangelog(ctx, tx, appendProjectChangelogParams{
			ProjectID:   issue.ProjectID,
			Entity:      "comment",
			Op:          "insert",
			EntityID:    out.ID,
			IssueID:     &issue.ID,
			TargetRef:   targetRef,
			TargetTitle: targetTitle,
			Summary:     fmt.Sprintf("Commented on %s", issue.Identifier),
			Details: model.ProjectChangelogDetails{
				Preview:          changelogPreview(out.Body),
				PushNotification: pushNotificationChangelogData(nil, pushMentionUsernames(out.Body)),
			},
			MembersOnly: out.Visibility == model.CommentVisibilityMembers,
		}); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `
			UPDATE issues SET next_comment_number = next_comment_number + 1, updated_at = now()
			WHERE id = $1
		`, p.IssueID)
		return err
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			switch pgErr.Code {
			case "23503":
				return model.Comment{}, fmt.Errorf("issue or author not found: %w", ErrNotFound)
			case "23514":
				return model.Comment{}, fmt.Errorf("body must be 1..10000 chars: %w", ErrConflict)
			}
		}
		// Defensive: all expected constraint failures are mapped above.
		return model.Comment{}, err
	}
	return out, nil
}

func (s *Store) GetComment(ctx context.Context, id uuid.UUID) (model.Comment, error) {
	const q = `SELECT ` + commentColumns + ` FROM comments WHERE id = $1`
	out, err := scanComment(s.db.QueryRow(ctx, q, id))
	if err != nil {
		if isNoRows(err) {
			return model.Comment{}, ErrNotFound
		}
		// Defensive: only no-rows has a domain mapping here.
		return model.Comment{}, err
	}
	return out, nil
}

func (s *Store) GetCommentForIssueByNumber(ctx context.Context, issueID uuid.UUID, number int) (model.Comment, error) {
	const q = `SELECT ` + commentColumns + ` FROM comments WHERE issue_id = $1 AND number = $2`
	out, err := scanComment(s.db.QueryRow(ctx, q, issueID, number))
	if err != nil {
		if isNoRows(err) {
			return model.Comment{}, ErrNotFound
		}
		return model.Comment{}, err
	}
	return out, nil
}

type CommentsCursor struct {
	CreatedAt time.Time `json:"t"`
	ID        uuid.UUID `json:"i"`
}

type ListCommentsForIssueParams struct {
	IssueID     uuid.UUID
	Cursor      *CommentsCursor
	Limit       int
	NewestFirst bool
	// IncludeMembersOnly adds members-only comments. Leave it false for a
	// reader who is not a project member.
	IncludeMembersOnly bool
}

func (s *Store) ListCommentsForIssue(ctx context.Context, p ListCommentsForIssueParams) ([]model.Comment, bool, error) {
	var issueID uuid.UUID
	if err := s.db.QueryRow(ctx, `SELECT id FROM issues WHERE id = $1 AND deleted_at IS NULL`, p.IssueID).Scan(&issueID); err != nil {
		if isNoRows(err) {
			return nil, false, ErrNotFound
		}
		// Defensive: only no-rows has a domain mapping here.
		return nil, false, err
	}

	args := []any{p.IssueID, p.IncludeMembersOnly}
	q := `
		SELECT c.id, c.issue_id, c.number, c.author_id, c.body, c.visibility, c.created_at, c.updated_at
		FROM comments c
		WHERE c.issue_id = $1 AND (c.visibility = 'shared' OR $2)
	`
	if p.Cursor != nil {
		args = append(args, p.Cursor.CreatedAt, p.Cursor.ID)
		op := ">"
		if p.NewestFirst {
			op = "<"
		}
		q += fmt.Sprintf(" AND (c.created_at, c.id) %s ($%d, $%d)", op, len(args)-1, len(args))
	}
	args = append(args, p.Limit+1)
	order := "ASC"
	if p.NewestFirst {
		order = "DESC"
	}
	q += fmt.Sprintf(" ORDER BY c.created_at %s, c.id %s LIMIT $%d", order, order, len(args))

	rows, err := s.db.Query(ctx, q, args...)
	if err != nil {
		// Defensive: comment list has no expected constraint mapping.
		return nil, false, err
	}
	defer rows.Close()

	out := make([]model.Comment, 0, p.Limit)
	for rows.Next() {
		c, err := scanComment(rows)
		if err != nil {
			// Defensive: selected columns match model fields.
			return nil, false, err
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		// Defensive: scan/query failures after setup are DB/runtime faults.
		return nil, false, err
	}
	hasMore := len(out) > p.Limit
	if hasMore {
		out = out[:p.Limit]
	}
	return out, hasMore, nil
}

func (s *Store) UpdateComment(ctx context.Context, p UpdateCommentParams) (model.Comment, error) {
	if p.Visibility != nil && !p.Visibility.Valid() {
		return model.Comment{}, ErrInvalidCommentVisibility
	}
	var out model.Comment
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		before, err := scanComment(tx.QueryRow(ctx, `
			SELECT `+commentColumns+`
			FROM comments
			WHERE id = $1 AND author_id = $2
			FOR UPDATE
		`, p.ID, p.AuthorID))
		if err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err
		}
		visibility := before.Visibility
		if p.Visibility != nil {
			visibility = *p.Visibility
		}
		if before.Body == p.Body && before.Visibility == visibility {
			out = before
			return nil
		}
		// Only a body change counts as an edit; changing who can see the
		// comment leaves its edited time alone.
		out, err = scanComment(tx.QueryRow(ctx, `
			UPDATE comments
			SET body = $3,
				visibility = $4,
				updated_at = CASE WHEN body = $3 THEN updated_at
					ELSE GREATEST(clock_timestamp(), created_at + interval '1 microsecond') END
			WHERE id = $1 AND author_id = $2
			RETURNING `+commentColumns, p.ID, p.AuthorID, p.Body, visibility))
		if err != nil {
			return err
		}
		issue, err := getIssueForChangelog(ctx, tx, out.IssueID, false)
		if err != nil {
			return err
		}
		membersOnly := out.Visibility == model.CommentVisibilityMembers
		if before.Visibility != out.Visibility {
			// Earlier entries carry the comment's body preview, so they
			// follow its new visibility.
			if _, err := tx.Exec(ctx, `
				UPDATE project_changelog_entries
				SET members_only = $2
				WHERE entity = 'comment' AND entity_id = $1 AND members_only <> $2
			`, out.ID, membersOnly); err != nil {
				return err
			}
		}
		var changes []model.ProjectChangelogChange
		changes = changelogAppendChange(changes, "body", "Comment", changelogPreview(before.Body), changelogPreview(out.Body))
		changes = changelogAppendChange(changes, "visibility", "Visibility", before.Visibility.Label(), out.Visibility.Label())
		summary := fmt.Sprintf("Edited comment on %s", issue.Identifier)
		if before.Body == out.Body {
			summary = fmt.Sprintf("Changed comment visibility on %s", issue.Identifier)
		}
		targetRef, targetTitle := changelogTarget(issue)
		return appendProjectChangelog(ctx, tx, appendProjectChangelogParams{
			ProjectID:   issue.ProjectID,
			Entity:      "comment",
			Op:          "update",
			EntityID:    out.ID,
			IssueID:     &issue.ID,
			TargetRef:   targetRef,
			TargetTitle: targetTitle,
			Summary:     summary,
			Details:     model.ProjectChangelogDetails{Changes: changes},
			MembersOnly: membersOnly,
		})
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23514" {
			return model.Comment{}, fmt.Errorf("body must be 1..10000 chars: %w", ErrConflict)
		}
		// Defensive: all expected update failures are mapped above.
		return model.Comment{}, err
	}
	return out, nil
}

func (s *Store) DeleteComment(ctx context.Context, p DeleteCommentParams) error {
	return pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		before, err := scanComment(tx.QueryRow(ctx, `
			SELECT `+commentColumns+`
			FROM comments
			WHERE id = $1 AND author_id = $2
			FOR UPDATE
		`, p.ID, p.AuthorID))
		if err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err
		}
		issue, err := getIssueForChangelog(ctx, tx, before.IssueID, false)
		if err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `DELETE FROM comments WHERE id = $1 AND author_id = $2`, p.ID, p.AuthorID)
		if err != nil {
			// Defensive: delete has no expected FK/check mapping.
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		// A deleted comment's previews are for members, so its earlier
		// entries are kept to them, as a members-only comment's are.
		if _, err := tx.Exec(ctx, `
			UPDATE project_changelog_entries
			SET members_only = true
			WHERE entity = 'comment' AND entity_id = $1 AND NOT members_only
		`, before.ID); err != nil {
			return err
		}
		targetRef, targetTitle := changelogTarget(issue)
		return appendProjectChangelog(ctx, tx, appendProjectChangelogParams{
			ProjectID:   issue.ProjectID,
			Entity:      "comment",
			Op:          "delete",
			EntityID:    before.ID,
			IssueID:     &issue.ID,
			TargetRef:   targetRef,
			TargetTitle: targetTitle,
			Summary:     fmt.Sprintf("Deleted comment on %s", issue.Identifier),
			Details:     model.ProjectChangelogDetails{Preview: changelogPreview(before.Body)},
			MembersOnly: before.Visibility == model.CommentVisibilityMembers,
		})
	})
}
