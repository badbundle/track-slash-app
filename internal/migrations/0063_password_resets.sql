-- +goose Up
-- +goose StatementBegin
-- A link that sets a new password, sent only to the account's verified
-- address. Only the token's SHA-256 hash is kept. A link works once and for 30
-- minutes, and asking again or changing the password any other way cancels
-- the ones still waiting. It is bound to the address it was sent to: once the
-- account's verified address changes, the link stops working.
CREATE TABLE password_resets (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    email       text NOT NULL,
    token_hash  bytea NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32),
    created_at  timestamptz NOT NULL DEFAULT now(),
    expires_at  timestamptz NOT NULL,
    used_at     timestamptz
);
CREATE INDEX password_resets_user
    ON password_resets (user_id, created_at DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS password_resets;
-- +goose StatementEnd
