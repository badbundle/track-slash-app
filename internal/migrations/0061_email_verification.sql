-- +goose Up
-- +goose StatementBegin
-- An address is verified once its owner opens a link sent to it and confirms.
-- Every address that exists now, and every address an admin sets, starts
-- unverified. Only a verified address is unique, and regardless of case, so an
-- address someone typed but never confirmed no longer keeps its real owner out.
ALTER TABLE users
    ADD COLUMN email_verified_at timestamptz,
    ADD CONSTRAINT users_email_verified_needs_email_check
        CHECK (email_verified_at IS NULL OR email IS NOT NULL),
    DROP CONSTRAINT users_email_key;
-- +goose StatementEnd

-- +goose StatementBegin
-- A deleted account's address is free for someone else to verify.
CREATE UNIQUE INDEX users_verified_email_key
    ON users (lower(email))
    WHERE email_verified_at IS NOT NULL AND deleted_at IS NULL;
-- +goose StatementEnd

-- +goose StatementBegin
-- A link that confirms an address. Only the token's hash is kept. A link works
-- once and for an hour, and asking for another one replaces it. sent_at stays
-- empty when the provider refused the message.
CREATE TABLE email_verifications (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    email       text NOT NULL,
    token_hash  bytea NOT NULL UNIQUE,
    created_at  timestamptz NOT NULL DEFAULT now(),
    expires_at  timestamptz NOT NULL,
    sent_at     timestamptz,
    used_at     timestamptz
);
CREATE INDEX email_verifications_user
    ON email_verifications (user_id, created_at DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS email_verifications;
DROP INDEX IF EXISTS users_verified_email_key;
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_email_verified_needs_email_check;
-- The old constraint allowed an address on one account only, verified or not.
-- Where several accounts now share one, a live account keeps it before a
-- deleted one, then a verified one, then the oldest; the others lose it.
UPDATE users SET email = NULL
WHERE id IN (
    SELECT id FROM (
        SELECT id, row_number() OVER (
            PARTITION BY email
            ORDER BY deleted_at IS NOT NULL, email_verified_at IS NULL, created_at, id
        ) AS holder
        FROM users
        WHERE email IS NOT NULL
    ) ranked
    WHERE holder > 1
);
ALTER TABLE users
    DROP COLUMN IF EXISTS email_verified_at,
    ADD CONSTRAINT users_email_key UNIQUE (email);
-- +goose StatementEnd
