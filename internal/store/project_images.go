package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/bradleymackey/track-slash/internal/model"
)

type ReplaceProjectImageResult struct {
	Project        model.Project
	DeletedObjects []model.StorageObject
}

func (s *Store) ReplaceProjectImage(ctx context.Context, projectID, objectID, thumbnailObjectID uuid.UUID) (ReplaceProjectImageResult, error) {
	if objectID == uuid.Nil || thumbnailObjectID == uuid.Nil || objectID == thumbnailObjectID {
		return ReplaceProjectImageResult{}, ErrConflict
	}
	var out ReplaceProjectImageResult
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		current, err := projectForImageUpdate(ctx, tx, projectID)
		if err != nil {
			return err
		}
		if _, err := projectImageStorageObjectForUpdate(ctx, tx, projectID, objectID); err != nil {
			return err
		}
		if _, err := projectImageStorageObjectForUpdate(ctx, tx, projectID, thumbnailObjectID); err != nil {
			return err
		}

		next, err := scanProject(tx.QueryRow(ctx, `
			UPDATE projects p
			SET image_object_id = $2,
			    image_thumbnail_object_id = $3,
			    updated_at = GREATEST(clock_timestamp(), p.updated_at + interval '1 microsecond')
			FROM users u
			WHERE p.id = $1 AND p.owner_id = u.id AND p.deleted_at IS NULL AND u.deleted_at IS NULL
			RETURNING p.id, p.owner_id, u.username, p.key, p.name, p.description,
			          p.image_object_id, p.image_thumbnail_object_id, p.sprints_enabled, p.created_at, p.updated_at
		`, projectID, objectID, thumbnailObjectID))
		if err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err // defensive: project row was locked above
		}
		out.Project = next

		var oldIDs []uuid.UUID
		if current.ImageObjectID != nil && *current.ImageObjectID != objectID && *current.ImageObjectID != thumbnailObjectID {
			oldIDs = append(oldIDs, *current.ImageObjectID)
		}
		if current.ImageThumbnailObjectID != nil && *current.ImageThumbnailObjectID != objectID && *current.ImageThumbnailObjectID != thumbnailObjectID {
			oldIDs = append(oldIDs, *current.ImageThumbnailObjectID)
		}
		deleted, err := softDeleteProjectImageStorageObjects(ctx, tx, projectID, oldIDs)
		if err != nil {
			return err
		}
		out.DeletedObjects = deleted
		return nil
	})
	if err != nil {
		return ReplaceProjectImageResult{}, err
	}
	return out, nil
}

func (s *Store) RemoveProjectImage(ctx context.Context, projectID uuid.UUID) (ReplaceProjectImageResult, error) {
	var out ReplaceProjectImageResult
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		current, err := projectForImageUpdate(ctx, tx, projectID)
		if err != nil {
			return err
		}
		if current.ImageObjectID == nil && current.ImageThumbnailObjectID == nil {
			out.Project = current
			return nil
		}

		next, err := scanProject(tx.QueryRow(ctx, `
			UPDATE projects p
			SET image_object_id = NULL,
			    image_thumbnail_object_id = NULL,
			    updated_at = GREATEST(clock_timestamp(), p.updated_at + interval '1 microsecond')
			FROM users u
			WHERE p.id = $1 AND p.owner_id = u.id AND p.deleted_at IS NULL AND u.deleted_at IS NULL
			RETURNING p.id, p.owner_id, u.username, p.key, p.name, p.description,
			          p.image_object_id, p.image_thumbnail_object_id, p.sprints_enabled, p.created_at, p.updated_at
		`, projectID))
		if err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err // defensive: project row was locked above
		}
		out.Project = next

		var oldIDs []uuid.UUID
		if current.ImageObjectID != nil {
			oldIDs = append(oldIDs, *current.ImageObjectID)
		}
		if current.ImageThumbnailObjectID != nil {
			oldIDs = append(oldIDs, *current.ImageThumbnailObjectID)
		}
		deleted, err := softDeleteProjectImageStorageObjects(ctx, tx, projectID, oldIDs)
		if err != nil {
			return err
		}
		out.DeletedObjects = deleted
		return nil
	})
	if err != nil {
		return ReplaceProjectImageResult{}, err
	}
	return out, nil
}

func (s *Store) GetProjectImageObject(ctx context.Context, projectID uuid.UUID, thumbnail bool) (model.StorageObject, error) {
	project, err := s.GetProject(ctx, projectID)
	if err != nil {
		return model.StorageObject{}, err
	}
	objectID := project.ImageObjectID
	if thumbnail {
		objectID = project.ImageThumbnailObjectID
	}
	if objectID == nil {
		return model.StorageObject{}, ErrNotFound
	}
	object, err := projectImageStorageObject(s.db.QueryRow(ctx, `
		SELECT so.id, so.project_id, so.number, so.owner_user_id, so.backend, so.bucket, so.object_key,
		       so.filename, so.content_type, so.byte_size, so.sha256, so.created_by_id,
		       so.created_at, so.updated_at, so.deleted_at
		FROM storage_objects so
		JOIN projects p ON p.id = so.project_id
		WHERE so.id = $1 AND so.project_id = $2 AND so.owner_user_id IS NULL
		  AND so.deleted_at IS NULL AND p.deleted_at IS NULL
	`, *objectID, projectID))
	if err != nil {
		return model.StorageObject{}, err
	}
	return object, nil
}

func projectForImageUpdate(ctx context.Context, tx pgx.Tx, projectID uuid.UUID) (model.Project, error) {
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
			return model.Project{}, ErrNotFound
		}
		return model.Project{}, err
	}
	return project, nil
}

func projectImageStorageObject(row storageObjectScanner) (model.StorageObject, error) {
	object, err := scanStorageObject(row)
	if err != nil {
		if isNoRows(err) {
			return model.StorageObject{}, ErrNotFound
		}
		return model.StorageObject{}, err
	}
	return object, nil
}

func projectImageStorageObjectForUpdate(ctx context.Context, tx pgx.Tx, projectID, objectID uuid.UUID) (model.StorageObject, error) {
	return projectImageStorageObject(tx.QueryRow(ctx, `
		SELECT so.id, so.project_id, so.number, so.owner_user_id, so.backend, so.bucket, so.object_key,
		       so.filename, so.content_type, so.byte_size, so.sha256, so.created_by_id,
		       so.created_at, so.updated_at, so.deleted_at
		FROM storage_objects so
		WHERE so.id = $1 AND so.project_id = $2 AND so.owner_user_id IS NULL AND so.deleted_at IS NULL
		FOR UPDATE
	`, objectID, projectID))
}

func softDeleteProjectImageStorageObjects(ctx context.Context, tx pgx.Tx, projectID uuid.UUID, objectIDs []uuid.UUID) ([]model.StorageObject, error) {
	deleted := make([]model.StorageObject, 0, len(objectIDs))
	seen := map[uuid.UUID]bool{}
	for _, objectID := range objectIDs {
		if objectID == uuid.Nil || seen[objectID] {
			continue
		}
		seen[objectID] = true
		object, err := projectImageStorageObjectForUpdate(ctx, tx, projectID, objectID)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				continue
			}
			return nil, err
		}
		var updatedAt, deletedAt time.Time
		if err := tx.QueryRow(ctx, `
			UPDATE storage_objects
			SET deleted_at = now(),
			    updated_at = GREATEST(clock_timestamp(), updated_at + interval '1 microsecond')
			WHERE id = $1 AND deleted_at IS NULL
			RETURNING updated_at, deleted_at
		`, objectID).Scan(&updatedAt, &deletedAt); err != nil {
			if isNoRows(err) {
				continue
			}
			return nil, err // defensive: selected object was live and locked above
		}
		object.UpdatedAt = updatedAt
		object.DeletedAt = &deletedAt
		deleted = append(deleted, object)
	}
	return deleted, nil
}
