-- +goose Up
-- +goose StatementBegin
-- An OAuth client is a registered application, such as a Claude.ai custom
-- connector, that may ask a trackslash user for access. Registration is manual:
-- there is no RFC 7591 endpoint, so client_id and secret_hash only ever appear
-- here because a signed-in user created them.
--
-- The secret is hashed, not encrypted. trackslash only ever verifies it and
-- never replays it to a third party, which is what forces the AES-GCM envelope
-- used for GitHub tokens in 0039. A leak of this table therefore yields nothing
-- a client could authenticate with.
CREATE TABLE oauth_clients (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    client_id     TEXT NOT NULL UNIQUE CHECK (length(client_id) BETWEEN 1 AND 100),
    secret_hash   BYTEA NOT NULL CHECK (octet_length(secret_hash) = 32),
    name          TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 100),
    redirect_uris TEXT[] NOT NULL CHECK (cardinality(redirect_uris) BETWEEN 1 AND 10),
    created_by_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    disabled_at   TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_oauth_clients_owner
    ON oauth_clients(created_by_id, created_at, id);
-- +goose StatementEnd

-- +goose StatementBegin
-- Remembering an approval is what makes reconnecting painless: a client the user
-- has already approved skips straight past the consent screen. Revoking the
-- client deletes the row, so the next connection asks again.
CREATE TABLE oauth_client_consents (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    client_id  UUID NOT NULL REFERENCES oauth_clients(id) ON DELETE CASCADE,
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    scope      TEXT NOT NULL DEFAULT '' CHECK (length(scope) <= 500),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (client_id, user_id)
);
-- +goose StatementEnd

-- +goose StatementBegin
-- Authorization codes are single-use and short-lived. consumed_at is what makes
-- the single use enforceable: the exchange claims the row under a lock, so a
-- replayed code is detected rather than honoured a second time.
--
-- Only the PKCE challenge is stored. The verifier arrives at the token endpoint
-- and is hashed for comparison, so a stolen database cannot complete a flow.
CREATE TABLE oauth_authorization_codes (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    code_hash      BYTEA NOT NULL UNIQUE CHECK (octet_length(code_hash) = 32),
    client_id      UUID NOT NULL REFERENCES oauth_clients(id) ON DELETE CASCADE,
    user_id        UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    redirect_uri   TEXT NOT NULL CHECK (length(redirect_uri) BETWEEN 1 AND 2048),
    code_challenge TEXT NOT NULL CHECK (length(code_challenge) BETWEEN 43 AND 128),
    scope          TEXT NOT NULL DEFAULT '' CHECK (length(scope) <= 500),
    resource       TEXT NOT NULL DEFAULT '' CHECK (length(resource) <= 2048),
    expires_at     TIMESTAMPTZ NOT NULL,
    consumed_at    TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_oauth_authorization_codes_expiry
    ON oauth_authorization_codes(expires_at);
-- +goose StatementEnd

-- +goose StatementBegin
-- Refresh tokens deliberately do not live in auth_tokens. The API auth
-- middleware accepts any row in that table as a bearer credential without
-- filtering on kind, so a refresh token stored there would authenticate REST
-- calls it was never meant to.
--
-- replaced_by_id records rotation lineage: every exchange issues a new token and
-- retires the old one, so presenting a retired token is a detectable signal that
-- a copy leaked.
CREATE TABLE oauth_refresh_tokens (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    token_hash      BYTEA NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32),
    client_id       UUID NOT NULL REFERENCES oauth_clients(id) ON DELETE CASCADE,
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    access_token_id UUID REFERENCES auth_tokens(id) ON DELETE SET NULL,
    scope           TEXT NOT NULL DEFAULT '' CHECK (length(scope) <= 500),
    replaced_by_id  UUID REFERENCES oauth_refresh_tokens(id) ON DELETE SET NULL,
    expires_at      TIMESTAMPTZ NOT NULL,
    revoked_at      TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_oauth_refresh_tokens_live_grant
    ON oauth_refresh_tokens(client_id, user_id)
    WHERE revoked_at IS NULL;
-- +goose StatementEnd

-- +goose StatementBegin
-- Ties an issued access token back to the client that asked for it, so revoking
-- a client can revoke everything it was ever granted.
--
-- The session sweep added in 0040 is scoped to kind = 'session' and so leaves
-- these rows alone. That is correct: OAuth access tokens carry a real expires_at
-- that AuthenticateToken already enforces on every request.
ALTER TABLE auth_tokens
    ADD COLUMN oauth_client_id UUID REFERENCES oauth_clients(id) ON DELETE CASCADE;

CREATE INDEX idx_auth_tokens_live_by_oauth_client
    ON auth_tokens(oauth_client_id)
    WHERE oauth_client_id IS NOT NULL AND revoked_at IS NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_auth_tokens_live_by_oauth_client;
ALTER TABLE auth_tokens DROP COLUMN IF EXISTS oauth_client_id;
DROP TABLE IF EXISTS oauth_refresh_tokens;
DROP TABLE IF EXISTS oauth_authorization_codes;
DROP TABLE IF EXISTS oauth_client_consents;
DROP TABLE IF EXISTS oauth_clients;
-- +goose StatementEnd
