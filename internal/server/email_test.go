package server

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/bradleymackey/track-slash/internal/mailer"
	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/store"
)

func TestUIRetryIn(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		wait time.Duration
		want string
	}{
		{0, "a minute"},
		{20 * time.Second, "a minute"},
		{time.Minute, "a minute"},
		{61 * time.Second, "2 minutes"},
		{59*time.Minute + 30*time.Second, "60 minutes"},
	} {
		if got := uiRetryIn(tt.wait); got != tt.want {
			t.Errorf("uiRetryIn(%s) = %q, want %q", tt.wait, got, tt.want)
		}
	}
}

func TestEmailLimiterKeysAreDistinct(t *testing.T) {
	t.Parallel()
	id := uuid.New()
	if got := emailVerifyUserKey(id); got != "email-verify-user:"+id.String() {
		t.Fatalf("user key = %q", got)
	}
	if got := passwordResetKey(id); got != "password-reset:"+id.String() {
		t.Fatalf("password reset key = %q", got)
	}
	if got := emailVerifyToKey("  Ada@Example.com "); got != "email-verify-to:ada@example.com" {
		t.Fatalf("recipient key = %q", got)
	}
}

// Email is on only with both a mailer and the public origin its links use.
func TestEmailMailerNeedsThePublicOrigin(t *testing.T) {
	t.Parallel()
	mail := mailer.NewCapturing()
	if emailMailer(Options{}) != nil || emailMailer(Options{Mailer: mail}) != nil || emailMailer(Options{PublicOrigin: "https://track.example.com"}) != nil {
		t.Fatal("email turned on without both a mailer and a public origin")
	}
	if emailMailer(Options{Mailer: mail, PublicOrigin: "https://track.example.com"}) != mail {
		t.Fatal("email off with a mailer and a public origin")
	}
}

func TestEmailMessages(t *testing.T) {
	t.Parallel()
	user := model.User{Username: "ada<script>"}
	msg := emailConfirmationMessage("https://track.example.com/", user, "ada@example.com", "tok+en/=")
	link := "https://track.example.com/settings/email/confirm?token=tok%2Ben%2F%3D"
	if msg.To != "ada@example.com" || msg.Subject != "Confirm your email for Trackslash" || !strings.Contains(msg.Text, link) {
		t.Fatalf("confirmation message = %+v", msg)
	}
	if !strings.Contains(msg.HTML, `href="`+link+`"`) || strings.Contains(msg.HTML, "<script>") || !strings.Contains(msg.HTML, "@ada&lt;script&gt;") {
		t.Fatalf("confirmation HTML is not escaped: %s", msg.HTML)
	}

	notice := emailChangedMessage("https://track.example.com", model.User{Username: "ada"}, "old@example.com")
	if notice.To != "old@example.com" || notice.Subject != "Your Trackslash email was changed" || !strings.Contains(notice.Text, "https://track.example.com/settings/login") || !strings.Contains(notice.HTML, `href="https://track.example.com/settings/login"`) {
		t.Fatalf("change notice = %+v", notice)
	}
	for _, m := range []mailer.Message{msg, notice} {
		if strings.Contains(m.HTML, "<img") || strings.Contains(m.Subject, "\n") {
			t.Fatalf("message has an image or a multi-line subject: %+v", m)
		}
	}
}

func TestEmailVerifiedBadge(t *testing.T) {
	t.Parallel()
	now := time.Now()
	if badge := (uiEmailPanelData{Status: model.EmailStatus{Email: "a@example.com", VerifiedAt: &now}}).VerifiedBadge(); !badge.Tinted || badge.Label != "Verified" {
		t.Fatalf("verified badge = %+v", badge)
	}
	if badge := (uiEmailPanelData{Status: model.EmailStatus{Email: "a@example.com"}}).VerifiedBadge(); badge.Tinted || badge.Label != "Not verified" {
		t.Fatalf("unverified badge = %+v", badge)
	}
}

func TestPasswordResetMessages(t *testing.T) {
	t.Parallel()
	account := store.PasswordResetAccount{Username: "ada<script>", Email: "ada@example.com"}
	reset := passwordResetMessage("https://track.example.com/", account, "tok+en/=")
	link := "https://track.example.com/reset-password?token=tok%2Ben%2F%3D"
	if reset.To != "ada@example.com" || reset.Subject != "Reset your Trackslash password" || !strings.Contains(reset.Text, link+"\n") {
		t.Fatalf("reset message = %+v", reset)
	}
	for _, want := range []string{"expires in 30 minutes", "If you didn't ask for this, ignore this email. Your password hasn't changed."} {
		if !strings.Contains(reset.Text, want) || !strings.Contains(reset.HTML, want) {
			t.Fatalf("reset message missing %q: %+v", want, reset)
		}
	}
	if !strings.Contains(reset.HTML, `href="`+link+`"`) {
		t.Fatalf("reset HTML link: %s", reset.HTML)
	}

	passkey := passwordResetPasskeyMessage("https://track.example.com/", account)
	if passkey.To != "ada@example.com" || passkey.Subject != "Sign in to Trackslash with your passkey" || !strings.Contains(passkey.Text, "https://track.example.com/login\n") || !strings.Contains(passkey.HTML, `href="https://track.example.com/login"`) {
		t.Fatalf("passkey message = %+v", passkey)
	}
	if strings.Contains(passkey.Text+passkey.HTML, "reset-password") {
		t.Fatalf("passkey message carries a reset link: %+v", passkey)
	}

	notice := passwordResetNoticeMessage("https://track.example.com", account)
	if notice.To != "ada@example.com" || notice.Subject != "Your Trackslash password was reset" || !strings.Contains(notice.Text, "Settings → Tokens") || !strings.Contains(notice.Text, "https://track.example.com/tokens\n") || !strings.Contains(notice.HTML, `href="https://track.example.com/tokens"`) {
		t.Fatalf("reset notice = %+v", notice)
	}
	for _, m := range []mailer.Message{reset, passkey, notice} {
		if strings.Contains(m.HTML, "<script>") || !strings.Contains(m.HTML, "@ada&lt;script&gt;") || !strings.Contains(m.Text, "@ada<script>") {
			t.Fatalf("message HTML is not escaped: %s", m.HTML)
		}
		if strings.Contains(m.HTML, "<img") || strings.Contains(m.Subject, "\n") {
			t.Fatalf("message has an image or a multi-line subject: %+v", m)
		}
	}
}
