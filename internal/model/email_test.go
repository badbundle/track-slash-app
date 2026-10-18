package model

import (
	"testing"
	"time"
)

func TestEmailStatusVerified(t *testing.T) {
	t.Parallel()
	now := time.Now()
	if (EmailStatus{Email: "ada@example.com"}).Verified() {
		t.Fatal("an address without a verification time is verified")
	}
	if !(EmailStatus{Email: "ada@example.com", VerifiedAt: &now}).Verified() {
		t.Fatal("a verified address is not verified")
	}
}

func TestEmailVerificationUsable(t *testing.T) {
	t.Parallel()
	now := time.Now()
	used := now.Add(-time.Minute)
	for _, tt := range []struct {
		name string
		v    EmailVerification
		want bool
	}{
		{name: "fresh", v: EmailVerification{ExpiresAt: now.Add(time.Hour)}, want: true},
		{name: "used", v: EmailVerification{ExpiresAt: now.Add(time.Hour), UsedAt: &used}, want: false},
		{name: "expired", v: EmailVerification{ExpiresAt: now.Add(-time.Second)}, want: false},
		{name: "expires now", v: EmailVerification{ExpiresAt: now}, want: false},
	} {
		if got := tt.v.Usable(now); got != tt.want {
			t.Errorf("%s: Usable = %v, want %v", tt.name, got, tt.want)
		}
	}
}
