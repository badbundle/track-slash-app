package server

import (
	"context"
	"errors"
	"html/template"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/store"
)

const uiWhiteboardBodyPlaceholder = "Jot down notes, ideas, and links. Markdown is supported."

// uiWhiteboardData backs the project Whiteboard tab: a compact list of page
// titles, most recently updated first, and the one page being read, created,
// or edited.
type uiWhiteboardData struct {
	CSRFToken  string
	Project    model.Project
	CanWrite   bool
	Action     string
	Items      []model.WhiteboardPageSummary
	HasMore    bool
	HasActive  bool
	Active     model.WhiteboardPage
	ActiveHTML template.HTML
	TitleInput string
	BodyInput  string
	Error      string
}

func (s *Server) uiViewWhiteboardPage(w http.ResponseWriter, r *http.Request) {
	project, ok := s.uiProjectFromRoute(w, r)
	if !ok {
		return
	}
	if err := s.uiRequireProjectAccess(r.Context(), currentUser(r), project.ID); err != nil {
		writeUIStoreError(w, err)
		return
	}
	if _, ok := s.uiWhiteboardPageFromRoute(w, r, project); !ok {
		return
	}
	s.renderUIProjectWhiteboard(w, r, project, nil)
}

func (s *Server) uiNewWhiteboardPage(w http.ResponseWriter, r *http.Request) {
	project, ok := s.uiProjectFromRoute(w, r)
	if !ok {
		return // defensive: uiProjectWriteHandler already resolved this project
	}
	s.renderUIProjectWhiteboard(w, r, project, func(panel *uiWhiteboardData) {
		panel.Action = "create"
	})
}

func (s *Server) uiCreateWhiteboardPage(w http.ResponseWriter, r *http.Request) {
	project, ok := s.uiProjectFromRoute(w, r)
	if !ok {
		return // defensive: uiProjectWriteHandler already resolved this project
	}
	titleInput, bodyInput := r.PostFormValue("title"), r.PostFormValue("body")
	title, body, message := uiValidateWhiteboardPageForm(titleInput, bodyInput)
	if message != "" {
		s.renderUIProjectWhiteboard(w, r, project, func(panel *uiWhiteboardData) {
			panel.Action = "create"
			panel.TitleInput = titleInput
			panel.BodyInput = bodyInput
			panel.Error = message
		})
		return
	}
	created, err := s.store.CreateWhiteboardPage(r.Context(), store.CreateWhiteboardPageParams{
		ProjectID:   project.ID,
		Title:       title,
		Body:        body,
		CreatedByID: currentUser(r).ID,
	})
	if err != nil {
		writeUIStoreError(w, err) // defensive: the project or page was just resolved; only a concurrent delete or DB outage fails here
		return
	}
	pagePath := uiProjectWhiteboardPagePath(project, created.Ref)
	uiSetHXReplaceURL(w, r, pagePath)
	// Render the response as the new page's own URL would, which is where the
	// browser's address bar now points.
	chi.RouteContext(r.Context()).URLParams.Add("pageRef", created.Ref)
	s.renderUIProjectWhiteboard(w, r, project, nil)
}

func (s *Server) uiEditWhiteboardPage(w http.ResponseWriter, r *http.Request) {
	project, ok := s.uiProjectFromRoute(w, r)
	if !ok {
		return // defensive: uiProjectWriteHandler already resolved this project
	}
	page, ok := s.uiWhiteboardPageFromRoute(w, r, project)
	if !ok {
		return
	}
	s.renderUIProjectWhiteboard(w, r, project, func(panel *uiWhiteboardData) {
		panel.Action = "edit"
		panel.TitleInput = page.Title
		panel.BodyInput = page.Body
	})
}

func (s *Server) uiUpdateWhiteboardPage(w http.ResponseWriter, r *http.Request) {
	project, ok := s.uiProjectFromRoute(w, r)
	if !ok {
		return // defensive: uiProjectWriteHandler already resolved this project
	}
	page, ok := s.uiWhiteboardPageFromRoute(w, r, project)
	if !ok {
		return
	}
	titleInput, bodyInput := r.PostFormValue("title"), r.PostFormValue("body")
	title, body, message := uiValidateWhiteboardPageForm(titleInput, bodyInput)
	if message != "" {
		s.renderUIProjectWhiteboard(w, r, project, func(panel *uiWhiteboardData) {
			panel.Action = "edit"
			panel.TitleInput = titleInput
			panel.BodyInput = bodyInput
			panel.Error = message
		})
		return
	}
	if _, err := s.store.UpdateWhiteboardPage(r.Context(), store.UpdateWhiteboardPageParams{
		ID:          page.ID,
		Title:       &title,
		Body:        &body,
		UpdatedByID: currentUser(r).ID,
	}); err != nil {
		writeUIStoreError(w, err) // defensive: the project or page was just resolved; only a concurrent delete or DB outage fails here
		return
	}
	uiSetHXReplaceURL(w, r, uiProjectWhiteboardPagePath(project, page.Ref))
	s.renderUIProjectWhiteboard(w, r, project, nil)
}

func (s *Server) uiDeleteWhiteboardPage(w http.ResponseWriter, r *http.Request) {
	project, ok := s.uiProjectFromRoute(w, r)
	if !ok {
		return // defensive: uiProjectWriteHandler already resolved this project
	}
	page, ok := s.uiWhiteboardPageFromRoute(w, r, project)
	if !ok {
		return
	}
	if err := s.store.DeleteWhiteboardPage(r.Context(), page.ID); err != nil {
		writeUIStoreError(w, err) // defensive: the project or page was just resolved; only a concurrent delete or DB outage fails here
		return
	}
	uiSetHXReplaceURL(w, r, uiProjectWhiteboardPath(project))
	s.renderUIProjectWhiteboard(w, r, project, nil)
}

// renderUIProjectWhiteboard builds the full project panel, so the header keeps
// every action the reader is allowed, then lets the caller set the whiteboard
// state before rendering the fragment or the whole page.
func (s *Server) renderUIProjectWhiteboard(w http.ResponseWriter, r *http.Request, project model.Project, mutate func(*uiWhiteboardData)) {
	panel, err := s.uiBuildProjectPanel(r.Context(), r, project.ID, "whiteboard")
	if err != nil {
		writeUIStoreError(w, err) // defensive: every caller checked access and resolved the project first
		return
	}
	if mutate != nil {
		mutate(panel.Whiteboard)
	}
	if isHTMXRequest(r) {
		renderUITemplate(w, http.StatusOK, "project-panel", panel)
		return
	}
	projects, err := s.uiVisibleProjects(r.Context(), currentUser(r))
	if err != nil {
		writeUIInternalError(w, "ui whiteboard visible projects", err) // defensive: the panel build above already reached the DB
		return
	}
	s.renderUIShell(w, r, http.StatusOK, uiShellData{
		User:          currentUser(r),
		Projects:      projects,
		SidebarActive: uiSidebarState{View: "project", ProjectID: project.ID},
		ProjectPanel:  panel,
	})
}

// uiBuildWhiteboard lists the project's pages and selects the page named by
// the route, or the most recently updated page when the route names none. A
// named page that no longer exists — because this request just deleted it —
// also falls back to the most recent page.
func (s *Server) uiBuildWhiteboard(ctx context.Context, r *http.Request, project model.Project, canWrite bool) (*uiWhiteboardData, error) {
	pages, hasMore, err := s.store.ListWhiteboardPages(ctx, store.ListWhiteboardPagesParams{
		ProjectID: project.ID,
		Limit:     MaxLimit,
	})
	if err != nil {
		return nil, err // defensive: uiBuildProjectPanel resolved the live project first
	}
	panel := &uiWhiteboardData{
		CSRFToken: uiSessionCSRFToken(r),
		Project:   project,
		CanWrite:  canWrite,
		Action:    "view",
		Items:     pages,
		HasMore:   hasMore,
	}
	if number, refErr := parseTypedRef(chi.URLParam(r, "pageRef"), "whiteboard"); refErr == nil {
		err := s.uiSelectWhiteboardPage(ctx, panel, number)
		if err == nil {
			return panel, nil
		}
		if !errors.Is(err, store.ErrNotFound) {
			return nil, err // defensive: DB outage past the no-rows branch
		}
	}
	if len(pages) > 0 {
		if err := s.uiSelectWhiteboardPage(ctx, panel, pages[0].Number); err != nil {
			return nil, err // defensive: the page was listed a moment ago
		}
	}
	return panel, nil
}

func (s *Server) uiSelectWhiteboardPage(ctx context.Context, panel *uiWhiteboardData, number int) error {
	page, err := s.store.GetWhiteboardPageByProjectNumber(ctx, panel.Project.ID, number)
	if err != nil {
		return err
	}
	panel.HasActive = true
	panel.Active = page
	panel.ActiveHTML = renderWhiteboardMarkdown(page)
	return nil
}

func (s *Server) uiWhiteboardPageFromRoute(w http.ResponseWriter, r *http.Request, project model.Project) (model.WhiteboardPage, bool) {
	number, err := parseTypedRef(chi.URLParam(r, "pageRef"), "whiteboard")
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return model.WhiteboardPage{}, false
	}
	page, err := s.store.GetWhiteboardPageByProjectNumber(r.Context(), project.ID, number)
	if err != nil {
		writeUIStoreError(w, err)
		return model.WhiteboardPage{}, false
	}
	return page, true
}

// uiValidateWhiteboardPageForm applies the API's title and body rules and
// returns the message to show beside the form when they fail.
func uiValidateWhiteboardPageForm(titleInput, bodyInput string) (string, string, string) {
	title, err := validateProjectContextTitle(titleInput)
	if err != nil {
		return "", "", err.Error()
	}
	body, err := validateProjectContextBody(bodyInput)
	if err != nil {
		return "", "", err.Error()
	}
	return title, body, ""
}

func uiWhiteboardEditor(panel *uiWhiteboardData) uiDescriptionEditorData {
	return uiDescriptionEditorData{
		Name:        "body",
		Source:      panel.BodyInput,
		Rows:        12,
		Placeholder: uiWhiteboardBodyPlaceholder,
	}
}

func uiWhiteboardBody(panel *uiWhiteboardData) uiDescriptionBodyData {
	return uiDescriptionBodyData{Source: panel.Active.Body, HTML: panel.ActiveHTML, EmptyLabel: "No content yet."}
}

func uiProjectWhiteboardPath(project model.Project) string {
	return uiProjectPath(project) + "/whiteboard"
}

func uiProjectWhiteboardNewPath(project model.Project) string {
	return uiProjectWhiteboardPath(project) + "/new"
}

func uiProjectWhiteboardPagePath(project model.Project, ref string) string {
	return uiProjectWhiteboardPath(project) + "/" + ref
}

func uiProjectWhiteboardPagePanelPath(project model.Project, ref string) string {
	return uiProjectWhiteboardPagePath(project, ref) + "/panel"
}

func uiProjectWhiteboardPageEditPath(project model.Project, ref string) string {
	return uiProjectWhiteboardPagePath(project, ref) + "/edit"
}

func uiProjectWhiteboardPageDeletePath(project model.Project, ref string) string {
	return uiProjectWhiteboardPagePath(project, ref) + "/delete"
}
