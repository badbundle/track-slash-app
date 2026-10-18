package server_test

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/bradleymackey/track-slash/internal/mailer"
	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/server"
	"github.com/bradleymackey/track-slash/internal/store"
)

const (
	emailTestOrigin   = "https://track.example.com"
	emailTestPassword = "correct-horse-battery"
	emailConfirmURL   = emailTestOrigin + "/settings/email/confirm?token="
)

// newEmailEnv is an app with email on, sending into a capturing mailer.
func newEmailEnv(t *testing.T) (*httpEnv, *mailer.Capturing) {
	t.Helper()
	mail := mailer.NewCapturing()
	return newHTTPEnvWithOptions(t, server.Options{Mailer: mail, PublicOrigin: emailTestOrigin}), mail
}

type emailAccount struct {
	user    model.User
	session string
}

func newEmailAccount(t *testing.T, e *httpEnv, prefix string) emailAccount {
	t.Helper()
	username, session := newUIPasswordAccount(t, e, prefix, emailTestPassword)
	user, err := e.store.GetUserByUsername(e.ctx, username)
	if err != nil {
		t.Fatalf("GetUserByUsername: %v", err)
	}
	return emailAccount{user: user, session: session}
}

// reauth issues a reauthentication token the way the password prompt does,
// without spending the route's rate limit.
func (a emailAccount) reauth(t *testing.T, e *httpEnv) string {
	t.Helper()
	token, err := e.store.CreatePasskeyReauthToken(e.ctx, a.user.ID)
	if err != nil {
		t.Fatalf("CreatePasskeyReauthToken: %v", err)
	}
	return token
}

func postEmailForm(t *testing.T, e *httpEnv, session, path string, form url.Values) (int, string, http.Header) {
	t.Helper()
	res := e.uiDoNoRedirect(t, http.MethodPost, path, session, strings.NewReader(form.Encode()))
	defer res.Body.Close()
	return res.StatusCode, readBody(t, res), res.Header
}

func changeEmail(t *testing.T, e *httpEnv, a emailAccount, address string) (int, string) {
	t.Helper()
	code, body, _ := postEmailForm(t, e, a.session, "/settings/email", url.Values{"email": {address}, "reauth_token": {a.reauth(t, e)}})
	return code, body
}

func resendEmail(t *testing.T, e *httpEnv, a emailAccount) (int, string, http.Header) {
	t.Helper()
	return postEmailForm(t, e, a.session, "/settings/email/resend", url.Values{})
}

func confirmEmail(t *testing.T, e *httpEnv, a emailAccount, token string) (int, string) {
	t.Helper()
	code, body, _ := postEmailForm(t, e, a.session, "/settings/email/confirm", url.Values{"token": {token}})
	return code, body
}

func lastEmail(t *testing.T, mail *mailer.Capturing) mailer.Message {
	t.Helper()
	messages := mail.Messages()
	if len(messages) == 0 {
		t.Fatal("no email was sent")
	}
	return messages[len(messages)-1]
}

// confirmToken reads the token out of a confirmation email's link.
func confirmToken(t *testing.T, msg mailer.Message) string {
	t.Helper()
	start := strings.Index(msg.Text, emailConfirmURL)
	if start < 0 {
		t.Fatalf("confirmation email has no %s link: %q", emailConfirmURL, msg.Text)
	}
	rest := msg.Text[start+len(emailConfirmURL):]
	raw := rest[:strings.IndexAny(rest, " \n")]
	token, err := url.QueryUnescape(raw)
	if err != nil || token == "" {
		t.Fatalf("confirmation token %q: %v", raw, err)
	}
	return token
}

func emailStatus(t *testing.T, e *httpEnv, userID uuid.UUID) model.EmailStatus {
	t.Helper()
	status, err := e.store.GetEmailStatus(e.ctx, userID)
	if err != nil {
		t.Fatalf("GetEmailStatus: %v", err)
	}
	return status
}

func requireBody(t *testing.T, what, body string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(body, w) {
			t.Fatalf("%s missing %q: %s", what, w, body)
		}
	}
}

func rejectBody(t *testing.T, what, body string, notWant ...string) {
	t.Helper()
	for _, w := range notWant {
		if strings.Contains(body, w) {
			t.Fatalf("%s still shows %q: %s", what, w, body)
		}
	}
}

func uniqueAddress(label string) string {
	return label + "-" + uuid.NewString()[:8] + "@example.com"
}

// With email unset the app is as it was: the profile form keeps its email
// field, there is no Email card, and the email routes do not exist.
func TestEmailOffKeepsTheAppAsItWas(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name    string
		options server.Options
	}{
		{name: "unset", options: server.Options{}},
		// Links are built only from the public origin, so a mailer without
		// one does not turn email on.
		{name: "no public origin", options: server.Options{Mailer: mailer.NewCapturing()}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			e := newHTTPEnvWithOptions(t, tt.options)
			a := newEmailAccount(t, e, "emailoff")
			body := e.uiGet(t, "/settings/profile", a.session)
			requireBody(t, "profile", body, `for="email">Email`, `name="email" type="email"`)
			rejectBody(t, "profile", body, "data-email-panel", "Verified", "Not verified", "/settings/email")

			for _, tc := range []struct{ method, path string }{
				{http.MethodPost, "/settings/email"},
				{http.MethodPost, "/settings/email/resend"},
				{http.MethodGet, "/settings/email/confirm?token=x"},
				{http.MethodPost, "/settings/email/confirm"},
			} {
				var payload io.Reader
				if tc.method == http.MethodPost {
					payload = strings.NewReader(url.Values{"email": {"a@example.com"}}.Encode())
				}
				res := e.uiDoNoRedirect(t, tc.method, tc.path, a.session, payload)
				res.Body.Close()
				if res.StatusCode != http.StatusNotFound {
					t.Fatalf("%s %s with email off = %d, want 404", tc.method, tc.path, res.StatusCode)
				}
			}

			// The profile form sets the address directly, unverified, as before.
			address := uniqueAddress("off")
			code, body, _ := postEmailForm(t, e, a.session, "/settings/profile", url.Values{"name": {"Off"}, "email": {address}})
			if code != http.StatusOK || !strings.Contains(body, "Profile saved.") || !strings.Contains(body, address) {
				t.Fatalf("profile save = %d %s", code, body)
			}
			if status := emailStatus(t, e, a.user.ID); status.Email != address || status.Verified() {
				t.Fatalf("status = %+v", status)
			}
		})
	}
}

// Changing the email sends a link to the new address; opening the link only
// shows a button, and the button's POST verifies the address.
func TestEmailChangeIsConfirmedThroughTheLink(t *testing.T) {
	t.Parallel()
	e, mail := newEmailEnv(t)
	a := newEmailAccount(t, e, "emailchange")

	body := e.uiGet(t, "/settings/profile", a.session)
	requireBody(t, "profile", body, `data-email-panel`, "No email", "Add email", `action="/settings/email"`, `data-email-change-form`, `data-passkey-password-modal`, `aria-labelledby="email-password-title"`, "Required before changing your email.", "with your password first")
	rejectBody(t, "profile", body, `for="email">Email`, `data-email-pending`, `data-email-remove`)
	assertPopulatedCSRFTokens(t, "/settings/profile", body)

	address := uniqueAddress("new")
	code, body := changeEmail(t, e, a, address)
	if code != http.StatusOK {
		t.Fatalf("change email = %d %s", code, body)
	}
	// The account has no address in effect until this one is confirmed.
	requireBody(t, "change result", body, "We sent a link to "+address, `data-email-pending="sent"`, "Waiting for you to confirm", "Resend", "No email", "Add email")
	msg := lastEmail(t, mail)
	if msg.To != address || msg.Subject != "Confirm your email for Trackslash" || !strings.Contains(msg.Text, "@"+a.user.Username) || !strings.Contains(msg.HTML, `href="`+emailConfirmURL) {
		t.Fatalf("confirmation email = %+v", msg)
	}
	for _, part := range []string{msg.Text, msg.HTML} {
		if strings.Contains(part, "<img") || strings.Count(part, "http") != strings.Count(part, emailTestOrigin) {
			t.Fatalf("confirmation email links outside the public origin or embeds images: %s", part)
		}
	}
	// Until it is confirmed, the account keeps the address it had.
	status := emailStatus(t, e, a.user.ID)
	if status.Email != "" || status.Verified() || status.Pending == nil || !status.Pending.Sent || status.Pending.Email != address {
		t.Fatalf("status before confirming = %+v", status)
	}

	// Opening the link, as a mail scanner would, verifies nothing.
	token := confirmToken(t, msg)
	link := strings.TrimPrefix(emailConfirmURL, emailTestOrigin) + url.QueryEscape(token)
	for i := 0; i < 2; i++ {
		page := e.uiGet(t, link, a.session)
		requireBody(t, "confirm page", page, `data-email-confirm="confirm"`, "Confirm this email", address, `name="token" value="`+token+`"`, `action="/settings/email/confirm"`, `data-sidebar-view="profile"`)
		assertPopulatedCSRFTokens(t, link, page)
	}
	if status := emailStatus(t, e, a.user.ID); status.Email != "" || status.Verified() {
		t.Fatalf("GET of the link changed the email: %+v", status)
	}

	code, body = confirmEmail(t, e, a, token)
	if code != http.StatusOK {
		t.Fatalf("confirm = %d %s", code, body)
	}
	requireBody(t, "confirmed page", body, `data-email-confirm="confirmed"`, address+"</span> is now your verified email.", `href="/settings/profile#email"`)
	status = emailStatus(t, e, a.user.ID)
	if status.Email != address || !status.Verified() || status.Pending != nil {
		t.Fatalf("status after confirming = %+v", status)
	}
	if len(mail.Messages()) != 1 {
		t.Fatalf("a first address got a change notice: %+v", mail.Messages())
	}
	body = e.uiGet(t, "/settings/profile", a.session)
	requireBody(t, "profile after confirming", body, `data-email-current`, address, `data-email-verified="true"`, "Verified", "New email", "Change email", `data-email-remove`)
	rejectBody(t, "profile after confirming", body, `data-email-pending`, "Send confirmation link")

	// The link works once.
	code, body = confirmEmail(t, e, a, token)
	if code != http.StatusBadRequest || !strings.Contains(body, `data-email-confirm="unusable"`) || !strings.Contains(body, "expired or has already been used") {
		t.Fatalf("reused link = %d %s", code, body)
	}
	requireBody(t, "used link page", e.uiGet(t, link, a.session), `data-email-confirm="unusable"`)

	// Asking for the address already in effect sends nothing.
	before := mail.Attempts()
	code, body = changeEmail(t, e, a, address)
	if code != http.StatusOK || !strings.Contains(body, "That&#39;s already your email.") || mail.Attempts() != before {
		t.Fatalf("same address = %d (attempts %d→%d) %s", code, before, mail.Attempts(), body)
	}
	// So does resending once everything is confirmed.
	code, body, _ = resendEmail(t, e, a)
	if code != http.StatusOK || !strings.Contains(body, "There&#39;s nothing to confirm.") || mail.Attempts() != before {
		t.Fatalf("resend with nothing waiting = %d %s", code, body)
	}
}

// The verified address that is replaced or removed is told about it.
func TestEmailChangeNoticeGoesToTheOldVerifiedAddress(t *testing.T) {
	t.Parallel()
	e, mail := newEmailEnv(t)
	a := newEmailAccount(t, e, "emailnotice")

	first := uniqueAddress("first")
	changeEmail(t, e, a, first)
	confirmEmail(t, e, a, confirmToken(t, lastEmail(t, mail)))

	second := uniqueAddress("second")
	changeEmail(t, e, a, second)
	token := confirmToken(t, lastEmail(t, mail))
	if code, body := confirmEmail(t, e, a, token); code != http.StatusOK {
		t.Fatalf("confirm second = %d %s", code, body)
	}
	notice := lastEmail(t, mail)
	if notice.To != first || notice.Subject != "Your Trackslash email was changed" || !strings.Contains(notice.Text, "@"+a.user.Username) || !strings.Contains(notice.Text, emailTestOrigin+"/settings/login") || strings.Contains(notice.Text, second) {
		t.Fatalf("change notice = %+v", notice)
	}

	// A notice that can't be sent doesn't undo the change.
	third := uniqueAddress("third")
	changeEmail(t, e, a, third)
	token = confirmToken(t, lastEmail(t, mail))
	mail.Fail(errors.New("421 service not available"))
	if code, body := confirmEmail(t, e, a, token); code != http.StatusOK || !strings.Contains(body, "is now your verified email.") {
		t.Fatalf("confirm with notice failing = %d %s", code, body)
	}
	if status := emailStatus(t, e, a.user.ID); status.Email != third || !status.Verified() {
		t.Fatalf("status after a failed notice = %+v", status)
	}
	mail.Fail(nil)

	// Removing the email tells the address that was removed.
	code, body, _ := postEmailForm(t, e, a.session, "/settings/email", url.Values{"email": {""}, "reauth_token": {a.reauth(t, e)}})
	if code != http.StatusOK || !strings.Contains(body, "Email removed.") || !strings.Contains(body, "No email") {
		t.Fatalf("remove = %d %s", code, body)
	}
	if notice := lastEmail(t, mail); notice.To != third || notice.Subject != "Your Trackslash email was changed" {
		t.Fatalf("removal notice = %+v", notice)
	}
	if status := emailStatus(t, e, a.user.ID); status.Email != "" || status.Verified() {
		t.Fatalf("status after removal = %+v", status)
	}
	// Removing an address nobody verified tells nobody.
	sent := len(mail.Messages())
	if _, err := e.store.UpdateUserProfile(e.ctx, a.user.ID, "Notice", uniqueAddress("unverified")); err != nil {
		t.Fatalf("UpdateUserProfile: %v", err)
	}
	if code, body, _ := postEmailForm(t, e, a.session, "/settings/email", url.Values{"email": {""}, "reauth_token": {a.reauth(t, e)}}); code != http.StatusOK || len(mail.Messages()) != sent {
		t.Fatalf("remove unverified = %d (%d messages) %s", code, len(mail.Messages()), body)
	}
}

func TestEmailChangeRequiresReauthentication(t *testing.T) {
	t.Parallel()
	e, mail := newEmailEnv(t)
	a := newEmailAccount(t, e, "emailreauth")
	address := uniqueAddress("reauth")

	for _, tt := range []struct {
		name  string
		token string
	}{
		{name: "missing", token: ""},
		{name: "wrong", token: "not-a-reauth-token"},
	} {
		code, body, _ := postEmailForm(t, e, a.session, "/settings/email", url.Values{"email": {address}, "reauth_token": {tt.token}})
		if code != http.StatusForbidden || !strings.Contains(body, "Confirm it&#39;s you to change your email.") || !strings.Contains(body, `value="`+address+`"`) {
			t.Fatalf("%s reauth = %d %s", tt.name, code, body)
		}
	}
	// Removing the email needs it too.
	if _, err := e.store.UpdateUserProfile(e.ctx, a.user.ID, "Reauth", address); err != nil {
		t.Fatalf("UpdateUserProfile: %v", err)
	}
	if code, body, _ := postEmailForm(t, e, a.session, "/settings/email", url.Values{"email": {""}}); code != http.StatusForbidden {
		t.Fatalf("remove without reauth = %d %s", code, body)
	}
	if mail.Attempts() != 0 {
		t.Fatalf("sent %d emails without reauthentication", mail.Attempts())
	}
	if status := emailStatus(t, e, a.user.ID); status.Email != address || status.Pending != nil {
		t.Fatalf("status after refused changes = %+v", status)
	}

	// The real password prompt's token works once.
	res := e.uiDoNoRedirectWithHeaders(t, http.MethodPost, "/settings/passkeys/reauth/password", a.session, strings.NewReader(`{"current_password":"`+emailTestPassword+`"}`), map[string]string{"Content-Type": "application/json"})
	raw := readBody(t, res)
	res.Body.Close()
	var reauth struct {
		ReauthToken string `json:"reauth_token"`
	}
	if err := json.Unmarshal([]byte(raw), &reauth); err != nil || res.StatusCode != http.StatusCreated || reauth.ReauthToken == "" {
		t.Fatalf("password reauth = %d %s (%v)", res.StatusCode, raw, err)
	}
	form := url.Values{"email": {uniqueAddress("prompted")}, "reauth_token": {reauth.ReauthToken}}
	if code, body, _ := postEmailForm(t, e, a.session, "/settings/email", form); code != http.StatusOK {
		t.Fatalf("change with password reauth = %d %s", code, body)
	}
	if code, _, _ := postEmailForm(t, e, a.session, "/settings/email", form); code != http.StatusForbidden {
		t.Fatalf("reused reauth token = %d, want 403", code)
	}
}

// A passkey-only account confirms it's itself with a passkey, so the card has
// no password prompt.
func TestEmailCardForAPasskeyOnlyAccount(t *testing.T) {
	t.Parallel()
	e, _ := newEmailEnv(t)
	a := newEmailAccount(t, e, "emailpasskey")
	if _, err := e.store.AddPasskeyCredential(e.ctx, a.user.ID, "localhost", "Laptop", serverPasskeyCredential("credential-"+uniqueProjectKey(t), 1)); err != nil {
		t.Fatalf("AddPasskeyCredential: %v", err)
	}
	if _, err := e.store.SetPasswordLoginEnabled(e.ctx, a.user.ID, false); err != nil {
		t.Fatalf("SetPasswordLoginEnabled: %v", err)
	}
	body := e.uiGet(t, "/settings/profile", a.session)
	requireBody(t, "profile", body, "data-email-panel", "with your passkey first")
	rejectBody(t, "profile", body, "data-passkey-password-modal", "Enter current password")
}

func TestEmailChangeValidation(t *testing.T) {
	t.Parallel()
	e, mail := newEmailEnv(t)
	a := newEmailAccount(t, e, "emailvalid")
	reauth := a.reauth(t, e)
	for _, bad := range []string{"not-an-address", "Ada <ada@example.com>", "a@example.com, b@example.com"} {
		code, body, _ := postEmailForm(t, e, a.session, "/settings/email", url.Values{"email": {bad}, "reauth_token": {reauth}})
		if code != http.StatusBadRequest || !strings.Contains(body, "Enter one email address, such as you@example.com.") {
			t.Fatalf("change to %q = %d %s", bad, code, body)
		}
	}
	// A refused address doesn't use up the reauthentication.
	if code, body, _ := postEmailForm(t, e, a.session, "/settings/email", url.Values{"email": {uniqueAddress("valid")}, "reauth_token": {reauth}}); code != http.StatusOK {
		t.Fatalf("change after invalid attempts = %d %s", code, body)
	}
	res := e.uiDoNoRedirect(t, http.MethodPost, "/settings/email", a.session, strings.NewReader("email=%zz"))
	body := readBody(t, res)
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest || !strings.Contains(body, "Unable to read form.") {
		t.Fatalf("malformed change form = %d %s", res.StatusCode, body)
	}
	res = e.uiDoNoRedirect(t, http.MethodPost, "/settings/email/confirm", a.session, strings.NewReader("token=%zz"))
	body = readBody(t, res)
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest || !strings.Contains(body, `data-email-confirm="unusable"`) {
		t.Fatalf("malformed confirm form = %d %s", res.StatusCode, body)
	}
	if mail.Attempts() != 1 {
		t.Fatalf("attempts = %d, want 1", mail.Attempts())
	}
}

// A link stops working after an hour, after it is used, and when a newer one
// replaces it; and it only ever works for the account it was sent for.
func TestEmailConfirmationLinkExpiry(t *testing.T) {
	t.Parallel()
	e, mail := newEmailEnv(t)
	a := newEmailAccount(t, e, "emailexpiry")
	other := newEmailAccount(t, e, "emailother")

	address := uniqueAddress("expiring")
	changeEmail(t, e, a, address)
	token := confirmToken(t, lastEmail(t, mail))

	// Another account can neither see nor use the link.
	link := "/settings/email/confirm?token=" + url.QueryEscape(token)
	page := e.uiGet(t, link, other.session)
	requireBody(t, "other account's confirm page", page, `data-email-confirm="unusable"`)
	rejectBody(t, "other account's confirm page", page, address, token)
	if code, _ := confirmEmail(t, e, other, token); code != http.StatusBadRequest {
		t.Fatalf("other account's confirm = %d", code)
	}

	if _, err := e.pool.Exec(e.ctx, `UPDATE email_verifications SET created_at = now() - interval '61 minutes', expires_at = now() - interval '1 minute' WHERE user_id = $1`, a.user.ID); err != nil {
		t.Fatalf("expire link: %v", err)
	}
	requireBody(t, "expired confirm page", e.uiGet(t, link, a.session), `data-email-confirm="unusable"`, "expired or has already been used")
	code, body := confirmEmail(t, e, a, token)
	if code != http.StatusBadRequest || !strings.Contains(body, "expired or has already been used") {
		t.Fatalf("expired confirm = %d %s", code, body)
	}
	if status := emailStatus(t, e, a.user.ID); status.Email == address || status.Verified() || status.Pending != nil {
		t.Fatalf("status after expiry = %+v", status)
	}
	if body := e.uiGet(t, "/settings/profile", a.session); strings.Contains(body, "data-email-pending") {
		t.Fatalf("expired link still shown as waiting: %s", body)
	}

	// A resend replaces the link it resends.
	changeEmail(t, e, a, address)
	older := confirmToken(t, lastEmail(t, mail))
	if code, body, _ := resendEmail(t, e, a); code != http.StatusOK || !strings.Contains(body, "We sent a link to "+address) {
		t.Fatalf("resend = %d %s", code, body)
	}
	newer := confirmToken(t, lastEmail(t, mail))
	if code, _ := confirmEmail(t, e, a, older); code != http.StatusBadRequest {
		t.Fatalf("replaced link = %d, want 400", code)
	}
	if code, _ := confirmEmail(t, e, a, newer); code != http.StatusOK {
		t.Fatalf("newest link = %d", code)
	}

	// Signed out, the link goes through sign-in and comes back with its token.
	res := e.uiDoNoRedirect(t, http.MethodGet, link, "", nil)
	res.Body.Close()
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/login?next="+url.QueryEscape(link) {
		t.Fatalf("signed-out link = %d %q", res.StatusCode, res.Header.Get("Location"))
	}
}

// Confirmation emails are limited per account and per recipient.
func TestEmailConfirmationRateLimits(t *testing.T) {
	t.Parallel()
	e, mail := newEmailEnv(t)
	a := newEmailAccount(t, e, "emaillimit")
	target := uniqueAddress("target")

	if code, body := changeEmail(t, e, a, target); code != http.StatusOK {
		t.Fatalf("change = %d %s", code, body)
	}
	for i := 0; i < 2; i++ {
		if code, body, _ := resendEmail(t, e, a); code != http.StatusOK {
			t.Fatalf("resend %d = %d %s", i, code, body)
		}
	}
	code, body, header := resendEmail(t, e, a)
	if code != http.StatusTooManyRequests || !strings.Contains(body, "Too many confirmation emails. Try again in 60 minutes.") || header.Get("Retry-After") == "" {
		t.Fatalf("fourth send = %d retry-after %q %s", code, header.Get("Retry-After"), body)
	}
	if mail.Attempts() != 3 {
		t.Fatalf("attempts = %d, want 3", mail.Attempts())
	}
	// The account's budget covers every address it sends to.
	if code, body := changeEmail(t, e, a, uniqueAddress("elsewhere")); code != http.StatusTooManyRequests {
		t.Fatalf("send to another address = %d %s", code, body)
	}

	// The recipient's budget holds whichever account asks.
	b := newEmailAccount(t, e, "emailneighbour")
	if code, body := changeEmail(t, e, b, strings.ToUpper(target)); code != http.StatusTooManyRequests {
		t.Fatalf("another account to the same address = %d %s", code, body)
	}
	// A refused send spends nothing from the other budget.
	for i := 0; i < 3; i++ {
		if code, body := changeEmail(t, e, b, uniqueAddress("fresh")); code != http.StatusOK {
			t.Fatalf("other account send %d = %d %s", i, code, body)
		}
	}
	if mail.Attempts() != 6 {
		t.Fatalf("attempts = %d, want 6", mail.Attempts())
	}
}

// When the provider is down or refuses the message, the page says so, the
// user can try again, and nothing is marked sent or verified.
func TestEmailSendFailureCanBeRetried(t *testing.T) {
	t.Parallel()
	e, mail := newEmailEnv(t)
	a := newEmailAccount(t, e, "emailfail")
	address := uniqueAddress("fail")

	mail.Fail(errors.New("554 5.7.0 daily sending quota exceeded"))
	code, body := changeEmail(t, e, a, address)
	if code != http.StatusBadGateway || !strings.Contains(body, "Couldn&#39;t send the email. Try again.") {
		t.Fatalf("refused send = %d %s", code, body)
	}
	requireBody(t, "refused send", body, `data-email-pending="unsent"`, "No link has reached", "Resend")
	rejectBody(t, "refused send", body, "We sent a link", `data-email-verified="true"`)
	status := emailStatus(t, e, a.user.ID)
	if status.Email != "" || status.Verified() || status.Pending == nil || status.Pending.Sent {
		t.Fatalf("status after a refused send = %+v", status)
	}
	if len(mail.Messages()) != 0 {
		t.Fatalf("refused messages were kept: %+v", mail.Messages())
	}

	mail.Fail(nil)
	if code, body, _ := resendEmail(t, e, a); code != http.StatusOK || !strings.Contains(body, `data-email-pending="sent"`) {
		t.Fatalf("retry = %d %s", code, body)
	}
	if code, _ := confirmEmail(t, e, a, confirmToken(t, lastEmail(t, mail))); code != http.StatusOK {
		t.Fatalf("confirm after retry = %d", code)
	}
	if status := emailStatus(t, e, a.user.ID); status.Email != address || !status.Verified() {
		t.Fatalf("status after retry = %+v", status)
	}
}

// An address waiting for confirmation blocks nobody; only a verified one is
// unique, regardless of case.
func TestPendingEmailDoesNotBlockAnotherAccount(t *testing.T) {
	t.Parallel()
	e, mail := newEmailEnv(t)
	a := newEmailAccount(t, e, "emailfirst")
	b := newEmailAccount(t, e, "emailsecond")
	shared := uniqueAddress("Shared")

	changeEmail(t, e, a, shared)
	aToken := confirmToken(t, lastEmail(t, mail))
	if code, body := changeEmail(t, e, b, strings.ToLower(shared)); code != http.StatusOK || !strings.Contains(body, "We sent a link") {
		t.Fatalf("second account asking for a pending address = %d %s", code, body)
	}
	bToken := confirmToken(t, lastEmail(t, mail))
	if code, body := confirmEmail(t, e, b, bToken); code != http.StatusOK {
		t.Fatalf("second account confirm = %d %s", code, body)
	}

	code, body := confirmEmail(t, e, a, aToken)
	if code != http.StatusConflict || !strings.Contains(body, `data-email-confirm="taken"`) || !strings.Contains(body, "Another account has already verified this address") {
		t.Fatalf("confirming an address another account verified = %d %s", code, body)
	}
	if status := emailStatus(t, e, a.user.ID); status.Email != "" || status.Verified() {
		t.Fatalf("first account after the conflict = %+v", status)
	}
}

// Links are built from TRACK_SLASH_PUBLIC_ORIGIN, never from the request.
func TestEmailLinksUseThePublicOriginUnderASpoofedHost(t *testing.T) {
	t.Parallel()
	e, mail := newEmailEnv(t)
	a := newEmailAccount(t, e, "emailhost")
	form := url.Values{"email": {uniqueAddress("host")}, "reauth_token": {a.reauth(t, e)}}
	req, err := http.NewRequestWithContext(e.ctx, http.MethodPost, e.ts.URL+"/settings/email", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Host = "evil.example.net"
	req.Header.Set("X-Forwarded-Host", "evil.example.net")
	req.Header.Set("X-Forwarded-Proto", "http")
	req.Header.Set("Forwarded", "host=evil.example.net;proto=http")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-CSRF-Token", uiCSRFTokenForTest("session", a.session))
	req.AddCookie(&http.Cookie{Name: uiCookieNameForTest, Value: a.session})
	res, err := e.ts.Client().Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	body := readBody(t, res)
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("change with a spoofed host = %d %s", res.StatusCode, body)
	}
	msg := lastEmail(t, mail)
	if !strings.Contains(msg.Text, emailConfirmURL) || !strings.Contains(msg.HTML, `href="`+emailConfirmURL) {
		t.Fatalf("link is not on the public origin: %+v", msg)
	}
	for _, part := range []string{msg.Text, msg.HTML} {
		if strings.Contains(part, "evil.example.net") || strings.Contains(part, "127.0.0.1") {
			t.Fatalf("link was built from the request: %s", part)
		}
	}
}

// An address that was set before email was on, or by an admin, starts
// unverified and can be confirmed without changing it.
func TestEmailConfirmsAnExistingUnverifiedAddress(t *testing.T) {
	t.Parallel()
	e, mail := newEmailEnv(t)
	a := newEmailAccount(t, e, "emaillegacy")
	legacy := uniqueAddress("legacy")
	if _, err := e.store.UpdateUserProfile(e.ctx, a.user.ID, "Legacy", legacy); err != nil {
		t.Fatalf("UpdateUserProfile: %v", err)
	}
	body := e.uiGet(t, "/settings/profile", a.session)
	requireBody(t, "legacy profile", body, legacy, `data-email-verified="false"`, "Not verified", "Send confirmation link", `action="/settings/email/resend"`)

	code, body, _ := resendEmail(t, e, a)
	if code != http.StatusOK || lastEmail(t, mail).To != legacy {
		t.Fatalf("send confirmation for the existing address = %d %s", code, body)
	}
	rejectBody(t, "legacy profile after sending", body, "Send confirmation link")
	if code, _ := confirmEmail(t, e, a, confirmToken(t, lastEmail(t, mail))); code != http.StatusOK {
		t.Fatalf("confirm existing address = %d", code)
	}
	if status := emailStatus(t, e, a.user.ID); status.Email != legacy || !status.Verified() {
		t.Fatalf("status = %+v", status)
	}

	// An address that can't receive mail is refused rather than sent to.
	if _, err := e.store.UpdateUserProfile(e.ctx, a.user.ID, "Legacy", "odd@"); err != nil {
		t.Fatalf("UpdateUserProfile odd: %v", err)
	}
	attempts := mail.Attempts()
	if code, body, _ := resendEmail(t, e, a); code != http.StatusBadRequest || !strings.Contains(body, "Enter one email address") || mail.Attempts() != attempts {
		t.Fatalf("resend to an address that can't receive mail = %d %s", code, body)
	}
}

// While email is on, the address changes only through a confirmed link: the
// profile form saves the name alone, and the API and MCP refuse a new address.
func TestEmailOnlyChangesThroughConfirmation(t *testing.T) {
	t.Parallel()
	e, mail := newEmailEnv(t)
	a := newEmailAccount(t, e, "emailonly")
	address := uniqueAddress("only")
	changeEmail(t, e, a, address)
	confirmEmail(t, e, a, confirmToken(t, lastEmail(t, mail)))

	code, body, _ := postEmailForm(t, e, a.session, "/settings/profile", url.Values{"name": {"Renamed"}, "email": {uniqueAddress("ignored")}})
	if code != http.StatusOK || !strings.Contains(body, "Profile saved.") {
		t.Fatalf("profile save = %d %s", code, body)
	}
	if status := emailStatus(t, e, a.user.ID); status.Email != address || !status.Verified() {
		t.Fatalf("profile form changed the email: %+v", status)
	}

	api, err := e.store.CreateAuthToken(e.ctx, store.CreateAuthTokenParams{UserID: a.user.ID, Kind: model.AuthTokenKindAPI, Name: "agent"})
	if err != nil {
		t.Fatalf("CreateAuthToken: %v", err)
	}
	code, raw := e.doWithToken(t, api.RawToken, http.MethodPatch, "/me/settings", map[string]any{"email": uniqueAddress("api")})
	if code != http.StatusBadRequest || !strings.Contains(string(raw), "Profile page") {
		t.Fatalf("API email change = %d %s", code, raw)
	}
	code, raw = e.doWithToken(t, api.RawToken, http.MethodPatch, "/me/settings", map[string]any{"name": "From API", "email": address})
	if code != http.StatusOK || decode[model.User](t, raw).Name != "From API" {
		t.Fatalf("API name change = %d %s", code, raw)
	}
	session := mcpConnect(t, e, api.RawToken)
	requireMCPErrorCode(t, mcpCallExpectError(t, e, session, "track_update_my_settings", map[string]any{"email": uniqueAddress("mcp")}), "validation_error")
	out := mcpCall(t, e, session, "track_update_my_settings", map[string]any{"name": "From MCP"})
	if decodeMCPField[model.User](t, out, "user").Name != "From MCP" {
		t.Fatalf("MCP name change = %s", out["user"])
	}
	if status := emailStatus(t, e, a.user.ID); status.Email != address || !status.Verified() {
		t.Fatalf("API or MCP changed the email: %+v", status)
	}
}

// With email off the API and MCP set an email directly, as before.
func TestEmailOffAPIStillSetsTheEmail(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	user, token := e.mustUserToken(t, "apiemail")
	address := uniqueAddress("apidirect")
	code, raw := e.doWithToken(t, token, http.MethodPatch, "/me/settings", map[string]any{"email": address})
	if code != http.StatusOK || decode[model.User](t, raw).Email != address {
		t.Fatalf("API email change = %d %s", code, raw)
	}
	mcpAddress := uniqueAddress("mcpdirect")
	out := mcpCall(t, e, mcpConnect(t, e, token), "track_update_my_settings", map[string]any{"email": mcpAddress})
	if decodeMCPField[model.User](t, out, "user").Email != mcpAddress {
		t.Fatalf("MCP email change = %s", out["user"])
	}
	if got, err := e.store.GetUser(e.ctx, user.ID); err != nil || got.Email != mcpAddress {
		t.Fatalf("user = %+v, %v", got, err)
	}
}
