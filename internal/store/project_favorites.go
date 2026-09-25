package store

import (
	"context"

	"github.com/google/uuid"

	"github.com/bradleymackey/track-slash/internal/model"
)

type ListFavoriteProjectsParams struct {
	User  model.User
	Limit int
}

func (s *Store) FavoriteProject(ctx context.Context, userID, projectID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO project_favorites (user_id, project_id)
		SELECT u.id, p.id
		FROM users u
		JOIN projects p ON p.id = $2
		WHERE u.id = $1 AND u.deleted_at IS NULL AND p.deleted_at IS NULL
		ON CONFLICT (user_id, project_id) DO NOTHING
	`, userID, projectID)
	if err != nil {
		return err
	}
	return s.ensureFavoriteProjectEligible(ctx, userID, projectID)
}

func (s *Store) UnfavoriteProject(ctx context.Context, userID, projectID uuid.UUID) error {
	if _, err := s.db.Exec(ctx, `
		DELETE FROM project_favorites
		WHERE user_id = $1 AND project_id = $2
	`, userID, projectID); err != nil {
		return err
	}
	return s.ensureFavoriteProjectEligible(ctx, userID, projectID)
}

func (s *Store) IsProjectFavorite(ctx context.Context, userID, projectID uuid.UUID) (bool, error) {
	var ok bool
	err := s.db.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM project_favorites pf
			JOIN projects p ON p.id = pf.project_id
			WHERE pf.user_id = $1 AND pf.project_id = $2 AND p.deleted_at IS NULL
		)
	`, userID, projectID).Scan(&ok)
	return ok, err
}

func (s *Store) FavoriteProjectIDs(ctx context.Context, userID uuid.UUID, projectIDs []uuid.UUID) (map[uuid.UUID]bool, error) {
	out := make(map[uuid.UUID]bool, len(projectIDs))
	if len(projectIDs) == 0 {
		return out, nil
	}
	rows, err := s.db.Query(ctx, `
		SELECT project_id
		FROM project_favorites
		WHERE user_id = $1 AND project_id = ANY($2)
	`, userID, projectIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

func (s *Store) ListFavoriteProjects(ctx context.Context, p ListFavoriteProjectsParams) ([]model.Project, error) {
	args := []any{p.User.ID, p.User.IsAdmin}
	q := `
		SELECT projects.id, projects.owner_id, owner.username, projects.key,
		       projects.name, projects.description, projects.image_object_id, projects.image_thumbnail_object_id,
		       projects.sprints_enabled, projects.created_at, projects.updated_at
		FROM project_favorites pf
		JOIN projects ON projects.id = pf.project_id
		JOIN users owner ON owner.id = projects.owner_id
		WHERE pf.user_id = $1
		  AND projects.deleted_at IS NULL
		  AND owner.deleted_at IS NULL
		  AND (
		      $2
		      OR EXISTS (
		          SELECT 1
		          FROM project_members pm
		          WHERE pm.project_id = projects.id AND pm.user_id = $1
		      )
		  )
		ORDER BY pf.created_at DESC, projects.key ASC, projects.id ASC
	`
	if p.Limit > 0 {
		args = append(args, p.Limit)
		q += ` LIMIT $3`
	}
	rows, err := s.db.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []model.Project{}
	for rows.Next() {
		project, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, project)
	}
	return out, rows.Err()
}

func (s *Store) ensureFavoriteProjectEligible(ctx context.Context, userID, projectID uuid.UUID) error {
	var ok bool
	err := s.db.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM users u
			JOIN projects p ON p.id = $2
			WHERE u.id = $1 AND u.deleted_at IS NULL AND p.deleted_at IS NULL
		)
	`, userID, projectID).Scan(&ok)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotFound
	}
	return nil
}
