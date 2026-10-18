package server

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/bradleymackey/track-slash/internal/store"
)

// The password reset routes exist only while email is on, since a reset is
// sent to the account's verified address. With email off they are not
// mounted, so they answer 404 like any unknown address. Reset is browser-only:
// there is no API or MCP route for it.
const (
	uiForgotPasswordPath = "/forgot-password"
	uiResetPasswordPath  = "/reset-password"
	// uiLoginAfterResetPath is where a successful reset sends the browser, to
	// sign in with the new password. It is never signed in automatically.
	uiLoginAfterResetPath = "/login?password_reset=done"
)

const (
	// uiForgotPasswordSent is the one answer every reset request gets,
	// whether or not anything was sent.
	uiForgotPasswordSent    = "If that account has a verified email, we've sent a reset link."
	uiForgotPasswordMissing = "Enter your username or email."
	uiResetPasswordExpired  = "This link has expired. Ask for a new one."
	uiResetPasswordMismatch = "Passwords don't match."
	uiResetPasswordDone     = "Password updated. Sign in with your new password."
)

// uiForgotPasswordData backs the page that asks for a reset link.
type uiForgotPasswordData struct {
	CSRFToken string
	// Sent replaces the form with uiForgotPasswordSent.
	Sent  bool
	Error string
}

// uiResetPasswordData backs the page a reset link opens.
type uiResetPasswordData struct {
	CSRFToken string
	// Expired replaces the form with uiResetPasswordExpired and a link to ask
	// again.
	Expired bool
	Token   string
	// Username names the account, for the page and for password managers.
	Username string
	Error    string
}

func (s *Server) mountUIPasswordResetRoutes(r chi.Router) {
	if !s.emailEnabled() {
		return
	}
	r.Get(uiForgotPasswordPath, s.uiForgotPasswordPage)
	r.With(s.uiPreAuthCSRFMiddleware).Post(uiForgotPasswordPath, s.authIPRateLimited(s.uiForgotPassword))
	r.Get(uiResetPasswordPath, s.uiResetPasswordPage)
	r.With(s.uiPreAuthCSRFMiddleware).Post(uiResetPasswordPath, s.authIPRateLimited(s.uiResetPassword))
}

func (s *Server) uiForgotPasswordPage(w http.ResponseWriter, r *http.Request) {
	s.renderUIForgotPassword(w, r, http.StatusOK, uiForgotPasswordData{})
}

// uiForgotPassword gives the same answer to every request. Finding the
// account, its budget, the link and the email all happen after the response,
// so neither the page nor how long it takes says whether the account exists,
// has a verified email, signs in with a passkey or has asked too often.
func (s *Server) uiForgotPassword(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.renderUIForgotPassword(w, r, http.StatusBadRequest, uiForgotPasswordData{Error: "Unable to read form."})
		return
	}
	identifier := strings.TrimSpace(r.Form.Get("identifier"))
	if identifier == "" {
		s.renderUIForgotPassword(w, r, http.StatusBadRequest, uiForgotPasswordData{Error: uiForgotPasswordMissing})
		return
	}
	s.renderUIForgotPassword(w, r, http.StatusOK, uiForgotPasswordData{Sent: true})
	s.goBackground(r.Context(), "password reset request", func(ctx context.Context) {
		s.requestPasswordReset(ctx, identifier)
	})
}

// requestPasswordReset sends the account identifier names whatever answers a
// forgotten password: a reset link, or, for an account that signs in with a
// passkey, a note saying so. It runs after the response, so it sends nothing,
// and says nothing, for an identifier that names no account with a verified
// email or for an account past its budget.
func (s *Server) requestPasswordReset(ctx context.Context, identifier string) {
	account, err := s.store.FindPasswordResetAccount(ctx, identifier)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			logInternalError("password reset lookup", err)
		}
		return
	}
	// Past the budget the request is dropped, and since the page has already
	// answered, nobody can tell. An older link still works.
	if allowed, _ := s.emailLimiter.allow(passwordResetKey(account.UserID)); !allowed {
		return
	}
	s.sendPasswordReset(ctx, account)
}

// sendPasswordReset sends account a reset link, or, when it doesn't sign in
// with a password, a note that it signs in with a passkey: the account chose
// that, and a reset would add a password it turned off.
func (s *Server) sendPasswordReset(ctx context.Context, account store.PasswordResetAccount) {
	msg := passwordResetPasskeyMessage(s.publicOrigin, account)
	if account.PasswordLogin {
		token, err := s.store.CreatePasswordReset(ctx, account.UserID, account.Email)
		if err != nil {
			// ErrNotFound means the account changed since it was found, such as
			// turning password sign-in off or changing its email; anything else
			// is a DB outage. Either way nothing is sent.
			logInternalError("password reset create", err)
			return
		}
		msg = passwordResetMessage(s.publicOrigin, account, token)
	}
	if err := s.mailer.Send(ctx, msg); err != nil {
		logInternalError("password reset send", err)
	}
}

// uiResetPasswordPage only shows the form. Mail scanners open links before
// people do, so opening one never uses it up: only the form's POST does.
func (s *Server) uiResetPasswordPage(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	account, err := s.store.CheckPasswordReset(r.Context(), token)
	switch {
	case errors.Is(err, store.ErrNotFound):
		s.renderUIResetPassword(w, r, http.StatusOK, uiResetPasswordData{Expired: true})
	case err != nil:
		// Defensive: only a DB outage reaches this.
		writeUIInternalError(w, "ui password reset check", err)
	default:
		s.renderUIResetPassword(w, r, http.StatusOK, uiResetPasswordData{Token: token, Username: account.Username})
	}
}

// uiResetPassword sets the new password, ends every web session of the
// account and sends the browser to sign in with it. It tells the verified
// address after the response, so a slow provider doesn't hold up sign-in.
func (s *Server) uiResetPassword(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.renderUIResetPassword(w, r, http.StatusBadRequest, uiResetPasswordData{Expired: true})
		return
	}
	token := r.Form.Get("token")
	password := r.Form.Get("password")
	retry := func(message string) {
		// The form keeps the link, so a mistyped password doesn't cost it. The
		// username is read again only to label the form; a link that stopped
		// working since shows as expired.
		data := uiResetPasswordData{Token: token, Error: message}
		account, err := s.store.CheckPasswordReset(r.Context(), token)
		switch {
		case errors.Is(err, store.ErrNotFound):
			data = uiResetPasswordData{Expired: true}
		case err != nil:
			// Defensive: only a DB outage reaches this.
			writeUIInternalError(w, "ui password reset recheck", err)
			return
		default:
			data.Username = account.Username
		}
		s.renderUIResetPassword(w, r, http.StatusBadRequest, data)
	}
	if password != r.Form.Get("password_confirm") {
		retry(uiResetPasswordMismatch)
		return
	}
	if err := store.ValidatePassword(password); err != nil {
		retry(err.Error())
		return
	}
	account, err := s.store.ResetPassword(r.Context(), token, password)
	switch {
	case errors.Is(err, store.ErrNotFound):
		s.renderUIResetPassword(w, r, http.StatusBadRequest, uiResetPasswordData{Expired: true})
		return
	case err != nil:
		// Defensive: the password was validated above, so only a DB outage
		// reaches this.
		writeUIInternalError(w, "ui password reset", err)
		return
	}
	s.goBackground(r.Context(), "password reset notice", func(ctx context.Context) {
		if err := s.mailer.Send(ctx, passwordResetNoticeMessage(s.publicOrigin, account)); err != nil {
			logInternalError("password reset notice send", err)
		}
	})
	http.Redirect(w, r, uiLoginAfterResetPath, http.StatusSeeOther)
}

func (s *Server) renderUIForgotPassword(w http.ResponseWriter, r *http.Request, status int, data uiForgotPasswordData) {
	var err error
	if data.CSRFToken, err = s.ensureUIPreAuthCSRFToken(w, r); err != nil {
		writeUIInternalError(w, "ui forgot password csrf token", err)
		return
	}
	renderUITemplate(w, status, "forgot-password", data)
}

func (s *Server) renderUIResetPassword(w http.ResponseWriter, r *http.Request, status int, data uiResetPasswordData) {
	var err error
	if data.CSRFToken, err = s.ensureUIPreAuthCSRFToken(w, r); err != nil {
		writeUIInternalError(w, "ui reset password csrf token", err)
		return
	}
	// The page carries the link's token, so no cache may keep it.
	w.Header().Set("Cache-Control", "no-store")
	renderUITemplate(w, status, "reset-password", data)
}
