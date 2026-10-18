package store

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/bradleymackey/track-slash/internal/mailer"
	"github.com/bradleymackey/track-slash/internal/model"
)

// EmailVerificationTTL is how long a confirmation link works.
const EmailVerificationTTL = time.Hour

// ErrInvalidEmail refuses an address that cannot be sent to: anything but one
// plain address such as you@example.com.
var ErrInvalidEmail = errors.New("invalid email")

// CreatedEmailVerification is a new confirmation link. Token is the raw
// secret for the link; only its hash is stored, so this is the one chance to
// read it.
type CreatedEmailVerification struct {
	Verification model.EmailVerification
	Token        string
}

// ConfirmedEmail is an address that has just been verified.
type ConfirmedEmail struct {
	Email string
	// Previous is the verified address this one replaced, when that was a
	// different mailbox. It is told about the change.
	Previous string
}

const emailVerificationColumns = `id, user_id, email, created_at, expires_at, sent_at, used_at`

func scanEmailVerification(row pgx.Row) (model.EmailVerification, error) {
	var v model.EmailVerification
	err := row.Scan(&v.ID, &v.UserID, &v.Email, &v.CreatedAt, &v.ExpiresAt, &v.SentAt, &v.UsedAt)
	return v, err
}

// GetEmailStatus returns the user's address in effect, whether it is verified,
// and the newest confirmation link still waiting, sent or not.
func (s *Store) GetEmailStatus(ctx context.Context, userID uuid.UUID) (model.EmailStatus, error) {
	const q = `
		SELECT COALESCE(u.email, ''), u.email_verified_at, v.email, v.expires_at, v.sent_at
		FROM users u
		LEFT JOIN LATERAL (
			SELECT email, expires_at, sent_at
			FROM email_verifications
			WHERE user_id = u.id AND used_at IS NULL AND expires_at > now()
			ORDER BY created_at DESC, id DESC
			LIMIT 1
		) v ON true
		WHERE u.id = $1 AND u.deleted_at IS NULL
	`
	var status model.EmailStatus
	var pendingEmail *string
	var pendingExpires, pendingSent *time.Time
	err := s.db.QueryRow(ctx, q, userID).Scan(&status.Email, &status.VerifiedAt, &pendingEmail, &pendingExpires, &pendingSent)
	if err != nil {
		if isNoRows(err) {
			return model.EmailStatus{}, ErrNotFound
		}
		// Defensive: only a DB outage reaches this.
		return model.EmailStatus{}, err
	}
	if pendingEmail != nil {
		status.Pending = &model.PendingEmail{Email: *pendingEmail, ExpiresAt: *pendingExpires, Sent: pendingSent != nil}
	}
	return status, nil
}

// CreateEmailVerification starts confirming email for the user. It replaces
// any link the user is still waiting on, so only the newest one works, and it
// commits before anything is sent: a send that fails leaves behind only a
// token that expires.
func (s *Store) CreateEmailVerification(ctx context.Context, userID uuid.UUID, email string) (CreatedEmailVerification, error) {
	email = strings.TrimSpace(email)
	if !mailer.ValidAddress(email) {
		return CreatedEmailVerification{}, ErrInvalidEmail
	}
	raw, err := generateToken()
	if err != nil {
		// Defensive: only a failing system random source reaches this.
		return CreatedEmailVerification{}, err
	}
	hash := tokenHash(raw)
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return CreatedEmailVerification{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, _, err := lockLiveUserEmail(ctx, tx, userID); err != nil {
		return CreatedEmailVerification{}, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM email_verifications WHERE user_id = $1 AND used_at IS NULL`, userID); err != nil {
		// Defensive: the user row is locked; only a DB outage reaches this.
		return CreatedEmailVerification{}, err
	}
	v, err := scanEmailVerification(tx.QueryRow(ctx, `
		INSERT INTO email_verifications (user_id, email, token_hash, expires_at)
		VALUES ($1, $2, $3, now() + $4::interval)
		RETURNING `+emailVerificationColumns,
		userID, email, hash[:], EmailVerificationTTL.String()))
	if err != nil {
		// Defensive: the token is fresh randomness and the user is locked.
		return CreatedEmailVerification{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return CreatedEmailVerification{}, err
	}
	return CreatedEmailVerification{Verification: v, Token: raw}, nil
}

// MarkEmailVerificationSent records that the provider accepted the link's
// message. A link that was replaced or used in the meantime is left alone.
func (s *Store) MarkEmailVerificationSent(ctx context.Context, id uuid.UUID) error {
	_, err := s.db.Exec(ctx, `UPDATE email_verifications SET sent_at = now() WHERE id = $1 AND used_at IS NULL`, id)
	return err
}

// LookupEmailVerification finds one of the user's links by its raw token,
// whatever its state. Another user's link is ErrNotFound.
func (s *Store) LookupEmailVerification(ctx context.Context, userID uuid.UUID, rawToken string) (model.EmailVerification, error) {
	hash := tokenHash(rawToken)
	v, err := scanEmailVerification(s.db.QueryRow(ctx, `
		SELECT `+emailVerificationColumns+`
		FROM email_verifications
		WHERE user_id = $1 AND token_hash = $2
	`, userID, hash[:]))
	if err != nil {
		if isNoRows(err) {
			return model.EmailVerification{}, ErrNotFound
		}
		// Defensive: only a DB outage reaches this.
		return model.EmailVerification{}, err
	}
	return v, nil
}

// ConfirmEmailVerification verifies the link's address and makes it the
// user's email, using up the link and dropping any others still waiting. A
// link that is unknown, another user's, used or expired is ErrNotFound. An
// address another live account has already verified, in any case, is
// ErrConflict, and nothing changes.
func (s *Store) ConfirmEmailVerification(ctx context.Context, userID uuid.UUID, rawToken string) (ConfirmedEmail, error) {
	hash := tokenHash(rawToken)
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return ConfirmedEmail{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	current, verifiedAt, err := lockLiveUserEmail(ctx, tx, userID)
	if err != nil {
		return ConfirmedEmail{}, err
	}
	var id uuid.UUID
	var email string
	err = tx.QueryRow(ctx, `
		SELECT id, email
		FROM email_verifications
		WHERE user_id = $1 AND token_hash = $2 AND used_at IS NULL AND expires_at > now()
		FOR UPDATE
	`, userID, hash[:]).Scan(&id, &email)
	if err != nil {
		if isNoRows(err) {
			return ConfirmedEmail{}, ErrNotFound
		}
		// Defensive: only a DB outage reaches this.
		return ConfirmedEmail{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE email_verifications SET used_at = now() WHERE id = $1`, id); err != nil {
		// Defensive: the row is locked; only a DB outage reaches this.
		return ConfirmedEmail{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE users SET email = $2, email_verified_at = now() WHERE id = $1`, userID, email); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return ConfirmedEmail{}, ErrConflict
		}
		// Defensive: the only constraint left is the CHECK, which a
		// non-empty address satisfies; only a DB outage reaches this.
		return ConfirmedEmail{}, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM email_verifications WHERE user_id = $1 AND used_at IS NULL`, userID); err != nil {
		// Defensive: the user row is locked; only a DB outage reaches this.
		return ConfirmedEmail{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ConfirmedEmail{}, err
	}
	confirmed := ConfirmedEmail{Email: email}
	if verifiedAt != nil && !strings.EqualFold(current, email) {
		confirmed.Previous = current
	}
	return confirmed, nil
}

// RemoveUserEmail clears the user's email and any link still waiting, and
// returns the verified address it removed, if there was one, to be told.
func (s *Store) RemoveUserEmail(ctx context.Context, userID uuid.UUID) (string, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	current, verifiedAt, err := lockLiveUserEmail(ctx, tx, userID)
	if err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `UPDATE users SET email = NULL, email_verified_at = NULL WHERE id = $1`, userID); err != nil {
		// Defensive: the user row is locked; only a DB outage reaches this.
		return "", err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM email_verifications WHERE user_id = $1 AND used_at IS NULL`, userID); err != nil {
		// Defensive: the user row is locked; only a DB outage reaches this.
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	if verifiedAt == nil {
		return "", nil
	}
	return current, nil
}

// lockLiveUserEmail locks a live user's row for an email change and returns
// the address in effect and when it was verified.
func lockLiveUserEmail(ctx context.Context, tx pgx.Tx, userID uuid.UUID) (string, *time.Time, error) {
	var email string
	var verifiedAt *time.Time
	err := tx.QueryRow(ctx, `
		SELECT COALESCE(email, ''), email_verified_at
		FROM users
		WHERE id = $1 AND deleted_at IS NULL
		FOR UPDATE
	`, userID).Scan(&email, &verifiedAt)
	if isNoRows(err) {
		return "", nil, ErrNotFound
	}
	return email, verifiedAt, err
}
