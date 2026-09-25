package server

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/store"
)

func uiProjectMemberAutocomplete(panel *uiProjectPanelData) uiAutocompleteEditData {
	queryReady := store.ProjectMemberCandidateQueryReady(panel.MemberInput)
	options := make([]uiAutocompleteOption, 0, len(panel.MemberCandidates))
	for _, candidate := range panel.MemberCandidates {
		options = append(options, uiAutocompleteOption{
			Value:      candidate.Username,
			Badge:      "@" + candidate.Username,
			Label:      candidate.Name,
			SearchText: candidate.Name + " " + candidate.Username,
		})
	}
	data := uiAutocompleteEditData{
		CSRFToken:      panel.CSRFToken,
		ID:             "project-member-user",
		Label:          "User",
		Name:           "username",
		Value:          panel.MemberInput,
		Placeholder:    "Search existing users",
		Autofocus:      true,
		Collapsible:    true,
		OptionsOpen:    queryReady,
		InputHXGet:     uiProjectMemberCandidatesPath(panel.Project),
		InputHXTrigger: "input changed delay:300ms",
		InputHXTarget:  "#project-member-options",
		InputHXSwap:    "outerHTML",
		InputHXPushURL: "false",
		OptionsID:      "project-member-options",
		Options:        options,
	}
	if queryReady {
		data.EmptyLabel = "No users found."
	}
	return data
}

func (s *Server) uiProjectMemberCandidates(w http.ResponseWriter, r *http.Request) {
	project, ok := s.uiProjectFromRoute(w, r)
	if !ok {
		return
	}
	if err := s.uiRequireProjectMemberManagement(r.Context(), currentUser(r), project.ID); err != nil {
		writeUIStoreError(w, err)
		return
	}
	input := r.URL.Query().Get("username")
	candidates, err := s.store.SearchAvailableProjectMembers(r.Context(), store.SearchAvailableProjectMembersParams{
		ProjectID: project.ID,
		Query:     input,
		Limit:     store.ProjectMemberCandidateLimit,
	})
	if err != nil {
		writeUIStoreError(w, err)
		return
	}
	renderUITemplate(w, http.StatusOK, "autocomplete-options", uiProjectMemberAutocomplete(&uiProjectPanelData{
		CSRFToken:        uiSessionCSRFToken(r),
		Project:          project,
		MemberInput:      input,
		MemberCandidates: candidates,
	}))
}

func (s *Server) uiAddProjectMember(w http.ResponseWriter, r *http.Request) {
	s.uiSaveProjectMember(w, r, "")
}

func (s *Server) uiUpdateProjectMember(w http.ResponseWriter, r *http.Request) {
	s.uiSaveProjectMember(w, r, chi.URLParam(r, "username"))
}

func (s *Server) uiSaveProjectMember(w http.ResponseWriter, r *http.Request, routeUsername string) {
	project, ok := s.uiProjectFromRoute(w, r)
	if !ok {
		return
	}
	if err := s.uiRequireProjectMemberManagement(r.Context(), currentUser(r), project.ID); err != nil {
		writeUIStoreError(w, err)
		return
	}
	if err := r.ParseForm(); err != nil {
		writeUIStoreError(w, errUIBadRequest)
		return
	}
	usernameInput := routeUsername
	if usernameInput == "" {
		usernameInput = r.Form.Get("username")
	}
	username, err := store.NormalizeUsername(strings.TrimPrefix(strings.TrimSpace(usernameInput), "@"))
	role := model.ProjectMemberRole(r.Form.Get("role"))
	if role == "" {
		role = model.ProjectMemberRoleMember
	}
	message := ""
	if err != nil {
		message = "Choose an existing user."
	} else if !role.Valid() {
		message = "Choose Member or Readonly."
	} else {
		user, getErr := s.store.GetUserByUsername(r.Context(), username)
		if getErr != nil {
			if errors.Is(getErr, store.ErrNotFound) {
				message = "Choose an existing user."
			} else {
				writeUIStoreError(w, getErr)
				return
			}
		} else if _, saveErr := s.store.SetProjectMemberRole(r.Context(), project.ID, user.ID, role); saveErr != nil {
			if errors.Is(saveErr, store.ErrConflict) {
				message = saveErr.Error()
			} else {
				writeUIStoreError(w, saveErr)
				return
			}
		}
	}
	panel, buildErr := s.uiBuildProjectMemberPanel(r.Context(), r, project, usernameInput, role, message)
	if buildErr != nil {
		writeUIStoreError(w, buildErr)
		return
	}
	if message == "" {
		panel.MemberInput = ""
		panel.MemberRoleInput = model.ProjectMemberRoleMember
	}
	renderUITemplate(w, http.StatusOK, "project-panel", panel)
}

func (s *Server) uiDeleteProjectMember(w http.ResponseWriter, r *http.Request) {
	project, ok := s.uiProjectFromRoute(w, r)
	if !ok {
		return
	}
	if err := s.uiRequireProjectMemberManagement(r.Context(), currentUser(r), project.ID); err != nil {
		writeUIStoreError(w, err)
		return
	}
	if err := r.ParseForm(); err != nil {
		writeUIStoreError(w, errUIBadRequest)
		return
	}
	username, err := store.NormalizeUsername(chi.URLParam(r, "username"))
	if err != nil {
		writeUIStoreError(w, errUIBadRequest)
		return
	}
	user, err := s.store.GetUserByUsername(r.Context(), username)
	if err != nil {
		writeUIStoreError(w, err)
		return
	}
	if err := s.store.RevokeProjectAccess(r.Context(), project.ID, user.ID); err != nil {
		writeUIStoreError(w, err)
		return
	}
	panel, err := s.uiBuildProjectMemberPanel(r.Context(), r, project, "", model.ProjectMemberRoleMember, "")
	if err != nil {
		writeUIStoreError(w, err)
		return
	}
	renderUITemplate(w, http.StatusOK, "project-panel", panel)
}

func (s *Server) uiUpdateProjectAccess(w http.ResponseWriter, r *http.Request) {
	project, ok := s.uiProjectFromRoute(w, r)
	if !ok {
		return
	}
	if err := s.uiRequireProjectMemberManagement(r.Context(), currentUser(r), project.ID); err != nil {
		writeUIStoreError(w, err)
		return
	}
	if err := r.ParseForm(); err != nil {
		writeUIStoreError(w, errUIBadRequest)
		return
	}
	_, err := s.store.UpdateProjectAccessSettings(r.Context(), project.ID, model.ProjectAccessSettings{
		IsPublic:            r.Form.Get("is_public") == "on",
		PublicIssueCreation: r.Form.Get("public_issue_creation") == "on",
	})
	if err != nil {
		writeUIStoreError(w, err)
		return
	}
	s.disconnectRealtimeClients()
	panel, err := s.uiBuildProjectMemberPanel(r.Context(), r, project, "", model.ProjectMemberRoleMember, "")
	if err != nil {
		writeUIStoreError(w, err)
		return
	}
	renderUITemplate(w, http.StatusOK, "project-panel", panel)
}

func (s *Server) uiUpdateProjectSprintMode(w http.ResponseWriter, r *http.Request) {
	project, ok := s.uiProjectFromRoute(w, r)
	if !ok {
		return
	}
	if err := s.uiRequireProjectMemberManagement(r.Context(), currentUser(r), project.ID); err != nil {
		writeUIStoreError(w, err)
		return
	}
	if err := r.ParseForm(); err != nil {
		writeUIStoreError(w, errUIBadRequest)
		return
	}
	message := ""
	if _, err := s.store.SetProjectSprintsEnabled(r.Context(), project.ID, r.Form.Get("sprints_enabled") == "on"); err != nil {
		if !errors.Is(err, store.ErrActiveSprintBlocksSprintsOff) {
			// Defensive: the project was just resolved and authorized, so only a
			// concurrent project delete or a DB fault lands here.
			writeUIStoreError(w, err)
			return
		}
		message = "Sprints were not disabled because a sprint is active."
	}
	panel, err := s.uiBuildProjectMemberPanel(r.Context(), r, project, "", model.ProjectMemberRoleMember, "")
	if err != nil {
		writeUIStoreError(w, err)
		return
	}
	panel.SprintModeError = message
	renderUITemplate(w, http.StatusOK, "project-panel", panel)
}

func (s *Server) uiBlockProjectUser(w http.ResponseWriter, r *http.Request) {
	project, ok := s.uiProjectFromRoute(w, r)
	if !ok {
		return
	}
	if err := s.uiRequireProjectMemberManagement(r.Context(), currentUser(r), project.ID); err != nil {
		writeUIStoreError(w, err)
		return
	}
	if err := r.ParseForm(); err != nil {
		writeUIStoreError(w, errUIBadRequest)
		return
	}
	input := strings.TrimSpace(strings.TrimPrefix(r.Form.Get("username"), "@"))
	message := ""
	username, err := store.NormalizeUsername(input)
	if err != nil {
		message = "Enter an exact existing username."
	} else {
		user, getErr := s.store.GetUserByUsername(r.Context(), username)
		if getErr != nil {
			if errors.Is(getErr, store.ErrNotFound) {
				message = "Enter an exact existing username."
			} else {
				writeUIStoreError(w, getErr)
				return
			}
		} else if _, blockErr := s.store.BlockProjectUser(r.Context(), project.ID, user.ID, currentUser(r).ID); blockErr != nil {
			if errors.Is(blockErr, store.ErrConflict) {
				message = blockErr.Error()
			} else {
				writeUIStoreError(w, blockErr)
				return
			}
		} else {
			s.disconnectRealtimeClients()
		}
	}
	panel, buildErr := s.uiBuildProjectMemberPanel(r.Context(), r, project, "", model.ProjectMemberRoleMember, "")
	if buildErr != nil {
		writeUIStoreError(w, buildErr)
		return
	}
	panel.BlockInput = input
	panel.BlockError = message
	if message == "" {
		panel.BlockInput = ""
	}
	renderUITemplate(w, http.StatusOK, "project-panel", panel)
}

func (s *Server) uiUnblockProjectUser(w http.ResponseWriter, r *http.Request) {
	project, ok := s.uiProjectFromRoute(w, r)
	if !ok {
		return
	}
	if err := s.uiRequireProjectMemberManagement(r.Context(), currentUser(r), project.ID); err != nil {
		writeUIStoreError(w, err)
		return
	}
	username, err := store.NormalizeUsername(chi.URLParam(r, "username"))
	if err != nil {
		writeUIStoreError(w, errUIBadRequest)
		return
	}
	user, err := s.store.GetUserByUsername(r.Context(), username)
	if err != nil {
		writeUIStoreError(w, err)
		return
	}
	if err := s.store.UnblockProjectUser(r.Context(), project.ID, user.ID); err != nil {
		writeUIStoreError(w, err)
		return
	}
	s.disconnectRealtimeClients()
	panel, err := s.uiBuildProjectMemberPanel(r.Context(), r, project, "", model.ProjectMemberRoleMember, "")
	if err != nil {
		writeUIStoreError(w, err)
		return
	}
	renderUITemplate(w, http.StatusOK, "project-panel", panel)
}

func (s *Server) uiBuildProjectMemberPanel(ctx context.Context, r *http.Request, project model.Project, input string, role model.ProjectMemberRole, message string) (*uiProjectPanelData, error) {
	panel, err := s.uiBuildProjectPanel(ctx, r, project.ID, "members")
	if err != nil {
		return nil, err
	}
	panel.MemberInput = input
	panel.MemberRoleInput = role
	panel.MemberError = message
	return panel, nil
}
