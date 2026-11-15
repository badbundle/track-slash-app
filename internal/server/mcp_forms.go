package server

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/store"
)

// mcpCreateFormDescription teaches the convention as well as the tool, because
// a tool description is how an agent learns when to reach for a form.
const mcpCreateFormDescription = "Ask a person for decisions through a form they answer in the browser. " +
	"Use it when work is blocked on a person's choices or on something only they can do: put every open question in one form instead of stopping, and carry on with the work that doesn't depend on the answers. " +
	"Give each question a default with the answer you suggest, so the person only has to confirm it. " +
	"Name the thread or session that is asking in origin_label, and set origin_url when it has a link, so the person knows where to return. " +
	"Give the person the url from the result, and read the answers later with track_get_form. " +
	"You don't need the person to tell you they have answered. The result's wait_url says when the form is no longer open: GET it with ?wait=50 from a background command, and again while its status is still open. It needs no credentials and returns only {\"status\"}. Keep wait_url, because it is shown only once. Without a way to run a command, call track_get_form with wait_seconds instead. " +
	"Never ask for a password, key or token: every member of the project can read a form's answers. " +
	"Question types: single_choice and multi_choice (each with options, and allow_other for a free-text Other), confirm (one checkbox, for \"I've done this\" or \"Go ahead\") and text. " +
	"A form's questions can't be edited after it is created."

type mcpFormInput struct {
	mcpProjectInput
	Form string `json:"form" jsonschema:"form ref, for example form-1"`
}

type mcpGetFormInput struct {
	mcpFormInput
	IncludeDefinition bool `json:"include_definition,omitempty" jsonschema:"also return the questions as they were defined and the raw answers by question id"`
	WaitSeconds       int  `json:"wait_seconds,omitempty" jsonschema:"hold the call for up to this many seconds (50 at most) while the form is still open, and return as soon as it is submitted or cancelled; call again to keep waiting"`
}

type mcpCreateFormInput struct {
	mcpProjectInput
	Title       string              `json:"title" jsonschema:"what the form is about, 1 to 200 characters"`
	Description string              `json:"description,omitempty" jsonschema:"optional Markdown: what is blocked and why"`
	Issue       string              `json:"issue,omitempty" jsonschema:"optional ref of the issue the form is about, for example TRACK-123; it must be in this project"`
	AssigneeID  *string             `json:"assignee_id,omitempty" jsonschema:"user id of the project member who answers; defaults to you"`
	OriginLabel string              `json:"origin_label,omitempty" jsonschema:"short name of the thread or session that is asking, so the person knows where to return"`
	OriginURL   string              `json:"origin_url,omitempty" jsonschema:"https link back to that thread, when it has one"`
	Sections    []model.FormSection `json:"sections" jsonschema:"the questions, in one or more sections; several sections are shown as steps"`
}

type mcpListInboxInput struct {
	mcpPageInput
	State string `json:"state,omitempty" jsonschema:"todo (the default) or done"`
}

type mcpListFormsInput struct {
	mcpProjectInput
	mcpPageInput
	Statuses   []string `json:"statuses,omitempty" jsonschema:"any of open, submitted, acknowledged, cancelled"`
	Issue      string   `json:"issue,omitempty" jsonschema:"only forms about this issue ref, for example TRACK-123"`
	AssigneeID *string  `json:"assignee_id,omitempty" jsonschema:"only forms waiting on, or answered by, this user id"`
}

// mcpFormBrief is a form without its definition and raw answers, which
// responses already carries question by question. It is what the form tools
// return unless the caller asks for the definition.
type mcpFormBrief struct {
	model.FormSummary
	Description  string                   `json:"description"`
	Comment      string                   `json:"comment"`
	SubmittedVia *model.FormSubmitSurface `json:"submitted_via"`
	URL          string                   `json:"url"`
	WaitURL      string                   `json:"wait_url,omitempty"`
	Responses    []model.FormAnswerView   `json:"responses"`
}

func (s *Server) mcpFormBrief(form model.Form) mcpFormBrief {
	full := s.formResponse(form)
	return mcpFormBrief{
		FormSummary:  form.Summary(),
		Description:  form.Description,
		Comment:      form.Comment,
		SubmittedVia: form.SubmittedVia,
		URL:          full.URL,
		Responses:    full.Responses,
	}
}

func (s *Server) mcpFormOutput(form model.Form, includeDefinition bool) mcpToolOutput {
	if includeDefinition {
		return mcpToolOutput{"form": s.formResponse(form)}
	}
	return mcpToolOutput{"form": s.mcpFormBrief(form)}
}

// errMCPFormWaitsFull refuses a wait when too many calls already wait on the
// form.
var errMCPFormWaitsFull = mcpAppError{Code: "rate_limited", Message: errFormWaitsFull.Error()}

// mcpFormError reports a problem the caller should fix as a validation
// error, and passes every other error through.
func mcpFormError(err error) error {
	if msg, ok := formErrorMessage(err); ok {
		return validationError(msg)
	}
	return err // defensive: the caller's problems are caught above; what is left is a concurrent delete or DB outage
}

// mcpFormProject resolves a project for one of its members. Anyone who can't
// read the project is refused as by every project tool; a reader who isn't a
// member is told nothing is there, as for a form that doesn't exist.
func (s *Server) mcpFormProject(ctx context.Context, auth authContext, input mcpProjectInput) (model.Project, store.ProjectPermissions, error) {
	project, err := s.mcpProject(ctx, auth, input)
	if err != nil {
		return model.Project{}, store.ProjectPermissions{}, err
	}
	permissions, err := s.mcpReadPermissions(ctx, auth, project.ID)
	if err != nil {
		return model.Project{}, store.ProjectPermissions{}, err // defensive: mcpProject just checked read access
	}
	if !permissions.CanReadMembersOnly {
		return model.Project{}, store.ProjectPermissions{}, store.ErrNotFound
	}
	return project, permissions, nil
}

// mcpForm resolves a form for a member of its project.
func (s *Server) mcpForm(ctx context.Context, auth authContext, input mcpFormInput) (model.Form, store.ProjectPermissions, error) {
	project, permissions, err := s.mcpFormProject(ctx, auth, input.mcpProjectInput)
	if err != nil {
		return model.Form{}, store.ProjectPermissions{}, err
	}
	number, err := mcpTypedRef(input.Form, "form")
	if err != nil {
		return model.Form{}, store.ProjectPermissions{}, err
	}
	form, err := s.store.GetFormByProjectNumber(ctx, project.ID, number)
	if err != nil {
		return model.Form{}, store.ProjectPermissions{}, err
	}
	return form, permissions, nil
}

// mcpWritableForm resolves a form for a member who can write to its project.
func (s *Server) mcpWritableForm(ctx context.Context, auth authContext, input mcpFormInput) (model.Form, error) {
	form, permissions, err := s.mcpForm(ctx, auth, input)
	if err != nil {
		return model.Form{}, err
	}
	if !permissions.CanWrite {
		return model.Form{}, errMCPForbidden
	}
	return form, nil
}

func mcpFormUserID(raw *string) (*uuid.UUID, error) {
	if raw == nil {
		return nil, nil
	}
	id, err := uuid.Parse(*raw)
	if err != nil {
		return nil, validationError("assignee_id must be a user id")
	}
	return &id, nil
}

func (s *Server) mcpCreateForm(ctx context.Context, req *mcp.CallToolRequest, input mcpCreateFormInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	project, permissions, err := s.mcpFormProject(ctx, auth, input.mcpProjectInput)
	if err != nil {
		return nil, err
	}
	if !permissions.CanWrite {
		return nil, errMCPForbidden
	}
	issueID, err := s.formIssueID(ctx, project, input.Issue)
	if err != nil {
		return nil, mcpFormError(err)
	}
	assigneeID := auth.User.ID
	if given, err := mcpFormUserID(input.AssigneeID); err != nil {
		return nil, err
	} else if given != nil {
		assigneeID = *given
	}
	waitKey, waitKeyHash := newFormWaitKey()
	form, err := s.store.CreateForm(ctx, store.CreateFormParams{
		ProjectID:   project.ID,
		IssueID:     issueID,
		Title:       input.Title,
		Description: input.Description,
		Definition:  model.FormDefinition{Sections: input.Sections},
		AssigneeID:  assigneeID,
		CreatedByID: auth.User.ID,
		OriginLabel: input.OriginLabel,
		OriginURL:   input.OriginURL,
		WaitKeyHash: waitKeyHash,
	})
	if err != nil {
		return nil, mcpFormError(err)
	}
	created := s.mcpFormBrief(form)
	created.WaitURL = s.formWaitURL(waitKey)
	return mcpToolOutput{"form": created}, nil
}

func (s *Server) mcpGetForm(ctx context.Context, req *mcp.CallToolRequest, input mcpGetFormInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	wait, err := s.formWaitDuration(input.WaitSeconds)
	if err != nil {
		return nil, validationError("wait_seconds must be a whole number of seconds, 0 or more")
	}
	form, _, err := s.mcpForm(ctx, auth, input.mcpFormInput)
	if err != nil {
		return nil, err
	}
	if wait > 0 && form.Status == model.FormStatusOpen {
		// Each read goes back through mcpForm, so someone who stops being a
		// member while they wait is refused exactly as on a call without a wait.
		_, err := s.waitWhileFormOpen(ctx, form.ID, wait, func(ctx context.Context) (model.FormStatus, error) {
			latest, _, err := s.mcpForm(ctx, auth, input.mcpFormInput)
			if err != nil {
				return "", err
			}
			form = latest
			return form.Status, nil
		})
		if errors.Is(err, errFormWaitsFull) {
			return nil, errMCPFormWaitsFull
		}
		if err != nil {
			return nil, err
		}
	}
	return s.mcpFormOutput(form, input.IncludeDefinition), nil
}

func (s *Server) mcpListForms(ctx context.Context, req *mcp.CallToolRequest, input mcpListFormsInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	project, _, err := s.mcpFormProject(ctx, auth, input.mcpProjectInput)
	if err != nil {
		return nil, err
	}
	limit, err := mcpLimit(input.Limit)
	if err != nil {
		return nil, validationError(err.Error())
	}
	params := store.ListProjectFormsParams{ProjectID: project.ID, Limit: limit}
	if input.Cursor != "" {
		var c store.FormsCursor
		if err := decodeCursor(input.Cursor, &c); err != nil {
			return nil, validationError(err.Error())
		}
		params.Cursor = &c
	}
	if params.Statuses, err = parseFormStatusFilters(input.Statuses); err != nil {
		return nil, validationError(err.Error())
	}
	if params.AssigneeID, err = mcpFormUserID(input.AssigneeID); err != nil {
		return nil, err
	}
	if params.IssueID, err = s.formIssueID(ctx, project, input.Issue); err != nil {
		return nil, mcpFormError(err)
	}
	forms, hasMore, err := s.store.ListProjectForms(ctx, params)
	if err != nil {
		return nil, err // defensive: the project was just resolved; only a concurrent delete or DB outage fails here
	}
	return mcpPageOut(s.formSummaryResponses(forms), formsNextCursor(forms, hasMore)), nil
}

func (s *Server) mcpAcknowledgeForm(ctx context.Context, req *mcp.CallToolRequest, input mcpFormInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	form, err := s.mcpWritableForm(ctx, auth, input)
	if err != nil {
		return nil, err
	}
	acknowledged, err := s.store.AcknowledgeForm(ctx, form.ID)
	if err != nil {
		return nil, err
	}
	return s.mcpFormOutput(acknowledged, false), nil
}

func (s *Server) mcpCancelForm(ctx context.Context, req *mcp.CallToolRequest, input mcpFormInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	form, err := s.mcpWritableForm(ctx, auth, input)
	if err != nil {
		return nil, err
	}
	cancelled, err := s.store.CancelForm(ctx, form.ID)
	if err != nil {
		return nil, err
	}
	return s.mcpFormOutput(cancelled, false), nil
}

func (s *Server) mcpListInbox(ctx context.Context, req *mcp.CallToolRequest, input mcpListInboxInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	state, err := parseInboxState(input.State)
	if err != nil {
		return nil, validationError(err.Error())
	}
	limit, err := mcpLimit(input.Limit)
	if err != nil {
		return nil, validationError(err.Error())
	}
	params := store.ListInboxFormsParams{UserID: auth.User.ID, State: state, Limit: limit}
	if input.Cursor != "" {
		var c store.InboxCursor
		if err := decodeCursor(input.Cursor, &c); err != nil {
			return nil, validationError(err.Error())
		}
		params.Cursor = &c
	}
	forms, hasMore, err := s.store.ListInboxForms(ctx, params)
	if err != nil {
		return nil, err // defensive: only a DB outage fails here
	}
	return mcpPageOut(s.formSummaryResponses(forms), inboxNextCursor(state, forms, hasMore)), nil
}
