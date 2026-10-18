package server

import (
	"bytes"
	"html/template"
	"net/url"
	"strings"

	"github.com/bradleymackey/track-slash/internal/mailer"
	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/store"
)

// Messages are plain: a text part, a minimal HTML part with the same words,
// a plain subject, and links only to the configured public origin. No images,
// tracking, shortened links or promotion.

var emailConfirmationHTML = template.Must(template.New("confirm").Parse(`<!doctype html>
<html><body>
<p>Confirm that this is the email for your Trackslash account, @{{.Username}}.</p>
<p>Open this link, sign in if asked, and select Confirm this email:<br><a href="{{.Link}}">{{.Link}}</a></p>
<p>The link works once and expires in 1 hour. If you didn't ask for this, ignore this email and nothing will change.</p>
</body></html>
`))

var emailChangedHTML = template.Must(template.New("changed").Parse(`<!doctype html>
<html><body>
<p>The email for the Trackslash account @{{.Username}} was changed, so this address no longer receives its email.</p>
<p>If you made this change, there's nothing to do.</p>
<p>If you didn't, sign in, change your password and remove any passkeys you don't recognise:<br><a href="{{.Link}}">{{.Link}}</a></p>
</body></html>
`))

var passwordResetHTML = template.Must(template.New("reset").Parse(`<!doctype html>
<html><body>
<p>Someone asked to reset the password for your Trackslash account, @{{.Username}}.</p>
<p>Open this link to choose a new password:<br><a href="{{.Link}}">{{.Link}}</a></p>
<p>The link works once and expires in 30 minutes. If you didn't ask for this, ignore this email. Your password hasn't changed.</p>
</body></html>
`))

var passwordResetPasskeyHTML = template.Must(template.New("reset-passkey").Parse(`<!doctype html>
<html><body>
<p>Someone asked to reset the password for your Trackslash account, @{{.Username}}.</p>
<p>This account signs in with a passkey, so there's no password to reset. Sign in with your passkey:<br><a href="{{.Link}}">{{.Link}}</a></p>
<p>If you didn't ask for this, ignore this email. Nothing has changed.</p>
</body></html>
`))

var passwordResetNoticeHTML = template.Must(template.New("reset-notice").Parse(`<!doctype html>
<html><body>
<p>The password for your Trackslash account, @{{.Username}}, was reset, and every browser signed in to it was signed out.</p>
<p>If you did this, there's nothing to do.</p>
<p>If you didn't, sign in with a passkey or reset your password again from the sign-in page. Then, in Settings → Tokens, revoke any API tokens and connected apps you don't recognise:<br><a href="{{.Link}}">{{.Link}}</a></p>
</body></html>
`))

type emailTemplateData struct {
	Username string
	Link     string
}

// emailConfirmLink is the link a confirmation email carries. It is built from
// the configured public origin, never from the request, so a spoofed Host
// header cannot point it anywhere else.
func emailConfirmLink(origin, token string) string {
	return strings.TrimRight(origin, "/") + uiEmailConfirmPath + "?token=" + url.QueryEscape(token)
}

func emailConfirmationMessage(origin string, user model.User, address, token string) mailer.Message {
	data := emailTemplateData{Username: user.Username, Link: emailConfirmLink(origin, token)}
	return mailer.Message{
		To:      address,
		Subject: "Confirm your email for Trackslash",
		Text: "Confirm that this is the email for your Trackslash account, @" + data.Username + ".\n\n" +
			"Open this link, sign in if asked, and select Confirm this email:\n" + data.Link + "\n\n" +
			"The link works once and expires in 1 hour. If you didn't ask for this, ignore this email and nothing will change.\n",
		HTML: renderEmailHTML(emailConfirmationHTML, data),
	}
}

func emailChangedMessage(origin string, user model.User, previous string) mailer.Message {
	data := emailTemplateData{Username: user.Username, Link: strings.TrimRight(origin, "/") + uiLoginSettingsPath}
	return mailer.Message{
		To:      previous,
		Subject: "Your Trackslash email was changed",
		Text: "The email for the Trackslash account @" + data.Username + " was changed, so this address no longer receives its email.\n\n" +
			"If you made this change, there's nothing to do.\n\n" +
			"If you didn't, sign in, change your password and remove any passkeys you don't recognise:\n" + data.Link + "\n",
		HTML: renderEmailHTML(emailChangedHTML, data),
	}
}

// renderEmailHTML runs one of the fixed templates above, whose only inputs are
// a username and a link, so it cannot fail.
func renderEmailHTML(tmpl *template.Template, data emailTemplateData) string {
	var buf bytes.Buffer
	_ = tmpl.Execute(&buf, data)
	return buf.String()
}

// passwordResetLink is the link a password reset email carries. Like every
// link in a message, it is built from the configured public origin, never
// from the request.
func passwordResetLink(origin, token string) string {
	return strings.TrimRight(origin, "/") + uiResetPasswordPath + "?token=" + url.QueryEscape(token)
}

// passwordResetMessage carries a reset link to the account's verified
// address.
func passwordResetMessage(origin string, account store.PasswordResetAccount, token string) mailer.Message {
	data := emailTemplateData{Username: account.Username, Link: passwordResetLink(origin, token)}
	return mailer.Message{
		To:      account.Email,
		Subject: "Reset your Trackslash password",
		Text: "Someone asked to reset the password for your Trackslash account, @" + data.Username + ".\n\n" +
			"Open this link to choose a new password:\n" + data.Link + "\n\n" +
			"The link works once and expires in 30 minutes. If you didn't ask for this, ignore this email. Your password hasn't changed.\n",
		HTML: renderEmailHTML(passwordResetHTML, data),
	}
}

// passwordResetPasskeyMessage answers a reset request for an account that
// signs in with a passkey: it has no password to reset, so it gets a way in
// rather than a link that would add one.
func passwordResetPasskeyMessage(origin string, account store.PasswordResetAccount) mailer.Message {
	data := emailTemplateData{Username: account.Username, Link: strings.TrimRight(origin, "/") + "/login"}
	return mailer.Message{
		To:      account.Email,
		Subject: "Sign in to Trackslash with your passkey",
		Text: "Someone asked to reset the password for your Trackslash account, @" + data.Username + ".\n\n" +
			"This account signs in with a passkey, so there's no password to reset. Sign in with your passkey:\n" + data.Link + "\n\n" +
			"If you didn't ask for this, ignore this email. Nothing has changed.\n",
		HTML: renderEmailHTML(passwordResetPasskeyHTML, data),
	}
}

// passwordResetNoticeMessage tells the verified address that the password was
// reset. A reset leaves API tokens and connected apps alone, so it points to
// where they are revoked.
func passwordResetNoticeMessage(origin string, account store.PasswordResetAccount) mailer.Message {
	data := emailTemplateData{Username: account.Username, Link: strings.TrimRight(origin, "/") + "/tokens"}
	return mailer.Message{
		To:      account.Email,
		Subject: "Your Trackslash password was reset",
		Text: "The password for your Trackslash account, @" + data.Username + ", was reset, and every browser signed in to it was signed out.\n\n" +
			"If you did this, there's nothing to do.\n\n" +
			"If you didn't, sign in with a passkey or reset your password again from the sign-in page. Then, in Settings → Tokens, revoke any API tokens and connected apps you don't recognise:\n" + data.Link + "\n",
		HTML: renderEmailHTML(passwordResetNoticeHTML, data),
	}
}
