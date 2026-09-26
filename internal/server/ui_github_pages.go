package server

import (
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/bradleymackey/track-slash/internal/githubintegration"
	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/store"
)

func (s *Server) uiConnectGitHubRepository(w http.ResponseWriter, r *http.Request) {
	project, ok := s.uiProjectFromRoute(w, r)
	if !ok {
		return
	}
	if err := s.uiRequireProjectMemberManagement(r.Context(), currentUser(r), project.ID); err != nil {
		writeUIStoreError(w, err)
		return
	}
	form := uiGitHubConnectForm{
		Repository:   strings.TrimSpace(r.FormValue("repository")),
		CredentialID: strings.TrimSpace(r.FormValue("credential_id")),
		TokenName:    r.FormValue("token_name"),
		NewToken:     strings.TrimSpace(r.FormValue("token")) != "",
	}
	if s.githubIntegration == nil {
		s.renderUIGitHubProjectError(w, r, project.ID, form, "GitHub integration is not configured on this server.")
		return
	}
	if _, _, err := githubintegration.ParseRepository(form.Repository); err != nil {
		s.renderUIGitHubProjectError(w, r, project.ID, form, uiGitHubActionMessage(err))
		return
	}
	// A pasted token is saved to the user's account first, so the next project
	// can pick it instead of asking for it again.
	saved := ""
	if form.NewToken {
		credential, err := s.githubIntegration.CreateCredential(r.Context(), githubintegration.CreateCredentialParams{
			UserID: currentUser(r).ID, Name: form.TokenName, Token: strings.TrimSpace(r.FormValue("token")),
		})
		if err != nil {
			s.renderUIGitHubProjectError(w, r, project.ID, form, uiGitHubTokenMessage(err))
			return
		}
		form = uiGitHubConnectForm{Repository: form.Repository, CredentialID: credential.ID.String()}
		saved = "Saved “" + credential.Name + "” to your account. "
	}
	credentialID, err := uuid.Parse(form.CredentialID)
	if err != nil {
		s.renderUIGitHubProjectError(w, r, project.ID, form, "Choose a saved token or add a new one.")
		return
	}
	_, err = s.githubIntegration.ConnectRepository(r.Context(), githubintegration.ConnectRepositoryParams{
		ProjectID: project.ID, Repository: form.Repository, CredentialID: credentialID, CreatedByID: currentUser(r).ID,
	})
	if err != nil {
		s.renderUIGitHubProjectError(w, r, project.ID, form, saved+uiGitHubConnectMessage(err))
		return
	}
	s.renderUIProjectPanel(w, r, project.ID, "about", nil)
}

// uiGitHubConnectForm is what the connect modal re-renders with after an
// error. The pasted token itself is never echoed back.
type uiGitHubConnectForm struct {
	Repository   string
	CredentialID string
	TokenName    string
	NewToken     bool
}

func (s *Server) uiDisconnectGitHubRepository(w http.ResponseWriter, r *http.Request) {
	project, ok := s.uiProjectFromRoute(w, r)
	if !ok {
		return
	}
	if err := s.uiRequireProjectMemberManagement(r.Context(), currentUser(r), project.ID); err != nil {
		writeUIStoreError(w, err)
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "connectionID"))
	if err != nil {
		writeUIStoreError(w, errUIBadRequest)
		return
	}
	if err := s.store.DisconnectGitHubConnection(r.Context(), project.ID, id); err != nil {
		writeUIStoreError(w, err)
		return
	}
	s.renderUIProjectPanel(w, r, project.ID, "about", nil)
}

func (s *Server) uiCreateGitHubIssueLink(w http.ResponseWriter, r *http.Request) {
	issue, _, ok := s.uiIssueFromRouteIncludingDeleted(w, r)
	if !ok {
		return
	}
	connectionRaw := strings.TrimSpace(r.FormValue("connection_id"))
	reference := strings.TrimSpace(r.FormValue("reference"))
	connectionID, err := uuid.Parse(connectionRaw)
	if err != nil {
		s.renderUIGitHubIssueError(w, r, issue.ID, connectionRaw, reference, "Choose a repository.")
		return
	}
	if s.githubIntegration == nil {
		s.renderUIGitHubIssueError(w, r, issue.ID, connectionRaw, reference, "GitHub integration is not configured on this server.")
		return
	}
	_, err = s.githubIntegration.CreateLink(r.Context(), githubintegration.CreateLinkParams{
		IssueID: issue.ID, ConnectionID: connectionID, Reference: reference, CreatedByID: currentUser(r).ID,
	})
	if err != nil {
		s.renderUIGitHubIssueError(w, r, issue.ID, connectionRaw, reference, uiGitHubActionMessage(err))
		return
	}
	s.renderUIGitHubIssueError(w, r, issue.ID, "", "", "")
}

func (s *Server) uiDeleteGitHubIssueLink(w http.ResponseWriter, r *http.Request) {
	issue, _, ok := s.uiIssueFromRouteIncludingDeleted(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "githubLinkID"))
	if err != nil {
		writeUIStoreError(w, errUIBadRequest)
		return
	}
	if err := s.store.DeleteGitHubIssueLink(r.Context(), issue.ID, id); err != nil {
		writeUIStoreError(w, err)
		return
	}
	s.renderUIGitHubIssueError(w, r, issue.ID, "", "", "")
}

func (s *Server) uiRefreshGitHubIssueLink(w http.ResponseWriter, r *http.Request) {
	issue, _, ok := s.uiIssueFromRouteIncludingDeleted(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "githubLinkID"))
	if err != nil {
		writeUIStoreError(w, errUIBadRequest)
		return
	}
	link, err := s.store.GetGitHubIssueLink(r.Context(), id)
	if err != nil || link.IssueID != issue.ID {
		if err == nil {
			err = store.ErrNotFound
		}
		writeUIStoreError(w, err)
		return
	}
	if s.githubIntegration != nil {
		_, _ = s.githubIntegration.RefreshLink(r.Context(), id)
	}
	s.renderUIGitHubIssueError(w, r, issue.ID, "", "", "")
}

func (s *Server) renderUIGitHubProjectError(w http.ResponseWriter, r *http.Request, projectID uuid.UUID, form uiGitHubConnectForm, message string) {
	s.renderUIProjectPanel(w, r, projectID, "about", func(panel *uiProjectPanelData) {
		panel.GitHubRepositoryInput = form.Repository
		panel.GitHubCredentialInput = form.CredentialID
		panel.GitHubTokenNameInput = form.TokenName
		panel.GitHubNewTokenOpen = form.NewToken
		panel.GitHubConnectionError = message
	})
}

func (s *Server) renderUIGitHubIssueError(w http.ResponseWriter, r *http.Request, issueID uuid.UUID, connectionID, reference, message string) {
	panel, err := s.uiBuildIssuePanel(r.Context(), r, issueID)
	if err != nil {
		writeUIStoreError(w, err)
		return
	}
	panel.GitHubConnectionID = connectionID
	panel.GitHubReference = reference
	panel.GitHubError = message
	s.renderUIIssuePanelResponse(w, r, panel)
}

func uiGitHubActionMessage(err error) string {
	switch {
	case errors.Is(err, githubintegration.ErrInvalid):
		return strings.TrimSuffix(err.Error(), ": "+githubintegration.ErrInvalid.Error())
	case errors.Is(err, githubintegration.ErrUnauthorized):
		return "The token does not allow access to that GitHub resource."
	case errors.Is(err, githubintegration.ErrUnavailable):
		return "GitHub could not find that repository, branch, or pull request."
	case errors.Is(err, githubintegration.ErrRateLimited):
		return "GitHub's rate limit was reached. Try again later."
	case errors.Is(err, store.ErrConflict):
		return "That GitHub resource is already linked."
	case errors.Is(err, store.ErrNotFound):
		return "The repository connection is no longer available."
	default:
		return "GitHub could not be reached. Try again later."
	}
}

// uiGitHubTokenMessage words errors from saving a token, where a rejected
// credential or a conflict means something different than for an issue link.
func uiGitHubTokenMessage(err error) string {
	switch {
	case errors.Is(err, githubintegration.ErrUnauthorized):
		return "GitHub did not accept that token. Check that it is correct and has not expired."
	case errors.Is(err, store.ErrConflict):
		return "You already have a saved token with that name."
	case errors.Is(err, store.ErrNotFound):
		return "That saved token no longer exists."
	default:
		return uiGitHubActionMessage(err)
	}
}

// uiGitHubConnectMessage words errors from connecting a repository, where
// the record that can go missing is the chosen saved token.
func uiGitHubConnectMessage(err error) string {
	if errors.Is(err, store.ErrNotFound) {
		return "That saved token no longer exists."
	}
	return uiGitHubActionMessage(err)
}

// uiGitHubTokenLabels says which token each connection uses. A saved token
// is named only to its owner; the name is their private label.
func uiGitHubTokenLabels(connections []model.GitHubConnection, credentials []model.GitHubCredential) map[uuid.UUID]string {
	names := make(map[uuid.UUID]string, len(credentials))
	for _, credential := range credentials {
		names[credential.ID] = credential.Name
	}
	labels := make(map[uuid.UUID]string, len(connections))
	for _, connection := range connections {
		switch {
		case connection.CredentialID == nil:
			labels[connection.ID] = "project token"
		case names[*connection.CredentialID] != "":
			labels[connection.ID] = "your token “" + names[*connection.CredentialID] + "”"
		default:
			labels[connection.ID] = "saved token"
		}
	}
	return labels
}
