-- +goose Up
-- +goose StatementBegin
-- A deleted comment's previews are for members, so deleting a comment keeps
-- its earlier changelog entries to them. Do the same for comments deleted
-- before. The delete entry itself is already hidden from everyone else.
UPDATE project_changelog_entries e
SET members_only = true
WHERE e.entity = 'comment' AND e.op <> 'delete' AND NOT e.members_only
  AND NOT EXISTS (SELECT 1 FROM comments c WHERE c.id = e.entity_id);
-- +goose StatementEnd

-- +goose Down
-- Entries about deleted comments stay with members: which of them were shared
-- before isn't recorded, and hiding them again is always safe.
