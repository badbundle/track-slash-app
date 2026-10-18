package server_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
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
	resetLinkPrefix  = emailTestOrigin + "/reset-password?token="
	resetNewPassword = "brand-new-horse-battery"
	resetSentAnswer  = "If that account has a verified email, we've sent a reset link."
	resetExpired     = "This link has expired. Ask for a new one."
)

// resetBrowser is a signed-out browser on the public origin. It keeps the
// pre-auth CSRF cookie the sign-in page set, as a browser would.
type resetBrowser struct {
	e      *httpEnv
	cookie *http.Cookie
}

func newResetBrowser(t *testing.T, e *httpEnv) *resetBrowser {
	t.Helper()
	res := e.uiDoNoRedirect(t, http.MethodGet, "/login", "", nil)
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET /login = %d", res.StatusCode)
	}
	return &resetBrowser{e: e, cookie: findUICookieNamed(t, res.Cookies(), uiPreAuthCookieNameForTest)}
}

func (b *resetBrowser) do(t *testing.T, method, path string, form url.Values, headers map[string]string) (int, string, http.Header) {
	t.Helper()
	var body *strings.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	} else {
		body = strings.NewReader("")
	}
	req, err := http.NewRequestWithContext(b.e.ctx, method, b.e.ts.URL+path, body)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Origin", emailTestOrigin)
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	req.AddCookie(b.cookie)
	client := *b.e.ts.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	res, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer res.Body.Close()
	return res.StatusCode, readBody(t, res), res.Header
}

func (b *resetBrowser) get(t *testing.T, path string) (int, string, http.Header) {
	t.Helper()
	return b.do(t, http.MethodGet, path, nil, nil)
}

func (b *resetBrowser) post(t *testing.T, path string, form url.Values) (int, string, http.Header) {
	t.Helper()
	form.Set("csrf_token", uiCSRFTokenForTest("pre-auth", b.cookie.Value))
	return b.do(t, http.MethodPost, path, form, nil)
}

func (b *resetBrowser) forgot(t *testing.T, identifier string) (int, string) {
	t.Helper()
	code, body, _ := b.post(t, "/forgot-password", url.Values{"identifier": {identifier}})
	return code, body
}

func (b *resetBrowser) reset(t *testing.T, token, password, confirm string) (int, string, http.Header) {
	t.Helper()
	return b.post(t, "/reset-password", url.Values{"token": {token}, "password": {password}, "password_confirm": {confirm}})
}

// waitForEmail waits for the work the requests so far left running after
// their responses, such as sending a reset email.
func waitForEmail(t *testing.T, e *httpEnv) {
	t.Helper()
	if err := e.srv.WaitBackground(e.ctx); err != nil {
		t.Fatalf("WaitBackground: %v", err)
	}
}

// newResetAccount is a password account with a verified email.
func newResetAccount(t *testing.T, e *httpEnv, prefix string) (emailAccount, string) {
	t.Helper()
	a := newEmailAccount(t, e, prefix)
	address := uniqueAddress(prefix)
	verifyAddress(t, e, a.user.ID, address)
	return a, address
}

func verifyAddress(t *testing.T, e *httpEnv, userID uuid.UUID, address string) {
	t.Helper()
	created, err := e.store.CreateEmailVerification(e.ctx, userID, address)
	if err != nil {
		t.Fatalf("CreateEmailVerification: %v", err)
	}
	if _, err := e.store.ConfirmEmailVerification(e.ctx, userID, created.Token); err != nil {
		t.Fatalf("ConfirmEmailVerification: %v", err)
	}
}

func messagesTo(mail *mailer.Capturing, address string) []mailer.Message {
	var out []mailer.Message
	for _, msg := range mail.Messages() {
		if msg.To == address {
			out = append(out, msg)
		}
	}
	return out
}

// resetToken reads the token out of a reset email's link.
func resetToken(t *testing.T, msg mailer.Message) string {
	t.Helper()
	start := strings.Index(msg.Text, resetLinkPrefix)
	if start < 0 {
		t.Fatalf("email has no %s link: %q", resetLinkPrefix, msg.Text)
	}
	rest := msg.Text[start+len(resetLinkPrefix):]
	raw := rest[:strings.IndexAny(rest, " \n")]
	token, err := url.QueryUnescape(raw)
	if err != nil || token == "" {
		t.Fatalf("reset token %q: %v", raw, err)
	}
	return token
}

func resetLinkPath(token string) string {
	return "/reset-password?token=" + url.QueryEscape(token)
}

// With email off there is nothing to send a reset to: no link on the sign-in
// page, and no reset routes.
func TestPasswordResetIsOffWithoutEmail(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name    string
		options server.Options
	}{
		{name: "unset", options: server.Options{}},
		{name: "no public origin", options: server.Options{Mailer: mailer.NewCapturing()}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			e := newHTTPEnvWithOptions(t, tt.options)
			rejectBody(t, "login", e.uiGet(t, "/login", ""), "Forgot password?", "/forgot-password")
			rejectBody(t, "login after a reset", e.uiGet(t, "/login?password_reset=done", ""), "Password updated", "data-login-notice")
			b := newResetBrowser(t, e)
			for _, tc := range []struct{ method, path string }{
				{http.MethodGet, "/forgot-password"},
				{http.MethodPost, "/forgot-password"},
				{http.MethodGet, "/reset-password?token=x"},
				{http.MethodPost, "/reset-password"},
			} {
				var code int
				if tc.method == http.MethodPost {
					code, _, _ = b.post(t, tc.path, url.Values{"identifier": {"anyone"}, "token": {"x"}})
				} else {
					code, _, _ = b.get(t, tc.path)
				}
				if code != http.StatusNotFound {
					t.Fatalf("%s %s with email off = %d, want 404", tc.method, tc.path, code)
				}
			}
		})
	}
}

// Someone with a verified email resets their password from the sign-in page,
// signs in with the new one, and every other session is signed out. Opening
// the link doesn't use it up; only setting the password does.
func TestPasswordResetFromTheSignInPage(t *testing.T) {
	t.Parallel()
	e, mail := newEmailEnv(t)
	a, address := newResetAccount(t, e, "resetflow")
	other, err := e.store.CreateAuthToken(e.ctx, store.CreateAuthTokenParams{UserID: a.user.ID, Kind: model.AuthTokenKindSession, Name: "other browser"})
	if err != nil {
		t.Fatalf("CreateAuthToken session: %v", err)
	}
	api, err := e.store.CreateAuthToken(e.ctx, store.CreateAuthTokenParams{UserID: a.user.ID, Kind: model.AuthTokenKindAPI, Name: "script"})
	if err != nil {
		t.Fatalf("CreateAuthToken api: %v", err)
	}

	requireBody(t, "login", e.uiGet(t, "/login", ""), `href="/forgot-password"`, "Forgot password?")
	b := newResetBrowser(t, e)
	code, page, _ := b.get(t, "/forgot-password")
	if code != http.StatusOK {
		t.Fatalf("GET /forgot-password = %d", code)
	}
	requireBody(t, "forgot page", page, `action="/forgot-password"`, `name="identifier"`, "Username or email", "Send reset link", `href="/login"`)
	assertPopulatedCSRFTokens(t, "/forgot-password", page)

	code, body := b.forgot(t, " "+strings.ToUpper(a.user.Username)+" ")
	if code != http.StatusOK {
		t.Fatalf("forgot = %d %s", code, body)
	}
	requireBody(t, "forgot answer", body, "data-forgot-password-sent", resetSentAnswer)
	rejectBody(t, "forgot answer", body, `name="identifier"`, address)
	waitForEmail(t, e)
	sent := messagesTo(mail, address)
	if len(sent) != 1 || len(mail.Messages()) != 1 {
		t.Fatalf("reset emails = %+v", mail.Messages())
	}
	msg := sent[0]
	if msg.Subject != "Reset your Trackslash password" {
		t.Fatalf("subject = %q", msg.Subject)
	}
	requireBody(t, "reset email", msg.Text, "@"+a.user.Username, "expires in 30 minutes", "If you didn't ask for this, ignore this email. Your password hasn't changed.")
	requireBody(t, "reset email HTML", msg.HTML, `href="`+resetLinkPrefix)
	token := resetToken(t, msg)

	// Only the token's hash is stored.
	var stored []byte
	if err := e.pool.QueryRow(e.ctx, `SELECT token_hash FROM password_resets WHERE user_id = $1`, a.user.ID).Scan(&stored); err != nil {
		t.Fatalf("read link: %v", err)
	}
	if sum := sha256.Sum256([]byte(token)); !bytes.Equal(stored, sum[:]) {
		t.Fatalf("stored %x, want the token's SHA-256", stored)
	}

	// Mail scanners open links first, so opening one uses nothing up.
	link := resetLinkPath(token)
	for i := 0; i < 2; i++ {
		code, page, header := b.get(t, link)
		if code != http.StatusOK || header.Get("Cache-Control") != "no-store" {
			t.Fatalf("open link %d = %d cache %q", i, code, header.Get("Cache-Control"))
		}
		requireBody(t, "reset page", page, "data-reset-password-form", `name="token" value="`+token+`"`, "@"+a.user.Username, `autocomplete="new-password"`, "Set new password")
		assertPopulatedCSRFTokens(t, "/reset-password", page)
	}

	// A mistyped or refused password keeps the link.
	code, body, _ = b.reset(t, token, resetNewPassword, "something-else-entirely")
	if code != http.StatusBadRequest {
		t.Fatalf("mismatched passwords = %d", code)
	}
	requireBody(t, "mismatch", body, "Passwords don&#39;t match.", "data-reset-password-form", `name="token" value="`+token+`"`, "@"+a.user.Username)
	code, body, _ = b.reset(t, token, "short", "short")
	if code != http.StatusBadRequest {
		t.Fatalf("short password = %d", code)
	}
	requireBody(t, "short", body, "password must be at least 12 chars", "data-reset-password-form")

	code, _, header := b.reset(t, token, resetNewPassword, resetNewPassword)
	if code != http.StatusSeeOther || header.Get("Location") != "/login?password_reset=done" {
		t.Fatalf("reset = %d %q", code, header.Get("Location"))
	}
	for _, cookie := range header.Values("Set-Cookie") {
		if strings.HasPrefix(cookie, uiCookieNameForTest+"=") {
			t.Fatalf("a reset signed the browser in: %q", cookie)
		}
	}
	requireBody(t, "login after reset", e.uiGet(t, "/login?password_reset=done", ""), "data-login-notice", "Password updated. Sign in with your new password.", "data-password-login open")

	// Every web session is signed out; API tokens stay, as the notice says.
	for _, session := range []string{a.session, other.RawToken} {
		res := e.uiDoNoRedirect(t, http.MethodGet, "/settings/profile", session, nil)
		res.Body.Close()
		if res.StatusCode != http.StatusSeeOther || !strings.HasPrefix(res.Header.Get("Location"), "/login") {
			t.Fatalf("session after reset = %d %q", res.StatusCode, res.Header.Get("Location"))
		}
	}
	if code, body := e.doWithToken(t, api.RawToken, http.MethodGet, "/me", nil); code != http.StatusOK {
		t.Fatalf("API token after reset = %d %s", code, body)
	}

	// Sign in with the new password, not the old one.
	code, _, header = b.post(t, "/login", url.Values{"username": {a.user.Username}, "password": {resetNewPassword}})
	if code != http.StatusSeeOther || !strings.Contains(strings.Join(header.Values("Set-Cookie"), "\n"), uiCookieNameForTest+"=") {
		t.Fatalf("sign in with the new password = %d %v", code, header.Values("Set-Cookie"))
	}
	if code, _, _ = b.post(t, "/login", url.Values{"username": {a.user.Username}, "password": {emailTestPassword}}); code != http.StatusUnauthorized {
		t.Fatalf("sign in with the old password = %d", code)
	}

	waitForEmail(t, e)
	sent = messagesTo(mail, address)
	if len(sent) != 2 {
		t.Fatalf("emails after the reset = %+v", sent)
	}
	notice := sent[1]
	if notice.Subject != "Your Trackslash password was reset" {
		t.Fatalf("notice subject = %q", notice.Subject)
	}
	requireBody(t, "notice", notice.Text, "@"+a.user.Username, "Settings → Tokens", emailTestOrigin+"/tokens")
	requireBody(t, "notice HTML", notice.HTML, `href="`+emailTestOrigin+`/tokens"`)
	rejectBody(t, "notice", notice.Text, "/reset-password", token)

	// The link is used up.
	code, page, _ = b.get(t, link)
	if code != http.StatusOK {
		t.Fatalf("used link = %d", code)
	}
	requireBody(t, "used link", page, "data-reset-password-expired", resetExpired, `href="/forgot-password"`)
	rejectBody(t, "used link", page, "data-reset-password-form", token)
	code, body, _ = b.reset(t, token, "another-good-password", "another-good-password")
	if code != http.StatusBadRequest {
		t.Fatalf("reuse = %d", code)
	}
	requireBody(t, "reuse", body, resetExpired)
}

// Every request gets the same page: an unknown name, an unverified email, a
// deleted account, an account that signs in with a passkey and a request
// past the account's budget can't be told apart. Only the mail differs.
func TestPasswordResetGivesTheSameAnswerEveryTime(t *testing.T) {
	t.Parallel()
	e, mail := newEmailEnv(t)
	_, passwordAddress := newResetAccount(t, e, "resetsame")
	unverified := newEmailAccount(t, e, "resetunver")
	unverifiedAddress := uniqueAddress("unverified")
	if _, err := e.store.UpdateUserProfile(e.ctx, unverified.user.ID, "Unverified", unverifiedAddress); err != nil {
		t.Fatalf("UpdateUserProfile: %v", err)
	}
	passwordOff, passwordOffAddress := newResetAccount(t, e, "resetoff")
	if _, err := e.store.AddPasskeyCredential(e.ctx, passwordOff.user.ID, "localhost", "Laptop", serverPasskeyCredential("reset-"+uuid.NewString(), 1)); err != nil {
		t.Fatalf("AddPasskeyCredential: %v", err)
	}
	if _, err := e.store.SetPasswordLoginEnabled(e.ctx, passwordOff.user.ID, false); err != nil {
		t.Fatalf("SetPasswordLoginEnabled: %v", err)
	}
	passkeyOnly, err := e.store.CreatePasskeyOnlyAccount(e.ctx, store.CreatePasskeyOnlyAccountParams{
		Username:       "resetpk" + strings.ToLower(uniqueProjectKey(t)),
		RPID:           "localhost",
		UserHandle:     []byte("handle-" + uuid.NewString()),
		CredentialName: "Phone",
		Credential:     serverPasskeyCredential("reset-"+uuid.NewString(), 1),
	})
	if err != nil {
		t.Fatalf("CreatePasskeyOnlyAccount: %v", err)
	}
	passkeyOnlyAddress := uniqueAddress("passkey")
	verifyAddress(t, e, passkeyOnly.ID, passkeyOnlyAddress)
	deleted, deletedAddress := newResetAccount(t, e, "resetgone")
	if err := e.store.DeleteUser(e.ctx, deleted.user.ID); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
	limited, limitedAddress := newResetAccount(t, e, "resetlimit")

	b := newResetBrowser(t, e)
	_, want := b.forgot(t, "nobody-"+uuid.NewString()[:8])
	requireBody(t, "answer", want, resetSentAnswer)
	for _, identifier := range []string{
		uniqueAddress("nobody"),
		unverified.user.Username, unverifiedAddress,
		deleted.user.Username, deletedAddress,
		passwordOff.user.Username, passkeyOnlyAddress,
		strings.ToUpper(passwordAddress),
		limitedAddress, limited.user.Username, strings.ToUpper(limitedAddress), limited.user.Username,
	} {
		code, got := b.forgot(t, identifier)
		if code != http.StatusOK || got != want {
			t.Fatalf("answer for %q = %d, differs from the answer for an unknown name:\n%s\nwant:\n%s", identifier, code, got, want)
		}
	}
	waitForEmail(t, e)

	for _, tt := range []struct {
		name    string
		address string
		count   int
		subject string
	}{
		{"password account", passwordAddress, 1, "Reset your Trackslash password"},
		{"password sign-in off", passwordOffAddress, 1, "Sign in to Trackslash with your passkey"},
		{"passkey only", passkeyOnlyAddress, 1, "Sign in to Trackslash with your passkey"},
		{"past its budget", limitedAddress, 3, "Reset your Trackslash password"},
		{"unverified", unverifiedAddress, 0, ""},
		{"deleted", deletedAddress, 0, ""},
	} {
		got := messagesTo(mail, tt.address)
		if len(got) != tt.count {
			t.Fatalf("%s: %d emails, want %d: %+v", tt.name, len(got), tt.count, got)
		}
		for _, msg := range got {
			if msg.Subject != tt.subject {
				t.Fatalf("%s: subject %q, want %q", tt.name, msg.Subject, tt.subject)
			}
			if tt.subject == "Sign in to Trackslash with your passkey" {
				requireBody(t, tt.name, msg.Text, "signs in with a passkey", emailTestOrigin+"/login\n")
				rejectBody(t, tt.name, msg.Text, "/reset-password")
			}
		}
	}
	if len(mail.Messages()) != 6 {
		t.Fatalf("emails = %d, want 6", len(mail.Messages()))
	}
	// A passkey account was sent no link, so nothing can add a password to it.
	var links int
	if err := e.pool.QueryRow(e.ctx, `SELECT count(*) FROM password_resets WHERE user_id = ANY($1)`, []uuid.UUID{passwordOff.user.ID, passkeyOnly.ID}).Scan(&links); err != nil || links != 0 {
		t.Fatalf("links for passkey accounts = %d, %v", links, err)
	}
	// Requests past the budget made no link either, so the last one sent
	// still works.
	last := messagesTo(mail, limitedAddress)[2]
	if code, page, _ := b.get(t, resetLinkPath(resetToken(t, last))); code != http.StatusOK || !strings.Contains(page, "data-reset-password-form") {
		t.Fatalf("last link sent within the budget = %d", code)
	}
}

// blockingMailer holds every send until it is released.
type blockingMailer struct {
	*mailer.Capturing
	started chan struct{}
	release chan struct{}
}

func (m *blockingMailer) Send(ctx context.Context, msg mailer.Message) error {
	m.started <- struct{}{}
	<-m.release
	return m.Capturing.Send(ctx, msg)
}

// The page answers before anything is sent, so how long it takes can't say
// whether an email went.
func TestPasswordResetAnswersBeforeSending(t *testing.T) {
	t.Parallel()
	mail := &blockingMailer{Capturing: mailer.NewCapturing(), started: make(chan struct{}, 1), release: make(chan struct{})}
	e := newHTTPEnvWithOptions(t, server.Options{Mailer: mail, PublicOrigin: emailTestOrigin})
	a, address := newResetAccount(t, e, "resetwait")
	b := newResetBrowser(t, e)
	// The send is still held here, so the answer can't have waited for it.
	if code, body := b.forgot(t, a.user.Username); code != http.StatusOK || !strings.Contains(body, resetSentAnswer) {
		t.Fatalf("forgot = %d %s", code, body)
	}
	<-mail.started
	if len(mail.Messages()) != 0 {
		t.Fatal("the email went before it was released")
	}
	close(mail.release)
	waitForEmail(t, e)
	if got := messagesTo(mail.Capturing, address); len(got) != 1 {
		t.Fatalf("emails = %+v", got)
	}
}

// A link stops working once it expires, once another is asked for, and once
// the password is changed some other way, and each says so with a way to ask
// again.
func TestPasswordResetBadLinks(t *testing.T) {
	t.Parallel()
	e, mail := newEmailEnv(t)
	a, address := newResetAccount(t, e, "resetbad")
	b := newResetBrowser(t, e)
	ask := func() string {
		t.Helper()
		b.forgot(t, address)
		waitForEmail(t, e)
		sent := messagesTo(mail, address)
		return resetToken(t, sent[len(sent)-1])
	}
	requireExpired := func(what, token string) {
		t.Helper()
		code, page, _ := b.get(t, resetLinkPath(token))
		if code != http.StatusOK {
			t.Fatalf("%s: open = %d", what, code)
		}
		requireBody(t, what, page, "data-reset-password-expired", resetExpired, `href="/forgot-password"`, "Ask for a new link")
		rejectBody(t, what, page, "data-reset-password-form")
		for _, confirm := range []string{resetNewPassword, "a-different-password"} {
			code, body, _ := b.reset(t, token, resetNewPassword, confirm)
			if code != http.StatusBadRequest || !strings.Contains(body, resetExpired) || strings.Contains(body, "data-reset-password-form") {
				t.Fatalf("%s: set password = %d %s", what, code, body)
			}
		}
	}

	first := ask()
	second := ask()
	requireExpired("replaced link", first)

	// A password changed from the Login page cancels the link.
	code, body, _ := postEmailForm(t, e, a.session, "/settings/password", url.Values{"current_password": {emailTestPassword}, "new_password": {"changed-horse-battery"}})
	if code != http.StatusOK || !strings.Contains(body, "Password changed") {
		t.Fatalf("change password = %d", code)
	}
	requireExpired("link after a password change", second)

	third := ask()
	if _, err := e.pool.Exec(e.ctx, `UPDATE password_resets SET created_at = now() - interval '31 minutes', expires_at = now() - interval '1 minute' WHERE user_id = $1`, a.user.ID); err != nil {
		t.Fatalf("expire link: %v", err)
	}
	requireExpired("expired link", third)
	requireExpired("made-up link", "not-a-real-token")
	requireExpired("no token", "")
	if _, err := e.store.AuthenticatePassword(e.ctx, a.user.Username, resetNewPassword); !errors.Is(err, store.ErrUnauthorized) {
		t.Fatalf("a dead link set the password: %v", err)
	}
}

// A provider that refuses mail changes nothing about the answer, and a
// notice that can't be sent doesn't undo the reset.
func TestPasswordResetSendFailures(t *testing.T) {
	t.Parallel()
	e, mail := newEmailEnv(t)
	a, address := newResetAccount(t, e, "resetfail")
	b := newResetBrowser(t, e)
	mail.Fail(errors.New("554 5.7.0 daily sending quota exceeded"))
	if code, body := b.forgot(t, a.user.Username); code != http.StatusOK || !strings.Contains(body, resetSentAnswer) {
		t.Fatalf("forgot with the provider down = %d %s", code, body)
	}
	waitForEmail(t, e)
	if mail.Attempts() != 1 || len(mail.Messages()) != 0 {
		t.Fatalf("attempts = %d, messages = %d", mail.Attempts(), len(mail.Messages()))
	}

	mail.Fail(nil)
	b.forgot(t, a.user.Username)
	waitForEmail(t, e)
	token := resetToken(t, lastEmail(t, mail))
	mail.Fail(errors.New("421 try again later"))
	if code, _, header := b.reset(t, token, resetNewPassword, resetNewPassword); code != http.StatusSeeOther || header.Get("Location") != "/login?password_reset=done" {
		t.Fatalf("reset with the provider down = %d", code)
	}
	waitForEmail(t, e)
	if mail.Attempts() != 3 || len(messagesTo(mail, address)) != 1 {
		t.Fatalf("attempts = %d, messages = %+v", mail.Attempts(), mail.Messages())
	}
	if _, err := e.store.AuthenticatePassword(e.ctx, a.user.Username, resetNewPassword); err != nil {
		t.Fatalf("new password after a failed notice: %v", err)
	}
}

// The forms check what they are sent, and both are pre-auth forms behind the
// sign-in CSRF check.
func TestPasswordResetFormChecks(t *testing.T) {
	t.Parallel()
	e, mail := newEmailEnv(t)
	b := newResetBrowser(t, e)

	code, body := b.forgot(t, "   ")
	if code != http.StatusBadRequest {
		t.Fatalf("empty identifier = %d", code)
	}
	requireBody(t, "empty identifier", body, "Enter your username or email.", `name="identifier"`)
	rejectBody(t, "empty identifier", body, resetSentAnswer)

	// A body that can't be parsed, with the CSRF token in its header.
	post := func(path, body string, headers map[string]string) (int, string) {
		t.Helper()
		req, err := http.NewRequestWithContext(e.ctx, http.MethodPost, e.ts.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatalf("NewRequest: %v", err)
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		for key, value := range headers {
			req.Header.Set(key, value)
		}
		req.AddCookie(b.cookie)
		res, err := e.ts.Client().Do(req)
		if err != nil {
			t.Fatalf("POST %s: %v", path, err)
		}
		defer res.Body.Close()
		return res.StatusCode, readBody(t, res)
	}
	csrfHeader := map[string]string{"X-CSRF-Token": uiCSRFTokenForTest("pre-auth", b.cookie.Value), "Origin": emailTestOrigin}
	for _, tt := range []struct{ path, want string }{
		{"/forgot-password", "Unable to read form."},
		{"/reset-password", resetExpired},
	} {
		if code, body := post(tt.path, "identifier=%zz&token=%zz", csrfHeader); code != http.StatusBadRequest || !strings.Contains(body, tt.want) {
			t.Fatalf("unreadable %s = %d %s", tt.path, code, body)
		}
	}

	for _, tt := range []struct {
		name    string
		headers map[string]string
		form    url.Values
	}{
		{"no token", map[string]string{"Origin": emailTestOrigin}, url.Values{"identifier": {"anyone"}}},
		{"another site", map[string]string{"Origin": "https://evil.example.net"}, url.Values{"identifier": {"anyone"}, "csrf_token": {uiCSRFTokenForTest("pre-auth", b.cookie.Value)}}},
	} {
		for _, path := range []string{"/forgot-password", "/reset-password"} {
			if code, _ := post(path, tt.form.Encode(), tt.headers); code != http.StatusForbidden {
				t.Fatalf("%s: POST %s = %d, want 403", tt.name, path, code)
			}
		}
	}
	waitForEmail(t, e)
	if mail.Attempts() != 0 {
		t.Fatalf("refused requests sent %d emails", mail.Attempts())
	}
}

// Both forms spend the sign-in budget of the address they come from.
func TestPasswordResetIsRateLimitedPerAddress(t *testing.T) {
	t.Parallel()
	mail := mailer.NewCapturing()
	e := newHTTPEnvWithOptions(t, server.Options{Mailer: mail, PublicOrigin: emailTestOrigin, AuthRateLimit: server.AuthRateLimitOptions{IPAttempts: 2}})
	a, _ := newResetAccount(t, e, "resetip")
	b := newResetBrowser(t, e)
	for i := 0; i < 2; i++ {
		if code, _ := b.forgot(t, a.user.Username); code != http.StatusOK {
			t.Fatalf("forgot %d = %d", i, code)
		}
	}
	code, _, header := b.reset(t, "any", resetNewPassword, resetNewPassword)
	if code != http.StatusTooManyRequests || header.Get("Retry-After") == "" {
		t.Fatalf("reset past the address's budget = %d %q", code, header.Get("Retry-After"))
	}
	if code, _ := b.forgot(t, a.user.Username); code != http.StatusTooManyRequests {
		t.Fatalf("forgot past the address's budget = %d", code)
	}
	waitForEmail(t, e)
	if mail.Attempts() != 2 {
		t.Fatalf("emails = %d, want 2", mail.Attempts())
	}
}
