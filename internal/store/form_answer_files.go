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

// The person answering a form can attach files to an answer, and an agent can
// fetch them.
//
//   - An answer file is a form_attachments row with answer_question_id set, so
//     it is a file of its form like the asker's media, and generic object
//     routes never show it. The asker's rows have no question.
//   - Only the form's assignee adds or removes one, and only while the form is
//     open: it is answering.
//   - A form_file_grants row is what a file's download URL stands for. It lasts
//     minutes, serves one file and stands for the member who made it.
//
// See STORAGE.md and SECURITY_MODEL.md.

// Named conflicts for the two limits on answer files. Both wrap ErrConflict.
var (
	ErrFormAnswerFilesFull = fmt.Errorf("this answer already has %d files: %w", model.FormMaxAnswerFiles, ErrConflict)
	ErrFormFilesFull       = fmt.Errorf("this form already has %d attached files: %w", model.FormMaxAnswerFilesPerForm, ErrConflict)
)

type AttachFormAnswerFileParams struct {
	FormID     uuid.UUID
	AssigneeID uuid.UUID
	QuestionID string
	// Object is the file's metadata. Its ProjectID and CreatedByID are set from
	// the form and the assignee.
	Object CreateStorageObjectParams
}

// AttachFormAnswerFile attaches a file to the assignee's answer to a question
// of an open form, in one transaction under the form's row lock, so two
// uploads racing for the last place leave one winner. The form must be open
// (otherwise the usual status conflict), the question must exist (otherwise a
// *model.FormError), and neither limit may be reached (ErrFormAnswerFilesFull,
// ErrFormFilesFull). It returns the form as it is now, with the new file.
func (s *Store) AttachFormAnswerFile(ctx context.Context, p AttachFormAnswerFileParams) (model.Form, model.FormAttachment, error) {
	var out model.Form
	var attached model.FormAttachment
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		form, err := lockFormForAssignee(ctx, tx, p.FormID, p.AssigneeID)
		if err != nil {
			return err
		}
		if err := CheckFormAnswerFileAttach(form, p.QuestionID); err != nil {
			return err
		}
		p.Object.ProjectID = form.ProjectID
		p.Object.CreatedByID = p.AssigneeID
		object, err := insertStorageObject(ctx, tx, p.Object)
		if err != nil {
			return err // defensive: the form row is locked and its project was live a moment ago; only a concurrent project delete or a DB outage fails here
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO form_attachments (project_id, form_id, storage_object_id, created_by_id, answer_question_id)
			VALUES ($1, $2, $3, $4, $5)
		`, form.ProjectID, form.ID, object.ID, p.AssigneeID, p.QuestionID); err != nil {
			return err // defensive: the object was just inserted for this project, and the form and creator exist
		}
		if err := touchFormAnswers(ctx, tx, form.ID); err != nil {
			return err // defensive: see touchFormAnswers
		}
		if out, err = getForm(ctx, tx, form.ID, false); err != nil {
			return err // defensive: the row is locked
		}
		for _, attachment := range out.Attachments {
			if attachment.StorageObjectID == object.ID {
				attached = attachment
			}
		}
		return nil
	})
	if err != nil {
		return model.Form{}, model.FormAttachment{}, err
	}
	return out, attached, nil
}

// RemoveFormAnswerFile removes the file of that object number from the
// assignee's answer to a question of an open form. The checks are the
// attach's. A file of another question, of the asker's media or of another
// form is not found. The object is soft-deleted, which queues its bytes for
// deletion. It returns the form as it is now.
func (s *Store) RemoveFormAnswerFile(ctx context.Context, formID, assigneeID uuid.UUID, questionID string, objectNumber int) (model.Form, error) {
	var out model.Form
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		form, err := lockFormForAssignee(ctx, tx, formID, assigneeID)
		if err != nil {
			return err
		}
		if err := checkFormAnswerFilesOpen(form, questionID); err != nil {
			return err
		}
		var objectID uuid.UUID
		for _, attachment := range form.Attachments {
			if attachment.AnswerQuestionID == questionID && attachment.Object.Number == objectNumber {
				objectID = attachment.StorageObjectID
			}
		}
		if objectID == uuid.Nil {
			return ErrNotFound
		}
		if _, err := tx.Exec(ctx, `DELETE FROM form_attachments WHERE form_id = $1 AND storage_object_id = $2`, form.ID, objectID); err != nil {
			return err // defensive: the row was just read under the form's lock
		}
		if _, err := tx.Exec(ctx, `
			UPDATE storage_objects
			SET deleted_at = now(),
			    updated_at = GREATEST(clock_timestamp(), updated_at + interval '1 microsecond')
			WHERE id = $1 AND deleted_at IS NULL
		`, objectID); err != nil {
			return err // defensive: soft-delete has no expected constraint failure
		}
		if err := touchFormAnswers(ctx, tx, form.ID); err != nil {
			return err // defensive: see touchFormAnswers
		}
		out, err = getForm(ctx, tx, form.ID, false)
		return err
	})
	if err != nil {
		return model.Form{}, err
	}
	return out, nil
}

// checkFormAnswerFilesOpen checks what both attaching and removing an answer
// file need: the form is open and the question is one of its own.
func checkFormAnswerFilesOpen(form model.Form, questionID string) error {
	if form.Status != model.FormStatusOpen {
		return formStatusConflict(form, "its answers can't change")
	}
	if _, ok := form.Definition.Question(questionID); !ok {
		return &model.FormError{Field: questionID, Message: "the form has no question with this id"}
	}
	return nil
}

// CheckFormAnswerFileAttach says whether a file can be attached to the answer
// to a question of the form as it is now: the form is open, the question is
// its own and neither limit is reached. AttachFormAnswerFile checks it again
// under the form's lock; a handler calls it first to refuse an upload before
// reading the file.
func CheckFormAnswerFileAttach(form model.Form, questionID string) error {
	if err := checkFormAnswerFilesOpen(form, questionID); err != nil {
		return err
	}
	if form.AnswerFileCount(questionID) >= model.FormMaxAnswerFiles {
		return ErrFormAnswerFilesFull
	}
	if form.AnswerFileCount("") >= model.FormMaxAnswerFilesPerForm {
		return ErrFormFilesFull
	}
	return nil
}

// touchFormAnswers records that a form's answers changed, as SaveFormAnswers
// does.
func touchFormAnswers(ctx context.Context, tx pgx.Tx, formID uuid.UUID) error {
	if _, err := tx.Exec(ctx, `UPDATE forms SET version = version + 1, updated_at = now() WHERE id = $1`, formID); err != nil {
		return err // defensive: the row is locked
	}
	return nil
}

// FormFileGrant is what a download URL's key stands for: one file of one form,
// for a short time.
type FormFileGrant struct {
	ID          uuid.UUID
	ProjectID   uuid.UUID
	FormID      uuid.UUID
	CreatedByID uuid.UUID
	ExpiresAt   time.Time
	CreatedAt   time.Time
	// Object is the file the URL serves.
	Object model.StorageObject
}

type CreateFormFileGrantParams struct {
	FormID          uuid.UUID
	StorageObjectID uuid.UUID
	CreatedByID     uuid.UUID
	KeyHash         []byte
	ExpiresAt       time.Time
}

// CreateFormFileGrant stores a download grant for a file the form carries
// (the asker's media or an answer file) in a live project. A file the form
// does not carry is ErrNotFound.
func (s *Store) CreateFormFileGrant(ctx context.Context, p CreateFormFileGrantParams) (FormFileGrant, error) {
	var out FormFileGrant
	err := s.db.QueryRow(ctx, `
		INSERT INTO form_file_grants (project_id, form_id, storage_object_id, created_by_id, key_hash, expires_at)
		SELECT fa.project_id, fa.form_id, fa.storage_object_id, $3, $4, $5
		FROM form_attachments fa
		JOIN projects p ON p.id = fa.project_id AND p.deleted_at IS NULL
		JOIN storage_objects so ON so.id = fa.storage_object_id AND so.deleted_at IS NULL
		WHERE fa.form_id = $1 AND fa.storage_object_id = $2
		RETURNING id, project_id, form_id, created_by_id, expires_at, created_at
	`, p.FormID, p.StorageObjectID, p.CreatedByID, p.KeyHash, p.ExpiresAt).Scan(
		&out.ID, &out.ProjectID, &out.FormID, &out.CreatedByID, &out.ExpiresAt, &out.CreatedAt,
	)
	if err != nil {
		if isNoRows(err) {
			return FormFileGrant{}, ErrNotFound
		}
		return FormFileGrant{}, mapFormFileGrantWriteError(err)
	}
	return out, nil
}

// GetFormFileGrant returns the grant a download key's SHA-256 belongs to, with
// the file it serves. The key stands for the member who made it, and that is
// checked on every use: it is not found when the key is unknown or expired, the
// project is gone, the file is no longer attached to that form, or its creator
// can no longer read the form (removed, blocked, or deleted).
func (s *Store) GetFormFileGrant(ctx context.Context, keyHash []byte) (FormFileGrant, error) {
	if len(keyHash) == 0 {
		return FormFileGrant{}, ErrNotFound
	}
	var out FormFileGrant
	row := s.db.QueryRow(ctx, `
		SELECT g.id, g.project_id, g.form_id, g.created_by_id, g.expires_at, g.created_at,
		       so.id, so.project_id, so.number, so.owner_user_id, so.backend, so.bucket, so.object_key,
		       so.filename, so.content_type, so.byte_size, so.sha256, so.created_by_id,
		       so.created_at, so.updated_at, so.deleted_at
		FROM form_file_grants g
		JOIN projects p ON p.id = g.project_id AND p.deleted_at IS NULL
		JOIN form_attachments fa ON fa.form_id = g.form_id AND fa.storage_object_id = g.storage_object_id
		JOIN storage_objects so ON so.id = fa.storage_object_id AND so.deleted_at IS NULL
		WHERE g.key_hash = $1 AND g.expires_at > now()
	`, keyHash)
	scanned := grantRow{row: row, grant: &out}
	object, err := scanStorageObject(&scanned)
	if err != nil {
		if isNoRows(err) {
			return FormFileGrant{}, ErrNotFound
		}
		return FormFileGrant{}, err // defensive: DB outage past the no-rows branch
	}
	out.Object = object
	creator, err := s.GetUser(ctx, out.CreatedByID)
	if err != nil {
		return FormFileGrant{}, err
	}
	permissions, err := s.ProjectPermissionsForUser(ctx, creator, out.ProjectID)
	if err != nil {
		return FormFileGrant{}, err // defensive: the query already required a live project; only its owner being deleted or a DB outage fails here
	}
	if !permissions.CanReadMembersOnly {
		return FormFileGrant{}, ErrNotFound
	}
	return out, nil
}

// grantRow reads the grant's own columns ahead of the storage object's, so the
// object can go through scanStorageObject.
type grantRow struct {
	row   pgx.Row
	grant *FormFileGrant
}

func (g *grantRow) Scan(dest ...any) error {
	head := []any{&g.grant.ID, &g.grant.ProjectID, &g.grant.FormID, &g.grant.CreatedByID, &g.grant.ExpiresAt, &g.grant.CreatedAt}
	return g.row.Scan(append(head, dest...)...)
}

// mapFormFileGrantWriteError maps a failure creating a grant. A foreign-key
// failure is a creator who doesn't exist, because the file is checked in the
// same statement. A unique failure is a key hash another grant holds, and a
// CHECK failure one that is not a SHA-256.
func mapFormFileGrantWriteError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23503":
			return fmt.Errorf("form or user not found: %w", ErrNotFound)
		case "23505":
			return fmt.Errorf("download key is already in use: %w", ErrConflict)
		case "23514":
			return fmt.Errorf("download key hash must be a SHA-256: %w", ErrConflict)
		}
	}
	return err // defensive: non-pg or unmapped pg error
}
