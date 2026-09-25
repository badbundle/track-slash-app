package store

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/bradleymackey/track-slash/internal/model"
)

type CreateProjectAttachmentParams struct {
	ProjectID       uuid.UUID
	StorageObjectID uuid.UUID
	CreatedByID     uuid.UUID
}

type ProjectAttachmentsCursor = IssueAttachmentsCursor

type ListProjectAttachmentsParams struct {
	ProjectID uuid.UUID
	Cursor    *ProjectAttachmentsCursor
	Limit     int
}

func scanProjectAttachment(row descriptionAttachmentScanner) (model.ProjectAttachment, error) {
	fields, err := scanDescriptionAttachmentFields(row)
	if err != nil {
		return model.ProjectAttachment{}, err
	}
	return model.ProjectAttachment{
		ID: fields.ID, ProjectID: fields.ProjectID, StorageObjectID: fields.StorageObjectID,
		Object: fields.Object, CreatedByID: fields.CreatedByID, CreatedAt: fields.CreatedAt, UpdatedAt: fields.UpdatedAt,
	}, nil
}

func projectAttachmentSelect() string {
	return `
		SELECT pa.id, pa.project_id, pa.project_id, pa.storage_object_id, pa.created_by_id, pa.created_at, pa.updated_at,
		       so.id, so.project_id, so.number, so.owner_user_id, so.backend, so.bucket, so.object_key,
		       so.filename, so.content_type, so.byte_size, so.sha256, so.created_by_id,
		       so.created_at, so.updated_at, so.deleted_at
		FROM project_attachments pa
		JOIN projects p ON p.id = pa.project_id
		JOIN storage_objects so ON so.id = pa.storage_object_id
	`
}

func (s *Store) CreateProjectAttachment(ctx context.Context, p CreateProjectAttachmentParams) (model.ProjectAttachment, error) {
	var out model.ProjectAttachment
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		project, err := scanProject(tx.QueryRow(ctx, `
			SELECT p.id, p.owner_id, u.username, p.key, p.name, p.description,
			       p.image_object_id, p.image_thumbnail_object_id, p.sprints_enabled, p.created_at, p.updated_at
			FROM projects p
			JOIN users u ON u.id = p.owner_id
			WHERE p.id = $1 AND p.deleted_at IS NULL AND u.deleted_at IS NULL
			FOR UPDATE OF p
		`, p.ProjectID))
		if err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err
		}

		object, err := scanStorageObject(tx.QueryRow(ctx, `
			SELECT so.id, so.project_id, so.number, so.owner_user_id, so.backend, so.bucket, so.object_key,
			       so.filename, so.content_type, so.byte_size, so.sha256, so.created_by_id,
			       so.created_at, so.updated_at, so.deleted_at
			FROM storage_objects so
			JOIN projects p ON p.id = so.project_id
			WHERE so.id = $1 AND so.deleted_at IS NULL AND p.deleted_at IS NULL
			FOR UPDATE OF so
		`, p.StorageObjectID))
		if err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err
		}
		if project.ID != object.ProjectID {
			return fmt.Errorf("project and storage object belong to different projects: %w", ErrConflict)
		}

		out, err = scanProjectAttachment(tx.QueryRow(ctx, `
			WITH inserted AS (
				INSERT INTO project_attachments (project_id, storage_object_id, created_by_id)
				VALUES ($1, $2, $3)
				RETURNING id, project_id, storage_object_id, created_by_id, created_at, updated_at
			)
			SELECT ins.id, ins.project_id, ins.project_id, ins.storage_object_id, ins.created_by_id, ins.created_at, ins.updated_at,
			       so.id, so.project_id, so.number, so.owner_user_id, so.backend, so.bucket, so.object_key,
			       so.filename, so.content_type, so.byte_size, so.sha256, so.created_by_id,
			       so.created_at, so.updated_at, so.deleted_at
			FROM inserted ins
			JOIN projects p ON p.id = ins.project_id
			JOIN storage_objects so ON so.id = ins.storage_object_id
			WHERE p.deleted_at IS NULL AND so.deleted_at IS NULL
		`, project.ID, object.ID, p.CreatedByID))
		if err != nil {
			if mapped := mapDescriptionAttachmentWriteError(err, "project"); mapped != nil {
				return mapped
			}
			return err // defensive: non-pg or unmapped pg error
		}

		return appendProjectChangelog(ctx, tx, appendProjectChangelogParams{
			ProjectID: project.ID, Entity: "project_attachment", Op: "insert", EntityID: out.ID,
			TargetRef: project.Key, TargetTitle: project.Name,
			Summary: fmt.Sprintf("Attached %s to project %s", out.Object.Filename, project.Key),
		})
	})
	if err != nil {
		return model.ProjectAttachment{}, err
	}
	return out, nil
}

func (s *Store) ListProjectAttachments(ctx context.Context, p ListProjectAttachmentsParams) ([]model.ProjectAttachment, bool, error) {
	if _, err := s.GetProject(ctx, p.ProjectID); err != nil {
		return nil, false, err
	}
	args := []any{p.ProjectID}
	q := projectAttachmentSelect() + `
		WHERE pa.project_id = $1 AND p.deleted_at IS NULL AND so.deleted_at IS NULL
	`
	if p.Cursor != nil {
		args = append(args, p.Cursor.Number)
		q += fmt.Sprintf(" AND so.number > $%d", len(args))
	}
	args = append(args, p.Limit+1)
	q += fmt.Sprintf(" ORDER BY so.number ASC LIMIT $%d", len(args))
	return scanDescriptionAttachmentRows(ctx, s, q, args, p.Limit, scanProjectAttachment)
}

func (s *Store) GetProjectAttachmentByObjectNumber(ctx context.Context, projectID uuid.UUID, objectNumber int) (model.ProjectAttachment, error) {
	out, err := scanProjectAttachment(s.db.QueryRow(ctx, projectAttachmentSelect()+`
		WHERE pa.project_id = $1 AND so.number = $2 AND p.deleted_at IS NULL AND so.deleted_at IS NULL
	`, projectID, objectNumber))
	if err != nil {
		if isNoRows(err) {
			return model.ProjectAttachment{}, ErrNotFound
		}
		return model.ProjectAttachment{}, err
	}
	return out, nil
}

func (s *Store) DeleteProjectAttachment(ctx context.Context, projectID, storageObjectID uuid.UUID) (model.ProjectAttachment, error) {
	var out model.ProjectAttachment
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		var err error
		out, err = scanProjectAttachment(tx.QueryRow(ctx, projectAttachmentSelect()+`
			WHERE pa.project_id = $1 AND pa.storage_object_id = $2
			  AND p.deleted_at IS NULL AND so.deleted_at IS NULL
			FOR UPDATE OF pa, so
		`, projectID, storageObjectID))
		if err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err
		}
		project, err := scanProject(tx.QueryRow(ctx, `
			SELECT p.id, p.owner_id, u.username, p.key, p.name, p.description,
			       p.image_object_id, p.image_thumbnail_object_id, p.sprints_enabled, p.created_at, p.updated_at
			FROM projects p JOIN users u ON u.id = p.owner_id WHERE p.id = $1
		`, projectID))
		if err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `DELETE FROM project_attachments WHERE project_id = $1 AND storage_object_id = $2`, projectID, storageObjectID)
		if err != nil {
			return err // defensive: delete has no expected constraint mapping
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		if err := softDeleteAttachedStorageObject(ctx, tx, storageObjectID, &out.Object); err != nil {
			return err
		}
		return appendProjectChangelog(ctx, tx, appendProjectChangelogParams{
			ProjectID: project.ID, Entity: "project_attachment", Op: "delete", EntityID: out.ID,
			TargetRef: project.Key, TargetTitle: project.Name,
			Summary: fmt.Sprintf("Removed attachment %s from project %s", out.Object.Filename, project.Key),
		})
	})
	if err != nil {
		return model.ProjectAttachment{}, err
	}
	return out, nil
}
