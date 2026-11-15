-- +goose Up
-- A wait key lets an agent's background command learn that a form is no
-- longer open without holding an account token. Only its SHA-256 is stored.
-- +goose StatementBegin
ALTER TABLE forms
    ADD COLUMN wait_key_hash BYTEA,
    ADD CONSTRAINT forms_wait_key_hash_length_check
        CHECK (wait_key_hash IS NULL OR octet_length(wait_key_hash) = 32);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE UNIQUE INDEX forms_wait_key_hash
    ON forms(wait_key_hash)
    WHERE wait_key_hash IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX forms_wait_key_hash;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE forms
    DROP CONSTRAINT forms_wait_key_hash_length_check,
    DROP COLUMN wait_key_hash;
-- +goose StatementEnd
