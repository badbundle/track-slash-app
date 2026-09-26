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

// The account pages a signed-in user manages for themselves. Profile, Login,
// and Notifications replaced one Settings page that mixed all three, so they
// keep its /settings prefix; Tokens predates them and keeps its own path.
const (
	uiProfilePath       = "/settings/profile"
	uiLoginSettingsPath = "/settings/login"
	uiNotificationsPath = "/settings/notifications"
	uiTokensPath        = "/tokens"
)

// uiAccountPages is the order the account pages appear in, in both the
// sidebar's account group and the account menu.
var uiAccountPages = []uiAccountPage{
	{View: "profile", Label: "Profile", Path: uiProfilePath, Icon: "circle-user-round"},
	{View: "login", Label: "Login", Path: uiLoginSettingsPath, Icon: "key-round"},
	{View: "notifications", Label: "Notifications", Path: uiNotificationsPath, Icon: "bell"},
	{View: "tokens", Label: "Tokens", Path: uiTokensPath, Icon: "braces"},
}

func uiAccountPageLinks() []uiAccountPage {
	return uiAccountPages
}

// uiSettingsPage keeps the old general Settings address working now that its
// sections live on their own pages. It lands on Profile, and the query rides
// along so a bookmarked or shared link keeps its parameters.
func (s *Server) uiSettingsPage(w http.ResponseWriter, r *http.Request) {
	target := uiWithRequestQuery(r, uiProfilePath)
	if isHTMXRequest(r) {
		// htmx would follow a 303 and swap Profile into #main while the address
		// bar still showed /settings. HX-Redirect makes the browser navigate.
		w.Header().Set("HX-Redirect", target)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func (s *Server) uiProfilePage(w http.ResponseWriter, r *http.Request) {
	s.renderUIProfile(w, r, currentUser(r), "", false)
}

func (s *Server) uiLoginSettingsPage(w http.ResponseWriter, r *http.Request) {
	s.renderUILoginSettings(w, r, "", false)
}

func (s *Server) uiNotificationsPage(w http.ResponseWriter, r *http.Request) {
	s.renderUINotifications(w, r)
}

func (s *Server) uiUpdateProfile(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.renderUIProfile(w, r, currentUser(r), "Unable to read form.", false)
		return
	}
	user, err := s.store.UpdateUserProfile(r.Context(), currentUser(r).ID, r.Form.Get("name"), r.Form.Get("email"))
	if err != nil {
		s.renderUIProfile(w, r, currentUser(r), err.Error(), false)
		return
	}
	s.renderUIProfile(w, r, user, "", true)
}

func (s *Server) uiUpdatePassword(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.renderUILoginSettings(w, r, "Unable to read form.", false)
		return
	}
	if err := s.store.ChangePassword(r.Context(), currentUser(r).ID, r.Form.Get("current_password"), r.Form.Get("new_password")); err != nil {
		if errors.Is(err, store.ErrUnauthorized) {
			s.renderUILoginSettings(w, r, "Current password not accepted.", false)
			return
		}
		s.renderUILoginSettings(w, r, err.Error(), false)
		return
	}
	s.renderUILoginSettings(w, r, "", true)
}

// renderUIProfile takes the user explicitly because a successful update must
// render the row it just wrote, which the request's signed-in user predates.
func (s *Server) renderUIProfile(w http.ResponseWriter, r *http.Request, user model.User, profileError string, profileSaved bool) {
	s.renderUIAccountPage(w, r, user, "profile", uiShellData{
		ProfilePanel: &uiProfilePanelData{
			CSRFToken:    uiSessionCSRFToken(r),
			User:         user,
			ProfileError: profileError,
			ProfileSaved: profileSaved,
		},
	})
}

// The internal-error branches in the account renderers below are defensive:
// they read the signed-in user's own rows, so only a DB outage reaches them.
func (s *Server) renderUILoginSettings(w http.ResponseWriter, r *http.Request, passwordError string, passwordChanged bool) {
	user := currentUser(r)
	passkeyCredentials, err := s.store.ListPasskeyCredentials(r.Context(), user.ID)
	if err != nil {
		writeUIInternalError(w, "ui login passkey credentials", err)
		return
	}
	passwordLogin, err := s.store.PasswordLoginState(r.Context(), user.ID)
	if err != nil {
		writeUIInternalError(w, "ui login password login state", err)
		return
	}
	s.renderUIAccountPage(w, r, user, "login", uiShellData{
		LoginPanel: &uiLoginPanelData{
			CSRFToken:       uiSessionCSRFToken(r),
			PasswordError:   passwordError,
			PasswordChanged: passwordChanged,
			PasswordLogin:   passwordLogin,
			Passkeys:        passkeyCredentials,
		},
	})
}

func (s *Server) renderUINotifications(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	pushPreferences, err := s.store.GetPushNotificationPreferences(r.Context(), user.ID)
	if err != nil {
		writeUIInternalError(w, "ui notifications push preferences", err)
		return
	}
	pushDeviceCount, err := s.store.CountActivePushSubscriptions(r.Context(), user.ID)
	if err != nil {
		writeUIInternalError(w, "ui notifications push subscriptions", err)
		return
	}
	s.renderUIAccountPage(w, r, user, "notifications", uiShellData{
		NotificationPanel: &uiNotificationPanelData{
			CSRFToken:       uiSessionCSRFToken(r),
			PushEnabled:     s.webPushPublicKey != "",
			PushPublicKey:   s.webPushPublicKey,
			PushPreferences: pushPreferences,
			PushDeviceCount: pushDeviceCount,
		},
	})
}

// renderUIAccountPage puts one account page in the shell and marks its entry in
// the sidebar's account group as the current page.
func (s *Server) renderUIAccountPage(w http.ResponseWriter, r *http.Request, user model.User, view string, shell uiShellData) {
	projects, err := s.uiVisibleProjects(r.Context(), user)
	if err != nil {
		writeUIInternalError(w, "ui "+view+" visible projects", err)
		return
	}
	shell.User = user
	shell.Projects = projects
	shell.SidebarActive = uiSidebarState{View: view}
	s.renderUIShell(w, r, http.StatusOK, shell)
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
	githubTokens, err := s.store.ListGitHubCredentials(r.Context(), currentUser(r).ID)
	if err != nil {
		writeUIInternalError(w, "ui tokens list github tokens", err)
		return
	}
	panel.CSRFToken = uiSessionCSRFToken(r)
	panel.Tokens = tokens
	panel.ActiveSessions = activeSessions
	panel.ConnectedApps = connectedApps
	panel.OAuthClients = clients
	panel.GitHubConfigured = s.githubIntegration != nil
	panel.GitHubTokens = githubTokens
	s.renderUIAccountPage(w, r, currentUser(r), "tokens", uiShellData{TokenPanel: &panel})
}

// uiPartitionAuthTokens keeps unrevoked API tokens for the per-row list and
// reduces web sessions and connector access tokens to live counts. Sessions and
// connector tokens are numerous and their names carry no information, so a row
// each buried the tokens people actually manage. A revoked API token can never
// be used or restored, so it leaves the list as soon as it is revoked; its row
// stays in auth_tokens.
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
			if token.RevokedAt == nil {
				apiTokens = append(apiTokens, token)
			}
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
