package server

import (
	"errors"
	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/store"
	"github.com/go-chi/chi/v5"
	"net/http"
	"net/url"
	"strings"
)

func (s *Server) uiHome(w http.ResponseWriter, r *http.Request) {
	projects, err := s.uiVisibleProjects(r.Context(), currentUser(r))
	if err != nil {
		writeUIInternalError(w, "ui home visible projects", err)
		return
	}
	if len(projects) == 0 {
		http.Redirect(w, r, "/projects", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, uiProjectHomePath(projects[0]), http.StatusSeeOther)
}

func (s *Server) uiWorkPage(w http.ResponseWriter, r *http.Request, view string) {
	panel, err := s.uiBuildWorkPanel(r.Context(), r, currentUser(r), view)
	if err != nil {
		writeUIStoreError(w, err)
		return
	}
	projects, err := s.uiVisibleProjects(r.Context(), currentUser(r))
	if err != nil {
		writeUIInternalError(w, "ui work visible projects", err)
		return
	}
	s.renderUIShell(w, r, http.StatusOK, uiShellData{
		User:          currentUser(r),
		Projects:      projects,
		SidebarActive: uiSidebarState{View: "me"},
		WorkPanel:     panel,
	})
}

func (s *Server) uiWorkPanel(w http.ResponseWriter, r *http.Request, view string) {
	panel, err := s.uiBuildWorkPanel(r.Context(), r, currentUser(r), view)
	if err != nil {
		writeUIStoreError(w, err)
		return
	}
	renderUITemplate(w, http.StatusOK, "work-panel", panel)
}

func (s *Server) uiProjectsPage(w http.ResponseWriter, r *http.Request) {
	s.renderUIProjects(w, r, http.StatusOK)
}

func (s *Server) uiProjectsPanel(w http.ResponseWriter, r *http.Request) {
	panel, err := s.uiBuildProjectsPanel(r.Context(), currentUser(r))
	if err != nil {
		writeUIInternalError(w, "ui projects panel", err)
		return
	}
	renderUITemplate(w, http.StatusOK, "projects-panel", panel)
}

func (s *Server) uiOwnerProjectsPage(w http.ResponseWriter, r *http.Request) {
	s.renderUIOwnerProjects(w, r, http.StatusOK)
}

func (s *Server) uiOwnerProjectsPanel(w http.ResponseWriter, r *http.Request) {
	panel, err := s.uiBuildOwnerProjectsPanel(r.Context(), currentUser(r), chi.URLParam(r, "owner"))
	if err != nil {
		writeUIStoreError(w, err)
		return
	}
	renderUITemplate(w, http.StatusOK, "projects-panel", panel)
}

func (s *Server) uiNewProjectPage(w http.ResponseWriter, r *http.Request) {
	s.renderUINewProject(w, r, http.StatusOK, "", "", "", "")
}

func (s *Server) uiNewProjectPanel(w http.ResponseWriter, r *http.Request) {
	renderUITemplate(w, http.StatusOK, "new-project-panel", uiNewProjectPanelData{CSRFToken: uiSessionCSRFToken(r)})
}

func (s *Server) uiCreateProject(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.renderUINewProject(w, r, http.StatusBadRequest, "Unable to read form.", "", "", "")
		return
	}
	key := strings.TrimSpace(r.Form.Get("key"))
	name := strings.TrimSpace(r.Form.Get("name"))
	description := r.Form.Get("description")
	if !projectKeyRe.MatchString(key) {
		s.renderUINewProject(w, r, http.StatusBadRequest, "Key must match ^[A-Z][A-Z0-9]{1,9}$.", key, name, description)
		return
	}
	if name == "" {
		s.renderUINewProject(w, r, http.StatusBadRequest, "Name required.", key, name, description)
		return
	}
	project, err := s.store.CreateProjectForUser(r.Context(), currentUser(r).ID, key, name, description)
	if err != nil {
		if errors.Is(err, store.ErrConflict) {
			s.renderUINewProject(w, r, http.StatusConflict, "Project key already exists.", key, name, description)
			return
		}
		writeUIStoreError(w, err)
		return
	}
	http.Redirect(w, r, uiProjectHomePath(project), http.StatusSeeOther)
}

func (s *Server) uiNewIssuePage(w http.ResponseWriter, r *http.Request) {
	input := uiNewIssueInputFromValues(r.URL.Query())
	panel, err := s.uiBuildNewIssuePanel(r.Context(), r, input)
	if err != nil {
		writeUIStoreError(w, err)
		return
	}
	projects, err := s.uiVisibleProjects(r.Context(), currentUser(r))
	if err != nil {
		writeUIInternalError(w, "ui new issue visible projects", err)
		return
	}
	s.renderUIShell(w, r, http.StatusOK, uiShellData{
		User:          currentUser(r),
		Projects:      projects,
		NewIssuePanel: panel,
	})
}

func (s *Server) uiNewIssuePanel(w http.ResponseWriter, r *http.Request) {
	input := uiNewIssueInputFromValues(r.URL.Query())
	panel, err := s.uiBuildNewIssuePanel(r.Context(), r, input)
	if err != nil {
		writeUIStoreError(w, err)
		return
	}
	renderUITemplate(w, http.StatusOK, "new-issue-panel", panel)
}

func (s *Server) uiNewIssueProjectOptions(w http.ResponseWriter, r *http.Request) {
	input := uiNewIssueInputFromValues(r.URL.Query())
	projects, err := s.uiIssueCreatableProjects(r.Context(), currentUser(r))
	if err != nil {
		writeUIInternalError(w, "ui new issue project options visible projects", err)
		return
	}
	data := uiNewIssueProjectAutocomplete(&uiNewIssuePanelData{
		CSRFToken:         uiSessionCSRFToken(r),
		ProjectInput:      input.ProjectInput,
		ProjectOptions:    uiFilterNewIssueProjects(projects, input.ProjectInput),
		ProjectSearchOpen: strings.TrimSpace(input.ProjectInput) != "",
	})
	renderUITemplate(w, http.StatusOK, "autocomplete-options", data)
}

func (s *Server) uiNewProjectIssuePage(w http.ResponseWriter, r *http.Request) {
	project, ok := s.uiProjectFromRoute(w, r)
	if !ok {
		return
	}
	input := uiNewIssueInputFromValues(r.URL.Query())
	input.ProjectID = project.ID.String()
	input.ProjectInput = ""
	input.ProjectScoped = true
	input.BackHref = uiProjectViewPath(project, "all")
	input.BackHXGet = uiProjectPanelPath(project, "all")
	panel, err := s.uiBuildNewIssuePanel(r.Context(), r, input)
	if err != nil {
		writeUIStoreError(w, err)
		return
	}
	projects, err := s.uiVisibleProjects(r.Context(), currentUser(r))
	if err != nil {
		writeUIInternalError(w, "ui new project issue visible projects", err)
		return
	}
	s.renderUIShell(w, r, http.StatusOK, uiShellData{
		User:          currentUser(r),
		Projects:      projects,
		SidebarActive: uiSidebarState{View: "project", ProjectID: project.ID},
		NewIssuePanel: panel,
	})
}

func (s *Server) uiNewProjectIssuePanel(w http.ResponseWriter, r *http.Request) {
	project, ok := s.uiProjectFromRoute(w, r)
	if !ok {
		return
	}
	input := uiNewIssueInputFromValues(r.URL.Query())
	input.ProjectID = project.ID.String()
	input.ProjectInput = ""
	input.ProjectScoped = true
	input.BackHref = uiProjectViewPath(project, "all")
	input.BackHXGet = uiProjectPanelPath(project, "all")
	panel, err := s.uiBuildNewIssuePanel(r.Context(), r, input)
	if err != nil {
		writeUIStoreError(w, err)
		return
	}
	renderUITemplate(w, http.StatusOK, "new-issue-panel", panel)
}

func (s *Server) uiCreateProjectIssue(w http.ResponseWriter, r *http.Request) {
	project, ok := s.uiProjectFromRoute(w, r)
	if !ok {
		return
	}
	s.uiCreateIssueForProject(w, r, &project)
}

func (s *Server) uiCreateIssue(w http.ResponseWriter, r *http.Request) {
	s.uiCreateIssueForProject(w, r, nil)
}

func (s *Server) uiCreateIssueForProject(w http.ResponseWriter, r *http.Request, routeProject *model.Project) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "unable to read form", http.StatusBadRequest)
		return
	}
	input := uiNewIssueInputFromValues(r.Form)
	input.ProjectScoped = input.ProjectScoped || routeProject != nil
	if routeProject != nil && input.ProjectID == "" {
		input.ProjectID = routeProject.ID.String()
		input.BackHref = uiProjectViewPath(*routeProject, "all")
		input.BackHXGet = uiProjectPanelPath(*routeProject, "all")
	}
	project, ok, message, err := s.uiProjectFromNewIssueSelection(r.Context(), currentUser(r), input.ProjectID)
	if err != nil {
		writeUIStoreError(w, err)
		return
	}
	if !ok {
		s.renderUINewIssueWithError(w, r, input, message)
		return
	}
	permissions, err := s.uiProjectPermissions(r.Context(), currentUser(r), project.ID)
	if err != nil {
		writeUIStoreError(w, err)
		return
	}
	if !permissions.CanWrite && (strings.TrimSpace(input.AssigneeInput) != "" || strings.TrimSpace(input.ReporterInput) != "") {
		s.renderUINewIssueWithError(w, r, input, "Public issue submissions cannot select an assignee or another reporter.")
		return
	}

	title := strings.TrimSpace(input.Title)
	if title == "" || len(title) > 200 {
		s.renderUINewIssueWithError(w, r, input, "Title required, max 200 chars.")
		return
	}

	var priority model.IssuePriority
	if input.Priority != "" {
		priority = model.IssuePriority(input.Priority)
		if !priority.Valid() {
			s.renderUINewIssueWithError(w, r, input, "Invalid priority.")
			return
		}
	}

	var dueDate *model.Date
	if input.DueDate != "" {
		parsed, err := model.ParseDate(input.DueDate)
		if err != nil {
			s.renderUINewIssueWithError(w, r, input, "Use YYYY-MM-DD.")
			return
		}
		dueDate = &parsed
	}

	assigneeID, message, err := s.uiIssueCreateUserID(r.Context(), input.AssigneeInput)
	if err != nil {
		writeUIStoreError(w, err)
		return
	}
	if message != "" {
		s.renderUINewIssueWithError(w, r, input, message)
		return
	}

	current := currentUser(r)
	currentID := current.ID
	reporterID := &currentID
	if strings.TrimSpace(input.ReporterInput) != "" {
		reporter, message, err := s.uiIssueCreateUserID(r.Context(), input.ReporterInput)
		if err != nil {
			writeUIStoreError(w, err)
			return
		}
		if message != "" {
			s.renderUINewIssueWithError(w, r, input, message)
			return
		}
		if reporter != nil {
			if !current.IsAdmin && *reporter != current.ID {
				s.renderUINewIssueWithError(w, r, input, "Reporter must be you.")
				return
			}
			reporterID = reporter
		}
	}

	created, err := s.store.CreateIssue(r.Context(), store.CreateIssueParams{
		ProjectID:   project.ID,
		Title:       title,
		Description: input.Description,
		Priority:    priority,
		AssigneeID:  assigneeID,
		ReporterID:  reporterID,
		DueDate:     dueDate,
	})
	if err != nil {
		writeUIStoreError(w, err)
		return
	}
	if !isHTMXRequest(r) {
		http.Redirect(w, r, uiIssuePath(created), http.StatusSeeOther)
		return
	}
	uiSetHXPushURL(w, r, uiIssuePath(created))
	panel, err := s.uiBuildIssuePanel(r.Context(), r, created.ID)
	if err != nil {
		writeUIStoreError(w, err)
		return
	}
	renderUITemplate(w, http.StatusOK, "issue-panel", panel)
}

func uiNewIssueInputFromValues(values url.Values) uiNewIssuePanelData {
	return uiNewIssuePanelData{
		ProjectID:     strings.TrimSpace(values.Get("project_id")),
		ProjectInput:  strings.TrimSpace(values.Get("project")),
		ProjectScoped: values.Get("project_scoped") == "true",
		Title:         values.Get("title"),
		Description:   values.Get("description"),
		Priority:      strings.TrimSpace(values.Get("priority")),
		DueDate:       strings.TrimSpace(values.Get("due_date")),
		AssigneeInput: values.Get("assignee"),
		ReporterInput: values.Get("reporter"),
	}
}

func (s *Server) renderUINewIssueWithError(w http.ResponseWriter, r *http.Request, input uiNewIssuePanelData, message string) {
	input.Error = message
	panel, err := s.uiBuildNewIssuePanel(r.Context(), r, input)
	if err != nil {
		writeUIStoreError(w, err)
		return
	}
	if !isHTMXRequest(r) {
		projects, err := s.uiVisibleProjects(r.Context(), currentUser(r))
		if err != nil {
			writeUIInternalError(w, "ui new issue error visible projects", err)
			return
		}
		active := uiSidebarState{}
		if panel.ProjectScoped {
			active = uiSidebarState{View: "project", ProjectID: panel.Project.ID}
		}
		s.renderUIShell(w, r, http.StatusOK, uiShellData{
			User:          currentUser(r),
			Projects:      projects,
			SidebarActive: active,
			NewIssuePanel: panel,
		})
		return
	}
	renderUITemplate(w, http.StatusOK, "new-issue-panel", panel)
}

func (s *Server) renderUIProjects(w http.ResponseWriter, r *http.Request, status int) {
	panel, err := s.uiBuildProjectsPanel(r.Context(), currentUser(r))
	if err != nil {
		writeUIInternalError(w, "ui render projects panel", err)
		return
	}
	s.renderUIShell(w, r, status, uiShellData{
		User:          currentUser(r),
		Projects:      panel.Projects,
		SidebarActive: uiSidebarState{View: "projects"},
		ProjectsPanel: panel,
	})
}

func (s *Server) renderUIOwnerProjects(w http.ResponseWriter, r *http.Request, status int) {
	panel, err := s.uiBuildOwnerProjectsPanel(r.Context(), currentUser(r), chi.URLParam(r, "owner"))
	if err != nil {
		writeUIStoreError(w, err)
		return
	}
	projects, err := s.uiVisibleProjects(r.Context(), currentUser(r))
	if err != nil {
		// Defensive: the owner panel already completed the same project query; this requires a subsequent DB outage.
		writeUIInternalError(w, "ui owner projects visible projects", err)
		return
	}
	s.renderUIShell(w, r, status, uiShellData{
		User:          currentUser(r),
		Projects:      projects,
		SidebarActive: uiSidebarState{View: "projects"},
		ProjectsPanel: panel,
	})
}

func (s *Server) renderUINewProject(w http.ResponseWriter, r *http.Request, status int, message, key, name, description string) {
	projects, err := s.uiVisibleProjects(r.Context(), currentUser(r))
	if err != nil {
		writeUIInternalError(w, "ui new project visible projects", err)
		return
	}
	s.renderUIShell(w, r, status, uiShellData{
		User:     currentUser(r),
		Projects: projects,
		NewProjectPanel: &uiNewProjectPanelData{
			CSRFToken:   uiSessionCSRFToken(r),
			Error:       message,
			Key:         key,
			Name:        name,
			Description: description,
		},
	})
}
