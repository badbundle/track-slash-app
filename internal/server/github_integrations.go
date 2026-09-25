package server

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/bradleymackey/track-slash/internal/githubintegration"
	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/store"
)

type githubConnectionsResponse struct {
	Configured  bool                     `json:"configured"`
	Connections []model.GitHubConnection `json:"connections"`
}

// connectGitHubRepositoryRequest takes a saved token by credential_id, or a
// token for this connection alone.
type connectGitHubRepositoryRequest struct {
	Repository   string    `json:"repository"`
	CredentialID uuid.UUID `json:"credential_id"`
	Token        string    `json:"token"`
}

type createGitHubIssueLinkRequest struct {
	ConnectionID uuid.UUID `json:"connection_id"`
	Reference    string    `json:"reference"`
}

func (s *Server) listGitHubConnections(w http.ResponseWriter, r *http.Request) {
	project, ok := s.projectFromRoute(w, r)
	if !ok || !s.requireProjectAccess(w, r, project.ID) {
		return
	}
	connections, err := s.store.ListGitHubConnections(r.Context(), project.ID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, githubConnectionsResponse{Configured: s.githubIntegration != nil, Connections: connections})
}

func (s *Server) connectGitHubRepository(w http.ResponseWriter, r *http.Request) {
	project, ok := s.projectFromRoute(w, r)
	if !ok || !s.requireProjectMemberManagement(w, r, project.ID) {
		return
	}
	if s.githubIntegration == nil {
		writeError(w, http.StatusServiceUnavailable, "GitHub integration is not configured")
		return
	}
	var req connectGitHubRepositoryRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !requireGitHubCredentialUse(w, r, req.CredentialID) {
		return
	}
	connection, err := s.githubIntegration.ConnectRepository(r.Context(), githubintegration.ConnectRepositoryParams{
		ProjectID: project.ID, Repository: strings.TrimSpace(req.Repository), CredentialID: req.CredentialID,
		Token: strings.TrimSpace(req.Token), CreatedByID: currentUser(r).ID,
	})
	if err != nil {
		writeGitHubIntegrationError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, connection)
}

func (s *Server) disconnectGitHubRepository(w http.ResponseWriter, r *http.Request) {
	project, ok := s.projectFromRoute(w, r)
	if !ok || !s.requireProjectMemberManagement(w, r, project.ID) {
		return
	}
	id, ok := githubUUIDParam(w, r, "connectionID")
	if !ok {
		return
	}
	if err := s.store.DisconnectGitHubConnection(r.Context(), project.ID, id); err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listGitHubIssueLinks(w http.ResponseWriter, r *http.Request) {
	issue, ok := s.issueFromRoute(w, r)
	if !ok || !s.requireProjectAccess(w, r, issue.ProjectID) {
		return
	}
	links, err := s.store.ListGitHubIssueLinks(r.Context(), issue.ID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, links)
}

func (s *Server) createGitHubIssueLink(w http.ResponseWriter, r *http.Request) {
	issue, ok := s.issueFromRoute(w, r)
	if !ok || !s.requireProjectWriteAccess(w, r, issue.ProjectID) {
		return
	}
	if s.githubIntegration == nil {
		writeError(w, http.StatusServiceUnavailable, "GitHub integration is not configured")
		return
	}
	var req createGitHubIssueLinkRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.ConnectionID == uuid.Nil {
		writeError(w, http.StatusBadRequest, "connection_id is required")
		return
	}
	link, err := s.githubIntegration.CreateLink(r.Context(), githubintegration.CreateLinkParams{
		IssueID: issue.ID, ConnectionID: req.ConnectionID, Reference: strings.TrimSpace(req.Reference), CreatedByID: currentUser(r).ID,
	})
	if err != nil {
		writeGitHubIntegrationError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, link)
}

func (s *Server) deleteGitHubIssueLink(w http.ResponseWriter, r *http.Request) {
	issue, ok := s.issueFromRoute(w, r)
	if !ok || !s.requireProjectWriteAccess(w, r, issue.ProjectID) {
		return
	}
	id, ok := githubUUIDParam(w, r, "githubLinkID")
	if !ok {
		return
	}
	if err := s.store.DeleteGitHubIssueLink(r.Context(), issue.ID, id); err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) refreshGitHubIssueLink(w http.ResponseWriter, r *http.Request) {
	issue, ok := s.issueFromRoute(w, r)
	if !ok || !s.requireProjectWriteAccess(w, r, issue.ProjectID) {
		return
	}
	if s.githubIntegration == nil {
		writeError(w, http.StatusServiceUnavailable, "GitHub integration is not configured")
		return
	}
	id, ok := githubUUIDParam(w, r, "githubLinkID")
	if !ok {
		return
	}
	link, err := s.store.GetGitHubIssueLink(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if link.IssueID != issue.ID {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	updated, err := s.githubIntegration.RefreshLink(r.Context(), id)
	if err != nil {
		if updated.ID != uuid.Nil {
			writeJSON(w, http.StatusOK, updated)
			return
		}
		writeGitHubIntegrationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func githubUUIDParam(w http.ResponseWriter, r *http.Request, name string) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, name))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid "+name)
		return uuid.Nil, false
	}
	return id, true
}

func writeGitHubIntegrationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrUnauthorized), errors.Is(err, store.ErrNotFound), errors.Is(err, store.ErrConflict):
		writeStoreError(w, err)
	case errors.Is(err, githubintegration.ErrInvalid):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, githubintegration.ErrRateLimited):
		var rateLimit *githubintegration.RateLimitError
		if errors.As(err, &rateLimit) && !rateLimit.RetryAt.IsZero() {
			seconds := int(time.Until(rateLimit.RetryAt).Seconds() + 0.999)
			if seconds < 1 {
				seconds = 1
			}
			w.Header().Set("Retry-After", strconv.Itoa(seconds))
		}
		writeError(w, http.StatusTooManyRequests, "GitHub rate limit reached; try again later")
	case errors.Is(err, githubintegration.ErrUnauthorized):
		writeError(w, http.StatusUnprocessableEntity, "GitHub credentials do not allow access to that resource")
	case errors.Is(err, githubintegration.ErrUnavailable):
		writeError(w, http.StatusUnprocessableEntity, "GitHub resource is unavailable")
	default:
		logInternalError("github integration", err)
		writeError(w, http.StatusBadGateway, "GitHub could not be reached")
	}
}
