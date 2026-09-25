package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/bradleymackey/track-slash/internal/model"
)

type projectScanner interface {
	Scan(dest ...any) error
}

func scanProject(row projectScanner) (model.Project, error) {
	var p model.Project
	err := row.Scan(
		&p.ID, &p.OwnerID, &p.OwnerUsername, &p.Key, &p.Name, &p.Description,
		&p.ImageObjectID, &p.ImageThumbnailObjectID, &p.SprintsEnabled, &p.CreatedAt, &p.UpdatedAt,
	)
	return p, err
}

func (s *Store) CreateProject(ctx context.Context, key, name, description string) (model.Project, error) {
	ownerID, err := s.firstProjectOwner(ctx)
	if err != nil {
		return model.Project{}, err
	}
	return s.CreateProjectForUser(ctx, ownerID, key, name, description)
}

func (s *Store) CreateProjectForUser(ctx context.Context, userID uuid.UUID, key, name, description string) (model.Project, error) {
	var p model.Project
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		const projectQ = `
			WITH owner AS (
				SELECT id, username FROM users WHERE id = $1 AND deleted_at IS NULL
			),
			inserted AS (
				INSERT INTO projects (owner_id, key, name, description)
				SELECT id, $2, $3, $4 FROM owner
				RETURNING id, owner_id, key, name, description, image_object_id, image_thumbnail_object_id, sprints_enabled, created_at, updated_at
			)
			SELECT inserted.id, inserted.owner_id, owner.username, inserted.key,
			       inserted.name, inserted.description, inserted.image_object_id, inserted.image_thumbnail_object_id,
			       inserted.sprints_enabled, inserted.created_at, inserted.updated_at
			FROM inserted
			JOIN owner ON owner.id = inserted.owner_id
		`
		var err error
		p, err = scanProject(tx.QueryRow(ctx, projectQ, userID, key, name, description))
		if err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				return ErrConflict
			}
			// Defensive: insert has no other expected pgcode mapping here.
			return err
		}

		tag, err := tx.Exec(ctx, `
			INSERT INTO project_members (project_id, user_id)
			SELECT $1, id FROM users WHERE id = $2 AND deleted_at IS NULL
			ON CONFLICT (project_id, user_id) DO NOTHING
		`, p.ID, userID)
		if err != nil {
			// Defensive: project row exists and user is selected from users; failure is a DB/runtime fault.
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		if err := appendProjectChangelog(ctx, tx, appendProjectChangelogParams{
			ProjectID:   p.ID,
			Entity:      "project",
			Op:          "insert",
			EntityID:    p.ID,
			TargetRef:   p.Key,
			TargetTitle: p.Name,
			Summary:     fmt.Sprintf("Created project %s", p.Key),
			Details:     model.ProjectChangelogDetails{Preview: changelogPreview(p.Description)},
		}); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return model.Project{}, err
	}
	return p, nil
}

func (s *Store) GetProject(ctx context.Context, id uuid.UUID) (model.Project, error) {
	const q = `
		SELECT p.id, p.owner_id, u.username, p.key, p.name, p.description,
		       p.image_object_id, p.image_thumbnail_object_id, p.sprints_enabled, p.created_at, p.updated_at
		FROM projects p
		JOIN users u ON u.id = p.owner_id
		WHERE p.id = $1 AND p.deleted_at IS NULL AND u.deleted_at IS NULL
	`
	p, err := scanProject(s.db.QueryRow(ctx, q, id))
	if err != nil {
		if isNoRows(err) {
			return model.Project{}, ErrNotFound
		}
		return model.Project{}, err
	}
	return p, nil
}

func (s *Store) GetProjectByOwnerKey(ctx context.Context, ownerUsername, key string) (model.Project, error) {
	ownerUsername = strings.ToLower(strings.TrimSpace(ownerUsername))
	key = strings.ToUpper(strings.TrimSpace(key))
	const q = `
		SELECT p.id, p.owner_id, u.username, p.key, p.name, p.description,
		       p.image_object_id, p.image_thumbnail_object_id, p.sprints_enabled, p.created_at, p.updated_at
		FROM projects p
		JOIN users u ON u.id = p.owner_id
		WHERE u.username = $1
		  AND p.key = $2
		  AND p.deleted_at IS NULL
		  AND u.deleted_at IS NULL
	`
	p, err := scanProject(s.db.QueryRow(ctx, q, ownerUsername, key))
	if err != nil {
		if isNoRows(err) {
			return model.Project{}, ErrNotFound
		}
		return model.Project{}, err
	}
	return p, nil
}

type UpdateProjectParams struct {
	Name        *string
	Description *string
}

func (s *Store) UpdateProject(ctx context.Context, id uuid.UUID, p UpdateProjectParams) (model.Project, error) {
	if p.Name != nil {
		name := strings.TrimSpace(*p.Name)
		if name == "" || len(name) > 200 {
			return model.Project{}, fmt.Errorf("name must be 1..200 chars: %w", ErrConflict)
		}
		p.Name = &name
	}
	if p.Description != nil {
		description := *p.Description
		if strings.TrimSpace(description) == "" {
			description = ""
		}
		p.Description = &description
	}

	var out model.Project
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		before, err := scanProject(tx.QueryRow(ctx, `
			SELECT p.id, p.owner_id, u.username, p.key, p.name, p.description,
			       p.image_object_id, p.image_thumbnail_object_id, p.sprints_enabled, p.created_at, p.updated_at
			FROM projects p
			JOIN users u ON u.id = p.owner_id
			WHERE p.id = $1 AND p.deleted_at IS NULL AND u.deleted_at IS NULL
			FOR UPDATE OF p
		`, id))
		if err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err
		}

		sets := []string{}
		args := []any{}
		i := 1
		if p.Name != nil {
			sets = append(sets, fmt.Sprintf("name = $%d", i))
			args = append(args, *p.Name)
			i++
		}
		if p.Description != nil {
			sets = append(sets, fmt.Sprintf("description = $%d", i))
			args = append(args, *p.Description)
			i++
		}
		if len(sets) == 0 {
			out = before
			return nil
		}

		sets = append(sets, "updated_at = now()")
		args = append(args, id)
		q := fmt.Sprintf(`
			UPDATE projects p
			SET %s
			FROM users u
			WHERE p.id = $%d
			  AND p.owner_id = u.id
			  AND p.deleted_at IS NULL
			  AND u.deleted_at IS NULL
			RETURNING p.id, p.owner_id, u.username, p.key, p.name, p.description,
			          p.image_object_id, p.image_thumbnail_object_id, p.sprints_enabled, p.created_at, p.updated_at
		`, strings.Join(sets, ", "), i)

		out, err = scanProject(tx.QueryRow(ctx, q, args...))
		if err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			// Defensive: update has no expected pgcode mapping.
			return err
		}
		changes := []model.ProjectChangelogChange{}
		changes = changelogAppendChange(changes, "name", "Name", before.Name, out.Name)
		changes = changelogAppendChange(changes, "description", "Description", changelogPreview(before.Description), changelogPreview(out.Description))
		if len(changes) == 0 {
			return nil
		}
		return appendProjectChangelog(ctx, tx, appendProjectChangelogParams{
			ProjectID:   out.ID,
			Entity:      "project",
			Op:          "update",
			EntityID:    out.ID,
			TargetRef:   out.Key,
			TargetTitle: out.Name,
			Summary:     fmt.Sprintf("Updated project %s", out.Key),
			Details:     model.ProjectChangelogDetails{Changes: changes},
		})
	})
	if err != nil {
		return model.Project{}, err
	}
	return out, nil
}

var (
	// ErrSprintsDisabled rejects starting a sprint in a project that is not in
	// sprint mode. It wraps ErrConflict so every surface answers 409.
	ErrSprintsDisabled = fmt.Errorf("sprints are disabled for this project: %w", ErrConflict)
	// ErrActiveSprintBlocksSprintsOff rejects leaving sprint mode while a sprint
	// is running, so turning sprints off never strands an active sprint.
	ErrActiveSprintBlocksSprintsOff = fmt.Errorf("complete the active sprint to disable sprints: %w", ErrConflict)
)

// SetProjectSprintsEnabled switches a project between sprint mode and working
// one issue at a time. It locks the project row, which UpdateSprint also locks
// before starting a sprint, so turning sprints off and starting a sprint cannot
// both succeed. Planned and completed sprints are never touched.
func (s *Store) SetProjectSprintsEnabled(ctx context.Context, id uuid.UUID, enabled bool) (model.Project, error) {
	var out model.Project
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		before, err := scanProject(tx.QueryRow(ctx, `
			SELECT p.id, p.owner_id, u.username, p.key, p.name, p.description,
			       p.image_object_id, p.image_thumbnail_object_id, p.sprints_enabled, p.created_at, p.updated_at
			FROM projects p
			JOIN users u ON u.id = p.owner_id
			WHERE p.id = $1 AND p.deleted_at IS NULL AND u.deleted_at IS NULL
			FOR UPDATE OF p
		`, id))
		if err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			// Defensive: a keyed row lock only fails here on a DB fault.
			return err
		}
		if before.SprintsEnabled == enabled {
			out = before
			return nil
		}
		if !enabled {
			var active bool
			if err := tx.QueryRow(ctx, `
				SELECT EXISTS (
					SELECT 1 FROM sprints
					WHERE project_id = $1 AND status = 'active' AND deleted_at IS NULL
				)
			`, id).Scan(&active); err != nil {
				// Defensive: an EXISTS probe on a locked project only fails on a DB fault.
				return err
			}
			if active {
				return ErrActiveSprintBlocksSprintsOff
			}
		}
		out, err = scanProject(tx.QueryRow(ctx, `
			UPDATE projects p
			SET sprints_enabled = $2, updated_at = now()
			FROM users u
			WHERE p.id = $1 AND p.owner_id = u.id
			RETURNING p.id, p.owner_id, u.username, p.key, p.name, p.description,
			          p.image_object_id, p.image_thumbnail_object_id, p.sprints_enabled, p.created_at, p.updated_at
		`, id, enabled))
		if err != nil {
			// Defensive: the row is locked above, so only a DB fault fails the update.
			return err
		}
		summary := fmt.Sprintf("Disabled sprints for project %s", out.Key)
		if enabled {
			summary = fmt.Sprintf("Enabled sprints for project %s", out.Key)
		}
		return appendProjectChangelog(ctx, tx, appendProjectChangelogParams{
			ProjectID:   out.ID,
			Entity:      "project",
			Op:          "update",
			EntityID:    out.ID,
			TargetRef:   out.Key,
			TargetTitle: out.Name,
			Summary:     summary,
			Details: model.ProjectChangelogDetails{Changes: []model.ProjectChangelogChange{{
				Field: "sprints_enabled",
				Label: "Sprints",
				From:  changelogEnabledLabel(before.SprintsEnabled),
				To:    changelogEnabledLabel(out.SprintsEnabled),
			}}},
		})
	})
	if err != nil {
		return model.Project{}, err
	}
	return out, nil
}

func changelogEnabledLabel(enabled bool) string {
	if enabled {
		return "Enabled"
	}
	return "Disabled"
}

type ProjectsCursor struct {
	CreatedAt time.Time `json:"t"`
	ID        uuid.UUID `json:"i"`
}

type ListProjectsParams struct {
	Cursor               *ProjectsCursor
	Limit                int
	VisibleToUser        *uuid.UUID
	WritableToUser       *uuid.UUID
	IssueCreatableToUser *uuid.UUID
}

func (s *Store) ListProjects(ctx context.Context, p ListProjectsParams) ([]model.Project, bool, error) {
	args := []any{}
	q := `
		SELECT projects.id, projects.owner_id, u.username, projects.key,
		       projects.name, projects.description, projects.image_object_id, projects.image_thumbnail_object_id,
		       projects.sprints_enabled, projects.created_at, projects.updated_at
		FROM projects
		JOIN users u ON u.id = projects.owner_id
		WHERE projects.deleted_at IS NULL AND u.deleted_at IS NULL
	`
	if p.VisibleToUser != nil {
		args = append(args, *p.VisibleToUser)
		q += fmt.Sprintf(` AND (
			projects.is_public
			OR projects.owner_id = $%d
			OR EXISTS (
				SELECT 1 FROM project_members pm
				WHERE pm.project_id = projects.id AND pm.user_id = $%d
			)
		) AND NOT EXISTS (
			SELECT 1 FROM project_user_blocks b
			WHERE b.project_id = projects.id AND b.user_id = $%d
		)`, len(args), len(args), len(args))
	}
	if p.WritableToUser != nil {
		args = append(args, *p.WritableToUser)
		q += fmt.Sprintf(` AND (
			projects.owner_id = $%d
			OR EXISTS (
				SELECT 1 FROM project_members pm
				WHERE pm.project_id = projects.id
				  AND pm.user_id = $%d
				  AND pm.role = 'member'
			)
		) AND NOT EXISTS (
			SELECT 1 FROM project_user_blocks b
			WHERE b.project_id = projects.id AND b.user_id = $%d
		)`, len(args), len(args), len(args))
	}
	if p.IssueCreatableToUser != nil {
		args = append(args, *p.IssueCreatableToUser)
		q += fmt.Sprintf(` AND (
			projects.owner_id = $%d
			OR EXISTS (
				SELECT 1 FROM project_members pm
				WHERE pm.project_id = projects.id
				  AND pm.user_id = $%d
				  AND pm.role = 'member'
			)
			OR (
				projects.is_public
				AND projects.public_issue_creation
				AND NOT EXISTS (
					SELECT 1 FROM project_members pm
					WHERE pm.project_id = projects.id AND pm.user_id = $%d
				)
			)
		) AND NOT EXISTS (
			SELECT 1 FROM project_user_blocks b
			WHERE b.project_id = projects.id AND b.user_id = $%d
		)`, len(args), len(args), len(args), len(args))
	}
	if p.Cursor != nil {
		args = append(args, p.Cursor.CreatedAt, p.Cursor.ID)
		q += fmt.Sprintf(` AND (projects.created_at, projects.id) > ($%d, $%d)`, len(args)-1, len(args))
	}
	args = append(args, p.Limit+1)
	q += fmt.Sprintf(` ORDER BY projects.created_at ASC, projects.id ASC LIMIT $%d`, len(args))

	rows, err := s.db.Query(ctx, q, args...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()

	out := make([]model.Project, 0, p.Limit)
	for rows.Next() {
		pr, err := scanProject(rows)
		if err != nil {
			return nil, false, err
		}
		out = append(out, pr)
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

func (s *Store) firstProjectOwner(ctx context.Context) (uuid.UUID, error) {
	var ownerID uuid.UUID
	err := s.db.QueryRow(ctx, `
		SELECT id
		FROM users
		WHERE deleted_at IS NULL
		ORDER BY is_admin DESC, created_at ASC, id ASC
		LIMIT 1
	`).Scan(&ownerID)
	if err != nil {
		if isNoRows(err) {
			return uuid.Nil, ErrNotFound
		}
		return uuid.Nil, err
	}
	return ownerID, nil
}

func (s *Store) DeleteProject(ctx context.Context, id uuid.UUID) error {
	return pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		project, err := scanProject(tx.QueryRow(ctx, `
			SELECT p.id, p.owner_id, u.username, p.key, p.name, p.description,
			       p.image_object_id, p.image_thumbnail_object_id, p.sprints_enabled, p.created_at, p.updated_at
			FROM projects p
			JOIN users u ON u.id = p.owner_id
			WHERE p.id = $1 AND p.deleted_at IS NULL AND u.deleted_at IS NULL
			FOR UPDATE OF p
		`, id))
		if err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err
		}
		tag, err := tx.Exec(ctx, `
			UPDATE projects SET deleted_at = now(), updated_at = now()
			WHERE id = $1 AND deleted_at IS NULL
		`, id)
		if err != nil {
			// Defensive: soft-delete has no expected FK/check mapping.
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		if _, err := tx.Exec(ctx, `
			UPDATE issues SET deleted_at = now(), updated_at = now()
			WHERE project_id = $1 AND deleted_at IS NULL
		`, id); err != nil {
			// Defensive: cascading soft-delete has no expected FK/check mapping.
			return err
		}
		_, err = tx.Exec(ctx, `
			UPDATE sprints SET deleted_at = now(), updated_at = now()
			WHERE project_id = $1 AND deleted_at IS NULL
		`, id)
		if err != nil {
			// Defensive: cascading soft-delete has no expected FK/check mapping.
		}
		if err != nil {
			return err
		}
		return appendProjectChangelog(ctx, tx, appendProjectChangelogParams{
			ProjectID:   project.ID,
			Entity:      "project",
			Op:          "delete",
			EntityID:    project.ID,
			TargetRef:   project.Key,
			TargetTitle: project.Name,
			Summary:     fmt.Sprintf("Deleted project %s", project.Key),
		})
	})
}
