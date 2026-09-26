package server

import (
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/bradleymackey/track-slash/internal/githubintegration"
	"github.com/bradleymackey/track-slash/internal/model"
)

type githubCredentialsResponse struct {
	Configured bool                     `json:"configured"`
	Tokens     []model.GitHubCredential `json:"tokens"`
}

type createGitHubCredentialRequest struct {
	Name  string `json:"name"`
	Token string `json:"token"`
}

type updateGitHubCredentialRequest struct {
	Name  *string `json:"name"`
	Token string  `json:"token"`
}

// Saved GitHub tokens are account credentials, so like API tokens they are
// managed only by first-party callers, never by an OAuth connector.

func (s *Server) listMyGitHubCredentials(w http.ResponseWriter, r *http.Request) {
	if !requireFirstPartyToken(w, r) {
		return
	}
	credentials, err := s.store.ListGitHubCredentials(r.Context(), currentUser(r).ID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, githubCredentialsResponse{Configured: s.githubIntegration != nil, Tokens: credentials})
}

func (s *Server) createMyGitHubCredential(w http.ResponseWriter, r *http.Request) {
	if !requireFirstPartyToken(w, r) {
		return
	}
	if s.githubIntegration == nil {
		writeError(w, http.StatusServiceUnavailable, "GitHub integration is not configured")
		return
	}
	var req createGitHubCredentialRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	credential, err := s.githubIntegration.CreateCredential(r.Context(), githubintegration.CreateCredentialParams{
		UserID: currentUser(r).ID, Name: req.Name, Token: strings.TrimSpace(req.Token),
	})
	if err != nil {
		writeGitHubIntegrationError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, credential)
}

func (s *Server) updateMyGitHubCredential(w http.ResponseWriter, r *http.Request) {
	if !requireFirstPartyToken(w, r) {
		return
	}
	if s.githubIntegration == nil {
		writeError(w, http.StatusServiceUnavailable, "GitHub integration is not configured")
		return
	}
	id, ok := githubUUIDParam(w, r, "credentialID")
	if !ok {
		return
	}
	var req updateGitHubCredentialRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	credential, err := s.githubIntegration.UpdateCredential(r.Context(), githubintegration.UpdateCredentialParams{
		ID: id, UserID: currentUser(r).ID, Name: req.Name, Token: strings.TrimSpace(req.Token),
	})
	if err != nil {
		writeGitHubIntegrationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, credential)
}

// deleteMyGitHubCredential works without the integration configured, so a
// token saved before the encryption key was removed can still be deleted.
func (s *Server) deleteMyGitHubCredential(w http.ResponseWriter, r *http.Request) {
	if !requireFirstPartyToken(w, r) {
		return
	}
	id, ok := githubUUIDParam(w, r, "credentialID")
	if !ok {
		return
	}
	if err := s.store.DeleteGitHubCredential(r.Context(), currentUser(r).ID, id); err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// requireGitHubCredentialUse refuses an OAuth connector the use of a saved
// token. Connecting a repository with one would let the connector point the
// user's GitHub access at any project they manage.
func requireGitHubCredentialUse(w http.ResponseWriter, r *http.Request, credentialID uuid.UUID) bool {
	if credentialID == uuid.Nil {
		return true
	}
	return requireFirstPartyToken(w, r)
}
