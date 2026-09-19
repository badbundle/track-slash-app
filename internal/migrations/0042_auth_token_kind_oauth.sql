-- +goose Up
-- +goose StatementBegin
-- Access tokens issued through the OAuth authorization-code flow live in
-- auth_tokens like every other bearer credential, so AuthenticateToken,
-- expiry, and revocation all work on them unchanged.
--
-- This value lands in a migration of its own on purpose. Postgres permits
-- ALTER TYPE ... ADD VALUE inside a transaction block, which is what goose
-- wraps each migration in, but forbids *using* the new value in that same
-- transaction. Everything that references 'oauth' therefore has to wait for
-- 0043, which runs in a later transaction.
ALTER TYPE auth_token_kind ADD VALUE IF NOT EXISTS 'oauth';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Postgres cannot remove a value from an enum, so there is nothing to undo.
-- 0043 drops the tables and the column that give this value its meaning.
SELECT 1;
-- +goose StatementEnd
