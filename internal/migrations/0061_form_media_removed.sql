-- +goose Up
-- A form's files are deleted 30 days after the form is picked up or
-- cancelled. media_removed_at records when that happened. It stays null for a
-- form whose files were never removed, and for a form that had none.
-- +goose StatementBegin
ALTER TABLE forms
    ADD COLUMN media_removed_at TIMESTAMPTZ;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE forms
    DROP COLUMN IF EXISTS media_removed_at;
-- +goose StatementEnd
