package store

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/bradleymackey/track-slash/internal/model"
)

const (
	// A code is handed straight back to the client, which exchanges it
	// immediately. A minute is generous for a redirect and a round trip, and
	// short enough that a code captured from a browser history or a proxy log
	// is almost always already dead.
	oauthAuthorizationCodeTTL = time.Minute
	oauthAccessTokenTTL       = time.Hour
	oauthRefreshTokenTTL      = 90 * 24 * time.Hour
	// Expired access tokens are deleted opportunistically rather than swept.
	// The session sweep in 0040 is deliberately scoped to kind = 'session', and
	// widening it would make every token refresh in the product pay for OAuth.
	oauthExpiredTokenPurgeLimit = 200
)

// ErrOAuthReplay marks a credential that was already used once. It is a signal
// that a copy leaked, so the caller revokes the whole grant rather than simply
// refusing. It wraps ErrUnauthorized so writeStoreError still degrades to 401
// for any caller that does not special-case it.
var ErrOAuthReplay = fmt.Errorf("oauth credential replayed: %w", ErrUnauthorized)

const oauthClientColumns = `
	id, client_id, name, redirect_uris, created_by_id, disabled_at, created_at, updated_at
`

// liveOAuthClientFilter keeps a client usable only while it is enabled and
// the account that registered it still exists; deleting that account ends
// the client for everyone who approved it.
const liveOAuthClientFilter = `
	disabled_at IS NULL
	AND EXISTS (SELECT 1 FROM users creator WHERE creator.id = oauth_clients.created_by_id AND creator.deleted_at IS NULL)
`

type oauthClientScanner interface {
	Scan(dest ...any) error
}

func scanOAuthClient(row oauthClientScanner) (model.OAuthClient, error) {
	var out model.OAuthClient
	err := row.Scan(
		&out.ID, &out.ClientID, &out.Name, &out.RedirectURIs, &out.CreatedByID,
		&out.DisabledAt, &out.CreatedAt, &out.UpdatedAt,
	)
	return out, err
}

type CreateOAuthClientParams struct {
	UserID       uuid.UUID
	Name         string
	RedirectURIs []string
}

// CreatedOAuthClient carries the one and only copy of the client secret. It is
// never persisted in a readable form, so a caller that drops it cannot get it
// back and must register a new client.
type CreatedOAuthClient struct {
	Client    model.OAuthClient
	RawSecret string
}

func (s *Store) CreateOAuthClient(ctx context.Context, p CreateOAuthClientParams) (CreatedOAuthClient, error) {
	clientID, err := generateToken()
	if err != nil {
		return CreatedOAuthClient{}, err
	}
	secret, err := generateToken()
	if err != nil {
		return CreatedOAuthClient{}, err
	}
	hash := tokenHash(secret)
	const q = `
		INSERT INTO oauth_clients (client_id, secret_hash, name, redirect_uris, created_by_id)
		SELECT $1, $2, $3, $4, id FROM users
		WHERE id = $5 AND deleted_at IS NULL
		RETURNING ` + oauthClientColumns
	client, err := scanOAuthClient(s.db.QueryRow(ctx, q, clientID, hash[:], p.Name, p.RedirectURIs, p.UserID))
	if err != nil {
		// The SELECT yields no row for an unknown or deleted user, so a missing
		// owner arrives here rather than as a foreign key violation. client_id
		// is 32 random bytes, so its unique index cannot realistically fire
		// either; only the CHECK constraints are reachable from real input.
		if isNoRows(err) {
			return CreatedOAuthClient{}, ErrNotFound
		}
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23514" {
			return CreatedOAuthClient{}, fmt.Errorf("name must be 1 to 100 characters, with between 1 and 10 redirect URIs: %w", ErrConflict)
		}
		return CreatedOAuthClient{}, err
	}
	return CreatedOAuthClient{Client: client, RawSecret: secret}, nil
}

func (s *Store) ListOAuthClientsForUser(ctx context.Context, userID uuid.UUID) ([]model.OAuthClient, error) {
	if _, err := s.GetUser(ctx, userID); err != nil {
		return nil, err
	}
	const q = `
		SELECT ` + oauthClientColumns + `
		FROM oauth_clients
		WHERE created_by_id = $1 AND disabled_at IS NULL
		ORDER BY created_at ASC, id ASC
	`
	rows, err := s.db.Query(ctx, q, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []model.OAuthClient{}
	for rows.Next() {
		client, err := scanOAuthClient(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, client)
	}
	return out, rows.Err()
}

// GetOAuthClientByClientID resolves the public client_id an authorization
// request carries. Disabled clients are invisible, so revoking one immediately
// stops new flows as well as existing tokens.
func (s *Store) GetOAuthClientByClientID(ctx context.Context, clientID string) (model.OAuthClient, error) {
	const q = `
		SELECT ` + oauthClientColumns + `
		FROM oauth_clients
		WHERE client_id = $1 AND ` + liveOAuthClientFilter + `
	`
	client, err := scanOAuthClient(s.db.QueryRow(ctx, q, clientID))
	if err != nil {
		if isNoRows(err) {
			return model.OAuthClient{}, ErrNotFound
		}
		return model.OAuthClient{}, err
	}
	return client, nil
}

// AuthenticateOAuthClient verifies a client_id and secret pair presented at the
// token endpoint.
//
// An unknown client_id and a wrong secret both return ErrUnauthorized after the
// same constant-time comparison, so the endpoint cannot be used to learn which
// client IDs exist.
func (s *Store) AuthenticateOAuthClient(ctx context.Context, clientID, secret string) (model.OAuthClient, error) {
	const q = `
		SELECT ` + oauthClientColumns + `, secret_hash
		FROM oauth_clients
		WHERE client_id = $1 AND ` + liveOAuthClientFilter + `
	`
	var out model.OAuthClient
	var storedHash []byte
	err := s.db.QueryRow(ctx, q, clientID).Scan(
		&out.ID, &out.ClientID, &out.Name, &out.RedirectURIs, &out.CreatedByID,
		&out.DisabledAt, &out.CreatedAt, &out.UpdatedAt, &storedHash,
	)
	if err != nil {
		if isNoRows(err) {
			// Hash the presented secret anyway, so an unknown client does the
			// same work as a known one with a wrong secret. The database round
			// trip dominates either way, so this narrows the signal rather than
			// removing it; the failure budget in the token endpoint is what
			// actually makes probing client IDs impractical.
			decoy := tokenHash(secret)
			subtle.ConstantTimeCompare(decoy[:], decoy[:])
			return model.OAuthClient{}, ErrUnauthorized
		}
		return model.OAuthClient{}, err
	}
	presented := tokenHash(secret)
	if subtle.ConstantTimeCompare(presented[:], storedHash) != 1 {
		return model.OAuthClient{}, ErrUnauthorized
	}
	return out, nil
}

// DisableOAuthClientForUser revokes a client and everything it was ever
// granted, in one transaction: the secret is scrubbed, remembered consent is
// dropped, and every live access and refresh token stops working at once.
func (s *Store) DisableOAuthClientForUser(ctx context.Context, userID, id uuid.UUID) error {
	return pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		var owned bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS (SELECT 1 FROM oauth_clients WHERE id = $1 AND created_by_id = $2 AND disabled_at IS NULL)
		`, id, userID).Scan(&owned); err != nil {
			return err
		}
		if !owned {
			return ErrNotFound
		}
		return disableOAuthClient(ctx, tx, id)
	})
}

// disableOAuthClient revokes a client and everything it was ever granted: the
// secret is scrubbed, remembered consent is dropped, and every live access and
// refresh token stops working at once.
func disableOAuthClient(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	// The hash column is NOT NULL with an exact-length CHECK, so the scrub
	// writes 32 zero bytes rather than nulling it. No real secret hashes to
	// that, so the row cannot authenticate even if it were re-enabled.
	if _, err := tx.Exec(ctx, `
		UPDATE oauth_clients
		SET disabled_at = now(),
		    secret_hash = decode(repeat('00', 32), 'hex'),
		    updated_at = now()
		WHERE id = $1 AND disabled_at IS NULL
	`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM oauth_client_consents WHERE client_id = $1`, id); err != nil {
		return err // defensive: DB outage mid-transaction
	}
	// Codes the client has not exchanged yet die with it. Client
	// authentication would already refuse the exchange, but revocation
	// should not rest on a filter in a different function.
	if _, err := tx.Exec(ctx, `DELETE FROM oauth_authorization_codes WHERE client_id = $1`, id); err != nil {
		return err // defensive: DB outage mid-transaction
	}
	if _, err := tx.Exec(ctx, `
		UPDATE oauth_refresh_tokens SET revoked_at = now()
		WHERE client_id = $1 AND revoked_at IS NULL
	`, id); err != nil {
		return err // defensive: DB outage mid-transaction
	}
	_, err := tx.Exec(ctx, `
		UPDATE auth_tokens SET revoked_at = now()
		WHERE oauth_client_id = $1 AND revoked_at IS NULL
	`, id)
	return err
}

// ListOAuthGrantsForUser lists the connectors a user has approved that still
// hold, or can still obtain, access for them: a remembered approval, a live
// refresh token or a live access token. Disabled clients are gone already.
func (s *Store) ListOAuthGrantsForUser(ctx context.Context, userID uuid.UUID) ([]model.OAuthGrant, error) {
	rows, err := s.db.Query(ctx, `
		SELECT c.id, c.name, COALESCE(owner.username, ''), g.connected_at
		FROM (
			SELECT client_id, min(created_at) AS connected_at
			FROM (
				SELECT client_id, created_at FROM oauth_client_consents WHERE user_id = $1
				UNION ALL
				SELECT client_id, created_at FROM oauth_refresh_tokens
				WHERE user_id = $1 AND revoked_at IS NULL AND expires_at > now()
				UNION ALL
				SELECT oauth_client_id, created_at FROM auth_tokens
				WHERE user_id = $1 AND oauth_client_id IS NOT NULL AND revoked_at IS NULL
				  AND (expires_at IS NULL OR expires_at > now())
			) held
			GROUP BY client_id
		) g
		JOIN oauth_clients c ON c.id = g.client_id AND c.disabled_at IS NULL
		LEFT JOIN users owner ON owner.id = c.created_by_id
		ORDER BY g.connected_at DESC, c.id
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.OAuthGrant{}
	for rows.Next() {
		var grant model.OAuthGrant
		if err := rows.Scan(&grant.ClientID, &grant.Name, &grant.RegisteredBy, &grant.ConnectedAt); err != nil {
			return nil, err
		}
		out = append(out, grant)
	}
	return out, rows.Err()
}

// DisconnectOAuthGrantForUser ends one connector's access for the user who
// approved it, whoever registered it: every token it holds for them is
// revoked, unexchanged codes are dropped, and the remembered approval is
// forgotten so the next connection asks again. It is ErrNotFound when the
// connector held nothing for this user.
func (s *Store) DisconnectOAuthGrantForUser(ctx context.Context, userID, clientID uuid.UUID) error {
	return pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		ended, err := endOAuthGrant(ctx, tx, clientID, userID)
		if err != nil {
			return err
		}
		if ended == 0 {
			return ErrNotFound
		}
		return nil
	})
}

// endOAuthGrant is revokeOAuthGrants plus forgetting the approval and any
// codes in flight, and reports how many rows it touched.
//
// The approval is deleted first. That waits for any exchange or refresh
// holding it (see lockOAuthGrant), so the revocations after it see every
// token those issued.
func endOAuthGrant(ctx context.Context, tx pgx.Tx, clientID, userID uuid.UUID) (int64, error) {
	var total int64
	for _, q := range []string{
		`DELETE FROM oauth_client_consents WHERE client_id = $1 AND user_id = $2`,
		`UPDATE oauth_refresh_tokens SET revoked_at = now() WHERE client_id = $1 AND user_id = $2 AND revoked_at IS NULL`,
		`UPDATE auth_tokens SET revoked_at = now() WHERE oauth_client_id = $1 AND user_id = $2 AND revoked_at IS NULL`,
		`DELETE FROM oauth_authorization_codes WHERE client_id = $1 AND user_id = $2`,
	} {
		tag, err := tx.Exec(ctx, q, clientID, userID)
		if err != nil {
			return 0, err
		}
		total += tag.RowsAffected()
	}
	return total, nil
}

// OAuthClientApprovedByUser reports whether the user has ever approved this
// client, for any scope. Only such a client is trusted to receive an error
// by redirect before the user has seen its consent screen.
func (s *Store) OAuthClientApprovedByUser(ctx context.Context, clientID, userID uuid.UUID) (bool, error) {
	var exists bool
	err := s.db.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM oauth_client_consents WHERE client_id = $1 AND user_id = $2)
	`, clientID, userID).Scan(&exists)
	return exists, err
}

// OAuthClientConsented reports whether this user has already approved this
// client for this scope. A remembered approval is what lets a reconnect skip
// the consent screen; revoking the client deletes the record, so the next one
// asks again.
//
// The scope is part of the match. An approval is for what was shown on the
// consent screen, so a request for anything else has to be approved on its own
// terms rather than inheriting a decision the user did not make.
func (s *Store) OAuthClientConsented(ctx context.Context, clientID, userID uuid.UUID, scope string) (bool, error) {
	var exists bool
	err := s.db.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM oauth_client_consents
			WHERE client_id = $1 AND user_id = $2 AND scope = $3
		)
	`, clientID, userID, scope).Scan(&exists)
	return exists, err
}

type CreateOAuthAuthorizationCodeParams struct {
	ClientID      uuid.UUID
	UserID        uuid.UUID
	RedirectURI   string
	CodeChallenge string
	Scope         string
	Resource      string
}

// CreateOAuthAuthorizationCode records the user's approval and mints the code
// that is handed back through the browser redirect.
func (s *Store) CreateOAuthAuthorizationCode(ctx context.Context, p CreateOAuthAuthorizationCodeParams) (string, error) {
	raw, err := generateToken()
	if err != nil {
		return "", err
	}
	hash := tokenHash(raw)
	err = pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			INSERT INTO oauth_client_consents (client_id, user_id, scope)
			VALUES ($1, $2, $3)
			ON CONFLICT (client_id, user_id) DO UPDATE SET scope = EXCLUDED.scope
		`, p.ClientID, p.UserID, p.Scope); err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23503" {
				return ErrNotFound
			}
			return err
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO oauth_authorization_codes
				(code_hash, client_id, user_id, redirect_uri, code_challenge, scope, resource, expires_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, now() + make_interval(secs => $8))
		`, hash[:], p.ClientID, p.UserID, p.RedirectURI, p.CodeChallenge, p.Scope, p.Resource,
			oauthAuthorizationCodeTTL.Seconds())
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23514" {
				return fmt.Errorf("invalid authorization code request: %w", ErrConflict)
			}
			return err
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return raw, nil
}

type ConsumedOAuthCode struct {
	ClientID      uuid.UUID
	UserID        uuid.UUID
	RedirectURI   string
	CodeChallenge string
	Scope         string
	Resource      string
}

// lockOAuthGrant holds a (client, user) grant's approval for the rest of the
// transaction, and is ErrUnauthorized once the approval is gone.
//
// The approval row serialises every change to a grant. Issuing tokens holds it
// shared, before any other lock; ending a grant deletes it first. A
// revocation's UPDATE cannot see a token another transaction has inserted but
// not yet committed, so without this a disconnect racing a refresh could miss
// the token minted alongside it and leave the connector working. With it, an
// exchange or refresh either waits for the disconnect and then finds no
// approval, or finishes first so the disconnect's revocations see what it
// issued.
func lockOAuthGrant(ctx context.Context, tx pgx.Tx, clientID, userID uuid.UUID) error {
	var held bool
	err := tx.QueryRow(ctx, `
		SELECT true FROM oauth_client_consents
		WHERE client_id = $1 AND user_id = $2
		FOR SHARE
	`, clientID, userID).Scan(&held)
	if isNoRows(err) {
		return ErrUnauthorized
	}
	return err
}

// ExchangeOAuthAuthorizationCode claims a code exactly once, for the client it
// was issued to, and issues the tokens it grants in the same transaction.
//
// client.ID is part of the lookup, not a check applied afterwards. A client
// that presents someone else's code must not be able to consume it: doing so
// would burn the code and leave the rightful client's next exchange looking
// like a replay, which revokes that client's entire grant. Matching in the
// query means a foreign code is simply not found and nothing is mutated.
//
// The row is locked before it is marked consumed, so two simultaneous exchanges
// cannot both succeed. A code that was already consumed means someone replayed
// it, which means a copy leaked: every token the pair holds is revoked before
// ErrOAuthReplay is returned, per RFC 6749 section 4.1.2. The tokens are issued
// before the code's lock is released, so a replay racing the first exchange
// waits for them and revokes them too.
//
// verify checks the claimed code against the rest of the token request before
// anything is issued. Its error is returned as it is, and the code stays spent.
func (s *Store) ExchangeOAuthAuthorizationCode(ctx context.Context, raw string, client model.OAuthClient, verify func(ConsumedOAuthCode) error) (IssuedOAuthTokens, error) {
	hash := tokenHash(raw)
	var out IssuedOAuthTokens
	var replayed bool
	var refused error
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		// The grant is locked before the code, in the order every change to a
		// grant takes its locks; see lockOAuthGrant.
		var userID uuid.UUID
		err := tx.QueryRow(ctx, `
			SELECT user_id FROM oauth_authorization_codes WHERE code_hash = $1 AND client_id = $2
		`, hash[:], client.ID).Scan(&userID)
		if err != nil {
			if isNoRows(err) {
				return ErrUnauthorized
			}
			return err
		}
		if err := lockOAuthGrant(ctx, tx, client.ID, userID); err != nil {
			return err
		}
		var code ConsumedOAuthCode
		var consumed *time.Time
		var expires time.Time
		err = tx.QueryRow(ctx, `
			SELECT client_id, user_id, redirect_uri, code_challenge, scope, resource, expires_at, consumed_at
			FROM oauth_authorization_codes
			WHERE code_hash = $1 AND client_id = $2
			FOR UPDATE
		`, hash[:], client.ID).Scan(&code.ClientID, &code.UserID, &code.RedirectURI, &code.CodeChallenge,
			&code.Scope, &code.Resource, &expires, &consumed)
		if err != nil {
			// Since the read above, only the purge of long-expired codes can
			// have deleted it.
			if isNoRows(err) {
				return ErrUnauthorized
			}
			return err // defensive: DB outage past the read above
		}
		if consumed != nil {
			if err := revokeOAuthGrants(ctx, tx, code.ClientID, code.UserID); err != nil {
				return err // defensive: DB outage past the replay check
			}
			// Report the replay after the transaction commits, not by failing
			// it: returning an error here would roll back the revocation that
			// is the entire point of detecting the replay.
			replayed = true
			return nil
		}
		if !expires.After(time.Now()) {
			return ErrUnauthorized
		}
		if _, err := tx.Exec(ctx, `
			UPDATE oauth_authorization_codes SET consumed_at = now() WHERE code_hash = $1
		`, hash[:]); err != nil {
			return err // defensive: DB outage past the row lock
		}
		// A refused request still spends the code, so the refusal is reported
		// after the commit as well.
		if refused = verify(code); refused != nil {
			return nil
		}
		return issueOAuthTokens(ctx, tx, issueOAuthTokensParams{
			ClientID:   client.ID,
			ClientName: client.Name,
			UserID:     code.UserID,
			Scope:      code.Scope,
		}, &out)
	})
	switch {
	case err != nil:
		return IssuedOAuthTokens{}, err
	case replayed:
		return IssuedOAuthTokens{}, ErrOAuthReplay
	case refused != nil:
		return IssuedOAuthTokens{}, refused
	}
	return out, nil
}

type issueOAuthTokensParams struct {
	ClientID   uuid.UUID
	ClientName string
	UserID     uuid.UUID
	Scope      string
}

type IssuedOAuthTokens struct {
	AccessToken  string
	RefreshToken string
	ExpiresIn    int
	// Scope is the grant's scope, carried back so the token endpoint reports
	// what was actually granted rather than assuming.
	Scope string
}

// issueOAuthTokens mints an access and refresh token pair for an approved
// grant, whose approval the caller holds with lockOAuthGrant. The access token
// is an ordinary auth_tokens row, so it authenticates through exactly the same
// path as an API token the user made themselves.
func issueOAuthTokens(ctx context.Context, tx pgx.Tx, p issueOAuthTokensParams, out *IssuedOAuthTokens) error {
	access, err := generateToken()
	if err != nil {
		return err
	}
	refresh, err := generateToken()
	if err != nil {
		return err
	}
	accessHash := tokenHash(access)
	refreshHash := tokenHash(refresh)

	var accessID uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO auth_tokens (user_id, kind, name, token_hash, expires_at, oauth_client_id)
		VALUES ($1, $2, $3, $4, now() + make_interval(secs => $5), $6)
		RETURNING id
	`, p.UserID, model.AuthTokenKindOAuth, p.ClientName, accessHash[:],
		oauthAccessTokenTTL.Seconds(), p.ClientID).Scan(&accessID)
	if err != nil {
		return err // defensive: the held approval's foreign keys vouch for the user and client
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO oauth_refresh_tokens
			(token_hash, client_id, user_id, access_token_id, scope, expires_at)
		VALUES ($1, $2, $3, $4, $5, now() + make_interval(secs => $6))
	`, refreshHash[:], p.ClientID, p.UserID, accessID, p.Scope, oauthRefreshTokenTTL.Seconds()); err != nil {
		return err // defensive: DB outage past the access token insert
	}
	// Expired rows are cleared here rather than by a background sweep, so an
	// instance that never refreshes never accumulates and one that refreshes
	// constantly pays a bounded cost per exchange.
	if _, err := tx.Exec(ctx, `
		DELETE FROM auth_tokens
		WHERE id IN (
			SELECT id FROM auth_tokens
			WHERE kind = $1 AND expires_at < now() - INTERVAL '1 day'
			LIMIT $2
		)
	`, model.AuthTokenKindOAuth, oauthExpiredTokenPurgeLimit); err != nil {
		return err // defensive: DB outage past the refresh token insert
	}
	if _, err := tx.Exec(ctx, `
		DELETE FROM oauth_authorization_codes
		WHERE expires_at < now() - INTERVAL '1 day'
	`); err != nil {
		return err // defensive: DB outage past the token purge
	}
	out.AccessToken = access
	out.RefreshToken = refresh
	out.ExpiresIn = int(oauthAccessTokenTTL.Seconds())
	out.Scope = p.Scope
	return nil
}

// RotateOAuthRefreshToken exchanges a refresh token for a fresh pair and
// retires the one presented.
//
// Rotation makes a leaked refresh token detectable: the legitimate client and
// the thief cannot both use it, and whoever arrives second presents a token
// that is already retired. That second use revokes the whole grant.
func (s *Store) RotateOAuthRefreshToken(ctx context.Context, raw string, clientID uuid.UUID) (IssuedOAuthTokens, error) {
	hash := tokenHash(raw)
	var out IssuedOAuthTokens
	var replayed bool
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		var id, rowClientID, userID uuid.UUID
		var scope string
		var expires time.Time
		var revoked *time.Time
		var replacedBy *uuid.UUID
		err := tx.QueryRow(ctx, `
			SELECT client_id, user_id FROM oauth_refresh_tokens WHERE token_hash = $1
		`, hash[:]).Scan(&rowClientID, &userID)
		if err != nil {
			if isNoRows(err) {
				return ErrUnauthorized
			}
			return err
		}
		// A token belonging to a different client is not this client's business
		// to rotate, and saying so precisely would confirm the token exists.
		if rowClientID != clientID {
			return ErrUnauthorized
		}
		// The grant is locked before the token, in the order every change to a
		// grant takes its locks; see lockOAuthGrant.
		if err := lockOAuthGrant(ctx, tx, rowClientID, userID); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `
			SELECT id, scope, expires_at, revoked_at, replaced_by_id
			FROM oauth_refresh_tokens
			WHERE token_hash = $1
			FOR UPDATE
		`, hash[:]).Scan(&id, &scope, &expires, &revoked, &replacedBy); err != nil {
			return err // defensive: nothing deletes a refresh token whose grant is held
		}
		if replacedBy != nil {
			if err := revokeOAuthGrants(ctx, tx, rowClientID, userID); err != nil {
				return err // defensive: DB outage past the replay check
			}
			// Reported after the commit, so the revocation survives. Returning
			// the error here would roll it back and leave the leaked token's
			// successors working.
			replayed = true
			return nil
		}
		if revoked != nil || !expires.After(time.Now()) {
			return ErrUnauthorized
		}

		var clientName string
		if err := tx.QueryRow(ctx, `
			SELECT name FROM oauth_clients WHERE id = $1 AND disabled_at IS NULL
		`, rowClientID).Scan(&clientName); err != nil {
			if isNoRows(err) {
				return ErrUnauthorized
			}
			return err
		}
		if err := issueOAuthTokens(ctx, tx, issueOAuthTokensParams{
			ClientID:   rowClientID,
			ClientName: clientName,
			UserID:     userID,
			Scope:      scope,
		}, &out); err != nil {
			return err
		}
		newHash := tokenHash(out.RefreshToken)
		_, err = tx.Exec(ctx, `
			UPDATE oauth_refresh_tokens
			SET revoked_at = now(),
			    replaced_by_id = (SELECT id FROM oauth_refresh_tokens WHERE token_hash = $2)
			WHERE id = $1
		`, id, newHash[:])
		return err
	})
	if err != nil {
		return IssuedOAuthTokens{}, err
	}
	if replayed {
		return IssuedOAuthTokens{}, ErrOAuthReplay
	}
	return out, nil
}

// RevokeOAuthToken implements RFC 7009 for a token this client owns. Revoking a
// refresh token takes the whole grant with it, since the client is saying it no
// longer wants access at all.
func (s *Store) RevokeOAuthToken(ctx context.Context, raw string, clientID uuid.UUID) error {
	hash := tokenHash(raw)
	return pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		var userID uuid.UUID
		err := tx.QueryRow(ctx, `
			SELECT user_id FROM oauth_refresh_tokens WHERE token_hash = $1 AND client_id = $2
		`, hash[:], clientID).Scan(&userID)
		switch {
		case err == nil:
			return revokeOAuthGrants(ctx, tx, clientID, userID)
		case !isNoRows(err):
			return err
		}
		// Not a refresh token, so it may be an access token. Anything else is
		// already invalid, and RFC 7009 section 2.2 wants success either way.
		_, err = tx.Exec(ctx, `
			UPDATE auth_tokens SET revoked_at = now()
			WHERE token_hash = $1 AND oauth_client_id = $2 AND revoked_at IS NULL
		`, hash[:], clientID)
		return err
	})
}

// revokeOAuthGrants stops every credential a client holds for one user. It is
// the response to a detected replay and to an explicit disconnect.
func revokeOAuthGrants(ctx context.Context, tx pgx.Tx, clientID, userID uuid.UUID) error {
	if _, err := tx.Exec(ctx, `
		UPDATE oauth_refresh_tokens SET revoked_at = now()
		WHERE client_id = $1 AND user_id = $2 AND revoked_at IS NULL
	`, clientID, userID); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `
		UPDATE auth_tokens SET revoked_at = now()
		WHERE oauth_client_id = $1 AND user_id = $2 AND revoked_at IS NULL
	`, clientID, userID)
	return err
}
