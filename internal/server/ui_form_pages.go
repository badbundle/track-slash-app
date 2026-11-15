package server

import (
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/store"
)

// The form page is where a person answers the questions an agent asked them.
// Every route is for the project's members: anyone else is answered exactly as
// for a form that doesn't exist, decided before the form is looked up. Only
// the assignee answers, submits or reopens; a writer may cancel. See
// SECURITY_MODEL.md.

// uiFormPanelData backs the form page. The panel shows every section on one
// page; each section renders through the form-section template so a stepped
// view can reuse it.
type uiFormPanelData struct {
	CSRFToken       string
	Project         model.Project
	Form            model.Form
	Base            string
	Breadcrumb      uiBreadcrumbData
	DescriptionHTML template.HTML
	IssueHref       string
	IssuePanelHref  string
	Origin          uiFormOriginData
	AssigneeName    string
	IsAssignee      bool
	CanCancel       bool
	// Notice is the message at the top of the page after a refused Done.
	Notice string
	// Open is set while the assignee is answering: they see the questions as
	// controls, and everyone else sees the answers so far.
	Open     bool
	Sections []uiFormSectionData
	Summary  []uiFormSummaryItem
	Footer   uiFormFooterData
	Comment  uiFormCommentData
	CopyText string
}

type uiFormOriginData struct {
	Label string
	Href  string
	Host  string
}

type uiFormSectionData struct {
	Title           string
	DescriptionHTML template.HTML
	Questions       []uiFormQuestionData
}

type uiFormQuestionData struct {
	CSRFToken  string
	Action     string
	Question   model.FormQuestion
	Answer     model.FormAnswer
	HelpHTML   template.HTML
	Options    []uiFormOptionData
	Unanswered bool
	// Suggested is the muted "Suggested: ..." line for confirm and text
	// questions, whose suggestion is shown as the starting answer.
	Suggested string
	Type      string
}

type uiFormOptionData struct {
	QuestionID  string
	ID          string
	Label       string
	Description string
	Input       string
	Checked     bool
	Suggested   bool
	IsOther     bool
	OtherText   string
}

type uiFormSummaryItem struct {
	Label    string
	Answer   string
	Answered bool
	Note     string
	Changed  bool
}

type uiFormProgressData struct {
	Answered int
	Required int
	// Saved shows the "Saved" tick after an autosave.
	Saved bool
}

// Text is the progress line: how many required questions have an answer.
func (p uiFormProgressData) Text() string {
	if p.Required == 0 {
		return "No required questions"
	}
	return fmt.Sprintf("%d of %d answered", p.Answered, p.Required)
}

// uiFormFooterData is the footer that holds progress and Done. OOB marks the
// copy an autosave sends back, which replaces the footer on the page.
type uiFormFooterData struct {
	CSRFToken string
	Base      string
	OOB       bool
	Progress  uiFormProgressData
	CanSubmit bool
}

type uiFormCommentData struct {
	CSRFToken string
	Base      string
	Comment   string
}

// uiFormSaveData backs the fragment an autosave answers with: the footer and
// the error slot of whatever was just saved.
type uiFormSaveData struct {
	Footer  uiFormFooterData
	SlotID  string
	Message string
}

const uiFormMaxFormBytes = 1 << 20

func (s *Server) uiViewForm(w http.ResponseWriter, r *http.Request) {
	project, perms, form, ok := s.uiFormFromRoute(w, r)
	if !ok {
		return
	}
	s.renderUIForm(w, r, project, perms, form, nil)
}

func (s *Server) uiViewFormPanel(w http.ResponseWriter, r *http.Request) {
	project, perms, form, ok := s.uiFormFromRoute(w, r)
	if !ok {
		return
	}
	panel, err := s.uiBuildFormPanel(r, project, perms, form)
	if err != nil {
		writeUIStoreError(w, err) // defensive: the assignee was a member a moment ago
		return
	}
	renderUITemplate(w, http.StatusOK, "form-panel", panel)
}

func (s *Server) uiSaveFormAnswer(w http.ResponseWriter, r *http.Request) {
	project, _, form, ok := s.uiAssigneeFormFromRoute(w, r)
	if !ok {
		return
	}
	question, ok := form.Definition.Question(chi.URLParam(r, "questionID"))
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, uiFormMaxFormBytes)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	saved, err := s.store.SaveFormAnswers(r.Context(), store.SaveFormAnswersParams{
		FormID:     form.ID,
		AssigneeID: currentUser(r).ID,
		Answers:    model.FormAnswers{question.ID: uiFormAnswerFromValues(question, r.PostForm)},
	})
	slotID := "form-question-error-" + question.ID
	s.finishFormSave(w, r, project, form, saved, err, slotID)
}

func (s *Server) uiSaveFormComment(w http.ResponseWriter, r *http.Request) {
	project, _, form, ok := s.uiAssigneeFormFromRoute(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, uiFormMaxFormBytes)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	comment := r.PostForm.Get("comment")
	saved, err := s.store.SaveFormAnswers(r.Context(), store.SaveFormAnswersParams{
		FormID:     form.ID,
		AssigneeID: currentUser(r).ID,
		Comment:    &comment,
	})
	s.finishFormSave(w, r, project, form, saved, err, "form-comment-error")
}

// finishFormSave answers an autosave. The request used hx-swap="none", so the
// answer is out-of-band fragments only: the footer, and the error slot of the
// field that was saved. A refused answer is still a 200 so htmx applies the
// swap. A form that moved on elsewhere reloads the page into its new state.
func (s *Server) finishFormSave(w http.ResponseWriter, r *http.Request, project model.Project, before, saved model.Form, err error, slotID string) {
	var formErr *model.FormError
	if err != nil {
		switch {
		case errors.As(err, &formErr):
			saved = before
		case errors.Is(err, store.ErrConflict):
			if !isHTMXRequest(r) {
				http.Redirect(w, r, formPath(project.OwnerUsername, project.Key, before.Ref), http.StatusSeeOther)
				return
			}
			w.Header().Set("HX-Refresh", "true")
			w.WriteHeader(http.StatusOK)
			return
		default:
			writeUIStoreError(w, err) // defensive: the form was just read; only an outage fails here
			return
		}
	}
	if !isHTMXRequest(r) {
		http.Redirect(w, r, formPath(project.OwnerUsername, project.Key, before.Ref), http.StatusSeeOther)
		return
	}
	data := uiFormSaveData{Footer: s.uiFormFooter(r, saved, true), SlotID: slotID}
	if formErr != nil {
		data.Message = formErr.Message
	}
	renderUITemplate(w, http.StatusOK, "form-save-response", data)
}

func (s *Server) uiSubmitForm(w http.ResponseWriter, r *http.Request) {
	project, perms, form, ok := s.uiAssigneeFormFromRoute(w, r)
	if !ok {
		return
	}
	submitted, err := s.store.SubmitForm(r.Context(), store.SubmitFormParams{
		FormID:     form.ID,
		AssigneeID: currentUser(r).ID,
		Via:        model.FormSubmitWeb,
	})
	var incomplete *model.FormIncompleteError
	switch {
	case err == nil:
		s.finishFormChange(w, r, project, perms, submitted)
	case errors.As(err, &incomplete):
		s.renderUIForm(w, r, project, perms, form, func(panel *uiFormPanelData) {
			panel.Notice = uiFormIncompleteNotice(len(incomplete.QuestionIDs))
			for i := range panel.Sections {
				for j := range panel.Sections[i].Questions {
					question := &panel.Sections[i].Questions[j]
					question.Unanswered = slices.Contains(incomplete.QuestionIDs, question.Question.ID)
				}
			}
		})
	default:
		s.renderFormAfterError(w, r, project, perms, form, err)
	}
}

func (s *Server) uiReopenForm(w http.ResponseWriter, r *http.Request) {
	project, perms, form, ok := s.uiAssigneeFormFromRoute(w, r)
	if !ok {
		return
	}
	reopened, err := s.store.ReopenForm(r.Context(), form.ID, currentUser(r).ID)
	if err != nil {
		s.renderFormAfterError(w, r, project, perms, form, err)
		return
	}
	s.finishFormChange(w, r, project, perms, reopened)
}

func (s *Server) uiCancelForm(w http.ResponseWriter, r *http.Request) {
	project, perms, form, ok := s.uiFormFromRoute(w, r)
	if !ok {
		return
	}
	if !perms.CanWrite {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	cancelled, err := s.store.CancelForm(r.Context(), form.ID)
	if err != nil {
		s.renderFormAfterError(w, r, project, perms, form, err)
		return
	}
	s.finishFormChange(w, r, project, perms, cancelled)
}

// finishFormChange answers a successful submit, reopen or cancel: htmx gets
// the form's new state, and a plain post goes back to the form's page.
func (s *Server) finishFormChange(w http.ResponseWriter, r *http.Request, project model.Project, perms store.ProjectPermissions, form model.Form) {
	if !isHTMXRequest(r) {
		http.Redirect(w, r, formPath(project.OwnerUsername, project.Key, form.Ref), http.StatusSeeOther)
		return
	}
	s.renderUIForm(w, r, project, perms, form, nil)
}

// renderFormAfterError answers a transition the store refused. A conflict
// means the form changed elsewhere, so the page is shown in its current state.
func (s *Server) renderFormAfterError(w http.ResponseWriter, r *http.Request, project model.Project, perms store.ProjectPermissions, form model.Form, err error) {
	if !errors.Is(err, store.ErrConflict) {
		writeUIStoreError(w, err) // defensive: the form was just read; only an outage fails here
		return
	}
	current, getErr := s.store.GetForm(r.Context(), form.ID)
	if getErr != nil {
		writeUIStoreError(w, getErr) // defensive: the form was read a moment ago
		return
	}
	s.renderUIForm(w, r, project, perms, current, nil)
}

// renderUIForm renders the form page: the whole document for a navigation and
// the panel alone for htmx. mutate lets a caller add a notice or marks.
func (s *Server) renderUIForm(w http.ResponseWriter, r *http.Request, project model.Project, perms store.ProjectPermissions, form model.Form, mutate func(*uiFormPanelData)) {
	panel, err := s.uiBuildFormPanel(r, project, perms, form)
	if err != nil {
		writeUIStoreError(w, err) // defensive: the assignee was a member a moment ago
		return
	}
	if mutate != nil {
		mutate(panel)
	}
	s.renderUIShell(w, r, http.StatusOK, uiShellData{
		User:          currentUser(r),
		SidebarActive: uiSidebarState{View: "project", ProjectID: project.ID},
		FormPanel:     panel,
	})
}

// uiFormFromRoute resolves the route's form for a member of its project. A
// reader who isn't a member, an unknown form and a malformed ref all get the
// same not-found page, and the membership check comes first.
func (s *Server) uiFormFromRoute(w http.ResponseWriter, r *http.Request) (model.Project, store.ProjectPermissions, model.Form, bool) {
	project, ok := s.uiProjectFromRoute(w, r)
	if !ok {
		return model.Project{}, store.ProjectPermissions{}, model.Form{}, false
	}
	perms, err := s.store.ProjectPermissionsForUser(r.Context(), currentUser(r), project.ID)
	if err != nil {
		writeUIStoreError(w, err) // defensive: the project was just resolved
		return model.Project{}, store.ProjectPermissions{}, model.Form{}, false
	}
	if !perms.CanReadMembersOnly {
		s.uiNotFound(w, r)
		return model.Project{}, store.ProjectPermissions{}, model.Form{}, false
	}
	number, err := parseTypedRef(chi.URLParam(r, "formRef"), "form")
	if err != nil {
		s.uiNotFound(w, r)
		return model.Project{}, store.ProjectPermissions{}, model.Form{}, false
	}
	form, err := s.store.GetFormByProjectNumber(r.Context(), project.ID, number)
	if errors.Is(err, store.ErrNotFound) {
		s.uiNotFound(w, r)
		return model.Project{}, store.ProjectPermissions{}, model.Form{}, false
	}
	if err != nil {
		writeUIStoreError(w, err) // defensive: DB outage past the no-rows branch
		return model.Project{}, store.ProjectPermissions{}, model.Form{}, false
	}
	return project, perms, form, true
}

// uiAssigneeFormFromRoute is uiFormFromRoute for the actions only the form's
// assignee takes. Anyone else who can see the form is refused with a 403.
func (s *Server) uiAssigneeFormFromRoute(w http.ResponseWriter, r *http.Request) (model.Project, store.ProjectPermissions, model.Form, bool) {
	project, perms, form, ok := s.uiFormFromRoute(w, r)
	if !ok {
		return model.Project{}, store.ProjectPermissions{}, model.Form{}, false
	}
	if form.AssigneeID != currentUser(r).ID {
		http.Error(w, "forbidden", http.StatusForbidden)
		return model.Project{}, store.ProjectPermissions{}, model.Form{}, false
	}
	return project, perms, form, true
}

func (s *Server) uiBuildFormPanel(r *http.Request, project model.Project, perms store.ProjectPermissions, form model.Form) (*uiFormPanelData, error) {
	assignee, err := s.store.GetUser(r.Context(), form.AssigneeID)
	if err != nil {
		return nil, err
	}
	token := uiSessionCSRFToken(r)
	isAssignee := form.AssigneeID == currentUser(r).ID
	base := formPath(project.OwnerUsername, project.Key, form.Ref)
	breadcrumb := uiBreadcrumbData{Items: append(uiProjectBreadcrumbItems(project),
		uiBreadcrumbItem{Label: "Forms", Plain: true},
		uiBreadcrumbItem{Label: form.Ref, Current: true},
	)}
	panel := &uiFormPanelData{
		CSRFToken:       token,
		Project:         project,
		Form:            form,
		Base:            base,
		Breadcrumb:      breadcrumb,
		DescriptionHTML: renderMarkdown(form.Description, nil),
		Origin:          uiFormOrigin(form.OriginLabel, form.OriginURL),
		AssigneeName:    uiUserLabel(assignee.Name, assignee.Username, assignee.Email),
		IsAssignee:      isAssignee,
		CanCancel:       perms.CanWrite && (form.Status == model.FormStatusOpen || form.Status == model.FormStatusSubmitted),
		Open:            form.Status == model.FormStatusOpen && isAssignee,
		Summary:         uiFormSummary(form),
		Footer:          s.uiFormFooter(r, form, false),
		Comment:         uiFormCommentData{CSRFToken: token, Base: base, Comment: form.Comment},
		CopyText:        uiFormCopyText(form, s.formURL(project.OwnerUsername, project.Key, form.Ref)),
	}
	if form.IssueIdentifier != nil {
		panel.IssueHref = "/" + form.OwnerUsername + "/issues/" + *form.IssueIdentifier
		panel.IssuePanelHref = panel.IssueHref + "/panel"
	}
	if panel.Open {
		for _, section := range form.Definition.Sections {
			data := uiFormSectionData{Title: section.Title, DescriptionHTML: renderMarkdown(section.Description, nil)}
			for _, question := range section.Questions {
				data.Questions = append(data.Questions, uiFormQuestion(token, base, question, form.Answers[question.ID]))
			}
			panel.Sections = append(panel.Sections, data)
		}
	}
	return panel, nil
}

func (s *Server) uiFormFooter(r *http.Request, form model.Form, oob bool) uiFormFooterData {
	answered, required := form.Definition.Progress(form.Answers)
	return uiFormFooterData{
		CSRFToken: uiSessionCSRFToken(r),
		Base:      formPath(form.OwnerUsername, form.ProjectKey, form.Ref),
		OOB:       oob,
		Progress:  uiFormProgressData{Answered: answered, Required: required, Saved: oob},
		CanSubmit: len(form.Definition.Unanswered(form.Answers)) == 0,
	}
}

func uiFormIncompleteNotice(count int) string {
	if count == 1 {
		return "1 question still needs an answer."
	}
	return fmt.Sprintf("%d questions still need an answer.", count)
}

// uiFormQuestion prepares one question card from the question and the answer
// saved so far.
func uiFormQuestion(token, base string, question model.FormQuestion, answer model.FormAnswer) uiFormQuestionData {
	data := uiFormQuestionData{
		CSRFToken: token,
		Action:    base + "/answers/" + question.ID,
		Question:  question,
		Answer:    answer,
		HelpHTML:  renderMarkdown(question.Help, nil),
		Type:      string(question.Type),
	}
	switch question.Type {
	case model.FormQuestionConfirm:
		if question.Default != nil && question.Default.Checked {
			data.Suggested = "Yes"
		}
	case model.FormQuestionText:
		if question.Default != nil && question.Default.Text != "" {
			data.Suggested = question.Default.Text
		}
	default:
		input := "radio"
		if question.Type == model.FormQuestionMultiChoice {
			input = "checkbox"
		}
		suggested := func(id string) bool {
			return question.Default != nil && slices.Contains(question.Default.Selected, id)
		}
		for _, option := range question.Options {
			data.Options = append(data.Options, uiFormOptionData{
				QuestionID:  question.ID,
				ID:          option.ID,
				Label:       option.Label,
				Description: option.Description,
				Input:       input,
				Checked:     slices.Contains(answer.Selected, option.ID),
				Suggested:   suggested(option.ID),
			})
		}
		if question.AllowOther {
			data.Options = append(data.Options, uiFormOptionData{
				QuestionID: question.ID,
				ID:         model.FormOtherOptionID,
				Label:      "Other",
				Input:      input,
				Checked:    slices.Contains(answer.Selected, model.FormOtherOptionID),
				Suggested:  suggested(model.FormOtherOptionID),
				IsOther:    true,
				OtherText:  answer.Other,
			})
		}
	}
	return data
}

// uiFormAnswerFromValues reads one question card's fields into an answer. A
// choice question keeps the Other text only while Other is chosen, because the
// hidden text box may still hold text from before.
func uiFormAnswerFromValues(question model.FormQuestion, values url.Values) model.FormAnswer {
	answer := model.FormAnswer{Note: values.Get("note")}
	switch question.Type {
	case model.FormQuestionConfirm:
		answer.Checked = values.Get("checked") == "true"
	case model.FormQuestionText:
		answer.Text = values.Get("text")
	default:
		answer.Selected = values["selected"]
		if slices.Contains(answer.Selected, model.FormOtherOptionID) {
			answer.Other = values.Get("other")
		}
	}
	return answer
}

// uiFormOrigin prepares the origin line. The link is kept only when it is an
// https URL with a host, whatever the store accepted.
func uiFormOrigin(label, rawURL string) uiFormOriginData {
	href, host := uiFormOriginHref(rawURL)
	return uiFormOriginData{Label: label, Href: href, Host: host}
}

// uiFormOriginHref returns the URL and its host when raw is an https URL with
// a host, and empty strings otherwise.
func uiFormOriginHref(raw string) (string, string) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.Hostname() == "" {
		return "", ""
	}
	return u.String(), u.Hostname()
}

func uiFormSummary(form model.Form) []uiFormSummaryItem {
	views := form.Definition.AnswerViews(form.Answers)
	items := make([]uiFormSummaryItem, 0, len(views))
	for _, view := range views {
		items = append(items, uiFormSummaryItem{
			Label:    view.Label,
			Answer:   uiFormAnswerText(view),
			Answered: view.Answered,
			Note:     view.Note,
			Changed:  view.ChangedFromDefault,
		})
	}
	return items
}

// uiFormAnswerText is an answer as a person reads it: the chosen options'
// labels, "Other: text", Yes or No, or the text written.
func uiFormAnswerText(view model.FormAnswerView) string {
	if !view.Answered {
		return "No answer"
	}
	switch view.Type {
	case model.FormQuestionConfirm:
		if view.Checked != nil && *view.Checked {
			return "Yes"
		}
		return "No"
	case model.FormQuestionText:
		return view.Text
	}
	var parts []string
	for _, option := range view.Selected {
		if option.ID == model.FormOtherOptionID {
			parts = append(parts, "Other: "+view.Other)
			continue
		}
		parts = append(parts, option.Label)
	}
	return strings.Join(parts, ", ")
}

// uiFormCopyText is the plain-text summary behind Copy answers: the form, its
// link, then each question with its answer, note and the closing comment.
func uiFormCopyText(form model.Form, formURL string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s · %s\n%s\n", form.Ref, form.Title, formURL)
	b.WriteString("\n")
	for i, view := range form.Definition.AnswerViews(form.Answers) {
		fmt.Fprintf(&b, "%d. %s: %s\n", i+1, view.Label, uiFormAnswerText(view))
		if view.Note != "" {
			fmt.Fprintf(&b, "   Note: %s\n", view.Note)
		}
	}
	if form.Comment != "" {
		fmt.Fprintf(&b, "\nAnything else: %s\n", form.Comment)
	}
	return strings.TrimRight(b.String(), "\n")
}
