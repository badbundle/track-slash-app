package server

import (
	"net/http"
	"strings"

	"github.com/bradleymackey/track-slash/internal/model"
)

// uiConfirmDeleteProject renders the project panel with the delete confirmation
// dialog open. Deleting a project takes every issue in it out of circulation, so
// the dialog states what happens and asks for the project key before the
// destructive button does anything.
func (s *Server) uiConfirmDeleteProject(w http.ResponseWriter, r *http.Request) {
	project, ok := s.uiProjectFromRoute(w, r)
	if !ok {
		return
	}
	if err := s.uiRequireProjectDeletion(r.Context(), currentUser(r), project.ID); err != nil {
		writeUIStoreError(w, err)
		return
	}
	s.renderUIProjectDeleteModal(w, r, project, "", "")
}

func (s *Server) uiDeleteProject(w http.ResponseWriter, r *http.Request) {
	project, ok := s.uiProjectFromRoute(w, r)
	if !ok {
		return
	}
	if err := s.uiRequireProjectDeletion(r.Context(), currentUser(r), project.ID); err != nil {
		writeUIStoreError(w, err)
		return
	}
	if err := r.ParseForm(); err != nil {
		writeUIStoreError(w, errUIBadRequest)
		return
	}
	confirmation := r.Form.Get("key")
	if !strings.EqualFold(strings.TrimSpace(confirmation), project.Key) {
		s.renderUIProjectDeleteModal(w, r, project, confirmation, "Type "+project.Key+" to confirm deletion.")
		return
	}
	if err := s.store.DeleteProject(r.Context(), project.ID); err != nil {
		// Defensive: the project resolved a moment ago, so this is a DB outage
		// or a concurrent delete of the same project.
		writeUIStoreError(w, err)
		return
	}
	target := uiOwnerProjectsPath(project.OwnerUsername)
	if isHTMXRequest(r) {
		// The project the panel was rendered from no longer exists, so there is
		// nothing to swap back into #main. Make the browser navigate instead.
		w.Header().Set("HX-Redirect", target)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

// uiProjectDeleteView keeps the dialog, and the cancel path back out of it, on
// the project view the actions menu was opened from.
func uiProjectDeleteView(r *http.Request) string {
	view := strings.TrimSpace(r.URL.Query().Get("view"))
	if view == "" {
		view = strings.TrimSpace(r.Form.Get("view"))
	}
	return uiProjectPanelView(view)
}

func (s *Server) renderUIProjectDeleteModal(w http.ResponseWriter, r *http.Request, project model.Project, input, message string) {
	panel, err := s.uiBuildProjectPanel(r.Context(), r, project.ID, uiProjectDeleteView(r))
	if err != nil {
		// Defensive: access was already checked, so only a DB outage gets here.
		writeUIStoreError(w, err)
		return
	}
	panel.DeleteProject = true
	panel.DeleteProjectInput = input
	panel.DeleteProjectError = message
	if isHTMXRequest(r) {
		renderUITemplate(w, http.StatusOK, "project-panel", panel)
		return
	}
	projects, err := s.uiVisibleProjects(r.Context(), currentUser(r))
	if err != nil {
		// Defensive: the sidebar project list only fails on a DB outage.
		writeUIInternalError(w, "ui project delete visible projects", err)
		return
	}
	s.renderUIShell(w, r, http.StatusOK, uiShellData{
		User:          currentUser(r),
		Projects:      projects,
		SidebarActive: uiSidebarState{View: "project", ProjectID: project.ID},
		ProjectPanel:  panel,
	})
}
