package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/bradleymackey/track-slash/internal/model"
)

type CreateStorageObjectParams struct {
	ID          uuid.UUID
	ProjectID   uuid.UUID
	Backend     string
	Bucket      string
	ObjectKey   string
	Filename    string
	ContentType string
	ByteSize    int64
	SHA256      string
	CreatedByID uuid.UUID
}

type CreateUserStorageObjectParams struct {
	ID          uuid.UUID
	OwnerUserID uuid.UUID
	Backend     string
	Bucket      string
	ObjectKey   string
	Filename    string
	ContentType string
	ByteSize    int64
	SHA256      string
	CreatedByID uuid.UUID
}

type StorageObjectsCursor struct {
	Number int `json:"n"`
}

type ListStorageObjectsParams struct {
	ProjectID uuid.UUID
	Cursor    *StorageObjectsCursor
	Limit     int
	// HideMembersOnlyIssueObjects leaves out files attached only to deleted
	// or private issues, which, like the issues themselves, are for project
	// members.
	HideMembersOnlyIssueObjects bool
}

// hiddenMembersOnlyIssueObject matches an object attached only to deleted or
// private issues, when the bound flag asks for those to be hidden. A file also
// attached to a live, non-private issue stays visible through that issue.
const hiddenMembersOnlyIssueObject = `(
	%s
	AND EXISTS (
		SELECT 1 FROM issue_attachments ia JOIN issues i ON i.id = ia.issue_id
		WHERE ia.storage_object_id = so.id AND (i.deleted_at IS NOT NULL OR i.private)
	)
	AND NOT EXISTS (
		SELECT 1 FROM issue_attachments ia JOIN issues i ON i.id = ia.issue_id
		WHERE ia.storage_object_id = so.id AND i.deleted_at IS NULL AND NOT i.private
	)
)`

// formMediaObject is true for a file a form carries or a pending upload for
// one. Generic object routes and tools leave these out for everyone, members
// included: form media is reachable only through its form.
const formMediaObject = `(
	EXISTS (SELECT 1 FROM form_uploads fu WHERE fu.storage_object_id = so.id)
	OR EXISTS (SELECT 1 FROM form_attachments fa WHERE fa.storage_object_id = so.id)
)`

type storageObjectScanner interface {
	Scan(dest ...any) error
}

func scanStorageObject(row storageObjectScanner) (model.StorageObject, error) {
	var out model.StorageObject
	var deletedAt sql.NullTime
	var projectID, ownerUserID uuid.NullUUID
	err := row.Scan(
		&out.ID, &projectID, &out.Number, &ownerUserID, &out.Backend, &out.Bucket, &out.ObjectKey,
		&out.Filename, &out.ContentType, &out.ByteSize, &out.SHA256, &out.CreatedByID,
		&out.CreatedAt, &out.UpdatedAt, &deletedAt,
	)
	if err != nil {
		return model.StorageObject{}, err
	}
	if deletedAt.Valid {
		out.DeletedAt = &deletedAt.Time
	}
	if projectID.Valid {
		out.ProjectID = projectID.UUID
	}
	if ownerUserID.Valid {
		id := ownerUserID.UUID
		out.OwnerUserID = &id
	}
	if out.Number > 0 {
		out.Ref = model.StorageObjectRef(out.Number)
	}
	return out, nil
}

func (s *Store) CreateStorageObject(ctx context.Context, p CreateStorageObjectParams) (model.StorageObject, error) {
	var out model.StorageObject
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		var err error
		out, err = insertStorageObject(ctx, tx, p)
		return err
	})
	if err != nil {
		return model.StorageObject{}, err
	}
	return out, nil
}

// insertStorageObject allocates the project's next object number under the
// project row lock and inserts the object, in the caller's transaction.
func insertStorageObject(ctx context.Context, tx pgx.Tx, p CreateStorageObjectParams) (model.StorageObject, error) {
	var number int
	if err := tx.QueryRow(ctx, `
		SELECT next_object_number
		FROM projects
		WHERE id = $1 AND deleted_at IS NULL
		FOR UPDATE
	`, p.ProjectID).Scan(&number); err != nil {
		if isNoRows(err) {
			return model.StorageObject{}, ErrNotFound
		}
		return model.StorageObject{}, err // defensive: DB outage past the no-rows branch
	}

	out, err := scanStorageObject(tx.QueryRow(ctx, `
		INSERT INTO storage_objects (
			id, project_id, number, owner_user_id, backend, bucket, object_key, filename, content_type,
			byte_size, sha256, created_by_id
		)
		VALUES ($1, $2, $3, NULL, $4, $5, $6, $7, $8, $9, $10, $11)
		RETURNING id, project_id, number, owner_user_id, backend, bucket, object_key, filename, content_type,
		          byte_size, sha256, created_by_id, created_at, updated_at, deleted_at
	`, p.ID, p.ProjectID, number, p.Backend, p.Bucket, p.ObjectKey, p.Filename, p.ContentType, p.ByteSize, p.SHA256, p.CreatedByID))
	if err != nil {
		if mapped := mapStorageObjectWriteError(err); mapped != nil {
			return model.StorageObject{}, mapped
		}
		return model.StorageObject{}, err // defensive: non-pg or unmapped pg error
	}

	if _, err := tx.Exec(ctx, `
		UPDATE projects
		SET next_object_number = next_object_number + 1,
		    updated_at = now()
		WHERE id = $1
	`, p.ProjectID); err != nil {
		return model.StorageObject{}, err // defensive: project row was locked above
	}
	return out, nil
}

func (s *Store) CreateUserStorageObject(ctx context.Context, p CreateUserStorageObjectParams) (model.StorageObject, error) {
	var out model.StorageObject
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		if _, err := scanUser(tx.QueryRow(ctx, `
			SELECT id, username, COALESCE(email, ''), name, is_admin, created_at, profile_image_object_id, profile_image_thumbnail_object_id
			FROM users
			WHERE id = $1 AND deleted_at IS NULL
			FOR UPDATE
		`, p.OwnerUserID)); err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err // defensive: DB outage past the no-rows branch
		}

		var err error
		out, err = scanStorageObject(tx.QueryRow(ctx, `
			INSERT INTO storage_objects (
				id, project_id, number, owner_user_id, backend, bucket, object_key, filename, content_type,
				byte_size, sha256, created_by_id
			)
			VALUES ($1, NULL, 0, $2, $3, $4, $5, $6, $7, $8, $9, $10)
			RETURNING id, project_id, number, owner_user_id, backend, bucket, object_key, filename, content_type,
			          byte_size, sha256, created_by_id, created_at, updated_at, deleted_at
		`, p.ID, p.OwnerUserID, p.Backend, p.Bucket, p.ObjectKey, p.Filename, p.ContentType, p.ByteSize, p.SHA256, p.CreatedByID))
		if err != nil {
			if mapped := mapStorageObjectWriteError(err); mapped != nil {
				return mapped
			}
			return err // defensive: non-pg or unmapped pg error
		}
		return nil
	})
	if err != nil {
		return model.StorageObject{}, err
	}
	return out, nil
}

func (s *Store) GetStorageObjectByProjectNumber(ctx context.Context, projectID uuid.UUID, number int) (model.StorageObject, error) {
	return s.GetVisibleStorageObjectByProjectNumber(ctx, projectID, number, false)
}

// GetVisibleStorageObjectByProjectNumber is GetStorageObjectByProjectNumber
// that, with hideMembersOnlyIssueObjects, treats a file attached only to deleted
// or private issues as missing.
func (s *Store) GetVisibleStorageObjectByProjectNumber(ctx context.Context, projectID uuid.UUID, number int, hideMembersOnlyIssueObjects bool) (model.StorageObject, error) {
	q := `
		SELECT so.id, so.project_id, so.number, so.owner_user_id, so.backend, so.bucket, so.object_key,
		       so.filename, so.content_type, so.byte_size, so.sha256, so.created_by_id,
		       so.created_at, so.updated_at, so.deleted_at
		FROM storage_objects so
		JOIN projects p ON p.id = so.project_id
		WHERE so.project_id = $1 AND so.number = $2 AND so.deleted_at IS NULL AND p.deleted_at IS NULL
		  AND so.id IS DISTINCT FROM p.image_object_id
		  AND so.id IS DISTINCT FROM p.image_thumbnail_object_id
		  AND NOT ` + formMediaObject + `
		  AND NOT ` + fmt.Sprintf(hiddenMembersOnlyIssueObject, "$3") + `
	`
	out, err := scanStorageObject(s.db.QueryRow(ctx, q, projectID, number, hideMembersOnlyIssueObjects))
	if err != nil {
		if isNoRows(err) {
			return model.StorageObject{}, ErrNotFound
		}
		return model.StorageObject{}, err
	}
	return out, nil
}

func (s *Store) ListStorageObjects(ctx context.Context, p ListStorageObjectsParams) ([]model.StorageObject, bool, error) {
	if _, err := s.GetProject(ctx, p.ProjectID); err != nil {
		return nil, false, err
	}
	args := []any{p.ProjectID, p.HideMembersOnlyIssueObjects}
	q := `
		SELECT so.id, so.project_id, so.number, so.owner_user_id, so.backend, so.bucket, so.object_key,
		       so.filename, so.content_type, so.byte_size, so.sha256, so.created_by_id,
		       so.created_at, so.updated_at, so.deleted_at
		FROM storage_objects so
		JOIN projects p ON p.id = so.project_id
		WHERE so.project_id = $1 AND so.deleted_at IS NULL AND p.deleted_at IS NULL
		  AND so.id IS DISTINCT FROM p.image_object_id
		  AND so.id IS DISTINCT FROM p.image_thumbnail_object_id
		  AND NOT ` + formMediaObject + `
		  AND NOT ` + fmt.Sprintf(hiddenMembersOnlyIssueObject, "$2") + `
	`
	if p.Cursor != nil {
		args = append(args, p.Cursor.Number)
		q += fmt.Sprintf(" AND so.number > $%d", len(args))
	}
	args = append(args, p.Limit+1)
	q += fmt.Sprintf(" ORDER BY so.number ASC LIMIT $%d", len(args))

	rows, err := s.db.Query(ctx, q, args...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()

	out := make([]model.StorageObject, 0, p.Limit)
	for rows.Next() {
		item, err := scanStorageObject(rows)
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

func (s *Store) DeleteStorageObject(ctx context.Context, id uuid.UUID) (model.StorageObject, error) {
	var out model.StorageObject
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		var err error
		out, err = scanStorageObject(tx.QueryRow(ctx, `
			SELECT so.id, so.project_id, so.number, so.owner_user_id, so.backend, so.bucket, so.object_key,
			       so.filename, so.content_type, so.byte_size, so.sha256, so.created_by_id,
			       so.created_at, so.updated_at, so.deleted_at
			FROM storage_objects so
			LEFT JOIN projects p ON p.id = so.project_id
			WHERE so.id = $1 AND so.deleted_at IS NULL AND (so.project_id IS NULL OR p.deleted_at IS NULL)
			FOR UPDATE OF so
		`, id))
		if err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err
		}
		var deletedAt time.Time
		err = tx.QueryRow(ctx, `
			UPDATE storage_objects
			SET deleted_at = now(),
			    updated_at = GREATEST(clock_timestamp(), updated_at + interval '1 microsecond')
			WHERE id = $1 AND deleted_at IS NULL
			RETURNING updated_at, deleted_at
		`, id).Scan(&out.UpdatedAt, &deletedAt)
		if err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err // defensive: soft-delete has no expected FK/check mapping
		}
		out.DeletedAt = &deletedAt
		return nil
	})
	if err != nil {
		return model.StorageObject{}, err
	}
	return out, nil
}

func mapStorageObjectWriteError(err error) error {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return nil
	}
	switch pgErr.Code {
	case "23503":
		return fmt.Errorf("invalid storage object reference: %w", ErrConflict)
	case "23505":
		return fmt.Errorf("storage object already exists: %w", ErrConflict)
	case "23514":
		return fmt.Errorf("invalid storage object metadata: %w", ErrConflict)
	default:
		return nil
	}
}

// StorageObjectBytesInUse reports whether a live storage object still uses
// the backend bytes at a key. A repeating issue's attachment copies share
// their original's bytes, so bytes are deleted only once this is false.
func (s *Store) StorageObjectBytesInUse(ctx context.Context, backend, bucket, objectKey string) (bool, error) {
	var inUse bool
	err := s.db.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM storage_objects
			WHERE backend = $1 AND bucket = $2 AND object_key = $3 AND deleted_at IS NULL
		)
	`, backend, bucket, objectKey).Scan(&inUse)
	return inUse, err
}
