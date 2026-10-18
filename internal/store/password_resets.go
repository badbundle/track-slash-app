package store

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"

	"github.com/bradleymackey/track-slash/internal/model"
)

// PasswordResetTTL is how long a password reset link works.
const PasswordResetTTL = 30 * time.Minute

// PasswordResetAccount is an account a password reset can be about: a live
// account with a verified email, the only address a reset is ever sent to.
type PasswordResetAccount struct {
	UserID   uuid.UUID
	Username string
	// Email is the account's verified address.
	Email string
	// PasswordLogin is whether the account signs in with a password: it has
	// one and has not turned password sign-in off. An account without it
	// signs in with a passkey and has no password to reset.
	PasswordLogin bool
}

// passwordSignInSQL is true while the user u signs in with a password: it has
// a password credential that is neither revoked nor turned off. kindParam is
// the placeholder that holds the password credential kind, such as "$3".
func passwordSignInSQL(kindParam string) string {
	return `EXISTS (
		SELECT 1 FROM auth_credentials c
		WHERE c.user_id = u.id
		  AND c.kind = ` + kindParam + `
		  AND c.revoked_at IS NULL
		  AND c.disabled_at IS NULL
	)`
}

// usablePasswordResetSQL narrows password_resets r, joined to its user u, to
// a link that can still set a password: unused and unexpired, for a live
// account that still signs in with a password and whose verified address is
// still the one the link was sent to, verified no later than the link was
// made. So a link stops working once the address is changed, removed or
// verified again. $1 is the token's hash and $2 the password credential kind.
var usablePasswordResetSQL = `
		r.token_hash = $1
		AND r.used_at IS NULL
		AND r.expires_at > now()
		AND u.deleted_at IS NULL
		AND u.email = r.email
		AND u.email_verified_at IS NOT NULL
		AND u.email_verified_at <= r.created_at
		AND ` + passwordSignInSQL("$2")

// FindPasswordResetAccount finds the live account a forgotten-password
// request names, by username or by verified email, either without regard to
// case. A username or address that names no live account with a verified
// email is ErrNotFound: an unverified address is never sent a reset, and a
// deleted account has none.
func (s *Store) FindPasswordResetAccount(ctx context.Context, identifier string) (PasswordResetAccount, error) {
	identifier = strings.TrimSpace(identifier)
	// Anything that can't be a username matches by email alone. A username
	// never holds an @ and a verified address always does, so at most one
	// account matches.
	username, _ := NormalizeUsername(identifier)
	var account PasswordResetAccount
	err := s.db.QueryRow(ctx, `
		SELECT u.id, u.username, u.email, `+passwordSignInSQL("$3")+`
		FROM users u
		WHERE u.deleted_at IS NULL
		  AND u.email_verified_at IS NOT NULL
		  AND (u.username = $1 OR lower(u.email) = lower($2))
	`, username, identifier, string(model.AuthCredentialKindPassword)).Scan(&account.UserID, &account.Username, &account.Email, &account.PasswordLogin)
	if err != nil {
		if isNoRows(err) {
			return PasswordResetAccount{}, ErrNotFound
		}
		// Defensive: only a DB outage reaches this.
		return PasswordResetAccount{}, err
	}
	return account, nil
}

// CreatePasswordReset makes a reset link for the account's verified address
// and returns its raw token. Only the token's hash is stored, so this is the
// one chance to read it. It cancels any link the account was still waiting
// on, so only the newest one works. An account that is gone, whose verified
// address is no longer email, or that no longer signs in with a password is
// ErrNotFound.
func (s *Store) CreatePasswordReset(ctx context.Context, userID uuid.UUID, email string) (string, error) {
	raw, err := generateToken()
	if err != nil {
		// Defensive: only a failing system random source reaches this.
		return "", err
	}
	hash := tokenHash(raw)
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var locked uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT u.id
		FROM users u
		WHERE u.id = $1
		  AND u.deleted_at IS NULL
		  AND u.email_verified_at IS NOT NULL
		  AND u.email = $2
		  AND `+passwordSignInSQL("$3")+`
		FOR UPDATE
	`, userID, email, string(model.AuthCredentialKindPassword)).Scan(&locked)
	if err != nil {
		if isNoRows(err) {
			return "", ErrNotFound
		}
		// Defensive: only a DB outage reaches this.
		return "", err
	}
	if err := cancelPasswordResets(ctx, tx, userID); err != nil {
		// Defensive: the user row is locked; only a DB outage reaches this.
		return "", err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO password_resets (user_id, email, token_hash, expires_at)
		VALUES ($1, $2, $3, now() + $4::interval)
	`, userID, email, hash[:], PasswordResetTTL.String()); err != nil {
		// Defensive: the token is fresh randomness and the user is locked.
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		// Defensive: only a DB outage reaches this.
		return "", err
	}
	return raw, nil
}

// CheckPasswordReset reports whether a reset link can still set a password,
// and for which account, without using it up: mail scanners open links
// before people do. A link that is unknown, used, expired, cancelled or no
// longer bound to the account's verified address is ErrNotFound.
func (s *Store) CheckPasswordReset(ctx context.Context, rawToken string) (PasswordResetAccount, error) {
	hash := tokenHash(rawToken)
	account := PasswordResetAccount{PasswordLogin: true}
	err := s.db.QueryRow(ctx, `
		SELECT u.id, u.username, u.email
		FROM password_resets r
		JOIN users u ON u.id = r.user_id
		WHERE `+usablePasswordResetSQL,
		hash[:], string(model.AuthCredentialKindPassword)).Scan(&account.UserID, &account.Username, &account.Email)
	if err != nil {
		if isNoRows(err) {
			return PasswordResetAccount{}, ErrNotFound
		}
		// Defensive: only a DB outage reaches this.
		return PasswordResetAccount{}, err
	}
	return account, nil
}

// ResetPassword sets a new password through a reset link. In one transaction
// it stores the new password's hash, uses up the link, cancels any other link
// still waiting, uses up any reauthentication still open, and ends every web
// session and browser push subscription of the account, so nothing signed in
// before the reset carries on. API tokens and connected apps are left alone:
// the account ends those on its Tokens page. A link that can no longer set a
// password is ErrNotFound and changes nothing; a new password that
// ValidatePassword refuses is its error.
func (s *Store) ResetPassword(ctx context.Context, rawToken, newPassword string) (PasswordResetAccount, error) {
	if err := ValidatePassword(newPassword); err != nil {
		return PasswordResetAccount{}, err
	}
	// A dead link is turned away before the slow hash, which is made outside
	// the transaction so no lock waits on it. The link is checked again under
	// the lock below.
	checked, err := s.CheckPasswordReset(ctx, rawToken)
	if err != nil {
		return PasswordResetAccount{}, err
	}
	nextHash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		// Defensive: ValidatePassword keeps to what bcrypt accepts.
		return PasswordResetAccount{}, err
	}
	hash := tokenHash(rawToken)
	tx, err := s.db.Begin(ctx)
	if err != nil {
		// Defensive: the link was just read; only a DB outage reaches this.
		return PasswordResetAccount{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// The user row is locked first, as every other change to the account's
	// sign-in methods and reset links does (see lockUserCredentials), so the
	// link and the password checked next stay as they are until this commits,
	// and a link can't be used twice.
	if err := lockUserCredentials(ctx, tx, checked.UserID); err != nil {
		// Defensive: a link goes with its user, so only a DB outage reaches this.
		return PasswordResetAccount{}, err
	}
	account := PasswordResetAccount{PasswordLogin: true}
	var resetID uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT r.id, u.id, u.username, u.email
		FROM password_resets r
		JOIN users u ON u.id = r.user_id
		WHERE `+usablePasswordResetSQL+`
		FOR UPDATE OF r
	`, hash[:], string(model.AuthCredentialKindPassword)).Scan(&resetID, &account.UserID, &account.Username, &account.Email)
	if err != nil {
		if isNoRows(err) {
			return PasswordResetAccount{}, ErrNotFound
		}
		// Defensive: only a DB outage reaches this.
		return PasswordResetAccount{}, err
	}
	for _, step := range []struct {
		sql  string
		args []any
	}{
		{`UPDATE auth_credentials SET secret_hash = $2
		  WHERE user_id = $1 AND kind = $3 AND revoked_at IS NULL AND disabled_at IS NULL`,
			[]any{account.UserID, string(nextHash), string(model.AuthCredentialKindPassword)}},
		{`UPDATE password_resets SET used_at = now() WHERE id = $1`, []any{resetID}},
		{`DELETE FROM password_resets WHERE user_id = $1 AND used_at IS NULL`, []any{account.UserID}},
		{`UPDATE passkey_reauth_tokens SET consumed_at = now() WHERE user_id = $1 AND consumed_at IS NULL`, []any{account.UserID}},
	} {
		if _, err := tx.Exec(ctx, step.sql, step.args...); err != nil {
			// Defensive: the rows are locked; only a DB outage reaches this.
			return PasswordResetAccount{}, err
		}
	}
	if _, err := revokeSessionAuthTokensTx(ctx, tx, account.UserID, uuid.Nil); err != nil {
		// Defensive: the user row is locked; only a DB outage reaches this.
		return PasswordResetAccount{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		// Defensive: only a DB outage reaches this.
		return PasswordResetAccount{}, err
	}
	return account, nil
}

// cancelPasswordResets drops every reset link the user is still waiting on.
// Asking for a new link cancels them, as does a new password set any other
// way or turning password sign-in off.
func cancelPasswordResets(ctx context.Context, tx pgx.Tx, userID uuid.UUID) error {
	_, err := tx.Exec(ctx, `DELETE FROM password_resets WHERE user_id = $1 AND used_at IS NULL`, userID)
	return err
}
