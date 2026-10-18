package model

import (
	"time"

	"github.com/google/uuid"
)

// EmailStatus is where a user's email stands: the address in effect, whether
// it is verified, and a change still waiting for its owner to confirm.
type EmailStatus struct {
	// Email is the address in effect, or "" when there is none. A new address
	// takes its place only once confirmed.
	Email      string
	VerifiedAt *time.Time
	Pending    *PendingEmail
}

// Verified reports whether the address in effect has been confirmed.
func (s EmailStatus) Verified() bool {
	return s.VerifiedAt != nil
}

// PendingEmail is the newest confirmation link a user is waiting on.
type PendingEmail struct {
	Email     string
	ExpiresAt time.Time
	// Sent is false when the provider refused the message; asking again sends
	// a fresh link.
	Sent bool
}

// EmailVerification is one confirmation link. Only the hash of its token is
// stored.
type EmailVerification struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	Email     string
	CreatedAt time.Time
	ExpiresAt time.Time
	SentAt    *time.Time
	UsedAt    *time.Time
}

// Usable reports whether the link can still confirm its address: it has not
// been used and has not expired.
func (v EmailVerification) Usable(now time.Time) bool {
	return v.UsedAt == nil && now.Before(v.ExpiresAt)
}
