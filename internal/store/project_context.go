package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/bradleymackey/track-slash/internal/model"
)

type CreateProjectContextParams struct {
	ProjectID      uuid.UUID
	Scope          model.ProjectContextScope
	Title          string
	Kind           model.ProjectContextKind
	ContentType    string
	Body           string
	SourceFilename *string
	CreatedByID    uuid.UUID
}

type CreateIssueContextParams struct {
	IssueID        uuid.UUID
	Title          string
	Kind           model.ProjectContextKind
	ContentType    string
	Body           string
	SourceFilename *string
	CreatedByID    uuid.UUID
}

type IssueContextLinkPair struct {
	IssueNumber   int
	ContextNumber int
}

type CreateIssueContextLinksParams struct {
	ProjectID uuid.UUID
	Links     []IssueContextLinkPair
}

type CreateIssueContextLinksResult struct {
	Requested int `json:"requested"`
	Created   int `json:"created"`
	Unchanged int `json:"unchanged"`
}

type UpdateProjectContextParams struct {
	ID          uuid.UUID
	Title       *string
	Body        *string
	ContentType *string
	Position    *int64
	UpdatedByID uuid.UUID
}

type ProjectContextsCursor struct {
	Number   int   `json:"n,omitempty"`
	Position int64 `json:"p,omitempty"`
}

type ListProjectContextsParams struct {
	ProjectID uuid.UUID
	Cursor    *ProjectContextsCursor
	Limit     int
	// IncludePrivate counts links to private issues too; see
	// ListIssuesParams.
	IncludePrivate bool
}

type ListContextsForIssueParams struct {
	IssueID uuid.UUID
	Cursor  *ProjectContextsCursor
	Limit   int
}

type ListIssuesForContextParams struct {
	ContextID uuid.UUID
	Cursor    *IssuesCursor
	Limit     int
	// IncludePrivate lists private issues too; see ListIssuesParams.
	IncludePrivate bool
}

type projectContextScanner interface {
	Scan(dest ...any) error
}

func scanProjectContext(row projectContextScanner) (model.ProjectContext, error) {
	var out model.ProjectContext
	err := row.Scan(
		&out.ID, &out.ProjectID, &out.Number, &out.Scope, &out.Position, &out.Title, &out.Kind, &out.ContentType, &out.Body,
		&out.SourceFilename, &out.CreatedByID, &out.UpdatedByID, &out.CreatedAt, &out.UpdatedAt,
	)
	if err != nil {
		return model.ProjectContext{}, err
	}
	out.Ref = model.ProjectContextRef(out.Number)
	return out, nil
}

func scanProjectContextSummary(row projectContextScanner) (model.ProjectContextSummary, error) {
	var out model.ProjectContextSummary
	err := row.Scan(
		&out.ID, &out.ProjectID, &out.Number, &out.Scope, &out.Position, &out.Title, &out.Kind, &out.ContentType,
		&out.SourceFilename, &out.CreatedByID, &out.UpdatedByID, &out.LinkedIssueCount, &out.CreatedAt, &out.UpdatedAt,
	)
	if err != nil {
		return model.ProjectContextSummary{}, err
	}
	out.Ref = model.ProjectContextRef(out.Number)
	return out, nil
}

func scanIssueContextLink(row projectContextScanner) (model.IssueContextLink, error) {
	var out model.IssueContextLink
	err := row.Scan(&out.ID, &out.ProjectID, &out.IssueID, &out.ContextID, &out.CreatedAt, &out.UpdatedAt)
	return out, err
}

func projectContextKindOrDefault(kind model.ProjectContextKind) model.ProjectContextKind {
	if kind == "" {
		return model.ProjectContextKindText
	}
	return kind
}

func projectContextContentTypeOrDefault(contentType, fallback string) string {
	if contentType == "" {
		return fallback
	}
	return contentType
}

func projectContextScopeOrDefault(scope model.ProjectContextScope) model.ProjectContextScope {
	if scope == "" {
		return model.ProjectContextScopeProject
	}
	return scope
}

func (s *Store) CreateProjectContext(ctx context.Context, p CreateProjectContextParams) (model.ProjectContext, error) {
	var out model.ProjectContext
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		var number int
		if err := tx.QueryRow(ctx, `
			SELECT next_context_number
			FROM projects
			WHERE id = $1 AND deleted_at IS NULL
			FOR UPDATE
		`, p.ProjectID).Scan(&number); err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err // defensive: DB outage past the no-rows branch
		}
		var position int64
		if err := tx.QueryRow(ctx, `
			SELECT COALESCE(MAX(position), 0) + 1
			FROM project_context
			WHERE project_id = $1 AND scope = 'project'
		`, p.ProjectID).Scan(&position); err != nil {
			return err
		}

		var err error
		out, err = scanProjectContext(tx.QueryRow(ctx, `
			INSERT INTO project_context (
				project_id, number, scope, position, title, kind, content_type, body, source_filename, created_by_id, updated_by_id
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $10)
			RETURNING id, project_id, number, scope, position, title, kind, content_type, body,
			          source_filename, created_by_id, updated_by_id, created_at, updated_at
		`, p.ProjectID, number, string(projectContextScopeOrDefault(p.Scope)), position, p.Title, string(projectContextKindOrDefault(p.Kind)), projectContextContentTypeOrDefault(p.ContentType, "text/markdown; charset=utf-8"), p.Body, p.SourceFilename, p.CreatedByID))
		if err != nil {
			if mapped := mapProjectContextWriteError(err); mapped != nil {
				return mapped
			}
			return err // defensive: non-pg or unmapped pg error
		}

		if _, err := tx.Exec(ctx, `
			UPDATE projects
			SET next_context_number = next_context_number + 1,
			    updated_at = now()
			WHERE id = $1
		`, p.ProjectID); err != nil {
			return err // defensive: project row was locked above
		}
		return appendProjectChangelog(ctx, tx, appendProjectChangelogParams{
			ProjectID:   out.ProjectID,
			Entity:      "project_context",
			Op:          "insert",
			EntityID:    out.ID,
			TargetRef:   changelogContextRef(out),
			TargetTitle: out.Title,
			Summary:     fmt.Sprintf("Created context %s", out.Title),
			Details:     model.ProjectChangelogDetails{Preview: changelogPreview(out.Body)},
		})
	})
	if err != nil {
		return model.ProjectContext{}, err
	}
	return out, nil
}

func (s *Store) CreateIssueContext(ctx context.Context, p CreateIssueContextParams) (model.ProjectContext, error) {
	var out model.ProjectContext
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		var projectID uuid.UUID
		if err := tx.QueryRow(ctx, `
			SELECT i.project_id
			FROM issues i
			JOIN projects p ON p.id = i.project_id
			WHERE i.id = $1 AND i.deleted_at IS NULL AND p.deleted_at IS NULL
		`, p.IssueID).Scan(&projectID); err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err // defensive: only no-rows has a domain mapping here
		}

		var number int
		if err := tx.QueryRow(ctx, `
			SELECT next_context_number
			FROM projects
			WHERE id = $1 AND deleted_at IS NULL
			FOR UPDATE
		`, projectID).Scan(&number); err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err // defensive: project ID came from the live issue row
		}

		// The entry naming the issue goes in before the context, so the
		// context's realtime event can tell it belongs to a private issue.
		issue, err := getIssueForChangelog(ctx, tx, p.IssueID, false)
		if err != nil {
			return err
		}
		contextID := uuid.New()
		targetRef, targetTitle := changelogTarget(issue)
		if err := appendProjectChangelog(ctx, tx, appendProjectChangelogParams{
			ProjectID:   projectID,
			Entity:      "project_context",
			Op:          "insert",
			EntityID:    contextID,
			IssueID:     &issue.ID,
			TargetRef:   targetRef,
			TargetTitle: targetTitle,
			Summary:     fmt.Sprintf("Created context for %s", issue.Identifier),
			Details:     model.ProjectChangelogDetails{Preview: changelogPreview(p.Body)},
		}); err != nil {
			return err
		}

		out, err = scanProjectContext(tx.QueryRow(ctx, `
			INSERT INTO project_context (
				id, project_id, number, scope, title, kind, content_type, body, source_filename, created_by_id, updated_by_id
			)
			VALUES ($1, $2, $3, 'issue', $4, $5, $6, $7, $8, $9, $9)
			RETURNING id, project_id, number, scope, position, title, kind, content_type, body,
			          source_filename, created_by_id, updated_by_id, created_at, updated_at
		`, contextID, projectID, number, p.Title, string(projectContextKindOrDefault(p.Kind)), projectContextContentTypeOrDefault(p.ContentType, "text/plain; charset=utf-8"), p.Body, p.SourceFilename, p.CreatedByID))
		if err != nil {
			if mapped := mapProjectContextWriteError(err); mapped != nil {
				return mapped
			}
			return err // defensive: non-pg or unmapped pg error
		}

		if _, err := tx.Exec(ctx, `
			INSERT INTO issue_context_links (project_id, issue_id, context_id)
			VALUES ($1, $2, $3)
		`, projectID, p.IssueID, out.ID); err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) {
				switch pgErr.Code {
				case "23505":
					return fmt.Errorf("context already linked: %w", ErrConflict)
				case "23503":
					return fmt.Errorf("invalid issue or context reference: %w", ErrConflict)
				}
			}
			return err // defensive: non-pg or unmapped pg error
		}

		if _, err := tx.Exec(ctx, `
			UPDATE projects
			SET next_context_number = next_context_number + 1,
			    updated_at = now()
			WHERE id = $1
		`, projectID); err != nil {
			return err // defensive: project row was locked above
		}
		return nil
	})
	if err != nil {
		return model.ProjectContext{}, err
	}
	return out, nil
}

func (s *Store) GetProjectContext(ctx context.Context, id uuid.UUID) (model.ProjectContext, error) {
	const q = `
		SELECT pc.id, pc.project_id, pc.number, pc.scope, pc.position, pc.title, pc.kind, pc.content_type, pc.body,
		       pc.source_filename, pc.created_by_id, pc.updated_by_id, pc.created_at, pc.updated_at
		FROM project_context pc
		JOIN projects p ON p.id = pc.project_id
		WHERE pc.id = $1 AND p.deleted_at IS NULL
	`
	out, err := scanProjectContext(s.db.QueryRow(ctx, q, id))
	if err != nil {
		if isNoRows(err) {
			return model.ProjectContext{}, ErrNotFound
		}
		return model.ProjectContext{}, err
	}
	return out, nil
}

func (s *Store) GetProjectContextByProjectNumber(ctx context.Context, projectID uuid.UUID, number int) (model.ProjectContext, error) {
	const q = `
		SELECT pc.id, pc.project_id, pc.number, pc.scope, pc.position, pc.title, pc.kind, pc.content_type, pc.body,
		       pc.source_filename, pc.created_by_id, pc.updated_by_id, pc.created_at, pc.updated_at
		FROM project_context pc
		JOIN projects p ON p.id = pc.project_id
		WHERE pc.project_id = $1 AND pc.number = $2 AND p.deleted_at IS NULL
	`
	out, err := scanProjectContext(s.db.QueryRow(ctx, q, projectID, number))
	if err != nil {
		if isNoRows(err) {
			return model.ProjectContext{}, ErrNotFound
		}
		return model.ProjectContext{}, err
	}
	return out, nil
}

func (s *Store) ListProjectContexts(ctx context.Context, p ListProjectContextsParams) ([]model.ProjectContextSummary, bool, error) {
	if _, err := s.GetProject(ctx, p.ProjectID); err != nil {
		return nil, false, err
	}
	args := []any{p.ProjectID}
	q := `
		SELECT pc.id, pc.project_id, pc.number, pc.scope, pc.position, pc.title, pc.kind, pc.content_type,
		       pc.source_filename, pc.created_by_id, pc.updated_by_id,
		       COUNT(i.id)::INT AS linked_issue_count,
		       pc.created_at, pc.updated_at
		FROM project_context pc
		JOIN projects p ON p.id = pc.project_id
		LEFT JOIN issue_context_links icl ON icl.context_id = pc.id
		LEFT JOIN issues i ON i.id = icl.issue_id AND i.deleted_at IS NULL AND ($2 OR NOT i.private)
		WHERE pc.project_id = $1 AND pc.scope = 'project' AND p.deleted_at IS NULL
	`
	args = append(args, p.IncludePrivate)
	if p.Cursor != nil {
		args = append(args, p.Cursor.Position)
		q += fmt.Sprintf(" AND pc.position > $%d", len(args))
	}
	args = append(args, p.Limit+1)
	q += fmt.Sprintf(`
		GROUP BY pc.id
		ORDER BY pc.position ASC
		LIMIT $%d
	`, len(args))

	rows, err := s.db.Query(ctx, q, args...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()

	out := make([]model.ProjectContextSummary, 0, p.Limit)
	for rows.Next() {
		item, err := scanProjectContextSummary(rows)
		if err != nil {
			return nil, false, err
		}
		out = append(out, item)
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

// issueScopedContextIssueID is the issue an issue-scoped context belongs to,
// so its changelog entries follow that issue's visibility. Project context
// belongs to no issue. The context's link names the issue while it lasts;
// deleting the context removes the link first, so the entry recording its
// creation, which always names the issue, is the fallback.
func issueScopedContextIssueID(ctx context.Context, tx pgx.Tx, contextItem model.ProjectContext) (*uuid.UUID, error) {
	if contextItem.Scope != model.ProjectContextScopeIssue {
		return nil, nil
	}
	var issueID uuid.UUID
	err := tx.QueryRow(ctx, `
		SELECT issue_id FROM (
			SELECT issue_id, 0 AS rank, created_at FROM issue_context_links WHERE context_id = $1
			UNION ALL
			SELECT issue_id, 1, created_at FROM project_changelog_entries
			WHERE entity = 'project_context' AND entity_id = $1 AND issue_id IS NOT NULL
		) owners
		ORDER BY rank, created_at
		LIMIT 1
	`, contextItem.ID).Scan(&issueID)
	if err != nil {
		if isNoRows(err) {
			return nil, nil
		}
		return nil, err // defensive: only a DB outage fails this lookup
	}
	return &issueID, nil
}

func (s *Store) UpdateProjectContext(ctx context.Context, p UpdateProjectContextParams) (model.ProjectContext, error) {
	var out model.ProjectContext
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		before, err := scanProjectContext(tx.QueryRow(ctx, `
			SELECT pc.id, pc.project_id, pc.number, pc.scope, pc.position, pc.title, pc.kind, pc.content_type, pc.body,
			       pc.source_filename, pc.created_by_id, pc.updated_by_id, pc.created_at, pc.updated_at
			FROM project_context pc
			JOIN projects pr ON pr.id = pc.project_id
			WHERE pc.id = $1 AND pr.deleted_at IS NULL
			FOR UPDATE OF pc
		`, p.ID))
		if err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err
		}
		contextIssueID, err := issueScopedContextIssueID(ctx, tx, before)
		if err != nil {
			return err
		}
		if p.Position != nil {
			if before.Scope != model.ProjectContextScopeProject || before.Position == nil {
				return fmt.Errorf("only project context can be positioned: %w", ErrConflict)
			}
			if _, err := tx.Exec(ctx, `SELECT id FROM projects WHERE id = $1 FOR UPDATE`, before.ProjectID); err != nil {
				return err
			}
			var count int64
			if err := tx.QueryRow(ctx, `
				SELECT COUNT(*) FROM project_context WHERE project_id = $1 AND scope = 'project'
			`, before.ProjectID).Scan(&count); err != nil {
				return err
			}
			target := *p.Position
			current := *before.Position
			if target < 1 || target > count {
				return fmt.Errorf("position must be between 1 and %d: %w", count, ErrConflict)
			}
			if target != current {
				offset := count + 1
				if _, err := tx.Exec(ctx, `UPDATE project_context SET position = $2 WHERE id = $1`, before.ID, count+offset+1); err != nil {
					return err
				}
				if target < current {
					if _, err := tx.Exec(ctx, `
						UPDATE project_context SET position = position + $4
						WHERE project_id = $1 AND scope = 'project' AND position >= $2 AND position < $3
					`, before.ProjectID, target, current, offset); err != nil {
						return err
					}
					if _, err := tx.Exec(ctx, `
						UPDATE project_context SET position = position - $4 + 1
						WHERE project_id = $1 AND scope = 'project' AND position >= $2::bigint + $4::bigint AND position < $3::bigint + $4::bigint
					`, before.ProjectID, target, current, offset); err != nil {
						return err
					}
				} else {
					if _, err := tx.Exec(ctx, `
						UPDATE project_context SET position = position + $4
						WHERE project_id = $1 AND scope = 'project' AND position > $2 AND position <= $3
					`, before.ProjectID, current, target, offset); err != nil {
						return err
					}
					if _, err := tx.Exec(ctx, `
						UPDATE project_context SET position = position - $4 - 1
						WHERE project_id = $1 AND scope = 'project' AND position > $2::bigint + $4::bigint AND position <= $3::bigint + $4::bigint
					`, before.ProjectID, current, target, offset); err != nil {
						return err
					}
				}
				if _, err := tx.Exec(ctx, `UPDATE project_context SET position = $2 WHERE id = $1`, before.ID, target); err != nil {
					return err
				}
			}
		}
		out, err = scanProjectContext(tx.QueryRow(ctx, `
			UPDATE project_context pc
			SET title = COALESCE($2, title),
			    body = COALESCE($3, body),
			    content_type = COALESCE($4, content_type),
			    updated_by_id = $5,
			    updated_at = GREATEST(clock_timestamp(), pc.updated_at + interval '1 microsecond')
			FROM projects pr
			WHERE pc.id = $1 AND pr.id = pc.project_id AND pr.deleted_at IS NULL
			RETURNING pc.id, pc.project_id, pc.number, pc.scope, pc.position, pc.title, pc.kind, pc.content_type, pc.body,
			          pc.source_filename, pc.created_by_id, pc.updated_by_id, pc.created_at, pc.updated_at
		`, p.ID, p.Title, p.Body, p.ContentType, p.UpdatedByID))
		if err != nil {
			return err
		}
		changes := []model.ProjectChangelogChange{}
		changes = changelogAppendChange(changes, "title", "Title", before.Title, out.Title)
		changes = changelogAppendChange(changes, "body", "Body", changelogPreview(before.Body), changelogPreview(out.Body))
		changes = changelogAppendChange(changes, "content_type", "Content type", before.ContentType, out.ContentType)
		beforePosition, outPosition := "", ""
		if before.Position != nil {
			beforePosition = fmt.Sprint(*before.Position)
		}
		if out.Position != nil {
			outPosition = fmt.Sprint(*out.Position)
		}
		changes = changelogAppendChange(changes, "position", "Position", beforePosition, outPosition)
		if len(changes) == 0 {
			return nil
		}
		return appendProjectChangelog(ctx, tx, appendProjectChangelogParams{
			ProjectID:   out.ProjectID,
			Entity:      "project_context",
			Op:          "update",
			EntityID:    out.ID,
			IssueID:     contextIssueID,
			TargetRef:   changelogContextRef(out),
			TargetTitle: out.Title,
			Summary:     fmt.Sprintf("Updated context %s", out.Title),
			Details:     model.ProjectChangelogDetails{Changes: changes},
		})
	})
	if err != nil {
		if isNoRows(err) {
			return model.ProjectContext{}, ErrNotFound
		}
		if mapped := mapProjectContextWriteError(err); mapped != nil {
			return model.ProjectContext{}, mapped
		}
		return model.ProjectContext{}, err
	}
	return out, nil
}

func (s *Store) DeleteProjectContext(ctx context.Context, id uuid.UUID) error {
	return pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		before, err := scanProjectContext(tx.QueryRow(ctx, `
			SELECT pc.id, pc.project_id, pc.number, pc.scope, pc.position, pc.title, pc.kind, pc.content_type, pc.body,
			       pc.source_filename, pc.created_by_id, pc.updated_by_id, pc.created_at, pc.updated_at
			FROM project_context pc
			JOIN projects pr ON pr.id = pc.project_id
			WHERE pc.id = $1 AND pr.deleted_at IS NULL
			FOR UPDATE OF pc
		`, id))
		if err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err
		}
		contextIssueID, err := issueScopedContextIssueID(ctx, tx, before)
		if err != nil {
			return err
		}
		var count int64
		if before.Position != nil {
			if _, err := tx.Exec(ctx, `SELECT id FROM projects WHERE id = $1 FOR UPDATE`, before.ProjectID); err != nil {
				return err
			}
			if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM project_context WHERE project_id = $1 AND scope = 'project'`, before.ProjectID).Scan(&count); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `
			UPDATE storage_objects so
			SET deleted_at = now(),
			    updated_at = GREATEST(clock_timestamp(), so.updated_at + interval '1 microsecond')
			WHERE so.deleted_at IS NULL
			  AND so.id IN (
				SELECT ca.storage_object_id FROM context_attachments ca WHERE ca.context_id = $1
			  )
		`, id); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `DELETE FROM project_context WHERE id = $1`, id)
		if err != nil {
			return err // defensive: delete has no expected FK/check mapping
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		if before.Position != nil && *before.Position < count {
			offset := count + 1
			if _, err := tx.Exec(ctx, `
				UPDATE project_context SET position = position + $3
				WHERE project_id = $1 AND scope = 'project' AND position > $2
			`, before.ProjectID, *before.Position, offset); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `
				UPDATE project_context SET position = position - $3 - 1
				WHERE project_id = $1 AND scope = 'project' AND position > $2::bigint + $3::bigint
			`, before.ProjectID, *before.Position, offset); err != nil {
				return err
			}
		}
		return appendProjectChangelog(ctx, tx, appendProjectChangelogParams{
			ProjectID:   before.ProjectID,
			Entity:      "project_context",
			Op:          "delete",
			EntityID:    before.ID,
			IssueID:     contextIssueID,
			TargetRef:   changelogContextRef(before),
			TargetTitle: before.Title,
			Summary:     fmt.Sprintf("Deleted context %s", before.Title),
		})
	})
}

func (s *Store) CreateIssueContextLink(ctx context.Context, issueID, contextID uuid.UUID) (model.IssueContextLink, error) {
	var out model.IssueContextLink
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		var issueProject uuid.UUID
		if err := tx.QueryRow(ctx, `
			SELECT i.project_id
			FROM issues i
			JOIN projects p ON p.id = i.project_id
			WHERE i.id = $1 AND i.deleted_at IS NULL AND p.deleted_at IS NULL
		`, issueID).Scan(&issueProject); err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err // defensive: only no-rows has a domain mapping here
		}

		var contextProject uuid.UUID
		var contextScope model.ProjectContextScope
		if err := tx.QueryRow(ctx, `
			SELECT pc.project_id, pc.scope
			FROM project_context pc
			JOIN projects p ON p.id = pc.project_id
			WHERE pc.id = $1 AND p.deleted_at IS NULL
		`, contextID).Scan(&contextProject, &contextScope); err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err // defensive: only no-rows has a domain mapping here
		}
		if issueProject != contextProject {
			return fmt.Errorf("issue and context belong to different projects: %w", ErrConflict)
		}
		if contextScope != model.ProjectContextScopeProject {
			return fmt.Errorf("issue-scoped context cannot be linked to another issue: %w", ErrConflict)
		}

		var err error
		out, err = scanIssueContextLink(tx.QueryRow(ctx, `
			INSERT INTO issue_context_links (project_id, issue_id, context_id)
			VALUES ($1, $2, $3)
			RETURNING id, project_id, issue_id, context_id, created_at, updated_at
		`, issueProject, issueID, contextID))
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) {
				switch pgErr.Code {
				case "23505":
					return fmt.Errorf("context already linked: %w", ErrConflict)
				case "23503":
					return fmt.Errorf("invalid issue or context reference: %w", ErrConflict)
				}
			}
			return err // defensive: non-pg or unmapped pg error
		}
		issue, err := getIssueForChangelog(ctx, tx, issueID, false)
		if err != nil {
			return err
		}
		contextItem, err := scanProjectContext(tx.QueryRow(ctx, `
			SELECT pc.id, pc.project_id, pc.number, pc.scope, pc.position, pc.title, pc.kind, pc.content_type, pc.body,
			       pc.source_filename, pc.created_by_id, pc.updated_by_id, pc.created_at, pc.updated_at
			FROM project_context pc
			WHERE pc.id = $1
		`, contextID))
		if err != nil {
			return err
		}
		targetRef, targetTitle := changelogTarget(issue)
		return appendProjectChangelog(ctx, tx, appendProjectChangelogParams{
			ProjectID:   out.ProjectID,
			Entity:      "issue_context_link",
			Op:          "insert",
			EntityID:    out.ID,
			IssueID:     &issue.ID,
			TargetRef:   targetRef,
			TargetTitle: targetTitle,
			Summary:     fmt.Sprintf("Attached context %s to %s", contextItem.Title, issue.Identifier),
		})
	})
	if err != nil {
		return model.IssueContextLink{}, err
	}
	return out, nil
}

func (s *Store) CreateIssueContextLinks(ctx context.Context, p CreateIssueContextLinksParams) (CreateIssueContextLinksResult, error) {
	result := CreateIssueContextLinksResult{Requested: len(p.Links)}
	if len(p.Links) == 0 {
		return result, nil
	}

	issueNumbers := make([]int, len(p.Links))
	contextNumbers := make([]int, len(p.Links))
	for i, link := range p.Links {
		issueNumbers[i] = link.IssueNumber
		contextNumbers[i] = link.ContextNumber
	}

	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		var resolved int
		if err := tx.QueryRow(ctx, `
			SELECT COUNT(*)
			FROM (
				SELECT 1
				FROM unnest($2::int[], $3::int[])
					AS requested(issue_number, context_number)
				JOIN projects p ON p.id = $1 AND p.deleted_at IS NULL
				JOIN issues i ON i.project_id = p.id
					AND i.number = requested.issue_number
					AND i.deleted_at IS NULL
				JOIN project_context pc ON pc.project_id = p.id
					AND pc.number = requested.context_number
					AND pc.scope = 'project'
				FOR SHARE OF p, i, pc
			) AS locked_links
		`, p.ProjectID, issueNumbers, contextNumbers).Scan(&resolved); err != nil {
			return err // defensive: validation query has no expected domain error
		}
		if resolved != len(p.Links) {
			return ErrNotFound
		}

		return tx.QueryRow(ctx, `
			WITH requested AS (
				SELECT issue_number, context_number
				FROM unnest($2::int[], $3::int[])
					AS input(issue_number, context_number)
			), resolved AS (
				SELECT DISTINCT i.id AS issue_id, pc.id AS context_id
				FROM requested
				JOIN issues i ON i.project_id = $1
					AND i.number = requested.issue_number
					AND i.deleted_at IS NULL
				JOIN project_context pc ON pc.project_id = $1
					AND pc.number = requested.context_number
					AND pc.scope = 'project'
			), inserted AS (
				INSERT INTO issue_context_links (project_id, issue_id, context_id)
				SELECT $1, resolved.issue_id, resolved.context_id
				FROM resolved
				ON CONFLICT (issue_id, context_id) DO NOTHING
				RETURNING id, project_id, issue_id, context_id
			), logged AS (
				INSERT INTO project_changelog_entries (
					project_id, actor_id, entity, op, entity_id, issue_id,
					target_ref, target_title, summary, details
				)
				SELECT inserted.project_id, $4, 'issue_context_link', 'insert', inserted.id, inserted.issue_id,
					p.key || '-' || i.number, i.title,
					'Attached context ' || pc.title || ' to ' || p.key || '-' || i.number,
					'{}'::jsonb
				FROM inserted
				JOIN projects p ON p.id = inserted.project_id
				JOIN issues i ON i.id = inserted.issue_id
				JOIN project_context pc ON pc.id = inserted.context_id
				RETURNING entity_id
			)
			SELECT COUNT(*) FROM logged
		`, p.ProjectID, issueNumbers, contextNumbers, actorFromContext(ctx)).Scan(&result.Created)
	})
	if err != nil {
		return CreateIssueContextLinksResult{}, err
	}
	result.Unchanged = result.Requested - result.Created
	return result, nil
}

func (s *Store) DeleteIssueContextLink(ctx context.Context, issueID, contextID uuid.UUID) error {
	return pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		link, err := scanIssueContextLink(tx.QueryRow(ctx, `
			SELECT id, project_id, issue_id, context_id, created_at, updated_at
			FROM issue_context_links
			WHERE issue_id = $1 AND context_id = $2
			FOR UPDATE
		`, issueID, contextID))
		if err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err
		}
		issue, err := getIssueForChangelog(ctx, tx, issueID, false)
		if err != nil {
			return err
		}
		contextItem, err := scanProjectContext(tx.QueryRow(ctx, `
			SELECT pc.id, pc.project_id, pc.number, pc.scope, pc.position, pc.title, pc.kind, pc.content_type, pc.body,
			       pc.source_filename, pc.created_by_id, pc.updated_by_id, pc.created_at, pc.updated_at
			FROM project_context pc
			WHERE pc.id = $1
		`, contextID))
		if err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `
			DELETE FROM issue_context_links
			WHERE issue_id = $1 AND context_id = $2
		`, issueID, contextID)
		if err != nil {
			return err // defensive: delete has no expected FK/check mapping
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		targetRef, targetTitle := changelogTarget(issue)
		return appendProjectChangelog(ctx, tx, appendProjectChangelogParams{
			ProjectID:   link.ProjectID,
			Entity:      "issue_context_link",
			Op:          "delete",
			EntityID:    link.ID,
			IssueID:     &issue.ID,
			TargetRef:   targetRef,
			TargetTitle: targetTitle,
			Summary:     fmt.Sprintf("Removed context %s from %s", contextItem.Title, issue.Identifier),
		})
	})
}

func (s *Store) ListContextsForIssue(ctx context.Context, p ListContextsForIssueParams) ([]model.ProjectContext, bool, error) {
	if _, err := s.ProjectIDForIssue(ctx, p.IssueID); err != nil {
		return nil, false, err
	}
	args := []any{p.IssueID}
	q := `
		SELECT pc.id, pc.project_id, pc.number, pc.scope, pc.position, pc.title, pc.kind, pc.content_type, pc.body,
		       pc.source_filename, pc.created_by_id, pc.updated_by_id, pc.created_at, pc.updated_at
		FROM issue_context_links icl
		JOIN project_context pc ON pc.id = icl.context_id
		JOIN projects p ON p.id = pc.project_id
		WHERE icl.issue_id = $1 AND p.deleted_at IS NULL
	`
	if p.Cursor != nil {
		args = append(args, p.Cursor.Number)
		q += fmt.Sprintf(" AND pc.number > $%d", len(args))
	}
	args = append(args, p.Limit+1)
	q += fmt.Sprintf(" ORDER BY pc.number ASC LIMIT $%d", len(args))

	rows, err := s.db.Query(ctx, q, args...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()

	out := make([]model.ProjectContext, 0, p.Limit)
	for rows.Next() {
		item, err := scanProjectContext(rows)
		if err != nil {
			return nil, false, err
		}
		out = append(out, item)
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

func (s *Store) ListIssuesForContext(ctx context.Context, p ListIssuesForContextParams) ([]model.Issue, bool, error) {
	if _, err := s.ProjectIDForProjectContext(ctx, p.ContextID); err != nil {
		return nil, false, err
	}
	args := []any{p.ContextID}
	q := `
		SELECT i.id, i.project_id, u.username, pr.key, i.number, i.title, i.description, i.status, i.close_reason, i.priority, i.worker, i.private,
		       i.assignee_id, i.reporter_id, i.sprint_id, i.parent_issue_id, i.due_date, i.created_at, i.updated_at
		FROM issue_context_links icl
		JOIN issues i ON i.id = icl.issue_id
		JOIN projects pr ON pr.id = i.project_id
		JOIN users u ON u.id = pr.owner_id
		WHERE icl.context_id = $1 AND i.deleted_at IS NULL AND pr.deleted_at IS NULL AND u.deleted_at IS NULL
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
	return out, hasMore, nil
}

func mapProjectContextWriteError(err error) error {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return nil
	}
	switch pgErr.Code {
	case "23503":
		return fmt.Errorf("project or user not found: %w", ErrNotFound)
	case "23514":
		return fmt.Errorf("title/body outside allowed length: %w", ErrConflict)
	case "22P02":
		return fmt.Errorf("invalid context kind or scope: %w", ErrConflict)
	}
	return nil
}
