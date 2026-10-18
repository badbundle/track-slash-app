package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/bradleymackey/track-slash/internal/mailer"
	"github.com/bradleymackey/track-slash/internal/store"
	"github.com/bradleymackey/track-slash/internal/testutil"
)

// The background half of a reset request sends nothing when it can't find
// the account, or when the account it found has changed by the time a link
// would be made.
func TestPasswordResetSendsNothingWhenTheLookupOrLinkFails(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	db := testutil.NewMigratedDatabase(t)
	st := store.New(db.Pool)
	mail := mailer.NewCapturing()
	s := NewWithOptions(st, nil, Options{Mailer: mail, PublicOrigin: "https://track.example.com"})

	ended, end := context.WithCancel(ctx)
	end()
	s.requestPasswordReset(ended, "anyone")

	user, err := st.CreateAccount(ctx, store.CreateAccountParams{
		Username: "resetstale" + strings.ReplaceAll(uuid.NewString()[:8], "-", ""),
		Password: "correct-horse-battery",
		Name:     "Stale",
	})
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	// Found as signing in with a password at an address it no longer has
	// verified: no link is made, so nothing is sent.
	s.sendPasswordReset(ctx, store.PasswordResetAccount{UserID: user.ID, Username: user.Username, Email: "moved@example.com", PasswordLogin: true})
	if mail.Attempts() != 0 {
		t.Fatalf("emails = %d, want none", mail.Attempts())
	}
}

// Without the database the reset pages fail plainly rather than showing a
// link as expired or setting nothing while saying it did.
func TestPasswordResetPagesWithoutTheDatabase(t *testing.T) {
	t.Parallel()
	db := testutil.NewMigratedDatabase(t)
	s := NewWithOptions(store.New(db.Pool), nil, Options{Mailer: mailer.NewCapturing(), PublicOrigin: "https://track.example.com"})
	ended, end := context.WithCancel(context.Background())
	end()
	for _, tt := range []struct {
		name    string
		handler http.HandlerFunc
		method  string
		form    url.Values
	}{
		{"open link", s.uiResetPasswordPage, http.MethodGet, nil},
		{"recheck after a mistyped password", s.uiResetPassword, http.MethodPost, url.Values{"token": {"x"}, "password": {"correct-horse-battery"}, "password_confirm": {"something-else-entirely"}}},
		{"set password", s.uiResetPassword, http.MethodPost, url.Values{"token": {"x"}, "password": {"correct-horse-battery"}, "password_confirm": {"correct-horse-battery"}}},
	} {
		req := httptest.NewRequest(tt.method, "https://track.example.com/reset-password?token=x", strings.NewReader(tt.form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		tt.handler(rec, req.WithContext(ended))
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("%s without the database = %d %s", tt.name, rec.Code, rec.Body.String())
		}
	}
}
