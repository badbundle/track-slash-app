package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/bradleymackey/track-slash/internal/model"
)

const (
	rawTokenBytes               = 32
	authTokenUsageWriteInterval = 5 * time.Minute
)

type AuthenticatedToken struct {
	User  model.User
	Token model.AuthToken
}

type CreateAuthTokenParams struct {
	UserID    uuid.UUID
	Kind      model.AuthTokenKind
	Name      string
	ExpiresAt *time.Time
}

type CreatedAuthToken struct {
	Token    model.AuthToken
	RawToken string
}

func (s *Store) CreateAuthToken(ctx context.Context, p CreateAuthTokenParams) (CreatedAuthToken, error) {
	if !p.Kind.Valid() {
		return CreatedAuthToken{}, fmt.Errorf("invalid token kind: %w", ErrConflict)
	}
	// Connector access tokens are minted by the authorization server, which
	// records the client they belong to. One created here would have no client
	// behind it, so nothing could revoke it and the tokens page would count it
	// as a connection that does not exist.
	if p.Kind == model.AuthTokenKindOAuth {
		return CreatedAuthToken{}, fmt.Errorf("oauth tokens are issued through the authorization server: %w", ErrConflict)
	}
	raw, err := generateToken()
	if err != nil {
		return CreatedAuthToken{}, err
	}
	hash := tokenHash(raw)
	const q = `
		INSERT INTO auth_tokens (user_id, kind, name, token_hash, expires_at)
		SELECT id, $2, $3, $4, $5 FROM users
		WHERE id = $1 AND deleted_at IS NULL
		RETURNING id, user_id, kind, name, created_at, last_used_at, expires_at, revoked_at
	`
	var out model.AuthToken
	err = s.db.QueryRow(ctx, q, p.UserID, string(p.Kind), p.Name, hash[:], p.ExpiresAt).Scan(
		&out.ID, &out.UserID, &out.Kind, &out.Name, &out.CreatedAt, &out.LastUsedAt, &out.ExpiresAt, &out.RevokedAt,
	)
	if err != nil {
		if isNoRows(err) {
			return CreatedAuthToken{}, ErrNotFound
		}
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23503" {
			return CreatedAuthToken{}, ErrNotFound
		}
		return CreatedAuthToken{}, err
	}
	return CreatedAuthToken{Token: out, RawToken: raw}, nil
}

func (s *Store) AuthenticateToken(ctx context.Context, raw string) (AuthenticatedToken, error) {
	hash := tokenHash(raw)
	const q = `
		SELECT
			u.id, u.username, COALESCE(u.email, ''), u.name, u.is_admin, u.created_at,
			u.profile_image_object_id, u.profile_image_thumbnail_object_id,
			t.id, t.user_id, t.kind, t.name, t.created_at, t.last_used_at, t.expires_at, t.revoked_at
		FROM auth_tokens t
		JOIN users u ON u.id = t.user_id
		WHERE t.token_hash = $1
		  AND t.revoked_at IS NULL
		  AND (t.expires_at IS NULL OR t.expires_at > now())
		  AND u.deleted_at IS NULL
	`
	var out AuthenticatedToken
	var profileImageID, profileThumbnailID uuid.NullUUID
	err := s.db.QueryRow(ctx, q, hash[:]).Scan(
		&out.User.ID, &out.User.Username, &out.User.Email, &out.User.Name, &out.User.IsAdmin, &out.User.CreatedAt,
		&profileImageID, &profileThumbnailID,
		&out.Token.ID, &out.Token.UserID, &out.Token.Kind, &out.Token.Name,
		&out.Token.CreatedAt, &out.Token.LastUsedAt, &out.Token.ExpiresAt, &out.Token.RevokedAt,
	)
	if err != nil {
		if isNoRows(err) {
			return AuthenticatedToken{}, ErrUnauthorized
		}
		return AuthenticatedToken{}, err
	}
	if profileImageID.Valid {
		id := profileImageID.UUID
		out.User.ProfileImageObjectID = &id
	}
	if profileThumbnailID.Valid {
		id := profileThumbnailID.UUID
		out.User.ProfileImageThumbnailObjectID = &id
	}
	// Usage tracking is best-effort: a non-critical audit write must not reject an otherwise valid token.
	_, _ = s.db.Exec(ctx, `
		UPDATE auth_tokens
		SET last_used_at = now()
		WHERE id = $1
		  AND (last_used_at IS NULL OR last_used_at < now() - make_interval(secs => $2))
	`, out.Token.ID, authTokenUsageWriteInterval.Seconds())
	return out, nil
}

func (s *Store) ListAuthTokens(ctx context.Context, userID uuid.UUID) ([]model.AuthToken, error) {
	if _, err := s.GetUser(ctx, userID); err != nil {
		return nil, err
	}
	const q = `
		SELECT id, user_id, kind, name, created_at, last_used_at, expires_at, revoked_at
		FROM auth_tokens
		WHERE user_id = $1
		ORDER BY created_at ASC, id ASC
	`
	rows, err := s.db.Query(ctx, q, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []model.AuthToken{}
	for rows.Next() {
		var tok model.AuthToken
		if err := rows.Scan(&tok.ID, &tok.UserID, &tok.Kind, &tok.Name, &tok.CreatedAt, &tok.LastUsedAt, &tok.ExpiresAt, &tok.RevokedAt); err != nil {
			return nil, err
		}
		out = append(out, tok)
	}
	return out, rows.Err()
}

func (s *Store) RevokeAuthToken(ctx context.Context, id uuid.UUID) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE auth_tokens SET revoked_at = now()
		WHERE id = $1 AND revoked_at IS NULL
	`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) RevokeAuthTokenForUser(ctx context.Context, userID, id uuid.UUID) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE auth_tokens SET revoked_at = now()
		WHERE id = $1 AND user_id = $2 AND revoked_at IS NULL
	`, id, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// RevokeSessionAuthTokensForUser revokes every live web session belonging to a
// user and reports how many it revoked. API tokens are long-lived by design and
// are never touched.
func (s *Store) RevokeSessionAuthTokensForUser(ctx context.Context, userID uuid.UUID) (int64, error) {
	tag, err := s.db.Exec(ctx, `
		UPDATE auth_tokens SET revoked_at = now()
		WHERE user_id = $1 AND kind = $2 AND revoked_at IS NULL
	`, userID, model.AuthTokenKindSession)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

func generateToken() (string, error) {
	b := make([]byte, rawTokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func tokenHash(raw string) [sha256.Size]byte {
	return sha256.Sum256([]byte(raw))
}
