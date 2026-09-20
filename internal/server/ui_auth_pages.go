package server

import (
	"context"
	"errors"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/store"
)

func (s *Server) uiLoginPage(w http.ResponseWriter, r *http.Request) {
	s.renderUILogin(w, r, http.StatusOK, uiLoginData{
		Next: safeUINext(r.URL.Query().Get("next")),
	})
}

func (s *Server) renderUILogin(w http.ResponseWriter, r *http.Request, status int, data uiLoginData) {
	if data.CSRFToken == "" {
		var err error
		data.CSRFToken, err = s.ensureUIPreAuthCSRFToken(w, r)
		if err != nil {
			writeUIInternalError(w, "ui login csrf token", err)
			return
		}
	}
	renderUITemplate(w, status, "login", data)
}

func (s *Server) uiLogin(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.renderUILogin(w, r, http.StatusBadRequest, uiLoginData{Error: "Unable to read form."})
		return
	}
	username := strings.TrimSpace(r.Form.Get("username"))
	password := r.Form.Get("password")
	next := safeUINext(r.Form.Get("next"))
	if !s.allowAuthIdentifier(w, username) {
		return
	}
	if username == "" || password == "" {
		s.renderUILogin(w, r, http.StatusUnauthorized, uiLoginData{Error: "Username and password required.", Next: next})
		return
	}
	u, err := s.store.AuthenticatePassword(r.Context(), username, password)
	if err != nil {
		if errors.Is(err, store.ErrUnauthorized) {
			s.renderUILogin(w, r, http.StatusUnauthorized, uiLoginData{Error: "Username or password not accepted.", Next: next})
			return
		}
		writeUIInternalError(w, "ui login authenticate password", err)
		return
	}
	created, err := s.createSessionToken(r, u, "web session")
	if err != nil {
		writeUIInternalError(w, "ui login create session token", err)
		return
	}
	s.setUISessionCookie(w, r, created.RawToken, created.Token.ExpiresAt)
	http.Redirect(w, r, next, http.StatusSeeOther)
}

func (s *Server) uiSignupPage(w http.ResponseWriter, r *http.Request) {
	s.renderUISignup(w, r, http.StatusOK, uiSignupData{
		Next: safeUINext(r.URL.Query().Get("next")),
	})
}

func (s *Server) renderUISignup(w http.ResponseWriter, r *http.Request, status int, data uiSignupData) {
	data.PreviewTermsRequired = s.previewTermsRequired
	if data.CSRFToken == "" {
		var err error
		data.CSRFToken, err = s.ensureUIPreAuthCSRFToken(w, r)
		if err != nil {
			writeUIInternalError(w, "ui signup csrf token", err)
			return
		}
	}
	renderUITemplate(w, status, "signup", data)
}

func (s *Server) uiSignup(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.renderUISignup(w, r, http.StatusBadRequest, uiSignupData{Error: "Unable to read form."})
		return
	}
	next := safeUINext(r.Form.Get("next"))
	if !s.allowAuthIdentifier(w, r.Form.Get("username")) {
		return
	}
	previewTermsVersion, err := s.previewTermsVersion(r.Form.Get("accept_terms") == "on")
	if err != nil {
		s.renderUISignup(w, r, http.StatusBadRequest, uiSignupData{Error: err.Error(), Next: next})
		return
	}
	u, err := s.store.CreateAccount(r.Context(), store.CreateAccountParams{
		Username:            r.Form.Get("username"),
		Password:            r.Form.Get("password"),
		Name:                r.Form.Get("name"),
		PreviewTermsVersion: previewTermsVersion,
	})
	if err != nil {
		if errors.Is(err, store.ErrConflict) {
			s.renderUISignup(w, r, http.StatusConflict, uiSignupData{Error: "Username already exists.", Next: next})
			return
		}
		s.renderUISignup(w, r, http.StatusBadRequest, uiSignupData{Error: err.Error(), Next: next})
		return
	}
	created, err := s.createSessionToken(r, u, "web session")
	if err != nil {
		writeUIInternalError(w, "ui signup create session token", err)
		return
	}
	s.setUISessionCookie(w, r, created.RawToken, created.Token.ExpiresAt)
	http.Redirect(w, r, next, http.StatusSeeOther)
}

func (s *Server) uiLogout(w http.ResponseWriter, r *http.Request) {
	if err := s.revokeUISessionCookie(r.Context(), r); err != nil {
		writeUIInternalError(w, "ui logout revoke session", err)
		return
	}
	s.clearUISessionCookie(w, r)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (s *Server) revokeUISessionCookie(ctx context.Context, r *http.Request) error {
	cookie, err := r.Cookie(uiAuthCookieName)
	if err != nil || strings.TrimSpace(cookie.Value) == "" {
		return nil
	}
	auth, err := s.store.AuthenticateToken(ctx, cookie.Value)
	if err != nil {
		if errors.Is(err, store.ErrUnauthorized) {
			return nil
		}
		return err
	}
	if auth.Token.Kind != model.AuthTokenKindSession {
		return nil
	}
	if err := s.store.RevokeAuthTokenForUser(ctx, auth.User.ID, auth.Token.ID); err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	return nil
}

func (s *Server) setUISessionCookie(w http.ResponseWriter, r *http.Request, raw string, expiresAt *time.Time) {
	cookie := &http.Cookie{
		Name:     uiAuthCookieName,
		Value:    raw,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.secureCookies || r.TLS != nil,
	}
	if expiresAt != nil {
		cookie.Expires = *expiresAt
		remaining := time.Until(*expiresAt)
		if remaining > 0 {
			cookie.MaxAge = int(math.Ceil(remaining.Seconds()))
		} else {
			cookie.MaxAge = -1
		}
	}
	http.SetCookie(w, cookie)
	// A successful browser login rotates away the pre-auth CSRF state.
	s.clearUIPreAuthCSRFCookie(w, r)
}

func (s *Server) clearUISessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     uiAuthCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		Expires:  time.Unix(0, 0),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.secureCookies || r.TLS != nil,
	})
}

func (s *Server) uiAuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(uiAuthCookieName)
		if err != nil || strings.TrimSpace(cookie.Value) == "" {
			if s.anonymousProjectReadAllowed(r, false) {
				next.ServeHTTP(w, r)
				return
			}
			redirectUILogin(w, r)
			return
		}
		auth, err := s.store.AuthenticateToken(r.Context(), cookie.Value)
		if err != nil {
			if errors.Is(err, store.ErrUnauthorized) {
				s.clearUISessionCookie(w, r)
				if s.anonymousProjectReadAllowed(r, false) {
					next.ServeHTTP(w, r)
					return
				}
				redirectUILogin(w, r)
				return
			}
			writeUIInternalError(w, "ui auth middleware authenticate token", err)
			return
		}
		// A connector's access token is not a browser session. The CSRF token is
		// derived from whatever is in this cookie, so anything holding the raw
		// token could otherwise drive the signed-in UI — including registering
		// another connector for this user, which no revocation would reach.
		if auth.Token.Kind == model.AuthTokenKindOAuth {
			s.clearUISessionCookie(w, r)
			if s.anonymousProjectReadAllowed(r, false) {
				next.ServeHTTP(w, r)
				return
			}
			redirectUILogin(w, r)
			return
		}
		ctx := context.WithValue(r.Context(), authContextKey{}, authContext{User: auth.User, Token: auth.Token})
		ctx = store.WithActor(ctx, auth.User.ID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
