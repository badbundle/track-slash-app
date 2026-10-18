package server

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bradleymackey/track-slash/internal/mailer"
	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/store"
)

// The email routes exist only while email is on. With it off they are not
// mounted, so they answer 404 like any unknown address.
const (
	uiEmailPath        = "/settings/email"
	uiEmailResendPath  = "/settings/email/resend"
	uiEmailConfirmPath = "/settings/email/confirm"
)

const (
	uiEmailSendFailed     = "Couldn't send the email. Try again."
	uiEmailInvalidAddress = "Enter one email address, such as you@example.com."
	uiEmailReauthRequired = "Confirm it's you to change your email."
)

// uiEmailConfirmState is what the confirmation page shows.
const (
	uiEmailConfirmReady     = "confirm"
	uiEmailConfirmDone      = "confirmed"
	uiEmailConfirmTaken     = "taken"
	uiEmailConfirmUnusable  = "unusable"
	uiEmailReauthModalID    = "email-password"
	uiEmailReauthModalLabel = "Required before changing your email."
)

// errEmailNeedsConfirmation refuses an email change over the API or MCP while
// email is on: an address takes effect only once its owner confirms a link
// sent to it, and that starts on the Profile page.
var errEmailNeedsConfirmation = errors.New("email changes are confirmed by email: change your email on the Profile page")

// uiEmailPanelData backs the Email card on the Profile page, shown only while
// email is on.
type uiEmailPanelData struct {
	CSRFToken string
	Status    model.EmailStatus
	// PasswordReauth renders the current-password prompt. Without it, a
	// passkey confirms it's you.
	PasswordReauth bool
	ReauthModal    uiReauthPasswordModalData
	// Draft keeps the address typed into a change that failed.
	Draft  string
	Notice string
	Error  string
}

// VerifiedBadge marks the address in effect with the shared access-badge: an
// emerald Verified, or a neutral Not verified.
func (p uiEmailPanelData) VerifiedBadge() uiAccessBadge {
	if p.Status.Verified() {
		return uiAccessBadge{Attr: `data-email-verified="true"`, Icon: "badge-check", Label: "Verified", Tinted: true}
	}
	return uiAccessBadge{Attr: `data-email-verified="false"`, Icon: "circle-dashed", Label: "Not verified"}
}

// uiReauthPasswordModalData backs the shared reauth-password-modal.
type uiReauthPasswordModalData struct {
	ID          string
	Description string
}

// uiEmailConfirmPanelData backs the page a confirmation link opens.
type uiEmailConfirmPanelData struct {
	CSRFToken string
	State     string
	Email     string
	Username  string
	Token     string
}

func emailMailer(opts Options) mailer.Mailer {
	if opts.Mailer == nil || opts.PublicOrigin == "" {
		return nil
	}
	return opts.Mailer
}

func (s *Server) emailEnabled() bool {
	return s.mailer != nil
}

func (s *Server) mountUIEmailRoutes(r chi.Router) {
	if !s.emailEnabled() {
		return
	}
	r.Post(uiEmailPath, s.uiChangeEmail)
	r.Post(uiEmailResendPath, s.uiResendEmailConfirmation)
	r.Get(uiEmailConfirmPath, s.uiEmailConfirmPage)
	r.Post(uiEmailConfirmPath, s.uiConfirmEmail)
}

// uiChangeEmail starts confirming a new address, or removes the email when the
// address is empty. Either is a change to how the account is reached, so it
// needs the same reauthentication as changing a passkey.
func (s *Server) uiChangeEmail(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	if err := r.ParseForm(); err != nil {
		s.renderUIEmailResult(w, r, http.StatusBadRequest, &uiEmailPanelData{Error: "Unable to read form."})
		return
	}
	address := strings.TrimSpace(r.Form.Get("email"))
	if address != "" && !mailer.ValidAddress(address) {
		s.renderUIEmailResult(w, r, http.StatusBadRequest, &uiEmailPanelData{Error: uiEmailInvalidAddress, Draft: address})
		return
	}
	if err := s.store.ConsumePasskeyReauthToken(r.Context(), user.ID, r.Form.Get("reauth_token")); err != nil {
		if !errors.Is(err, store.ErrUnauthorized) {
			// Defensive: only a DB outage reaches this.
			writeUIInternalError(w, "ui change email reauth", err)
			return
		}
		s.renderUIEmailResult(w, r, http.StatusForbidden, &uiEmailPanelData{Error: uiEmailReauthRequired, Draft: address})
		return
	}
	if address == "" {
		previous, err := s.store.RemoveUserEmail(r.Context(), user.ID)
		if err != nil {
			// Defensive: the signed-in user's own row.
			writeUIStoreError(w, err)
			return
		}
		s.sendEmailChangedNotice(r.Context(), user, previous)
		s.renderUIEmailResult(w, r, http.StatusOK, &uiEmailPanelData{Notice: "Email removed."})
		return
	}
	status, err := s.store.GetEmailStatus(r.Context(), user.ID)
	if err != nil {
		// Defensive: the signed-in user's own row.
		writeUIStoreError(w, err)
		return
	}
	if status.Verified() && status.Email == address {
		s.renderUIEmailResult(w, r, http.StatusOK, &uiEmailPanelData{Notice: "That's already your email."})
		return
	}
	s.startEmailConfirmation(w, r, user, address)
}

// uiResendEmailConfirmation sends a fresh link for the change still waiting,
// or for the address in effect when nobody has confirmed it yet. The address
// was already chosen, so this needs no reauthentication.
func (s *Server) uiResendEmailConfirmation(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	status, err := s.store.GetEmailStatus(r.Context(), user.ID)
	if err != nil {
		// Defensive: the signed-in user's own row.
		writeUIStoreError(w, err)
		return
	}
	switch {
	case status.Pending != nil:
		s.startEmailConfirmation(w, r, user, status.Pending.Email)
	case status.Email != "" && !status.Verified():
		s.startEmailConfirmation(w, r, user, status.Email)
	default:
		s.renderUIEmailResult(w, r, http.StatusOK, &uiEmailPanelData{Notice: "There's nothing to confirm."})
	}
}

// startEmailConfirmation sends a confirmation link and waits for the provider.
// The link is committed first, so a refused message leaves only a token that
// expires, and only an accepted one marks the link as sent.
func (s *Server) startEmailConfirmation(w http.ResponseWriter, r *http.Request, user model.User, address string) {
	// An address set before email was on, or by an admin, may not be one mail
	// can go to. It is refused before it spends any of the send budget.
	if !mailer.ValidAddress(address) {
		s.renderUIEmailResult(w, r, http.StatusBadRequest, &uiEmailPanelData{Error: uiEmailInvalidAddress})
		return
	}
	if wait, ok := s.allowEmailConfirmation(user.ID, address); !ok {
		w.Header().Set("Retry-After", strconv.Itoa(retryAfterSeconds(wait)))
		s.renderUIEmailResult(w, r, http.StatusTooManyRequests, &uiEmailPanelData{Error: "Too many confirmation emails. Try again in " + uiRetryIn(wait) + "."})
		return
	}
	created, err := s.store.CreateEmailVerification(r.Context(), user.ID, address)
	if err != nil {
		// Defensive: the address was checked above and the row is the
		// signed-in user's own.
		writeUIStoreError(w, err)
		return
	}
	if err := s.mailer.Send(r.Context(), emailConfirmationMessage(s.publicOrigin, user, address, created.Token)); err != nil {
		logInternalError("email confirmation send", err)
		s.renderUIEmailResult(w, r, http.StatusBadGateway, &uiEmailPanelData{Error: uiEmailSendFailed})
		return
	}
	if err := s.store.MarkEmailVerificationSent(r.Context(), created.Verification.ID); err != nil {
		// Defensive: the message went and its link works; only the page's
		// note that it was sent is lost, so this is logged and not shown.
		logInternalError("email confirmation mark sent", err)
	}
	s.renderUIEmailResult(w, r, http.StatusOK, &uiEmailPanelData{Notice: "We sent a link to " + address + ". Your email changes once you open it and confirm."})
}

// uiEmailConfirmPage only shows what the link would do. Mail scanners open
// links before people do, so opening one never verifies anything: only the
// button's POST does.
func (s *Server) uiEmailConfirmPage(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	token := r.URL.Query().Get("token")
	panel := &uiEmailConfirmPanelData{State: uiEmailConfirmUnusable, Token: token}
	verification, err := s.store.LookupEmailVerification(r.Context(), user.ID, token)
	switch {
	case errors.Is(err, store.ErrNotFound):
	case err != nil:
		// Defensive: only a DB outage reaches this.
		writeUIInternalError(w, "ui email confirm lookup", err)
		return
	case verification.Usable(time.Now()):
		panel.State = uiEmailConfirmReady
		panel.Email = verification.Email
	}
	s.renderUIEmailConfirm(w, r, http.StatusOK, user, panel)
}

func (s *Server) uiConfirmEmail(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	if err := r.ParseForm(); err != nil {
		s.renderUIEmailConfirm(w, r, http.StatusBadRequest, user, &uiEmailConfirmPanelData{State: uiEmailConfirmUnusable})
		return
	}
	confirmed, err := s.store.ConfirmEmailVerification(r.Context(), user.ID, r.Form.Get("token"))
	switch {
	case errors.Is(err, store.ErrNotFound):
		s.renderUIEmailConfirm(w, r, http.StatusBadRequest, user, &uiEmailConfirmPanelData{State: uiEmailConfirmUnusable})
		return
	case errors.Is(err, store.ErrConflict):
		s.renderUIEmailConfirm(w, r, http.StatusConflict, user, &uiEmailConfirmPanelData{State: uiEmailConfirmTaken})
		return
	case err != nil:
		// Defensive: only a DB outage reaches this.
		writeUIInternalError(w, "ui email confirm", err)
		return
	}
	s.sendEmailChangedNotice(r.Context(), user, confirmed.Previous)
	s.renderUIEmailConfirm(w, r, http.StatusOK, user, &uiEmailConfirmPanelData{State: uiEmailConfirmDone, Email: confirmed.Email})
}

// sendEmailChangedNotice tells a verified address that it is no longer the
// account's. The change has already happened, so a failure is only logged.
func (s *Server) sendEmailChangedNotice(ctx context.Context, user model.User, previous string) {
	if previous == "" {
		return
	}
	if err := s.mailer.Send(ctx, emailChangedMessage(s.publicOrigin, user, previous)); err != nil {
		logInternalError("email changed notice send", err)
	}
}

// renderUIEmailResult renders Profile around the Email card's latest result.
// It reads the user again, because the change may have replaced the address
// the request started with.
func (s *Server) renderUIEmailResult(w http.ResponseWriter, r *http.Request, status int, email *uiEmailPanelData) {
	user, err := s.store.GetUser(r.Context(), currentUser(r).ID)
	if err != nil {
		// Defensive: the signed-in user's own row.
		writeUIStoreError(w, err)
		return
	}
	s.renderUIProfilePanelStatus(w, r, status, user, &uiProfilePanelData{Email: email})
}

func (s *Server) renderUIEmailConfirm(w http.ResponseWriter, r *http.Request, status int, user model.User, panel *uiEmailConfirmPanelData) {
	fresh, err := s.store.GetUser(r.Context(), user.ID)
	if err != nil {
		// Defensive: the signed-in user's own row.
		writeUIStoreError(w, err)
		return
	}
	panel.CSRFToken = uiSessionCSRFToken(r)
	panel.Username = fresh.Username
	s.renderUIAccountPageStatus(w, r, status, fresh, "profile", uiShellData{EmailConfirmPanel: panel})
}

// fillUIEmailPanel loads what the Email card shows about the user. Both reads
// are of the signed-in user's own rows, so its errors are defensive.
func (s *Server) fillUIEmailPanel(ctx context.Context, user model.User, panel *uiEmailPanelData) error {
	status, err := s.store.GetEmailStatus(ctx, user.ID)
	if err != nil {
		return err
	}
	passwordLogin, err := s.store.PasswordLoginState(ctx, user.ID)
	if err != nil {
		return err
	}
	panel.Status = status
	panel.PasswordReauth = passwordLogin.Enabled
	panel.ReauthModal = uiReauthPasswordModalData{ID: uiEmailReauthModalID, Description: uiEmailReauthModalLabel}
	return nil
}

// saveProfile saves a name and email sent over the API or MCP. An unchanged
// email is left alone, so a save can never write back an address read before
// a confirmation replaced it. While email is on, a different address is
// refused with errEmailNeedsConfirmation.
func (s *Server) saveProfile(ctx context.Context, user model.User, name, email string) (model.User, error) {
	if strings.TrimSpace(email) == user.Email {
		return s.store.UpdateUserName(ctx, user.ID, name)
	}
	if s.emailEnabled() {
		return model.User{}, errEmailNeedsConfirmation
	}
	return s.store.UpdateUserProfile(ctx, user.ID, name, email)
}
