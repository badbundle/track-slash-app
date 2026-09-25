package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/bradleymackey/track-slash/internal/model"
)

// CreateGitHubCredentialParams carries a caller-chosen ID because the
// ciphertext is sealed with associated data that includes it.
type CreateGitHubCredentialParams struct {
	ID              uuid.UUID
	UserID          uuid.UUID
	Name            string
	GitHubLogin     string
	TokenCiphertext []byte
	TokenNonce      []byte
}

// UpdateGitHubCredentialParams renames a credential, replaces its token, or
// both. A nil Name keeps the current name; a nil TokenCiphertext keeps the
// current token, login, and nonce.
type UpdateGitHubCredentialParams struct {
	ID              uuid.UUID
	UserID          uuid.UUID
	Name            *string
	GitHubLogin     string
	TokenCiphertext []byte
	TokenNonce      []byte
}

type GitHubCredentialSecret struct {
	Credential model.GitHubCredential
	Ciphertext []byte `json:"-"`
	Nonce      []byte `json:"-"`
}

const githubCredentialColumns = `
	g.id, g.user_id, g.name, g.github_login,
	(SELECT count(*) FROM github_repository_connections c WHERE c.credential_id = g.id AND c.disabled_at IS NULL),
	g.last_validated_at, g.created_at, g.updated_at
`

func scanGitHubCredential(row pgx.Row, extra ...any) (model.GitHubCredential, error) {
	var out model.GitHubCredential
	dest := []any{
		&out.ID, &out.UserID, &out.Name, &out.GitHubLogin, &out.ConnectionCount,
		&out.LastValidatedAt, &out.CreatedAt, &out.UpdatedAt,
	}
	err := row.Scan(append(dest, extra...)...)
	return out, err
}

func githubCredentialWriteError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return fmt.Errorf("a saved GitHub token with that name already exists: %w", ErrConflict)
	}
	return err
}

func (s *Store) CreateGitHubCredential(ctx context.Context, p CreateGitHubCredentialParams) (model.GitHubCredential, error) {
	out, err := scanGitHubCredential(s.db.QueryRow(ctx, `
		INSERT INTO github_credentials AS g (id, user_id, name, github_login, token_ciphertext, token_nonce)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING `+githubCredentialColumns,
		p.ID, p.UserID, p.Name, p.GitHubLogin, p.TokenCiphertext, p.TokenNonce,
	))
	if err != nil {
		return model.GitHubCredential{}, githubCredentialWriteError(err)
	}
	return out, nil
}

func (s *Store) ListGitHubCredentials(ctx context.Context, userID uuid.UUID) ([]model.GitHubCredential, error) {
	rows, err := s.db.Query(ctx, `
		SELECT `+githubCredentialColumns+`
		FROM github_credentials g
		WHERE g.user_id = $1
		ORDER BY lower(g.name), g.id
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]model.GitHubCredential, 0)
	for rows.Next() {
		credential, err := scanGitHubCredential(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, credential)
	}
	return out, rows.Err()
}

// GetGitHubCredentialSecret loads a credential only for its owner, so one user
// can never connect a repository with another user's token.
func (s *Store) GetGitHubCredentialSecret(ctx context.Context, userID, id uuid.UUID) (GitHubCredentialSecret, error) {
	var out GitHubCredentialSecret
	credential, err := scanGitHubCredential(s.db.QueryRow(ctx, `
		SELECT `+githubCredentialColumns+`, g.token_ciphertext, g.token_nonce
		FROM github_credentials g
		WHERE g.id = $1 AND g.user_id = $2
	`, id, userID), &out.Ciphertext, &out.Nonce)
	if isNoRows(err) {
		return GitHubCredentialSecret{}, ErrNotFound
	}
	out.Credential = credential
	return out, err
}

func (s *Store) UpdateGitHubCredential(ctx context.Context, p UpdateGitHubCredentialParams) (model.GitHubCredential, error) {
	replaceToken := p.TokenCiphertext != nil
	out, err := scanGitHubCredential(s.db.QueryRow(ctx, `
		UPDATE github_credentials AS g
		SET name = COALESCE($3, g.name),
		    github_login = CASE WHEN $4 THEN $5 ELSE g.github_login END,
		    token_ciphertext = CASE WHEN $4 THEN $6 ELSE g.token_ciphertext END,
		    token_nonce = CASE WHEN $4 THEN $7 ELSE g.token_nonce END,
		    last_validated_at = CASE WHEN $4 THEN now() ELSE g.last_validated_at END,
		    updated_at = now()
		WHERE g.id = $1 AND g.user_id = $2
		RETURNING `+githubCredentialColumns,
		p.ID, p.UserID, p.Name, replaceToken, p.GitHubLogin, p.TokenCiphertext, p.TokenNonce,
	))
	if isNoRows(err) {
		return model.GitHubCredential{}, ErrNotFound
	}
	if err != nil {
		return model.GitHubCredential{}, githubCredentialWriteError(err)
	}
	return out, nil
}

// DeleteGitHubCredential removes a saved token and disconnects every
// repository connection that used it, in every project. Their issue links keep
// their last known state, exactly as after a manual disconnect, and
// reconnecting the repository with another token resumes them.
func (s *Store) DeleteGitHubCredential(ctx context.Context, userID, id uuid.UUID) error {
	return pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		var exists bool
		if err := tx.QueryRow(ctx, `
			SELECT true FROM github_credentials WHERE id = $1 AND user_id = $2 FOR UPDATE
		`, id, userID).Scan(&exists); err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err // defensive: DB outage past the no-rows branch
		}
		rows, err := tx.Query(ctx, `
			UPDATE github_repository_connections
			SET `+githubConnectionDisconnect+`
			WHERE credential_id = $1
			RETURNING `+githubConnectionColumns,
			id,
		)
		if err != nil {
			return err
		}
		connections, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (model.GitHubConnection, error) {
			return scanGitHubConnection(row)
		})
		if err != nil {
			return err
		}
		for _, connection := range connections {
			// The token's name is the owner's private label, so the project
			// changelog does not repeat it.
			summary := "Disconnected GitHub repository " + connection.FullName() + " because its saved token was removed"
			if err := recordGitHubConnectionDisconnected(ctx, tx, connection, summary); err != nil {
				return err
			}
		}
		_, err = tx.Exec(ctx, `DELETE FROM github_credentials WHERE id = $1`, id)
		return err
	})
}
