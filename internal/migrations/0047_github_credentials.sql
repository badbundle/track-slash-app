-- +goose Up
-- +goose StatementBegin
-- A GitHub token saved once on a user's account and referenced by any number
-- of repository connections. Rotating it here rotates it for every project
-- that uses it. The id is chosen by the application because it is part of the
-- AES-GCM associated data that binds the ciphertext to this row.
CREATE TABLE github_credentials (
    id                UUID PRIMARY KEY,
    user_id           UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name              TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 100),
    github_login      TEXT NOT NULL CHECK (length(github_login) BETWEEN 1 AND 100),
    token_ciphertext  BYTEA NOT NULL CHECK (octet_length(token_ciphertext) > 16),
    token_nonce       BYTEA NOT NULL CHECK (octet_length(token_nonce) = 12),
    last_validated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX idx_github_credentials_user_name
    ON github_credentials(user_id, lower(name));
-- +goose StatementEnd

-- +goose StatementBegin
-- An active connection draws its token from exactly one place: a saved
-- credential, or the ciphertext it has carried since 0039. Disconnected rows
-- keep neither.
ALTER TABLE github_repository_connections
    ADD COLUMN credential_id UUID REFERENCES github_credentials(id),
    ALTER COLUMN token_ciphertext DROP NOT NULL,
    ALTER COLUMN token_nonce DROP NOT NULL,
    ADD CONSTRAINT github_connections_token_pair
        CHECK ((token_ciphertext IS NULL) = (token_nonce IS NULL)),
    ADD CONSTRAINT github_connections_one_token_source
        CHECK (disabled_at IS NOT NULL OR (credential_id IS NULL) <> (token_ciphertext IS NULL));

CREATE INDEX idx_github_connections_credential_active
    ON github_repository_connections(credential_id)
    WHERE credential_id IS NOT NULL AND disabled_at IS NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Connections that relied on a saved credential have no token of their own to
-- fall back to, so they are disconnected with a scrubbed placeholder.
UPDATE github_repository_connections
SET disabled_at = COALESCE(disabled_at, now()),
    token_ciphertext = decode(repeat('00', 17), 'hex'),
    token_nonce = decode(repeat('00', 12), 'hex'),
    updated_at = now()
WHERE token_ciphertext IS NULL;
DROP INDEX IF EXISTS idx_github_connections_credential_active;
ALTER TABLE github_repository_connections
    DROP CONSTRAINT IF EXISTS github_connections_one_token_source,
    DROP CONSTRAINT IF EXISTS github_connections_token_pair,
    DROP COLUMN IF EXISTS credential_id,
    ALTER COLUMN token_nonce SET NOT NULL,
    ALTER COLUMN token_ciphertext SET NOT NULL;
DROP TABLE IF EXISTS github_credentials;
-- +goose StatementEnd
