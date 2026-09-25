package store

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/bradleymackey/track-slash/internal/model"
)

func (s *Store) GrantProjectAccess(ctx context.Context, projectID, userID uuid.UUID) (model.ProjectMember, error) {
	return s.SetProjectMemberRole(ctx, projectID, userID, model.ProjectMemberRoleMember)
}

func (s *Store) SetProjectMemberRole(ctx context.Context, projectID, userID uuid.UUID, role model.ProjectMemberRole) (model.ProjectMember, error) {
	if !role.Valid() {
		return model.ProjectMember{}, fmt.Errorf("invalid project member role: %w", ErrConflict)
	}
	var out model.ProjectMember
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		project, err := scanProject(tx.QueryRow(ctx, `
			SELECT p.id, p.owner_id, u.username, p.key, p.name, p.description,
			       p.image_object_id, p.image_thumbnail_object_id, p.sprints_enabled, p.created_at, p.updated_at
			FROM projects p
			JOIN users u ON u.id = p.owner_id
			WHERE p.id = $1 AND p.deleted_at IS NULL AND u.deleted_at IS NULL
			FOR UPDATE OF p
		`, projectID))
		if err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err
		}
		var thumbnailID uuid.NullUUID
		if err := tx.QueryRow(ctx, `
			SELECT username, name, profile_image_thumbnail_object_id
			FROM users WHERE id = $1 AND deleted_at IS NULL
		`, userID).Scan(&out.Username, &out.Name, &thumbnailID); err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err
		}
		if thumbnailID.Valid {
			id := thumbnailID.UUID
			out.ProfileImageThumbnailObjectID = &id
		}
		out.ProjectID = projectID
		out.UserID = userID
		out.Role = role
		var blocked bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM project_user_blocks
				WHERE project_id = $1 AND user_id = $2
			)
		`, projectID, userID).Scan(&blocked); err != nil {
			return err
		}
		if blocked {
			return fmt.Errorf("blocked user cannot be added to project: %w", ErrConflict)
		}

		var existingRole model.ProjectMemberRole
		var existingCreatedAt time.Time
		err = tx.QueryRow(ctx, `
			SELECT role, created_at FROM project_members
			WHERE project_id = $1 AND user_id = $2
			FOR UPDATE
		`, projectID, userID).Scan(&existingRole, &existingCreatedAt)
		exists := err == nil
		if err != nil {
			if !isNoRows(err) {
				return err
			}
			err = nil
		}
		if userID == project.OwnerID && role != model.ProjectMemberRoleMember {
			return fmt.Errorf("project owner role cannot be changed: %w", ErrConflict)
		}
		if exists && existingRole == role {
			out.CreatedAt = existingCreatedAt
			return nil
		}
		if exists {
			if err := tx.QueryRow(ctx, `
				UPDATE project_members SET role = $3
				WHERE project_id = $1 AND user_id = $2
				RETURNING created_at
			`, projectID, userID, role).Scan(&out.CreatedAt); err != nil {
				return err
			}
		} else if err := tx.QueryRow(ctx, `
			INSERT INTO project_members (project_id, user_id, role)
			VALUES ($1, $2, $3)
			RETURNING created_at
		`, projectID, userID, role).Scan(&out.CreatedAt); err != nil {
			return err
		}

		op := "grant"
		summary := fmt.Sprintf("Added @%s to project %s as %s", out.Username, project.Key, role)
		details := model.ProjectChangelogDetails{}
		if exists {
			op = "update"
			summary = fmt.Sprintf("Changed @%s from %s to %s in project %s", out.Username, existingRole, role, project.Key)
			details.Changes = []model.ProjectChangelogChange{{Field: "role", Label: "Role", From: string(existingRole), To: string(role)}}
		}
		return appendProjectChangelog(ctx, tx, appendProjectChangelogParams{
			ProjectID:   project.ID,
			Entity:      "project_member",
			Op:          op,
			EntityID:    userID,
			TargetRef:   project.Key,
			TargetTitle: project.Name,
			Summary:     summary,
			Details:     details,
		})
	})
	if err != nil {
		return model.ProjectMember{}, err
	}
	return out, nil
}

func (s *Store) RevokeProjectAccess(ctx context.Context, projectID, userID uuid.UUID) error {
	return pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		var project model.Project
		var username string
		if err := tx.QueryRow(ctx, `
			SELECT p.id, p.owner_id, owner.username, p.key, p.name, p.description,
			       p.image_object_id, p.image_thumbnail_object_id, p.created_at, p.updated_at, member.username
			FROM project_members pm
			JOIN projects p ON p.id = pm.project_id
			JOIN users owner ON owner.id = p.owner_id
			JOIN users member ON member.id = pm.user_id
			WHERE pm.project_id = $1 AND pm.user_id = $2 AND p.deleted_at IS NULL
			FOR UPDATE OF pm
		`, projectID, userID).Scan(
			&project.ID, &project.OwnerID, &project.OwnerUsername, &project.Key, &project.Name, &project.Description,
			&project.ImageObjectID, &project.ImageThumbnailObjectID, &project.CreatedAt, &project.UpdatedAt, &username,
		); err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err
		}
		if userID == project.OwnerID {
			return fmt.Errorf("project owner cannot be removed: %w", ErrConflict)
		}
		tag, err := tx.Exec(ctx, `
			DELETE FROM project_members WHERE project_id = $1 AND user_id = $2
		`, projectID, userID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return appendProjectChangelog(ctx, tx, appendProjectChangelogParams{
			ProjectID:   project.ID,
			Entity:      "project_member",
			Op:          "revoke",
			EntityID:    userID,
			TargetRef:   project.Key,
			TargetTitle: project.Name,
			Summary:     fmt.Sprintf("Removed @%s from project %s", username, project.Key),
		})
	})
}

func (s *Store) GetProjectAccessSettings(ctx context.Context, projectID uuid.UUID) (model.ProjectAccessSettings, error) {
	var out model.ProjectAccessSettings
	err := s.db.QueryRow(ctx, `
		SELECT p.is_public, p.public_issue_creation
		FROM projects p
		JOIN users owner ON owner.id = p.owner_id
		WHERE p.id = $1 AND p.deleted_at IS NULL AND owner.deleted_at IS NULL
	`, projectID).Scan(&out.IsPublic, &out.PublicIssueCreation)
	if err != nil {
		if isNoRows(err) {
			return model.ProjectAccessSettings{}, ErrNotFound
		}
		return model.ProjectAccessSettings{}, err
	}
	return out, nil
}

func (s *Store) UpdateProjectAccessSettings(ctx context.Context, projectID uuid.UUID, settings model.ProjectAccessSettings) (model.ProjectAccessSettings, error) {
	if !settings.IsPublic {
		settings.PublicIssueCreation = false
	}
	var out model.ProjectAccessSettings
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		var projectKey, projectName string
		var before model.ProjectAccessSettings
		if err := tx.QueryRow(ctx, `
			SELECT p.key, p.name, p.is_public, p.public_issue_creation
			FROM projects p
			JOIN users owner ON owner.id = p.owner_id
			WHERE p.id = $1 AND p.deleted_at IS NULL AND owner.deleted_at IS NULL
			FOR UPDATE OF p
		`, projectID).Scan(&projectKey, &projectName, &before.IsPublic, &before.PublicIssueCreation); err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err
		}
		if before == settings {
			out = before
			return nil
		}
		if err := tx.QueryRow(ctx, `
			UPDATE projects
			SET is_public = $2, public_issue_creation = $3, updated_at = now()
			WHERE id = $1
			RETURNING is_public, public_issue_creation
		`, projectID, settings.IsPublic, settings.PublicIssueCreation).Scan(&out.IsPublic, &out.PublicIssueCreation); err != nil {
			return err
		}
		changes := make([]model.ProjectChangelogChange, 0, 2)
		if before.IsPublic != out.IsPublic {
			changes = append(changes, model.ProjectChangelogChange{Field: "is_public", Label: "Public access", From: fmt.Sprintf("%t", before.IsPublic), To: fmt.Sprintf("%t", out.IsPublic)})
		}
		if before.PublicIssueCreation != out.PublicIssueCreation {
			changes = append(changes, model.ProjectChangelogChange{Field: "public_issue_creation", Label: "Public issue creation", From: fmt.Sprintf("%t", before.PublicIssueCreation), To: fmt.Sprintf("%t", out.PublicIssueCreation)})
		}
		return appendProjectChangelog(ctx, tx, appendProjectChangelogParams{
			ProjectID:   projectID,
			Entity:      "project",
			Op:          "update",
			EntityID:    projectID,
			TargetRef:   projectKey,
			TargetTitle: projectName,
			Summary:     fmt.Sprintf("Updated public access for project %s", projectKey),
			Details:     model.ProjectChangelogDetails{Changes: changes},
		})
	})
	if err != nil {
		return model.ProjectAccessSettings{}, err
	}
	return out, nil
}

func (s *Store) BlockProjectUser(ctx context.Context, projectID, userID, createdByID uuid.UUID) (model.ProjectUserBlock, error) {
	var out model.ProjectUserBlock
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		var ownerID uuid.UUID
		var projectKey, projectName string
		if err := tx.QueryRow(ctx, `
			SELECT p.owner_id, p.key, p.name
			FROM projects p
			JOIN users owner ON owner.id = p.owner_id
			WHERE p.id = $1 AND p.deleted_at IS NULL AND owner.deleted_at IS NULL
			FOR UPDATE OF p
		`, projectID).Scan(&ownerID, &projectKey, &projectName); err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err
		}
		if userID == ownerID {
			return fmt.Errorf("project owner cannot be blocked: %w", ErrConflict)
		}
		var creatorExists bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS (SELECT 1 FROM users WHERE id = $1 AND deleted_at IS NULL)
		`, createdByID).Scan(&creatorExists); err != nil {
			return err
		}
		if !creatorExists {
			return ErrNotFound
		}
		var thumbnailID uuid.NullUUID
		if err := tx.QueryRow(ctx, `
			SELECT username, name, profile_image_thumbnail_object_id
			FROM users WHERE id = $1 AND deleted_at IS NULL
		`, userID).Scan(&out.Username, &out.Name, &thumbnailID); err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err
		}
		out.ProjectID = projectID
		out.UserID = userID
		if thumbnailID.Valid {
			id := thumbnailID.UUID
			out.ProfileImageThumbnailObjectID = &id
		}
		var existingCreator uuid.UUID
		err := tx.QueryRow(ctx, `
			SELECT id, created_by_id, created_at
			FROM project_user_blocks
			WHERE project_id = $1 AND user_id = $2
			FOR UPDATE
		`, projectID, userID).Scan(&out.ID, &existingCreator, &out.CreatedAt)
		if err == nil {
			out.CreatedByID = existingCreator
			return nil
		}
		if !isNoRows(err) {
			return err
		}
		if err := tx.QueryRow(ctx, `
			INSERT INTO project_user_blocks (project_id, user_id, created_by_id)
			VALUES ($1, $2, $3)
			RETURNING id, created_by_id, created_at
		`, projectID, userID, createdByID).Scan(&out.ID, &out.CreatedByID, &out.CreatedAt); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			DELETE FROM project_members WHERE project_id = $1 AND user_id = $2
		`, projectID, userID); err != nil {
			return err
		}
		return appendProjectChangelog(ctx, tx, appendProjectChangelogParams{
			ProjectID:   projectID,
			Entity:      "project_block",
			Op:          "insert",
			EntityID:    out.ID,
			TargetRef:   projectKey,
			TargetTitle: projectName,
			Summary:     fmt.Sprintf("Blocked @%s from project %s", out.Username, projectKey),
		})
	})
	if err != nil {
		return model.ProjectUserBlock{}, err
	}
	return out, nil
}

func (s *Store) UnblockProjectUser(ctx context.Context, projectID, userID uuid.UUID) error {
	return pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		var blockID uuid.UUID
		var projectKey, projectName, username string
		if err := tx.QueryRow(ctx, `
			SELECT b.id, p.key, p.name, u.username
			FROM project_user_blocks b
			JOIN projects p ON p.id = b.project_id
			JOIN users u ON u.id = b.user_id
			WHERE b.project_id = $1 AND b.user_id = $2
			  AND p.deleted_at IS NULL AND u.deleted_at IS NULL
			FOR UPDATE OF b
		`, projectID, userID).Scan(&blockID, &projectKey, &projectName, &username); err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM project_user_blocks WHERE id = $1`, blockID); err != nil {
			return err
		}
		return appendProjectChangelog(ctx, tx, appendProjectChangelogParams{
			ProjectID:   projectID,
			Entity:      "project_block",
			Op:          "delete",
			EntityID:    blockID,
			TargetRef:   projectKey,
			TargetTitle: projectName,
			Summary:     fmt.Sprintf("Unblocked @%s from project %s", username, projectKey),
		})
	})
}

func (s *Store) ListProjectUserBlocks(ctx context.Context, projectID uuid.UUID) ([]model.ProjectUserBlock, error) {
	if _, err := s.GetProject(ctx, projectID); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(ctx, `
		SELECT b.id, b.project_id, b.user_id, u.username, u.name,
		       u.profile_image_thumbnail_object_id, b.created_by_id, b.created_at
		FROM project_user_blocks b
		JOIN users u ON u.id = b.user_id
		WHERE b.project_id = $1 AND u.deleted_at IS NULL
		ORDER BY lower(u.name), lower(u.username), b.id
	`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.ProjectUserBlock{}
	for rows.Next() {
		var block model.ProjectUserBlock
		var thumbnailID uuid.NullUUID
		if err := rows.Scan(&block.ID, &block.ProjectID, &block.UserID, &block.Username, &block.Name, &thumbnailID, &block.CreatedByID, &block.CreatedAt); err != nil {
			return nil, err
		}
		if thumbnailID.Valid {
			id := thumbnailID.UUID
			block.ProfileImageThumbnailObjectID = &id
		}
		out = append(out, block)
	}
	return out, rows.Err()
}

func (s *Store) ListProjectMembers(ctx context.Context, projectID uuid.UUID) ([]model.ProjectMember, error) {
	if _, err := s.GetProject(ctx, projectID); err != nil {
		return nil, err
	}
	const q = `
		SELECT pm.project_id, pm.user_id, u.username, u.name, u.profile_image_thumbnail_object_id,
		       pm.role, pm.created_at
		FROM project_members pm
		JOIN projects p ON p.id = pm.project_id
		JOIN users u ON u.id = pm.user_id
		WHERE pm.project_id = $1 AND u.deleted_at IS NULL
		ORDER BY (pm.user_id = p.owner_id) DESC, lower(u.name) ASC, lower(u.username) ASC, pm.user_id ASC
	`
	rows, err := s.db.Query(ctx, q, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []model.ProjectMember{}
	for rows.Next() {
		var m model.ProjectMember
		var thumbnailID uuid.NullUUID
		if err := rows.Scan(&m.ProjectID, &m.UserID, &m.Username, &m.Name, &thumbnailID, &m.Role, &m.CreatedAt); err != nil {
			return nil, err
		}
		if thumbnailID.Valid {
			id := thumbnailID.UUID
			m.ProfileImageThumbnailObjectID = &id
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) ListProjectAssignees(ctx context.Context, projectID uuid.UUID) ([]model.ProjectAssignee, error) {
	if _, err := s.GetProject(ctx, projectID); err != nil {
		return nil, err
	}
	const q = `
		WITH assignees AS (
			SELECT u.id, u.username, u.name, u.profile_image_thumbnail_object_id
			FROM project_members pm
			JOIN users u ON u.id = pm.user_id
			WHERE pm.project_id = $1 AND u.deleted_at IS NULL
			  AND NOT EXISTS (
			      SELECT 1 FROM project_user_blocks b
			      WHERE b.project_id = pm.project_id AND b.user_id = u.id
			  )
			UNION
			SELECT u.id, u.username, u.name, u.profile_image_thumbnail_object_id
			FROM issues i
			JOIN users u ON u.id = i.assignee_id
			WHERE i.project_id = $1 AND i.deleted_at IS NULL AND u.deleted_at IS NULL
			  AND NOT EXISTS (
			      SELECT 1 FROM project_user_blocks b
			      WHERE b.project_id = i.project_id AND b.user_id = u.id
			  )
		)
		SELECT id, username, name, profile_image_thumbnail_object_id
		FROM assignees
		ORDER BY lower(name) ASC, lower(username) ASC, id ASC
	`
	rows, err := s.db.Query(ctx, q, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []model.ProjectAssignee{}
	for rows.Next() {
		var a model.ProjectAssignee
		var thumbnailID uuid.NullUUID
		if err := rows.Scan(&a.ID, &a.Username, &a.Name, &thumbnailID); err != nil {
			return nil, err
		}
		if thumbnailID.Valid {
			id := thumbnailID.UUID
			a.ProfileImageThumbnailObjectID = &id
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

type SearchProjectMembersParams struct {
	ProjectID    uuid.UUID
	Query        string
	Limit        int
	WritableOnly bool
}

func (s *Store) SearchProjectMembers(ctx context.Context, p SearchProjectMembersParams) ([]model.User, error) {
	if _, err := s.GetProject(ctx, p.ProjectID); err != nil {
		return nil, err
	}
	query := strings.ToLower(strings.TrimSpace(p.Query))
	q := `
		SELECT u.id, u.username, COALESCE(u.email, ''), u.name, u.is_admin, u.created_at,
		       u.profile_image_object_id, u.profile_image_thumbnail_object_id
		FROM project_members pm
		JOIN projects p ON p.id = pm.project_id
		JOIN users u ON u.id = pm.user_id
		WHERE pm.project_id = $1
		  AND p.deleted_at IS NULL
		  AND u.deleted_at IS NULL
	`
	if p.WritableOnly {
		q += ` AND (pm.role = 'member' OR p.owner_id = u.id)`
	}
	q += ` AND (
		      $2 = ''
		      OR lower(u.name) LIKE '%' || $2 || '%'
		      OR lower(u.username) LIKE '%' || $2 || '%'
		      OR lower(COALESCE(u.email, '')) LIKE '%' || $2 || '%'
		  )
		ORDER BY lower(u.name) ASC, lower(u.username) ASC, u.id ASC
		LIMIT $3
	`
	rows, err := s.db.Query(ctx, q, p.ProjectID, query, p.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []model.User{}
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

type SearchAvailableProjectMembersParams struct {
	ProjectID uuid.UUID
	Query     string
	Limit     int
}

const (
	ProjectMemberCandidateMinQueryLength = 2
	ProjectMemberCandidateLimit          = 10
)

func ProjectMemberCandidateQueryReady(query string) bool {
	return utf8.RuneCountInString(strings.TrimSpace(query)) >= ProjectMemberCandidateMinQueryLength
}

func (s *Store) SearchAvailableProjectMembers(ctx context.Context, p SearchAvailableProjectMembersParams) ([]model.ProjectMemberCandidate, error) {
	project, err := s.GetProject(ctx, p.ProjectID)
	if err != nil {
		return nil, err
	}
	if !ProjectMemberCandidateQueryReady(p.Query) {
		return []model.ProjectMemberCandidate{}, nil
	}
	query := strings.ToLower(strings.TrimSpace(p.Query))
	limit := min(p.Limit, ProjectMemberCandidateLimit)
	rows, err := s.db.Query(ctx, `
		SELECT u.id, u.username, u.name, u.profile_image_thumbnail_object_id
		FROM users u
		WHERE u.deleted_at IS NULL
		  AND u.id <> $2
		  AND NOT EXISTS (
		      SELECT 1 FROM project_members pm
		      WHERE pm.project_id = $1 AND pm.user_id = u.id
		  )
		  AND NOT EXISTS (
		      SELECT 1 FROM project_user_blocks b
		      WHERE b.project_id = $1 AND b.user_id = u.id
		  )
		  AND (
		      $3 = ''
		      OR lower(u.name) LIKE '%' || $3 || '%'
		      OR lower(u.username) LIKE '%' || $3 || '%'
		  )
		ORDER BY lower(u.name) ASC, lower(u.username) ASC, u.id ASC
		LIMIT $4
	`, p.ProjectID, project.OwnerID, query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []model.ProjectMemberCandidate{}
	for rows.Next() {
		var candidate model.ProjectMemberCandidate
		var thumbnailID uuid.NullUUID
		if err := rows.Scan(&candidate.ID, &candidate.Username, &candidate.Name, &thumbnailID); err != nil {
			return nil, err
		}
		if thumbnailID.Valid {
			id := thumbnailID.UUID
			candidate.ProfileImageThumbnailObjectID = &id
		}
		out = append(out, candidate)
	}
	return out, rows.Err()
}

type ProjectPermissions struct {
	Role                model.ProjectMemberRole
	IsOwner             bool
	IsPublic            bool
	PublicIssueCreation bool
	IsBlocked           bool
	CanRead             bool
	CanWrite            bool
	CanCreateIssues     bool
	CanManageMembers    bool
	// CanDelete covers deleting the project itself, which is a stronger
	// authority than editing its contents: write members may delete issues,
	// sprints, and context, but only the owner or a site admin may remove the
	// project they all live in.
	CanDelete bool
}

func (s *Store) ProjectPermissionsForUser(ctx context.Context, user model.User, projectID uuid.UUID) (ProjectPermissions, error) {
	var ownerID uuid.UUID
	var role string
	var isPublic, publicIssueCreation, isBlocked bool
	err := s.db.QueryRow(ctx, `
		SELECT p.owner_id, p.is_public, p.public_issue_creation,
		       COALESCE(pm.role::text, ''), EXISTS (
		           SELECT 1 FROM project_user_blocks b
		           WHERE b.project_id = p.id AND b.user_id = $2
		       )
		FROM projects p
		JOIN users owner ON owner.id = p.owner_id
		LEFT JOIN project_members pm ON pm.project_id = p.id AND pm.user_id = $2
		WHERE p.id = $1 AND p.deleted_at IS NULL AND owner.deleted_at IS NULL
	`, projectID, user.ID).Scan(&ownerID, &isPublic, &publicIssueCreation, &role, &isBlocked)
	if err != nil {
		if isNoRows(err) {
			return ProjectPermissions{}, ErrNotFound
		}
		return ProjectPermissions{}, err
	}
	permissions := ProjectPermissions{
		Role:                model.ProjectMemberRole(role),
		IsOwner:             ownerID == user.ID,
		IsPublic:            isPublic,
		PublicIssueCreation: publicIssueCreation,
		IsBlocked:           isBlocked,
	}
	if user.IsAdmin || permissions.IsOwner {
		permissions.CanRead = true
		permissions.CanWrite = true
		permissions.CanCreateIssues = true
		permissions.CanManageMembers = true
		permissions.CanDelete = true
		if permissions.Role == "" {
			permissions.Role = model.ProjectMemberRoleMember
		}
		return permissions, nil
	}
	if permissions.IsBlocked {
		return permissions, nil
	}
	permissions.CanRead = permissions.Role.Valid() || isPublic
	permissions.CanWrite = permissions.Role == model.ProjectMemberRoleMember
	permissions.CanCreateIssues = permissions.CanWrite || (user.ID != uuid.Nil && permissions.Role == "" && isPublic && publicIssueCreation)
	return permissions, nil
}

func (s *Store) UserCanAccessProject(ctx context.Context, user model.User, projectID uuid.UUID) (bool, error) {
	permissions, err := s.ProjectPermissionsForUser(ctx, user, projectID)
	return permissions.CanRead, err
}

func (s *Store) UserCanWriteProject(ctx context.Context, user model.User, projectID uuid.UUID) (bool, error) {
	permissions, err := s.ProjectPermissionsForUser(ctx, user, projectID)
	return permissions.CanWrite, err
}

func (s *Store) UserCanCreateProjectIssue(ctx context.Context, user model.User, projectID uuid.UUID) (bool, error) {
	permissions, err := s.ProjectPermissionsForUser(ctx, user, projectID)
	return permissions.CanCreateIssues, err
}

func (s *Store) UserCanManageProjectMembers(ctx context.Context, user model.User, projectID uuid.UUID) (bool, error) {
	permissions, err := s.ProjectPermissionsForUser(ctx, user, projectID)
	return permissions.CanManageMembers, err
}

func (s *Store) UserCanDeleteProject(ctx context.Context, user model.User, projectID uuid.UUID) (bool, error) {
	permissions, err := s.ProjectPermissionsForUser(ctx, user, projectID)
	return permissions.CanDelete, err
}

func (s *Store) ProjectIDForIssue(ctx context.Context, id uuid.UUID) (uuid.UUID, error) {
	return s.lookupProjectID(ctx, `
		SELECT i.project_id
		FROM issues i
		JOIN projects p ON p.id = i.project_id
		WHERE i.id = $1 AND i.deleted_at IS NULL AND p.deleted_at IS NULL
	`, id)
}

func (s *Store) ProjectIDForComment(ctx context.Context, id uuid.UUID) (uuid.UUID, error) {
	return s.lookupProjectID(ctx, `
		SELECT i.project_id
		FROM comments c
		JOIN issues i ON i.id = c.issue_id
		JOIN projects p ON p.id = i.project_id
		WHERE c.id = $1 AND i.deleted_at IS NULL AND p.deleted_at IS NULL
	`, id)
}

func (s *Store) ProjectIDForSprint(ctx context.Context, id uuid.UUID) (uuid.UUID, error) {
	return s.lookupProjectID(ctx, `
		SELECT s.project_id
		FROM sprints s
		JOIN projects p ON p.id = s.project_id
		WHERE s.id = $1 AND s.deleted_at IS NULL AND p.deleted_at IS NULL
	`, id)
}

func (s *Store) ProjectIDForIssueLink(ctx context.Context, id uuid.UUID) (uuid.UUID, error) {
	return s.lookupProjectID(ctx, `
		SELECT il.project_id
		FROM issue_links il
		JOIN projects p ON p.id = il.project_id
		WHERE il.id = $1 AND p.deleted_at IS NULL
	`, id)
}

func (s *Store) ProjectIDForProjectContext(ctx context.Context, id uuid.UUID) (uuid.UUID, error) {
	return s.lookupProjectID(ctx, `
		SELECT pc.project_id
		FROM project_context pc
		JOIN projects p ON p.id = pc.project_id
		WHERE pc.id = $1 AND p.deleted_at IS NULL
	`, id)
}

func (s *Store) ProjectIDForIssueContextLink(ctx context.Context, id uuid.UUID) (uuid.UUID, error) {
	return s.lookupProjectID(ctx, `
		SELECT icl.project_id
		FROM issue_context_links icl
		JOIN projects p ON p.id = icl.project_id
		WHERE icl.id = $1 AND p.deleted_at IS NULL
	`, id)
}

func (s *Store) ProjectIDForIssueTag(ctx context.Context, id uuid.UUID) (uuid.UUID, error) {
	return s.lookupProjectID(ctx, `
		SELECT t.project_id
		FROM issue_tags t
		JOIN projects p ON p.id = t.project_id
		WHERE t.id = $1 AND p.deleted_at IS NULL
	`, id)
}

func (s *Store) ProjectIDForIssueTagLink(ctx context.Context, id uuid.UUID) (uuid.UUID, error) {
	return s.lookupProjectID(ctx, `
		SELECT l.project_id
		FROM issue_tag_links l
		JOIN projects p ON p.id = l.project_id
		WHERE l.id = $1 AND p.deleted_at IS NULL
	`, id)
}

func (s *Store) ProjectIDForIssueAttachment(ctx context.Context, id uuid.UUID) (uuid.UUID, error) {
	return s.lookupProjectID(ctx, `
		SELECT ia.project_id
		FROM issue_attachments ia
		JOIN issues i ON i.id = ia.issue_id
		JOIN projects p ON p.id = ia.project_id
		JOIN storage_objects so ON so.id = ia.storage_object_id
		WHERE ia.id = $1 AND i.deleted_at IS NULL AND p.deleted_at IS NULL AND so.deleted_at IS NULL
	`, id)
}

func (s *Store) ProjectIDForProjectAttachment(ctx context.Context, id uuid.UUID) (uuid.UUID, error) {
	return s.lookupProjectID(ctx, `
		SELECT pa.project_id
		FROM project_attachments pa
		JOIN projects p ON p.id = pa.project_id
		JOIN storage_objects so ON so.id = pa.storage_object_id
		WHERE pa.id = $1 AND p.deleted_at IS NULL AND so.deleted_at IS NULL
	`, id)
}

func (s *Store) ProjectIDForProjectChangelog(ctx context.Context, id uuid.UUID) (uuid.UUID, error) {
	return s.lookupProjectID(ctx, `
		SELECT e.project_id
		FROM project_changelog_entries e
		JOIN projects p ON p.id = e.project_id
		WHERE e.id = $1 AND p.deleted_at IS NULL
	`, id)
}

func (s *Store) lookupProjectID(ctx context.Context, q string, id uuid.UUID) (uuid.UUID, error) {
	var projectID uuid.UUID
	if err := s.db.QueryRow(ctx, q, id).Scan(&projectID); err != nil {
		if isNoRows(err) {
			return uuid.Nil, ErrNotFound
		}
		return uuid.Nil, err
	}
	return projectID, nil
}
