package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/store"
)

// Forms are for a project's members only. Someone who can read the project
// without being a member, such as a public viewer, is answered on every form
// route exactly as for a form that doesn't exist, so a route never says
// whether a form is there. See SECURITY_MODEL.md.

type createFormReq struct {
	Title       string              `json:"title"`
	Description string              `json:"description"`
	Issue       string              `json:"issue"`
	AssigneeID  *uuid.UUID          `json:"assignee_id"`
	OriginLabel string              `json:"origin_label"`
	OriginURL   string              `json:"origin_url"`
	Sections    []model.FormSection `json:"sections"`
	// Attachments are object refs of files uploaded with an upload URL, such
	// as object-12, that the form carries besides those its questions show.
	Attachments []string `json:"attachments"`
}

type saveFormAnswersReq struct {
	Answers model.FormAnswers `json:"answers"`
	Comment *string           `json:"comment"`
}

// formResponse is a form as the API and MCP return it: the form, its page's
// URL to hand to the person, and each question with its answer in form order.
// WaitURL is set only on the response that creates the form: it holds the
// form's wait key, which the server can't show again. Attachments are the
// files the form carries.
type formResponse struct {
	model.Form
	URL         string                   `json:"url"`
	WaitURL     string                   `json:"wait_url,omitempty"`
	Responses   []formAnswerViewResponse `json:"responses"`
	Attachments []formAttachmentResponse `json:"attachments"`
}

// formAnswerViewResponse is a question with its answer, as the API and MCP
// return it. Files describes each file the person attached to the answer; it
// replaces the plain refs the model's view carries.
type formAnswerViewResponse struct {
	model.FormAnswerView
	Files []formAttachmentResponse `json:"files"`
}

type formSummaryResponse struct {
	model.FormSummary
	URL string `json:"url"`
}

// formPath is a form's page, relative to the site.
func formPath(ownerUsername, projectKey, ref string) string {
	return fmt.Sprintf("/%s/projects/%s/forms/%s", ownerUsername, projectKey, ref)
}

// formURL is a form's page as a link someone can open: absolute when the
// server knows its public origin, and otherwise the path alone.
func (s *Server) formURL(ownerUsername, projectKey, ref string) string {
	return strings.TrimSuffix(s.publicOrigin, "/") + formPath(ownerUsername, projectKey, ref)
}

func (s *Server) formResponse(form model.Form) formResponse {
	return formResponse{
		Form:        form,
		URL:         s.formURL(form.OwnerUsername, form.ProjectKey, form.Ref),
		Responses:   s.formAnswerViewResponses(form),
		Attachments: s.formAttachmentResponses(form),
	}
}

// formAnswerViewResponses lists every question with its answer, in form order,
// each with the files attached to the answer.
func (s *Server) formAnswerViewResponses(form model.Form) []formAnswerViewResponse {
	byRef := map[string]model.StorageObject{}
	for _, attachment := range form.Attachments {
		if attachment.IsAnswerFile() {
			byRef[attachment.Object.Ref] = attachment.Object
		}
	}
	views := form.Definition.AnswerViews(form.Answers)
	out := make([]formAnswerViewResponse, len(views))
	for i, view := range views {
		files := make([]formAttachmentResponse, 0, len(view.Files))
		for _, ref := range view.Files {
			files = append(files, s.formAttachmentResponse(form, byRef[ref]))
		}
		view.Files = nil
		out[i] = formAnswerViewResponse{FormAnswerView: view, Files: files}
	}
	return out
}

func (s *Server) formSummaryResponses(forms []model.FormSummary) []formSummaryResponse {
	out := make([]formSummaryResponse, len(forms))
	for i, form := range forms {
		out[i] = formSummaryResponse{FormSummary: form, URL: s.formURL(form.OwnerUsername, form.ProjectKey, form.Ref)}
	}
	return out
}

// formErrorMessage turns an error from creating or answering a form into the
// message a caller should fix, if it is one: a problem the model found, a
// submit with questions unanswered, or an assignee or issue that doesn't
// belong to the project.
func formErrorMessage(err error) (string, bool) {
	var formErr *model.FormError
	var incomplete *model.FormIncompleteError
	var notClaimable *store.FormMediaNotClaimableError
	switch {
	case errors.As(err, &notClaimable):
		return notClaimable.Error(), true
	case errors.As(err, &formErr):
		return formErr.Error(), true
	case errors.As(err, &incomplete):
		return incomplete.Error(), true
	case errors.Is(err, store.ErrFormAssigneeNotMember):
		return "assignee_id: must be a member of the project", true
	case errors.Is(err, store.ErrFormIssueNotInProject):
		return "issue: not found in this project", true
	}
	return "", false
}

func writeFormError(w http.ResponseWriter, err error) {
	if msg, ok := formErrorMessage(err); ok {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	writeStoreError(w, err)
}

// formProjectFromRoute resolves the route's project for a member. Anyone who
// can't read the project is refused as on every project route; a reader who
// isn't a member is told nothing is there.
func (s *Server) formProjectFromRoute(w http.ResponseWriter, r *http.Request) (model.Project, store.ProjectPermissions, bool) {
	project, ok := s.projectFromRoute(w, r)
	if !ok {
		return model.Project{}, store.ProjectPermissions{}, false
	}
	permissions, ok := s.requireProjectReadPermissions(w, r, project.ID)
	if !ok {
		return model.Project{}, store.ProjectPermissions{}, false
	}
	if !permissions.CanReadMembersOnly {
		writeError(w, http.StatusNotFound, "not found")
		return model.Project{}, store.ProjectPermissions{}, false
	}
	return project, permissions, true
}

// formFromRoute resolves {formRef} for a member of its project.
func (s *Server) formFromRoute(w http.ResponseWriter, r *http.Request) (model.Form, store.ProjectPermissions, bool) {
	project, permissions, ok := s.formProjectFromRoute(w, r)
	if !ok {
		return model.Form{}, store.ProjectPermissions{}, false
	}
	number, ok := parseTypedRefParam(w, r, "formRef", "form")
	if !ok {
		return model.Form{}, store.ProjectPermissions{}, false
	}
	form, err := s.store.GetFormByProjectNumber(r.Context(), project.ID, number)
	if err != nil {
		writeStoreError(w, err)
		return model.Form{}, store.ProjectPermissions{}, false
	}
	return form, permissions, true
}

// writableFormFromRoute resolves {formRef} for a member who can write to its
// project: the people who may pick a form up or cancel it.
func (s *Server) writableFormFromRoute(w http.ResponseWriter, r *http.Request) (model.Form, bool) {
	form, permissions, ok := s.formFromRoute(w, r)
	if !ok {
		return model.Form{}, false
	}
	if !permissions.CanWrite {
		writeForbidden(w)
		return model.Form{}, false
	}
	return form, true
}

// answerableFormFromRoute resolves {formRef} for the one person who may
// answer it: its assignee, signed in themselves or using their own API token.
// A connected application is refused even when it acts for the assignee,
// because a form exists to be answered by a person.
func (s *Server) answerableFormFromRoute(w http.ResponseWriter, r *http.Request) (model.Form, bool) {
	form, _, ok := s.formFromRoute(w, r)
	if !ok {
		return model.Form{}, false
	}
	if currentAuth(r).Token.Kind == model.AuthTokenKindOAuth {
		writeError(w, http.StatusForbidden, "connected applications cannot answer forms")
		return model.Form{}, false
	}
	if form.AssigneeID != currentUser(r).ID {
		writeError(w, http.StatusForbidden, "only the form's assignee can answer it")
		return model.Form{}, false
	}
	return form, true
}

// formIssueID resolves an issue ref a form names to an issue of the project.
// An empty ref links no issue.
func (s *Server) formIssueID(ctx context.Context, project model.Project, raw string) (*uuid.UUID, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	ref, err := parseIssueRef(raw)
	if err != nil {
		return nil, &model.FormError{Field: "issue", Message: err.Error()}
	}
	if ref.ProjectKey != project.Key {
		return nil, store.ErrFormIssueNotInProject
	}
	issue, err := s.store.GetIssueByOwnerKeyNumber(ctx, project.OwnerUsername, project.Key, ref.Number)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, store.ErrFormIssueNotInProject
		}
		return nil, err // defensive: DB outage past the not-found branch
	}
	return &issue.ID, nil
}

func (s *Server) createForm(w http.ResponseWriter, r *http.Request) {
	project, permissions, ok := s.formProjectFromRoute(w, r)
	if !ok {
		return
	}
	if !permissions.CanWrite {
		writeForbidden(w)
		return
	}
	var req createFormReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	issueID, err := s.formIssueID(r.Context(), project, req.Issue)
	if err != nil {
		writeFormError(w, err)
		return
	}
	assigneeID := currentUser(r).ID
	if req.AssigneeID != nil {
		assigneeID = *req.AssigneeID
	}
	attachments, err := model.ParseFormAttachments(req.Attachments)
	if err != nil {
		writeFormError(w, err)
		return
	}
	waitKey, waitKeyHash := newFormWaitKey()
	form, err := s.store.CreateForm(r.Context(), store.CreateFormParams{
		ProjectID:   project.ID,
		IssueID:     issueID,
		Title:       req.Title,
		Description: req.Description,
		Definition:  model.FormDefinition{Sections: req.Sections},
		AssigneeID:  assigneeID,
		CreatedByID: currentUser(r).ID,
		OriginLabel: req.OriginLabel,
		OriginURL:   req.OriginURL,
		WaitKeyHash: waitKeyHash,

		AttachmentObjectNumbers: attachments,
	})
	if err != nil {
		writeFormError(w, err)
		return
	}
	created := s.formResponse(form)
	created.WaitURL = s.formWaitURL(waitKey)
	writeJSON(w, http.StatusCreated, created)
}

// parseFormStatusFilters reads repeated status values into a filter.
func parseFormStatusFilters(values []string) ([]model.FormStatus, error) {
	var out []model.FormStatus
	for _, raw := range values {
		status := model.FormStatus(strings.ToLower(strings.TrimSpace(raw)))
		if !status.Valid() {
			return nil, errors.New("status must be open, submitted, acknowledged or cancelled")
		}
		out = append(out, status)
	}
	return out, nil
}

func (s *Server) listForms(w http.ResponseWriter, r *http.Request) {
	project, _, ok := s.formProjectFromRoute(w, r)
	if !ok {
		return
	}
	query := r.URL.Query()
	limit, err := parseLimit(query.Get("limit"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	params := store.ListProjectFormsParams{ProjectID: project.ID, Limit: limit}
	if raw := query.Get("cursor"); raw != "" {
		var c store.FormsCursor
		if err := decodeCursor(raw, &c); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		params.Cursor = &c
	}
	if params.Statuses, err = parseFormStatusFilters(query["status"]); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if raw := query.Get("assignee_id"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "assignee_id must be a user id")
			return
		}
		params.AssigneeID = &id
	}
	if params.IssueID, err = s.formIssueID(r.Context(), project, query.Get("issue")); err != nil {
		writeFormError(w, err)
		return
	}
	forms, hasMore, err := s.store.ListProjectForms(r.Context(), params)
	if err != nil {
		writeStoreError(w, err) // defensive: the project was just resolved; only a concurrent delete or DB outage fails here
		return
	}
	writePage(w, s.formSummaryResponses(forms), formsNextCursor(forms, hasMore))
}

func formsNextCursor(forms []model.FormSummary, hasMore bool) *string {
	if !hasMore {
		return nil
	}
	next := encodeCursor(store.FormsCursor{Number: forms[len(forms)-1].Number})
	return &next
}

func (s *Server) getForm(w http.ResponseWriter, r *http.Request) {
	form, _, ok := s.formFromRoute(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, s.formResponse(form))
}

func (s *Server) saveFormAnswers(w http.ResponseWriter, r *http.Request) {
	form, ok := s.answerableFormFromRoute(w, r)
	if !ok {
		return
	}
	var req saveFormAnswersReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	saved, err := s.store.SaveFormAnswers(r.Context(), store.SaveFormAnswersParams{
		FormID:     form.ID,
		AssigneeID: currentUser(r).ID,
		Answers:    req.Answers,
		Comment:    req.Comment,
	})
	if err != nil {
		writeFormError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.formResponse(saved))
}

func (s *Server) submitForm(w http.ResponseWriter, r *http.Request) {
	form, ok := s.answerableFormFromRoute(w, r)
	if !ok {
		return
	}
	submitted, err := s.store.SubmitForm(r.Context(), store.SubmitFormParams{
		FormID:     form.ID,
		AssigneeID: currentUser(r).ID,
		Via:        model.FormSubmitAPI,
	})
	if err != nil {
		writeFormError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.formResponse(submitted))
}

func (s *Server) reopenForm(w http.ResponseWriter, r *http.Request) {
	form, ok := s.answerableFormFromRoute(w, r)
	if !ok {
		return
	}
	reopened, err := s.store.ReopenForm(r.Context(), form.ID, currentUser(r).ID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.formResponse(reopened))
}

func (s *Server) acknowledgeForm(w http.ResponseWriter, r *http.Request) {
	form, ok := s.writableFormFromRoute(w, r)
	if !ok {
		return
	}
	acknowledged, err := s.store.AcknowledgeForm(r.Context(), form.ID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.formResponse(acknowledged))
}

func (s *Server) cancelForm(w http.ResponseWriter, r *http.Request) {
	form, ok := s.writableFormFromRoute(w, r)
	if !ok {
		return
	}
	cancelled, err := s.store.CancelForm(r.Context(), form.ID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.formResponse(cancelled))
}
