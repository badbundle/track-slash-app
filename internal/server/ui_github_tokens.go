package server

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/bradleymackey/track-slash/internal/githubintegration"
)

const uiGitHubTokenNewForm = "new"

func (s *Server) uiCreateGitHubToken(w http.ResponseWriter, r *http.Request) {
	if s.githubIntegration == nil {
		s.renderUIGitHubTokensUnconfigured(w, r)
		return
	}
	name := r.FormValue("name")
	credential, err := s.githubIntegration.CreateCredential(r.Context(), githubintegration.CreateCredentialParams{
		UserID: currentUser(r).ID, Name: name, Token: strings.TrimSpace(r.FormValue("token")),
	})
	if err != nil {
		s.renderUIGitHubTokenForm(w, r, uiGitHubTokenNewForm, name, uiGitHubTokenMessage(err))
		return
	}
	s.renderUIGitHubTokenSaved(w, r, "Saved “"+credential.Name+"”. Choose it when you connect a repository.")
}

func (s *Server) uiUpdateGitHubToken(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "credentialID"))
	if err != nil {
		writeUIStoreError(w, errUIBadRequest)
		return
	}
	if s.githubIntegration == nil {
		s.renderUIGitHubTokensUnconfigured(w, r)
		return
	}
	name := r.FormValue("name")
	token := strings.TrimSpace(r.FormValue("token"))
	credential, err := s.githubIntegration.UpdateCredential(r.Context(), githubintegration.UpdateCredentialParams{
		ID: id, UserID: currentUser(r).ID, Name: &name, Token: token,
	})
	if err != nil {
		s.renderUIGitHubTokenForm(w, r, id.String(), name, uiGitHubTokenMessage(err))
		return
	}
	if token == "" {
		s.renderUIGitHubTokenSaved(w, r, "Saved “"+credential.Name+"”.")
		return
	}
	s.renderUIGitHubTokenSaved(w, r, "Replaced the token for “"+credential.Name+"”. Every repository that uses it now uses the new token.")
}

func (s *Server) uiDeleteGitHubToken(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "credentialID"))
	if err != nil {
		writeUIStoreError(w, errUIBadRequest)
		return
	}
	if err := s.store.DeleteGitHubCredential(r.Context(), currentUser(r).ID, id); err != nil {
		writeUIStoreError(w, err)
		return
	}
	s.renderUIGitHubTokenSaved(w, r, "Removed the saved token.")
}

// renderUIGitHubTokensUnconfigured answers a token form posted after the
// encryption key was removed. The section itself explains the integration is
// off, so there is no form left to attach an error to.
func (s *Server) renderUIGitHubTokensUnconfigured(w http.ResponseWriter, r *http.Request) {
	s.renderUITokenPanel(w, r, uiTokenPanelData{})
}

func (s *Server) renderUIGitHubTokenForm(w http.ResponseWriter, r *http.Request, form, name, message string) {
	s.renderUITokenPanel(w, r, uiTokenPanelData{GitHubTokenFormFor: form, GitHubTokenNameInput: name, GitHubTokenError: message})
}

func (s *Server) renderUIGitHubTokenSaved(w http.ResponseWriter, r *http.Request, message string) {
	s.renderUITokenPanel(w, r, uiTokenPanelData{GitHubTokenSaved: message})
}
