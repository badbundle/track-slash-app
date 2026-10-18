package store

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"

	"github.com/bradleymackey/track-slash/internal/model"
)

func ValidateEmail(email string) error {
	email = strings.TrimSpace(email)
	if email != "" && !strings.Contains(email, "@") {
		return errors.New("invalid email")
	}
	return nil
}

func (s *Store) UpdateUserProfile(ctx context.Context, userID uuid.UUID, name, email string) (model.User, error) {
	name = strings.TrimSpace(name)
	email = strings.TrimSpace(email)
	if name == "" {
		return model.User{}, errors.New("name required")
	}
	if err := ValidateEmail(email); err != nil {
		return model.User{}, err
	}
	var emailValue any
	if email != "" {
		emailValue = email
	}
	// A new address starts unverified and an unchanged one keeps its
	// verification. Only verified addresses are unique, so this can no longer
	// collide with another account's address.
	const q = `
		UPDATE users
		SET name = $2,
		    email = $3,
		    email_verified_at = CASE WHEN email IS NOT DISTINCT FROM $3 THEN email_verified_at END
		WHERE id = $1 AND deleted_at IS NULL
		RETURNING id, username, COALESCE(email, ''), name, is_admin, created_at, profile_image_object_id, profile_image_thumbnail_object_id
	`
	u, err := scanUser(s.db.QueryRow(ctx, q, userID, name, emailValue))
	if err != nil {
		if isNoRows(err) {
			return model.User{}, ErrNotFound
		}
		return model.User{}, err
	}
	return u, nil
}

// UpdateUserName changes only the display name, so saving a name can never
// write back an address read before a confirmation changed it.
func (s *Store) UpdateUserName(ctx context.Context, userID uuid.UUID, name string) (model.User, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return model.User{}, errors.New("name required")
	}
	u, err := scanUser(s.db.QueryRow(ctx, `
		UPDATE users SET name = $2
		WHERE id = $1 AND deleted_at IS NULL
		RETURNING id, username, COALESCE(email, ''), name, is_admin, created_at, profile_image_object_id, profile_image_thumbnail_object_id
	`, userID, name))
	if err != nil {
		if isNoRows(err) {
			return model.User{}, ErrNotFound
		}
		return model.User{}, err
	}
	return u, nil
}

func (s *Store) ChangePassword(ctx context.Context, userID uuid.UUID, currentPassword, newPassword string) error {
	if err := ValidatePassword(newPassword); err != nil {
		return err
	}
	const q = `
		SELECT c.id, c.secret_hash
		FROM auth_credentials c
		JOIN users u ON u.id = c.user_id
		WHERE c.user_id = $1
		  AND c.kind = $2
		  AND c.revoked_at IS NULL
		  AND c.disabled_at IS NULL
		  AND u.deleted_at IS NULL
	`
	var credentialID uuid.UUID
	var hash string
	err := s.db.QueryRow(ctx, q, userID, string(model.AuthCredentialKindPassword)).Scan(&credentialID, &hash)
	if err != nil {
		if isNoRows(err) {
			return ErrUnauthorized
		}
		return err
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(currentPassword)) != nil {
		return ErrUnauthorized
	}
	nextHash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	if _, err := s.db.Exec(ctx, `
		UPDATE auth_credentials SET secret_hash = $2, last_used_at = now()
		WHERE id = $1
	`, credentialID, string(nextHash)); err != nil {
		// Defensive: credential row was just read; failure here is a DB/runtime fault.
		return err
	}
	return nil
}

func (s *Store) HasPasswordCredential(ctx context.Context, userID uuid.UUID) (bool, error) {
	const q = `
		SELECT EXISTS (
			SELECT 1
			FROM auth_credentials c
			JOIN users u ON u.id = c.user_id
			WHERE c.user_id = $1
			  AND c.kind = $2
			  AND c.revoked_at IS NULL
			  AND c.disabled_at IS NULL
			  AND u.deleted_at IS NULL
		)
	`
	var ok bool
	if err := s.db.QueryRow(ctx, q, userID, string(model.AuthCredentialKindPassword)).Scan(&ok); err != nil {
		return false, err
	}
	return ok, nil
}

// lockUserCredentials serialises changes to a user's sign-in methods. Turning
// off password login and revoking a passkey each check that another way to
// sign in remains; without the lock two of them racing could each see the
// other's credential and together leave the account with none.
func lockUserCredentials(ctx context.Context, tx pgx.Tx, userID uuid.UUID) error {
	var locked uuid.UUID
	err := tx.QueryRow(ctx, `SELECT id FROM users WHERE id = $1 FOR UPDATE`, userID).Scan(&locked)
	if isNoRows(err) {
		return ErrNotFound
	}
	return err
}

type passwordLoginQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func (s *Store) PasswordLoginState(ctx context.Context, userID uuid.UUID) (model.PasswordLoginState, error) {
	return passwordLoginState(ctx, s.db, userID)
}

func (s *Store) SetPasswordLoginEnabled(ctx context.Context, userID uuid.UUID, enabled bool) (model.PasswordLoginState, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return model.PasswordLoginState{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := lockUserCredentials(ctx, tx, userID); err != nil {
		return model.PasswordLoginState{}, err
	}
	state, err := passwordLoginState(ctx, tx, userID)
	if err != nil {
		return model.PasswordLoginState{}, err
	}
	if !state.HasPassword {
		return model.PasswordLoginState{}, ErrConflict
	}
	if !enabled && state.ActivePasskeys == 0 {
		return model.PasswordLoginState{}, ErrConflict
	}

	if enabled {
		if _, err := tx.Exec(ctx, `
			UPDATE auth_credentials
			SET disabled_at = NULL
			WHERE user_id = $1
			  AND kind = $2
			  AND revoked_at IS NULL
		`, userID, string(model.AuthCredentialKindPassword)); err != nil {
			return model.PasswordLoginState{}, err
		}
	} else if state.Enabled {
		if _, err := tx.Exec(ctx, `
			UPDATE auth_credentials
			SET disabled_at = now()
			WHERE user_id = $1
			  AND kind = $2
			  AND revoked_at IS NULL
			  AND disabled_at IS NULL
		`, userID, string(model.AuthCredentialKindPassword)); err != nil {
			return model.PasswordLoginState{}, err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE passkey_reauth_tokens
			SET consumed_at = now()
			WHERE user_id = $1
			  AND consumed_at IS NULL
		`, userID); err != nil {
			return model.PasswordLoginState{}, err
		}
	}

	next, err := passwordLoginState(ctx, tx, userID)
	if err != nil {
		return model.PasswordLoginState{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return model.PasswordLoginState{}, err
	}
	return next, nil
}

func passwordLoginState(ctx context.Context, q passwordLoginQuerier, userID uuid.UUID) (model.PasswordLoginState, error) {
	const query = `
		SELECT
			EXISTS (
				SELECT 1 FROM auth_credentials c
				WHERE c.user_id = u.id
				  AND c.kind = $2
				  AND c.revoked_at IS NULL
			),
			EXISTS (
				SELECT 1 FROM auth_credentials c
				WHERE c.user_id = u.id
				  AND c.kind = $2
				  AND c.revoked_at IS NULL
				  AND c.disabled_at IS NULL
			),
			(
				SELECT count(*)
				FROM auth_credentials c
				WHERE c.user_id = u.id
				  AND c.kind = $3
				  AND c.revoked_at IS NULL
				  AND c.disabled_at IS NULL
			)
		FROM users u
		WHERE u.id = $1 AND u.deleted_at IS NULL
	`
	var state model.PasswordLoginState
	if err := q.QueryRow(ctx, query, userID, string(model.AuthCredentialKindPassword), string(model.AuthCredentialKindPasskey)).Scan(
		&state.HasPassword, &state.Enabled, &state.ActivePasskeys,
	); err != nil {
		if isNoRows(err) {
			return model.PasswordLoginState{}, ErrNotFound
		}
		return model.PasswordLoginState{}, err
	}
	state.CanDisable = state.HasPassword && state.Enabled && state.ActivePasskeys > 0
	return state, nil
}
