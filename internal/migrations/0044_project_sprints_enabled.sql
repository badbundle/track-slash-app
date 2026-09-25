-- +goose Up
-- +goose StatementBegin
-- Sprint mode is a per-project choice. New projects start without sprints:
-- work is picked up one issue at a time, and planned sprints remain available
-- as a planning aid. The store only lets a project leave sprint mode while it
-- has no active sprint, and only lets a sprint start while sprint mode is on.
ALTER TABLE projects
    ADD COLUMN sprints_enabled BOOLEAN NOT NULL DEFAULT false;

-- Projects that already use sprints keep working the way they do today. Any
-- live sprint, whether planned, active or completed, counts as use; a project
-- whose only sprints were deleted never really ran in sprint mode.
UPDATE projects p
SET sprints_enabled = true
WHERE EXISTS (
    SELECT 1 FROM sprints s
    WHERE s.project_id = p.id AND s.deleted_at IS NULL
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE projects DROP COLUMN IF EXISTS sprints_enabled;
-- +goose StatementEnd
