-- Comment visibility changes, issue-scoped context lookups and the realtime
-- trigger find a changelog entity's entries by (entity, entity_id) alone,
-- which neither the project nor the issue index serves.
-- +goose Up
-- +goose StatementBegin
CREATE INDEX idx_project_changelog_entity
    ON project_changelog_entries(entity, entity_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_project_changelog_entity;
-- +goose StatementEnd
