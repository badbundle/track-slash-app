-- Deleting a planned sprint used to leave its issues pointing at it, so they
-- dropped out of the backlog and every board (TRACK-108). DeleteSprint now
-- clears them; this returns the ones stranded before that to the backlog.
-- +goose Up
-- +goose StatementBegin
UPDATE issues SET sprint_id = NULL, updated_at = now()
WHERE sprint_id IN (SELECT id FROM sprints WHERE deleted_at IS NOT NULL);
-- +goose StatementEnd

-- +goose Down
-- Putting issues back in a deleted sprint would strand them again, so there
-- is nothing to undo.
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
