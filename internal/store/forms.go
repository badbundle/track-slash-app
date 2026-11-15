package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/bradleymackey/track-slash/internal/model"
)

// A form's assignee has to be a member of its project, and its issue an issue
// of that project. Both wrap ErrNotFound, and are named so a handler can tell
// the caller which field was wrong.
var (
	ErrFormAssigneeNotMember = fmt.Errorf("assignee is not a member of the project: %w", ErrNotFound)
	ErrFormIssueNotInProject = fmt.Errorf("issue is not in this project: %w", ErrNotFound)
)

type CreateFormParams struct {
	ProjectID   uuid.UUID
	IssueID     *uuid.UUID
	Title       string
	Description string
	Definition  model.FormDefinition
	AssigneeID  uuid.UUID
	CreatedByID uuid.UUID
	OriginLabel string
	OriginURL   string
}

// SaveFormAnswersParams saves answers to some or all of a form's questions.
// A question that isn't named keeps the answer it has. Comment, when set,
// replaces the closing "Anything else?" text.
type SaveFormAnswersParams struct {
	FormID     uuid.UUID
	AssigneeID uuid.UUID
	Answers    model.FormAnswers
	Comment    *string
}

type SubmitFormParams struct {
	FormID     uuid.UUID
	AssigneeID uuid.UUID
	Via        model.FormSubmitSurface
}

// FormsCursor pages through a project's forms, newest first.
type FormsCursor struct {
	Number int `json:"n"`
}

type ListProjectFormsParams struct {
	ProjectID  uuid.UUID
	Statuses   []model.FormStatus
	IssueID    *uuid.UUID
	AssigneeID *uuid.UUID
	Cursor     *FormsCursor
	Limit      int
}

// formSelect reads a form with its project's key and owner, and its issue
// while that issue is live. A form whose issue has been deleted reads as
// linked to none.
const formSelect = `
	SELECT f.id, f.project_id, p.key, owner.username, f.number, i.id, i.number,
	       f.title, f.description, f.definition, f.answers, f.comment, f.status,
	       f.assignee_id, f.created_by_id, f.origin_label, f.origin_url,
	       f.submitted_via, f.submitted_at, f.acknowledged_at, f.cancelled_at,
	       f.created_at, f.updated_at
	FROM forms f
	JOIN projects p ON p.id = f.project_id AND p.deleted_at IS NULL
	JOIN users owner ON owner.id = p.owner_id
	LEFT JOIN issues i ON i.id = f.issue_id AND i.deleted_at IS NULL
`

type formScanner interface {
	Scan(dest ...any) error
}

type formQueryer interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func scanForm(row formScanner) (model.Form, error) {
	var out model.Form
	var issueID uuid.NullUUID
	var issueNumber *int
	var definition, answers []byte
	var via *string
	if err := row.Scan(
		&out.ID, &out.ProjectID, &out.ProjectKey, &out.OwnerUsername, &out.Number, &issueID, &issueNumber,
		&out.Title, &out.Description, &definition, &answers, &out.Comment, &out.Status,
		&out.AssigneeID, &out.CreatedByID, &out.OriginLabel, &out.OriginURL,
		&via, &out.SubmittedAt, &out.AcknowledgedAt, &out.CancelledAt,
		&out.CreatedAt, &out.UpdatedAt,
	); err != nil {
		return model.Form{}, err
	}
	if err := json.Unmarshal(definition, &out.Definition); err != nil {
		return model.Form{}, err // defensive: only CreateForm writes a definition, from the same type
	}
	if err := json.Unmarshal(answers, &out.Answers); err != nil {
		return model.Form{}, err // defensive: only this file writes answers, from the same type
	}
	out.Ref = model.FormRef(out.Number)
	if issueID.Valid {
		id := issueID.UUID
		identifier := fmt.Sprintf("%s-%d", out.ProjectKey, *issueNumber)
		out.IssueID = &id
		out.IssueIdentifier = &identifier
	}
	if via != nil {
		surface := model.FormSubmitSurface(*via)
		out.SubmittedVia = &surface
	}
	return out, nil
}

func getForm(ctx context.Context, q formQueryer, id uuid.UUID, lock bool) (model.Form, error) {
	query := formSelect + " WHERE f.id = $1"
	if lock {
		query += " FOR UPDATE OF f"
	}
	out, err := scanForm(q.QueryRow(ctx, query, id))
	if err != nil {
		if isNoRows(err) {
			return model.Form{}, ErrNotFound
		}
		return model.Form{}, err // defensive: DB outage past the no-rows branch
	}
	return out, nil
}

// CreateForm adds a form to a live project and allocates its project-scoped
// form-N ref under the project row lock. The form starts open, holding each
// question's suggested answer. A details or definition problem comes back as
// a *model.FormError.
func (s *Store) CreateForm(ctx context.Context, p CreateFormParams) (model.Form, error) {
	details, err := model.ValidateFormDetails(model.FormDetails{
		Title:       p.Title,
		Description: p.Description,
		OriginLabel: p.OriginLabel,
		OriginURL:   p.OriginURL,
	})
	if err != nil {
		return model.Form{}, err
	}
	definition, err := model.ValidateFormDefinition(p.Definition)
	if err != nil {
		return model.Form{}, err
	}
	rawDefinition, _ := json.Marshal(definition)
	rawAnswers, _ := json.Marshal(definition.StartingAnswers())

	var out model.Form
	err = pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		var number int
		if err := tx.QueryRow(ctx, `
			SELECT next_form_number
			FROM projects
			WHERE id = $1 AND deleted_at IS NULL
			FOR UPDATE
		`, p.ProjectID).Scan(&number); err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err // defensive: DB outage past the no-rows branch
		}

		var assigneeIsMember bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM projects p
				JOIN users u ON u.id = $2 AND u.deleted_at IS NULL
				LEFT JOIN project_members pm ON pm.project_id = p.id AND pm.user_id = u.id
				WHERE p.id = $1 AND (p.owner_id = u.id OR pm.user_id IS NOT NULL)
			)
		`, p.ProjectID, p.AssigneeID).Scan(&assigneeIsMember); err != nil {
			return err // defensive: the project row was just read
		}
		if !assigneeIsMember {
			return ErrFormAssigneeNotMember
		}

		if p.IssueID != nil {
			var issueInProject bool
			if err := tx.QueryRow(ctx, `
				SELECT EXISTS (
					SELECT 1 FROM issues
					WHERE id = $1 AND project_id = $2 AND deleted_at IS NULL
				)
			`, *p.IssueID, p.ProjectID).Scan(&issueInProject); err != nil {
				return err // defensive: the project row was just read
			}
			if !issueInProject {
				return ErrFormIssueNotInProject
			}
		}

		var id uuid.UUID
		if err := tx.QueryRow(ctx, `
			INSERT INTO forms (
				project_id, number, issue_id, title, description, definition, answers,
				assignee_id, created_by_id, origin_label, origin_url
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
			RETURNING id
		`, p.ProjectID, number, p.IssueID, details.Title, details.Description, rawDefinition, rawAnswers,
			p.AssigneeID, p.CreatedByID, details.OriginLabel, details.OriginURL).Scan(&id); err != nil {
			return mapFormWriteError(err)
		}

		if _, err := tx.Exec(ctx, `
			UPDATE projects
			SET next_form_number = next_form_number + 1
			WHERE id = $1
		`, p.ProjectID); err != nil {
			return err // defensive: project row was locked above
		}
		var err error
		out, err = getForm(ctx, tx, id, false)
		return err
	})
	if err != nil {
		return model.Form{}, err
	}
	return out, nil
}

// GetForm returns a form of a live project by id.
func (s *Store) GetForm(ctx context.Context, id uuid.UUID) (model.Form, error) {
	return getForm(ctx, s.db, id, false)
}

// GetFormByProjectNumber returns a form by its form-N number. Forms of
// deleted projects are not found.
func (s *Store) GetFormByProjectNumber(ctx context.Context, projectID uuid.UUID, number int) (model.Form, error) {
	out, err := scanForm(s.db.QueryRow(ctx, formSelect+" WHERE f.project_id = $1 AND f.number = $2", projectID, number))
	if err != nil {
		if isNoRows(err) {
			return model.Form{}, ErrNotFound
		}
		return model.Form{}, err // defensive: DB outage past the no-rows branch
	}
	return out, nil
}

// ListProjectForms lists a project's forms newest first, without their
// questions and answers. The filters narrow it to some statuses, to one
// issue's forms, or to the forms waiting on one person.
func (s *Store) ListProjectForms(ctx context.Context, p ListProjectFormsParams) ([]model.FormSummary, bool, error) {
	if _, err := s.GetProject(ctx, p.ProjectID); err != nil {
		return nil, false, err
	}
	args := []any{p.ProjectID}
	q := formSelect + " WHERE f.project_id = $1"
	if len(p.Statuses) > 0 {
		statuses := make([]string, len(p.Statuses))
		for i, status := range p.Statuses {
			statuses[i] = string(status)
		}
		args = append(args, statuses)
		q += fmt.Sprintf(" AND f.status::text = ANY($%d)", len(args))
	}
	if p.IssueID != nil {
		args = append(args, *p.IssueID)
		q += fmt.Sprintf(" AND i.id = $%d", len(args))
	}
	if p.AssigneeID != nil {
		args = append(args, *p.AssigneeID)
		q += fmt.Sprintf(" AND f.assignee_id = $%d", len(args))
	}
	if p.Cursor != nil {
		args = append(args, p.Cursor.Number)
		q += fmt.Sprintf(" AND f.number < $%d", len(args))
	}
	args = append(args, p.Limit+1)
	q += fmt.Sprintf(" ORDER BY f.number DESC LIMIT $%d", len(args))

	rows, err := s.db.Query(ctx, q, args...)
	if err != nil {
		return nil, false, err // defensive: the project lookup above already reached the DB
	}
	defer rows.Close()

	out := make([]model.FormSummary, 0, p.Limit)
	for rows.Next() {
		form, err := scanForm(rows)
		if err != nil {
			return nil, false, err // defensive: columns match the fixed SELECT list
		}
		out = append(out, form.Summary())
	}
	if err := rows.Err(); err != nil {
		return nil, false, err // defensive: connection lost mid-read
	}
	hasMore := len(out) > p.Limit
	if hasMore {
		out = out[:p.Limit]
	}
	return out, hasMore, nil
}

// SaveFormAnswers saves the assignee's answers to an open form as they make
// them. Answers may be incomplete; SubmitForm checks that nothing required is
// missing. An answer that doesn't fit its question comes back as a
// *model.FormError.
func (s *Store) SaveFormAnswers(ctx context.Context, p SaveFormAnswersParams) (model.Form, error) {
	var out model.Form
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		form, err := lockFormForAssignee(ctx, tx, p.FormID, p.AssigneeID)
		if err != nil {
			return err
		}
		if form.Status != model.FormStatusOpen {
			return formStatusConflict(form, "its answers can't change")
		}
		changes, err := form.Definition.CleanAnswers(p.Answers)
		if err != nil {
			return err
		}
		comment := form.Comment
		if p.Comment != nil {
			if comment, err = model.ValidateFormComment(*p.Comment); err != nil {
				return err
			}
		}
		rawAnswers, _ := json.Marshal(form.Answers.Merged(changes))
		if _, err := tx.Exec(ctx, `
			UPDATE forms
			SET answers = $2, comment = $3, version = version + 1, updated_at = now()
			WHERE id = $1
		`, form.ID, rawAnswers, comment); err != nil {
			return err // defensive: the row is locked and the values were validated
		}
		out, err = getForm(ctx, tx, form.ID, false)
		return err
	})
	if err != nil {
		return model.Form{}, err
	}
	return out, nil
}

// SubmitForm marks an open form answered. Its assignee submits it, and only
// once every required question has an answer: otherwise the error is a
// *model.FormIncompleteError naming the questions that still need one.
func (s *Store) SubmitForm(ctx context.Context, p SubmitFormParams) (model.Form, error) {
	if !p.Via.Valid() {
		return model.Form{}, fmt.Errorf("unknown submit surface %q: %w", p.Via, ErrConflict)
	}
	return s.transitionForm(ctx, p.FormID, &p.AssigneeID, model.FormStatusSubmitted, func(form model.Form) error {
		return form.Definition.CheckComplete(form.Answers)
	}, `submitted_at = now(), submitted_via = $3`, string(p.Via))
}

// ReopenForm returns a submitted form to its assignee so they can change
// their answers, which it keeps. A form that was picked up can't be reopened.
func (s *Store) ReopenForm(ctx context.Context, formID, assigneeID uuid.UUID) (model.Form, error) {
	return s.transitionForm(ctx, formID, &assigneeID, model.FormStatusOpen, nil,
		`submitted_at = NULL, submitted_via = NULL`)
}

// AcknowledgeForm marks a submitted form picked up: its answers were read and
// used, and can no longer change.
func (s *Store) AcknowledgeForm(ctx context.Context, formID uuid.UUID) (model.Form, error) {
	return s.transitionForm(ctx, formID, nil, model.FormStatusAcknowledged, nil,
		`acknowledged_at = now()`)
}

// CancelForm withdraws a form that hasn't been picked up yet.
func (s *Store) CancelForm(ctx context.Context, formID uuid.UUID) (model.Form, error) {
	return s.transitionForm(ctx, formID, nil, model.FormStatusCancelled, nil,
		`cancelled_at = now()`)
}

// transitionForm moves a form to another status under its row lock, so two
// transitions racing each other leave one winner and one conflict. assigneeID,
// when set, is who must be the form's assignee. check can refuse the move
// after seeing the locked form. set is the extra column assignments for the
// move, with args as its parameters from $3 on.
func (s *Store) transitionForm(ctx context.Context, formID uuid.UUID, assigneeID *uuid.UUID, to model.FormStatus, check func(model.Form) error, set string, args ...any) (model.Form, error) {
	var out model.Form
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		var form model.Form
		var err error
		if assigneeID != nil {
			form, err = lockFormForAssignee(ctx, tx, formID, *assigneeID)
		} else {
			form, err = getForm(ctx, tx, formID, true)
		}
		if err != nil {
			return err
		}
		if !form.Status.CanTransitionTo(to) {
			return formStatusConflict(form, fmt.Sprintf("it can't become %s", to))
		}
		if check != nil {
			if err := check(form); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `
			UPDATE forms
			SET status = $2, `+set+`, version = version + 1, updated_at = now()
			WHERE id = $1
		`, append([]any{form.ID, string(to)}, args...)...); err != nil {
			return err // defensive: the row is locked and the move was checked
		}
		out, err = getForm(ctx, tx, form.ID, false)
		return err
	})
	if err != nil {
		return model.Form{}, err
	}
	return out, nil
}

// lockFormForAssignee locks a form for an action only its assignee takes.
// Handlers check this first; the store refuses anyone else as a backstop.
func lockFormForAssignee(ctx context.Context, tx pgx.Tx, formID, assigneeID uuid.UUID) (model.Form, error) {
	form, err := getForm(ctx, tx, formID, true)
	if err != nil {
		return model.Form{}, err
	}
	if form.AssigneeID != assigneeID {
		return model.Form{}, fmt.Errorf("only the form's assignee can answer it: %w", ErrConflict)
	}
	return form, nil
}

func formStatusConflict(form model.Form, consequence string) error {
	return fmt.Errorf("%s is %s, so %s: %w", form.Ref, form.Status, consequence, ErrConflict)
}

// mapFormWriteError maps a foreign-key failure on insert. The project, the
// assignee and the issue were checked in the same transaction, so in practice
// it is a creator who doesn't exist. The form's CHECKs and its unique number
// can't fail here: the model validated every length first, and the number was
// allocated under the project row lock.
func mapFormWriteError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23503" {
		return fmt.Errorf("project, issue or user not found: %w", ErrNotFound)
	}
	return err // defensive: non-pg or unmapped pg error
}
