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

// A form can carry files. An agent uploads them first, through an upload URL
// that holds its own secret, and names them when it creates the form:
//
//   - A form_upload_grants row is what an upload URL stands for. It lasts a
//     while and takes a number of files.
//   - Each file uploaded is a storage object with a form_uploads row, a
//     pending upload. Generic object routes never show one.
//   - Creating the form claims the pending uploads it names: they move to
//     form_attachments and belong to that form from then on.
//   - Pending uploads that no form claims are swept away (SweepFormUploads).
//
// A storage object belongs to at most one attachment flow, and a pending
// upload is a flow of its own until a form claims it. See STORAGE.md.

// ErrFormUploadGrantFull is returned for an upload URL that is still valid
// but has taken all the files it allows.
var ErrFormUploadGrantFull = fmt.Errorf("this upload URL has no files left: %w", ErrConflict)

// FormMediaNotClaimableError names the files a new form asked for that are
// not the creator's pending uploads in its project. It wraps ErrNotFound.
// Nothing is claimed when it is returned.
type FormMediaNotClaimableError struct {
	// Refs are the object refs, such as object-9, in the order the form
	// names them.
	Refs []string
}

func (e *FormMediaNotClaimableError) Error() string {
	if len(e.Refs) == 1 {
		return "media: " + e.Refs[0] + " is not one of your uploads waiting for a form"
	}
	return "media: " + strings.Join(e.Refs, ", ") + " are not among your uploads waiting for a form"
}

func (e *FormMediaNotClaimableError) Unwrap() error { return ErrNotFound }

// FormUploadGrant is what an upload URL's key stands for.
type FormUploadGrant struct {
	ID          uuid.UUID
	ProjectID   uuid.UUID
	CreatedByID uuid.UUID
	ExpiresAt   time.Time
	MaxFiles    int
	FilesUsed   int
	CreatedAt   time.Time
}

type CreateFormUploadGrantParams struct {
	ProjectID   uuid.UUID
	CreatedByID uuid.UUID
	// KeyHash is the SHA-256 of the key in the upload URL.
	KeyHash   []byte
	ExpiresAt time.Time
	MaxFiles  int
}

// CreateFormUploadGrant stores an upload grant for a live project.
func (s *Store) CreateFormUploadGrant(ctx context.Context, p CreateFormUploadGrantParams) (FormUploadGrant, error) {
	var out FormUploadGrant
	err := s.db.QueryRow(ctx, `
		INSERT INTO form_upload_grants (project_id, created_by_id, key_hash, expires_at, max_files)
		SELECT p.id, $2, $3, $4, $5
		FROM projects p
		WHERE p.id = $1 AND p.deleted_at IS NULL
		RETURNING id, project_id, created_by_id, expires_at, max_files, files_used, created_at
	`, p.ProjectID, p.CreatedByID, p.KeyHash, p.ExpiresAt, p.MaxFiles).Scan(
		&out.ID, &out.ProjectID, &out.CreatedByID, &out.ExpiresAt, &out.MaxFiles, &out.FilesUsed, &out.CreatedAt,
	)
	if err != nil {
		if isNoRows(err) {
			return FormUploadGrant{}, ErrNotFound
		}
		return FormUploadGrant{}, mapFormUploadGrantWriteError(err)
	}
	return out, nil
}

// GetFormUploadGrant returns the grant an upload key's SHA-256 belongs to. The
// key stands for the member who made it, and that is checked on every use: it
// is not found when the key is unknown or expired, the project is gone, or its
// creator can no longer write to the project (removed, blocked, made read-only
// or deleted). A grant that is otherwise valid but has taken all its files is
// ErrFormUploadGrantFull.
func (s *Store) GetFormUploadGrant(ctx context.Context, keyHash []byte) (FormUploadGrant, error) {
	if len(keyHash) == 0 {
		return FormUploadGrant{}, ErrNotFound
	}
	var out FormUploadGrant
	if err := s.db.QueryRow(ctx, `
		SELECT g.id, g.project_id, g.created_by_id, g.expires_at, g.max_files, g.files_used, g.created_at
		FROM form_upload_grants g
		JOIN projects p ON p.id = g.project_id AND p.deleted_at IS NULL
		WHERE g.key_hash = $1 AND g.expires_at > now()
	`, keyHash).Scan(
		&out.ID, &out.ProjectID, &out.CreatedByID, &out.ExpiresAt, &out.MaxFiles, &out.FilesUsed, &out.CreatedAt,
	); err != nil {
		if isNoRows(err) {
			return FormUploadGrant{}, ErrNotFound
		}
		return FormUploadGrant{}, err // defensive: DB outage past the no-rows branch
	}
	creator, err := s.GetUser(ctx, out.CreatedByID)
	if err != nil {
		return FormUploadGrant{}, err
	}
	permissions, err := s.ProjectPermissionsForUser(ctx, creator, out.ProjectID)
	if err != nil {
		return FormUploadGrant{}, err // defensive: the grant query already required a live project; only its owner being deleted or a DB outage fails here
	}
	if !permissions.CanWrite {
		return FormUploadGrant{}, ErrNotFound
	}
	if out.FilesUsed >= out.MaxFiles {
		return FormUploadGrant{}, ErrFormUploadGrantFull
	}
	return out, nil
}

type CreateFormUploadParams struct {
	// Object is the file's metadata. Its ProjectID and CreatedByID say whose
	// pending upload it is.
	Object CreateStorageObjectParams
	// GrantID is the upload grant to count the file against, or nil for an
	// upload made with an account token.
	GrantID *uuid.UUID
}

// CreateFormUpload records an uploaded file as a pending upload, in one
// transaction: it takes a place from the grant (when there is one), creates
// the storage object and links the two. The object is never visible as an
// ordinary project object, not even for the moment between the two inserts.
// A grant with no place left is ErrFormUploadGrantFull, and nothing is
// created.
func (s *Store) CreateFormUpload(ctx context.Context, p CreateFormUploadParams) (model.StorageObject, error) {
	var out model.StorageObject
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		if p.GrantID != nil {
			// One statement checks the limit and counts the file, so uploads
			// racing for the last place leave one winner.
			tag, err := tx.Exec(ctx, `
				UPDATE form_upload_grants
				SET files_used = files_used + 1
				WHERE id = $1 AND project_id = $2 AND created_by_id = $3
				  AND files_used < max_files AND expires_at > now()
			`, *p.GrantID, p.Object.ProjectID, p.Object.CreatedByID)
			if err != nil {
				return err // defensive: the statement has no expected constraint failure
			}
			if tag.RowsAffected() == 0 {
				return ErrFormUploadGrantFull
			}
		}
		var err error
		out, err = insertStorageObject(ctx, tx, p.Object)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO form_uploads (storage_object_id, project_id, created_by_id, grant_id)
			VALUES ($1, $2, $3, $4)
		`, out.ID, out.ProjectID, p.Object.CreatedByID, p.GrantID); err != nil {
			return err // defensive: the object was just inserted for this project and creator, and the grant row was just updated
		}
		return nil
	})
	if err != nil {
		return model.StorageObject{}, err
	}
	return out, nil
}

// claimFormMedia moves the named pending uploads to the form, in the form's
// transaction. Each must be a live object of the project that the creator
// uploaded and no form has claimed. If any is not, nothing moves and the
// error names every one that isn't.
func claimFormMedia(ctx context.Context, tx pgx.Tx, projectID, formID, creatorID uuid.UUID, numbers []int) error {
	// Deleting the pending rows takes their locks, so two forms racing for the
	// same upload leave one winner.
	rows, err := tx.Query(ctx, `
		DELETE FROM form_uploads fu
		USING storage_objects so
		WHERE fu.storage_object_id = so.id
		  AND fu.project_id = $1 AND fu.created_by_id = $2
		  AND so.project_id = $1 AND so.number = ANY($3) AND so.deleted_at IS NULL
		RETURNING so.id, so.number
	`, projectID, creatorID, numbers)
	if err != nil {
		return err // defensive: DB outage
	}
	defer rows.Close()
	ids := make([]uuid.UUID, 0, len(numbers))
	claimed := map[int]bool{}
	for rows.Next() {
		var id uuid.UUID
		var number int
		if err := rows.Scan(&id, &number); err != nil {
			return err // defensive: columns match the fixed RETURNING list
		}
		ids = append(ids, id)
		claimed[number] = true
	}
	if err := rows.Err(); err != nil {
		return err // defensive: connection lost mid-read
	}
	rows.Close()

	if len(ids) != len(numbers) {
		var missing []string
		for _, number := range numbers {
			if !claimed[number] {
				missing = append(missing, model.StorageObjectRef(number))
			}
		}
		return &FormMediaNotClaimableError{Refs: missing}
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO form_attachments (project_id, form_id, storage_object_id, created_by_id)
		SELECT $1, $2, id, $3 FROM unnest($4::uuid[]) AS id
	`, projectID, formID, creatorID, ids); err != nil {
		return err // defensive: the form was just inserted, and each object was a pending upload, which no attachment holds
	}
	return nil
}

const formAttachmentSelect = `
	SELECT fa.id, fa.project_id, fa.form_id, fa.storage_object_id, fa.created_by_id, fa.created_at, fa.created_at,
	       so.id, so.project_id, so.number, so.owner_user_id, so.backend, so.bucket, so.object_key,
	       so.filename, so.content_type, so.byte_size, so.sha256, so.created_by_id,
	       so.created_at, so.updated_at, so.deleted_at,
	       fa.answer_question_id
	FROM form_attachments fa
	JOIN projects p ON p.id = fa.project_id AND p.deleted_at IS NULL
	JOIN storage_objects so ON so.id = fa.storage_object_id AND so.deleted_at IS NULL
`

type formAttachmentQueryer interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// formAttachmentRow adds formAttachmentSelect's last column, the question an
// answer file belongs to (NULL for the asker's media), to the columns the
// shared attachment scanner reads.
type formAttachmentRow struct {
	row      descriptionAttachmentScanner
	question *string
}

func (r *formAttachmentRow) Scan(dest ...any) error {
	return r.row.Scan(append(dest, &r.question)...)
}

func scanFormAttachment(row descriptionAttachmentScanner) (model.FormAttachment, error) {
	wrapped := &formAttachmentRow{row: row}
	fields, err := scanDescriptionAttachmentFields(wrapped)
	if err != nil {
		return model.FormAttachment{}, err
	}
	out := model.FormAttachment{
		ID: fields.ID, FormID: fields.ParentID, StorageObjectID: fields.StorageObjectID,
		Object: fields.Object, CreatedByID: fields.CreatedByID, CreatedAt: fields.CreatedAt,
	}
	if wrapped.question != nil {
		out.AnswerQuestionID = *wrapped.question
	}
	return out, nil
}

func listFormAttachments(ctx context.Context, q formAttachmentQueryer, formID uuid.UUID) ([]model.FormAttachment, error) {
	rows, err := q.Query(ctx, formAttachmentSelect+` WHERE fa.form_id = $1 ORDER BY so.number ASC`, formID)
	if err != nil {
		return nil, err // defensive: DB outage
	}
	defer rows.Close()
	var out []model.FormAttachment
	for rows.Next() {
		item, err := scanFormAttachment(rows)
		if err != nil {
			return nil, err // defensive: columns match the fixed SELECT list
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err // defensive: connection lost mid-read
	}
	return out, nil
}

// ListFormAttachments lists the files a form carries, with their storage
// object metadata, by object number.
func (s *Store) ListFormAttachments(ctx context.Context, formID uuid.UUID) ([]model.FormAttachment, error) {
	return listFormAttachments(ctx, s.db, formID)
}

// GetFormAttachmentByObjectNumber returns the file of that number on that
// form. A file that is pending, or on another form, is not found.
func (s *Store) GetFormAttachmentByObjectNumber(ctx context.Context, formID uuid.UUID, number int) (model.FormAttachment, error) {
	out, err := scanFormAttachment(s.db.QueryRow(ctx,
		formAttachmentSelect+` WHERE fa.form_id = $1 AND so.number = $2`, formID, number))
	if err != nil {
		if isNoRows(err) {
			return model.FormAttachment{}, ErrNotFound
		}
		return model.FormAttachment{}, err // defensive: DB outage past the no-rows branch
	}
	return out, nil
}

// FormUploadSweep says what SweepFormUploads removed.
type FormUploadSweep struct {
	Uploads int
	Grants  int
	// FileGrants are the expired download grants removed.
	FileGrants int
}

// SweepFormUploads deletes the pending uploads older than olderThan, which no
// form claimed: each storage object is soft-deleted, which queues its bytes
// for deletion, and its pending row goes. It also deletes the upload grants
// that expired more than olderThan ago, and the file download grants that
// expired that long ago. Claimed files and young uploads stay.
func (s *Store) SweepFormUploads(ctx context.Context, olderThan time.Duration) (FormUploadSweep, error) {
	var out FormUploadSweep
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		// Skipping locked rows leaves an upload a form is claiming right now
		// to that form.
		tag, err := tx.Exec(ctx, `
			WITH stale AS (
				SELECT storage_object_id
				FROM form_uploads
				WHERE created_at < now() - $1::interval
				FOR UPDATE SKIP LOCKED
			), removed AS (
				DELETE FROM form_uploads fu
				USING stale
				WHERE fu.storage_object_id = stale.storage_object_id
				RETURNING fu.storage_object_id
			)
			UPDATE storage_objects so
			SET deleted_at = now(),
			    updated_at = GREATEST(clock_timestamp(), so.updated_at + interval '1 microsecond')
			FROM removed
			WHERE so.id = removed.storage_object_id AND so.deleted_at IS NULL
		`, olderThan)
		if err != nil {
			return err // defensive: DB outage
		}
		out.Uploads = int(tag.RowsAffected())

		tag, err = tx.Exec(ctx, `
			DELETE FROM form_upload_grants
			WHERE expires_at < now() - $1::interval
		`, olderThan)
		if err != nil {
			return err // defensive: DB outage
		}
		out.Grants = int(tag.RowsAffected())

		tag, err = tx.Exec(ctx, `
			DELETE FROM form_file_grants
			WHERE expires_at < now() - $1::interval
		`, olderThan)
		if err != nil {
			return err // defensive: DB outage
		}
		out.FileGrants = int(tag.RowsAffected())
		return nil
	})
	if err != nil {
		return FormUploadSweep{}, err
	}
	return out, nil
}

// FormMediaExpiry says what ExpireFormMedia removed.
type FormMediaExpiry struct {
	Forms int
	Files int
}

// ExpireFormMedia deletes the files of forms that were picked up or cancelled
// more than olderThan ago, at most limit forms at a time, and records when on
// each form (media_removed_at). A form that is open or submitted is never
// touched, however old, and a form with no files is not marked.
//
// For each form, in one transaction: its storage objects are soft-deleted,
// which queues their bytes for deletion exactly as SweepFormUploads does, its
// attachment rows go and the form is marked. The form's updated_at is left
// alone, because the Inbox orders finished forms by it. Forms that another
// transaction holds are skipped and picked up by a later run.
func (s *Store) ExpireFormMedia(ctx context.Context, olderThan time.Duration, limit int) (FormMediaExpiry, error) {
	var out FormMediaExpiry
	err := s.db.QueryRow(ctx, `
		WITH due AS (
			SELECT f.id
			FROM forms f
			WHERE f.status IN ('acknowledged', 'cancelled')
			  AND f.media_removed_at IS NULL
			  AND CASE f.status WHEN 'acknowledged' THEN f.acknowledged_at ELSE f.cancelled_at END < now() - $1::interval
			  AND EXISTS (SELECT 1 FROM form_attachments fa WHERE fa.form_id = f.id)
			ORDER BY CASE f.status WHEN 'acknowledged' THEN f.acknowledged_at ELSE f.cancelled_at END, f.id
			LIMIT $2
			FOR UPDATE OF f SKIP LOCKED
		), removed AS (
			DELETE FROM form_attachments fa
			USING due
			WHERE fa.form_id = due.id
			RETURNING fa.storage_object_id
		), deleted AS (
			UPDATE storage_objects so
			SET deleted_at = now(),
			    updated_at = GREATEST(clock_timestamp(), so.updated_at + interval '1 microsecond')
			FROM removed
			WHERE so.id = removed.storage_object_id AND so.deleted_at IS NULL
			RETURNING so.id
		), marked AS (
			UPDATE forms f
			SET media_removed_at = now()
			FROM due
			WHERE f.id = due.id
			RETURNING f.id
		)
		SELECT (SELECT count(*) FROM marked), (SELECT count(*) FROM removed)
	`, olderThan, limit).Scan(&out.Forms, &out.Files)
	if err != nil {
		return FormMediaExpiry{}, err // defensive: the statement has no expected failure; only a DB outage gets here
	}
	return out, nil
}

// mapFormUploadGrantWriteError maps a failure creating a grant. A foreign-key
// failure is a creator who doesn't exist, because the project is checked in the
// same statement. A unique failure is a key hash another grant holds, and a
// CHECK failure one that is not a SHA-256 or a file limit below one.
func mapFormUploadGrantWriteError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23503":
			return fmt.Errorf("project or user not found: %w", ErrNotFound)
		case "23505":
			return fmt.Errorf("upload key is already in use: %w", ErrConflict)
		case "23514":
			return fmt.Errorf("invalid upload grant: %w", ErrConflict)
		}
	}
	return err // defensive: non-pg or unmapped pg error
}
