package server

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/store"
)

type projectMemberReq struct {
	Role model.ProjectMemberRole `json:"role,omitempty"`
}

// projectAccessReq sets a project's access mode. is_public and
// public_issue_creation are the pair the mode replaced, still accepted from
// older clients; access_mode wins when both are sent.
type projectAccessReq struct {
	AccessMode          *model.ProjectAccessMode `json:"access_mode,omitempty"`
	IsPublic            *bool                    `json:"is_public,omitempty"`
	PublicIssueCreation *bool                    `json:"public_issue_creation,omitempty"`
}

// mode is the requested access mode, or false when the request names none.
func (req projectAccessReq) mode() (model.ProjectAccessMode, bool) {
	if req.AccessMode != nil {
		return *req.AccessMode, true
	}
	return legacyProjectAccessMode(req.IsPublic, req.PublicIssueCreation)
}

const errProjectAccessModeRequired = "access_mode is required"

// legacyProjectAccessMode maps the legacy flags onto a mode, a flag left out
// reading as off. It reports false when both are left out, so a request that
// names no mode at all is refused rather than making the project private.
func legacyProjectAccessMode(isPublic, publicIssueCreation *bool) (model.ProjectAccessMode, bool) {
	if isPublic == nil && publicIssueCreation == nil {
		return "", false
	}
	return model.ProjectAccessModeFromFlags(isPublic != nil && *isPublic, publicIssueCreation != nil && *publicIssueCreation), true
}

func (s *Server) grantProjectMember(w http.ResponseWriter, r *http.Request) {
	project, ok := s.projectFromRoute(w, r)
	if !ok {
		return
	}
	if !s.requireProjectMemberManagement(w, r, project.ID) {
		return
	}
	user, ok := s.projectMemberUserFromRoute(w, r)
	if !ok {
		return
	}
	req := projectMemberReq{Role: model.ProjectMemberRoleMember}
	if r.Body != nil && r.ContentLength != 0 {
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if req.Role == "" {
			req.Role = model.ProjectMemberRoleMember
		}
	}
	if !req.Role.Valid() {
		writeError(w, http.StatusBadRequest, "role must be member or readonly")
		return
	}
	member, err := s.store.SetProjectMemberRole(r.Context(), project.ID, user.ID, req.Role)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, member)
}

func (s *Server) revokeProjectMember(w http.ResponseWriter, r *http.Request) {
	project, ok := s.projectFromRoute(w, r)
	if !ok {
		return
	}
	if !s.requireProjectMemberManagement(w, r, project.ID) {
		return
	}
	user, ok := s.projectMemberUserFromRoute(w, r)
	if !ok {
		return
	}
	if err := s.store.RevokeProjectAccess(r.Context(), project.ID, user.ID); err != nil {
		writeStoreError(w, err)
		return
	}
	// A removed member's open socket was authorized as a member; make it
	// reauthorize, as blocking and access changes already do.
	s.disconnectRealtimeClients()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) getProjectAccess(w http.ResponseWriter, r *http.Request) {
	project, ok := s.projectFromRoute(w, r)
	if !ok {
		return
	}
	if !s.requireProjectAccess(w, r, project.ID) {
		return
	}
	settings, err := s.store.GetProjectAccessSettings(r.Context(), project.ID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

func (s *Server) updateProjectAccess(w http.ResponseWriter, r *http.Request) {
	project, ok := s.projectFromRoute(w, r)
	if !ok {
		return
	}
	if !s.requireProjectMemberManagement(w, r, project.ID) {
		return
	}
	var req projectAccessReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	mode, ok := req.mode()
	if !ok {
		writeError(w, http.StatusBadRequest, errProjectAccessModeRequired)
		return
	}
	if !mode.Valid() {
		writeError(w, http.StatusBadRequest, store.ErrInvalidProjectAccessMode.Error())
		return
	}
	settings, err := s.store.UpdateProjectAccessMode(r.Context(), project.ID, mode)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	s.disconnectRealtimeClients()
	writeJSON(w, http.StatusOK, settings)
}

func (s *Server) listProjectBlocks(w http.ResponseWriter, r *http.Request) {
	project, ok := s.projectFromRoute(w, r)
	if !ok {
		return
	}
	if !s.requireProjectMemberManagement(w, r, project.ID) {
		return
	}
	blocks, err := s.store.ListProjectUserBlocks(r.Context(), project.ID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, blocks)
}

func (s *Server) blockProjectUser(w http.ResponseWriter, r *http.Request) {
	project, ok := s.projectFromRoute(w, r)
	if !ok {
		return
	}
	if !s.requireProjectMemberManagement(w, r, project.ID) {
		return
	}
	user, ok := s.projectMemberUserFromRoute(w, r)
	if !ok {
		return
	}
	block, err := s.store.BlockProjectUser(r.Context(), project.ID, user.ID, currentUser(r).ID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	s.disconnectRealtimeClients()
	writeJSON(w, http.StatusOK, block)
}

func (s *Server) unblockProjectUser(w http.ResponseWriter, r *http.Request) {
	project, ok := s.projectFromRoute(w, r)
	if !ok {
		return
	}
	if !s.requireProjectMemberManagement(w, r, project.ID) {
		return
	}
	user, ok := s.projectMemberUserFromRoute(w, r)
	if !ok {
		return
	}
	if err := s.store.UnblockProjectUser(r.Context(), project.ID, user.ID); err != nil {
		writeStoreError(w, err)
		return
	}
	s.disconnectRealtimeClients()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listProjectMembers(w http.ResponseWriter, r *http.Request) {
	project, ok := s.projectFromRoute(w, r)
	if !ok {
		return
	}
	if !s.requireProjectAccess(w, r, project.ID) {
		return
	}
	members, err := s.store.ListProjectMembers(r.Context(), project.ID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, members)
}

func (s *Server) searchAvailableProjectMembers(w http.ResponseWriter, r *http.Request) {
	project, ok := s.projectFromRoute(w, r)
	if !ok {
		return
	}
	if !s.requireProjectMemberManagement(w, r, project.ID) {
		return
	}
	limit, err := parseLimit(r.URL.Query().Get("limit"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	candidates, err := s.store.SearchAvailableProjectMembers(r.Context(), store.SearchAvailableProjectMembersParams{
		ProjectID: project.ID,
		Query:     r.URL.Query().Get("q"),
		Limit:     min(limit, store.ProjectMemberCandidateLimit),
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, candidates)
}

func (s *Server) listProjectAssignees(w http.ResponseWriter, r *http.Request) {
	project, ok := s.projectFromRoute(w, r)
	if !ok {
		return
	}
	permissions, ok := s.requireProjectReadPermissions(w, r, project.ID)
	if !ok {
		return
	}
	assignees, err := s.store.ListProjectAssignees(r.Context(), project.ID, permissions.CanReadMembersOnly)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, assignees)
}

func (s *Server) searchProjectMembers(w http.ResponseWriter, r *http.Request) {
	project, ok := s.projectFromRoute(w, r)
	if !ok {
		return
	}
	permissions, ok := s.requireProjectReadPermissions(w, r, project.ID)
	if !ok {
		return
	}
	limit, err := parseLimit(r.URL.Query().Get("limit"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	users, err := s.store.SearchProjectMembers(r.Context(), store.SearchProjectMembersParams{
		ProjectID:  project.ID,
		Query:      r.URL.Query().Get("q"),
		Limit:      limit,
		MatchEmail: permissions.CanManageMembers,
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, safeProjectMemberIdentities(users))
}

func safeProjectMemberIdentities(users []model.User) []model.ProjectMemberCandidate {
	out := make([]model.ProjectMemberCandidate, 0, len(users))
	for _, user := range users {
		out = append(out, model.ProjectMemberCandidate{
			ID:                            user.ID,
			Username:                      user.Username,
			Name:                          user.Name,
			ProfileImageThumbnailObjectID: user.ProfileImageThumbnailObjectID,
		})
	}
	return out
}

func (s *Server) projectMemberUserFromRoute(w http.ResponseWriter, r *http.Request) (model.User, bool) {
	username, err := store.NormalizeUsername(chi.URLParam(r, "username"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return model.User{}, false
	}
	user, err := s.store.GetUserByUsername(r.Context(), username)
	if err != nil {
		writeStoreError(w, err)
		return model.User{}, false
	}
	return user, true
}
