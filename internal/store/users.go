package store

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/bradleymackey/track-slash/internal/model"
)

type userScanner interface {
	Scan(dest ...any) error
}

func scanUser(row userScanner) (model.User, error) {
	var u model.User
	var profileImageID, profileThumbnailID uuid.NullUUID
	err := row.Scan(&u.ID, &u.Username, &u.Email, &u.Name, &u.IsAdmin, &u.CreatedAt, &profileImageID, &profileThumbnailID)
	if err != nil {
		return model.User{}, err
	}
	if profileImageID.Valid {
		id := profileImageID.UUID
		u.ProfileImageObjectID = &id
	}
	if profileThumbnailID.Valid {
		id := profileThumbnailID.UUID
		u.ProfileImageThumbnailObjectID = &id
	}
	return u, nil
}

func (s *Store) CreateUser(ctx context.Context, email, name string) (model.User, error) {
	username := UsernameFromEmail(email)
	return s.CreateUserProfile(ctx, username, email, name)
}

func (s *Store) CreateUserProfile(ctx context.Context, username, email, name string) (model.User, error) {
	username, err := NormalizeUsername(username)
	if err != nil {
		return model.User{}, err
	}
	const q = `
		INSERT INTO users (username, email, name)
		VALUES ($1, $2, $3)
		RETURNING id, username, COALESCE(email, ''), name, is_admin, created_at, profile_image_object_id, profile_image_thumbnail_object_id
	`
	u, err := scanUser(s.db.QueryRow(ctx, q, username, email, name))
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return model.User{}, ErrConflict
		}
		return model.User{}, err
	}
	return u, nil
}

func (s *Store) GetUser(ctx context.Context, id uuid.UUID) (model.User, error) {
	const q = `SELECT id, username, COALESCE(email, ''), name, is_admin, created_at, profile_image_object_id, profile_image_thumbnail_object_id FROM users WHERE id = $1 AND deleted_at IS NULL`
	u, err := scanUser(s.db.QueryRow(ctx, q, id))
	if err != nil {
		if isNoRows(err) {
			return model.User{}, ErrNotFound
		}
		return model.User{}, err
	}
	return u, nil
}

func (s *Store) GetUserByUsername(ctx context.Context, username string) (model.User, error) {
	username, err := NormalizeUsername(username)
	if err != nil {
		return model.User{}, err
	}
	const q = `SELECT id, username, COALESCE(email, ''), name, is_admin, created_at, profile_image_object_id, profile_image_thumbnail_object_id FROM users WHERE username = $1 AND deleted_at IS NULL`
	u, err := scanUser(s.db.QueryRow(ctx, q, username))
	if err != nil {
		if isNoRows(err) {
			return model.User{}, ErrNotFound
		}
		return model.User{}, err
	}
	return u, nil
}

// ErrEmailBelongsToNonAdmin refuses to make an existing account an admin just
// because it holds an email. Anyone can set their own email unverified, so an
// operator bootstrapping an admin by email could otherwise promote a stranger
// who had claimed it first.
var ErrEmailBelongsToNonAdmin = fmt.Errorf("that email belongs to an existing account that is not an admin: %w", ErrConflict)

// ErrEmailBelongsToDeletedUser refuses to bring a deleted account back by
// bootstrapping an admin with its email. Deleting an account is final: the
// row keeps its email, so the bootstrap needs a different one.
var ErrEmailBelongsToDeletedUser = fmt.Errorf("that email belongs to a deleted account, which cannot be restored: %w", ErrConflict)

// ErrEmailHeldBySeveralAccounts refuses to pick one account by an email that
// several hold. Only a verified address is unique, so an unverified one can be
// on more than one account.
var ErrEmailHeldBySeveralAccounts = fmt.Errorf("several accounts hold that email, so it does not say which one to use: %w", ErrConflict)

// CreateOrUpdateAdminUser creates an admin with this email, or refreshes the
// live admin who already has it. A live non-admin with the email is refused
// with ErrEmailBelongsToNonAdmin; PromoteUserToAdminByEmail does that on
// purpose. An email held only by deleted accounts is refused with
// ErrEmailBelongsToDeletedUser.
func (s *Store) CreateOrUpdateAdminUser(ctx context.Context, email, name string) (model.User, error) {
	username := UsernameFromEmail(email)
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return model.User{}, err // defensive: DB outage
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// Bootstraps take turns, so two at once cannot both create an admin for
	// the same email now that the email column is not unique.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('track-slash admin bootstrap'))`); err != nil {
		return model.User{}, err // defensive: DB outage
	}
	holders, err := lockUsersHoldingEmail(ctx, tx, email, true)
	if err != nil {
		return model.User{}, err // defensive: DB outage
	}
	live := slices.DeleteFunc(slices.Clone(holders), func(h emailHolder) bool { return h.deleted })
	var u model.User
	switch {
	case slices.ContainsFunc(live, func(h emailHolder) bool { return !h.admin }):
		return model.User{}, ErrEmailBelongsToNonAdmin
	case len(live) > 1:
		return model.User{}, ErrEmailHeldBySeveralAccounts
	case len(live) == 1:
		u, err = scanUser(tx.QueryRow(ctx, `
			UPDATE users SET username = $2, name = $3
			WHERE id = $1
			RETURNING id, username, COALESCE(email, ''), name, is_admin, created_at, profile_image_object_id, profile_image_thumbnail_object_id
		`, live[0].id, username, name))
	case len(holders) > 0:
		return model.User{}, ErrEmailBelongsToDeletedUser
	default:
		u, err = scanUser(tx.QueryRow(ctx, `
			INSERT INTO users (username, email, name, is_admin)
			VALUES ($1, $2, $3, true)
			RETURNING id, username, COALESCE(email, ''), name, is_admin, created_at, profile_image_object_id, profile_image_thumbnail_object_id
		`, username, email, name))
	}
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			// Another account has the username this email gives.
			return model.User{}, ErrConflict
		}
		return model.User{}, err // defensive: DB outage
	}
	if err := tx.Commit(ctx); err != nil {
		return model.User{}, err // defensive: DB outage
	}
	return u, nil
}

// PromoteUserToAdminByEmail makes the live account with this email an admin,
// keeping its username and name. It is for an operator who has checked who
// holds the email, and it refuses when several live accounts do.
func (s *Store) PromoteUserToAdminByEmail(ctx context.Context, email string) (model.User, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return model.User{}, err // defensive: DB outage
	}
	defer func() { _ = tx.Rollback(ctx) }()
	holders, err := lockUsersHoldingEmail(ctx, tx, email, false)
	if err != nil {
		return model.User{}, err // defensive: DB outage
	}
	switch len(holders) {
	case 0:
		return model.User{}, ErrNotFound
	case 1:
	default:
		return model.User{}, ErrEmailHeldBySeveralAccounts
	}
	u, err := scanUser(tx.QueryRow(ctx, `
		UPDATE users SET is_admin = true
		WHERE id = $1
		RETURNING id, username, COALESCE(email, ''), name, is_admin, created_at, profile_image_object_id, profile_image_thumbnail_object_id
	`, holders[0].id))
	if err != nil {
		// Defensive: the row is locked; only a DB outage reaches this.
		return model.User{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return model.User{}, err // defensive: DB outage
	}
	return u, nil
}

type emailHolder struct {
	id      uuid.UUID
	admin   bool
	deleted bool
}

// lockUsersHoldingEmail locks the accounts whose email is exactly email,
// oldest first, leaving out deleted ones unless includeDeleted is set.
func lockUsersHoldingEmail(ctx context.Context, tx pgx.Tx, email string, includeDeleted bool) ([]emailHolder, error) {
	rows, err := tx.Query(ctx, `
		SELECT id, is_admin, deleted_at IS NOT NULL FROM users
		WHERE email = $1 AND ($2 OR deleted_at IS NULL)
		ORDER BY created_at, id
		FOR UPDATE
	`, email, includeDeleted)
	if err != nil {
		// Defensive: a fixed query; only a DB outage reaches this.
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (emailHolder, error) {
		var h emailHolder
		err := row.Scan(&h.id, &h.admin, &h.deleted)
		return h, err
	})
}

// UsersCursor is the keyset position for ListUsers. Encoded base64 JSON at the
// HTTP boundary; the schema is server-private.
type UsersCursor struct {
	CreatedAt time.Time `json:"t"`
	ID        uuid.UUID `json:"i"`
}

type ListUsersParams struct {
	Cursor *UsersCursor
	Limit  int // caller is expected to have clamped to a sane upper bound
}

// ListUsers returns up to Limit users plus a HasMore flag the caller turns
// into a next_cursor. Ordered (created_at ASC, id ASC) so the keyset is
// strictly monotonic.
func (s *Store) ListUsers(ctx context.Context, p ListUsersParams) ([]model.User, bool, error) {
	args := []any{}
	q := `SELECT id, username, COALESCE(email, ''), name, is_admin, created_at, profile_image_object_id, profile_image_thumbnail_object_id FROM users WHERE deleted_at IS NULL`
	if p.Cursor != nil {
		args = append(args, p.Cursor.CreatedAt, p.Cursor.ID)
		q += ` AND (created_at, id) > ($1, $2)`
	}
	args = append(args, p.Limit+1)
	q += fmt.Sprintf(` ORDER BY created_at ASC, id ASC LIMIT $%d`, len(args))

	rows, err := s.db.Query(ctx, q, args...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()

	users := make([]model.User, 0, p.Limit)
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, false, err
		}
		users = append(users, u)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	hasMore := len(users) > p.Limit
	if hasMore {
		users = users[:p.Limit]
	}
	return users, hasMore, nil
}

// ListUserProfilesByID returns the live users among ids, ordered by username.
// It is for rendering people to other users, so it never reads email or the
// admin flag: those stay empty and false. Unknown and deleted ids are skipped.
func (s *Store) ListUserProfilesByID(ctx context.Context, ids []uuid.UUID) ([]model.User, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	const q = `SELECT id, username, '', name, false, created_at, profile_image_object_id, profile_image_thumbnail_object_id
		FROM users WHERE id = ANY($1) AND deleted_at IS NULL ORDER BY username`
	rows, err := s.db.Query(ctx, q, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	users := make([]model.User, 0, len(ids))
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return users, nil
}

// DeleteUser soft-deletes a user and ends every way into the account in the
// same transaction: its tokens, sessions, connector grants, codes, password and
// passkeys are all revoked, so none of them rests on a deleted_at filter to
// stay dead. The connectors they registered go with them, for everyone who
// approved one: their secrets are scrubbed and every token they hold stops
// working at once.
func (s *Store) DeleteUser(ctx context.Context, id uuid.UUID) error {
	return pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			UPDATE users SET deleted_at = now()
			WHERE id = $1 AND deleted_at IS NULL
		`, id)
		if err != nil {
			// Defensive: soft-delete has no expected FK/check mapping.
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		// The approvals go first, as when a single grant ends; see
		// lockOAuthGrant.
		for _, q := range []string{
			`DELETE FROM oauth_client_consents WHERE user_id = $1`,
			`DELETE FROM oauth_authorization_codes WHERE user_id = $1`,
			`UPDATE oauth_refresh_tokens SET revoked_at = now() WHERE user_id = $1 AND revoked_at IS NULL`,
			`UPDATE auth_tokens SET revoked_at = now() WHERE user_id = $1 AND revoked_at IS NULL`,
			`UPDATE auth_credentials SET revoked_at = now() WHERE user_id = $1 AND revoked_at IS NULL`,
		} {
			if _, err := tx.Exec(ctx, q, id); err != nil {
				return err // defensive: DB outage mid-transaction
			}
		}
		rows, err := tx.Query(ctx, `SELECT id FROM oauth_clients WHERE created_by_id = $1 AND disabled_at IS NULL`, id)
		if err != nil {
			return err
		}
		clientIDs, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
		if err != nil {
			return err
		}
		for _, clientID := range clientIDs {
			if err := disableOAuthClient(ctx, tx, clientID); err != nil {
				return err
			}
		}
		return nil
	})
}
