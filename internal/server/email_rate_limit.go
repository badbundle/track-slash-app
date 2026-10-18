package server

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	// emailConfirmationLimit is how many confirmation emails one account may
	// send, and one address may receive, in emailConfirmationWindow. It stops
	// the form being used to fill someone's inbox and keeps a deployment well
	// under its provider's daily cap.
	emailConfirmationLimit  = 3
	emailConfirmationWindow = time.Hour
)

func newEmailLimiter() *fixedWindowLimiter {
	return newFixedWindowLimiter(emailConfirmationLimit, emailConfirmationWindow, defaultAuthLimitEntries, time.Now)
}

// emailVerifyUserKey is the budget for confirmation emails one account sends.
func emailVerifyUserKey(userID uuid.UUID) string {
	return "email-verify-user:" + userID.String()
}

// emailVerifyToKey is the budget for confirmation emails one address
// receives, whichever accounts ask.
func emailVerifyToKey(address string) string {
	return "email-verify-to:" + strings.ToLower(strings.TrimSpace(address))
}

// allowEmailConfirmation spends one send from both the account's and the
// recipient's budgets. When either is spent it spends neither and says how
// long until the first frees up. Two requests racing on a last send can each
// pass the check, which costs at most one message more.
func (s *Server) allowEmailConfirmation(userID uuid.UUID, address string) (time.Duration, bool) {
	keys := []string{emailVerifyUserKey(userID), emailVerifyToKey(address)}
	for _, key := range keys {
		if blocked, wait := s.emailLimiter.blocked(key); blocked {
			return wait, false
		}
	}
	for _, key := range keys {
		s.emailLimiter.allow(key)
	}
	return 0, true
}

// uiRetryIn says how long a wait is, rounded up to whole minutes.
func uiRetryIn(wait time.Duration) string {
	minutes := int(math.Ceil(wait.Minutes()))
	if minutes <= 1 {
		return "a minute"
	}
	return fmt.Sprintf("%d minutes", minutes)
}
