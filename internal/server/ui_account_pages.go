package server

import (
	"errors"
	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/store"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"net/http"
	"strings"
	"time"
)

func (s *Server) uiTokensPage(w http.ResponseWriter, r *http.Request) {
	clientID, secret := s.takeUIOAuthSecretRevealCookie(w, r)
	s.renderUITokenPanel(w, r, uiTokenPanelData{
		Created:             s.takeUITokenRevealCookie(w, r),
		CreatedClientID:     clientID,
		CreatedClientSecret: secret,
	})
}

func (s *Server) uiRealtime(w http.ResponseWriter, r *http.Request) {
	if s.hub == nil {
		http.NotFound(w, r)
		return
	}
	s.hub.Handler(s.uiWebSocketOrigins, s.authorizeTopic).ServeHTTP(w, r)
}

func (s *Server) uiSettingsPage(w http.ResponseWriter, r *http.Request) {
	s.renderUISettings(w, r, currentUser(r), "", false, "", false)
}

func (s *Server) uiUpdateProfile(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.renderUISettings(w, r, currentUser(r), "Unable to read form.", false, "", false)
		return
	}
	user, err := s.store.UpdateUserProfile(r.Context(), currentUser(r).ID, r.Form.Get("name"), r.Form.Get("email"))
	if err != nil {
		s.renderUISettings(w, r, currentUser(r), err.Error(), false, "", false)
		return
	}
	s.renderUISettings(w, r, user, "", true, "", false)
}

func (s *Server) uiUpdatePassword(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.renderUISettings(w, r, currentUser(r), "", false, "Unable to read form.", false)
		return
	}
	if err := s.store.ChangePassword(r.Context(), currentUser(r).ID, r.Form.Get("current_password"), r.Form.Get("new_password")); err != nil {
		if errors.Is(err, store.ErrUnauthorized) {
			s.renderUISettings(w, r, currentUser(r), "", false, "Current password not accepted.", false)
			return
		}
		s.renderUISettings(w, r, currentUser(r), "", false, err.Error(), false)
		return
	}
	s.renderUISettings(w, r, currentUser(r), "", false, "", true)
}

func (s *Server) renderUISettings(w http.ResponseWriter, r *http.Request, user model.User, profileError string, profileSaved bool, passwordError string, passwordChanged bool) {
	projects, err := s.uiVisibleProjects(r.Context(), user)
	if err != nil {
		writeUIInternalError(w, "ui settings visible projects", err)
		return
	}
	passkeyCredentials, err := s.store.ListPasskeyCredentials(r.Context(), user.ID)
	if err != nil {
		writeUIInternalError(w, "ui settings passkey credentials", err)
		return
	}
	passwordLogin, err := s.store.PasswordLoginState(r.Context(), user.ID)
	if err != nil {
		writeUIInternalError(w, "ui settings password login state", err)
		return
	}
	pushPreferences, err := s.store.GetPushNotificationPreferences(r.Context(), user.ID)
	if err != nil {
		writeUIInternalError(w, "ui settings push preferences", err)
		return
	}
	pushDeviceCount, err := s.store.CountActivePushSubscriptions(r.Context(), user.ID)
	if err != nil {
		writeUIInternalError(w, "ui settings push subscriptions", err)
		return
	}
	s.renderUIShell(w, r, http.StatusOK, uiShellData{
		User:     user,
		Projects: projects,
		SettingsPanel: &uiSettingsPanelData{
			CSRFToken:       uiSessionCSRFToken(r),
			User:            user,
			ProfileError:    profileError,
			ProfileSaved:    profileSaved,
			PasswordError:   passwordError,
			PasswordChanged: passwordChanged,
			PasswordLogin:   passwordLogin,
			Passkeys:        passkeyCredentials,
			PushEnabled:     s.webPushPublicKey != "",
			PushPublicKey:   s.webPushPublicKey,
			PushPreferences: pushPreferences,
			PushDeviceCount: pushDeviceCount,
		},
	})
}

func (s *Server) uiCreateToken(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.renderUITokens(w, r, "Unable to read form.", "")
		return
	}
	name := strings.TrimSpace(r.Form.Get("name"))
	if name == "" || len(name) > 200 {
		s.renderUITokens(w, r, "Name required, max 200 chars.", "")
		return
	}
	created, err := s.store.CreateAuthToken(r.Context(), store.CreateAuthTokenParams{
		UserID: currentUser(r).ID,
		Kind:   model.AuthTokenKindAPI,
		Name:   name,
	})
	if err != nil {
		s.renderUITokens(w, r, "Unable to create token.", "")
		return
	}
	// Post/Redirect/Get: answering 200 to a form post meant a browser reload
	// re-submitted it and silently created a second token. An htmx post never
	// becomes the browser's current address, so it has nothing to replay.
	if !isHTMXRequest(r) {
		s.setUITokenRevealCookie(w, r, created.RawToken)
		http.Redirect(w, r, uiTokenRevealPath, http.StatusSeeOther)
		return
	}
	s.renderUITokens(w, r, "", created.RawToken)
}

func (s *Server) uiRevokeToken(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid token id", http.StatusBadRequest)
		return
	}
	if err := s.store.RevokeAuthTokenForUser(r.Context(), currentUser(r).ID, id); err != nil {
		writeUIStoreError(w, err)
		return
	}
	if currentAuth(r).Token.ID == id {
		s.clearUISessionCookie(w, r)
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/tokens", http.StatusSeeOther)
}

func (s *Server) renderUITokens(w http.ResponseWriter, r *http.Request, message, created string) {
	s.renderUITokenPanel(w, r, uiTokenPanelData{Error: message, Created: created})
}

// renderUITokenPanel rebuilds the whole page around whatever transient state a
// preceding action left behind, so the token and connector sections never
// disagree about what exists.
func (s *Server) renderUITokenPanel(w http.ResponseWriter, r *http.Request, panel uiTokenPanelData) {
	all, err := s.store.ListAuthTokens(r.Context(), currentUser(r).ID)
	if err != nil {
		writeUIInternalError(w, "ui tokens list auth tokens", err)
		return
	}
	tokens, activeSessions, connectedApps := uiPartitionAuthTokens(all, time.Now())
	clients, err := s.store.ListOAuthClientsForUser(r.Context(), currentUser(r).ID)
	if err != nil {
		writeUIInternalError(w, "ui tokens list oauth clients", err)
		return
	}
	projects, err := s.uiVisibleProjects(r.Context(), currentUser(r))
	if err != nil {
		writeUIInternalError(w, "ui tokens visible projects", err)
		return
	}
	panel.CSRFToken = uiSessionCSRFToken(r)
	panel.Tokens = tokens
	panel.ActiveSessions = activeSessions
	panel.ConnectedApps = connectedApps
	panel.OAuthClients = clients
	s.renderUIShell(w, r, http.StatusOK, uiShellData{
		User:       currentUser(r),
		Projects:   projects,
		TokenPanel: &panel,
	})
}

// uiPartitionAuthTokens keeps API tokens for the per-row list and reduces web
// sessions and connector access tokens to live counts. Both are numerous and
// their names carry no information, so a row each buried the tokens people
// actually manage.
//
// The sweep that revokes expired sessions is lazy: it runs at most hourly, and
// only on the back of a token refresh. An unrevoked session is therefore not
// necessarily one you can still sign in with, and counting those would tell
// someone they had sessions open that no longer work.
func uiPartitionAuthTokens(all []model.AuthToken, now time.Time) ([]model.AuthToken, int, int) {
	apiTokens := make([]model.AuthToken, 0, len(all))
	activeSessions := 0
	connectedApps := 0
	for _, token := range all {
		switch token.Kind {
		case model.AuthTokenKindSession:
			if token.Live(now) {
				activeSessions++
			}
		case model.AuthTokenKindOAuth:
			// Connectors mint a fresh access token roughly every hour, so these
			// are counted rather than listed. The connector itself is the thing
			// a person manages, and it has its own row.
			if token.Live(now) {
				connectedApps++
			}
		default:
			apiTokens = append(apiTokens, token)
		}
	}
	return apiTokens, activeSessions, connectedApps
}

func (s *Server) uiRevokeSessionTokens(w http.ResponseWriter, r *http.Request) {
	if _, err := s.store.RevokeSessionAuthTokensForUser(r.Context(), currentUser(r).ID); err != nil {
		// Revoking nothing is a success, so only a DB outage reaches here. The
		// error status keeps the caller signed in rather than bouncing them to
		// /login as though the sessions were gone.
		writeUIStoreError(w, err)
		return
	}
	// The action does what it says, so it ends the session that invoked it too.
	s.clearUISessionCookie(w, r)
	if isHTMXRequest(r) {
		w.Header().Set("HX-Redirect", "/login")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}
