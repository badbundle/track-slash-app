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

-- +goose StatementBegin
-- Files on a private issue's own context reach only subscribers who may read
-- members-only content, as the context's own events do. The entry that
-- created the context still names its issue once the context and its link
-- are gone.
CREATE OR REPLACE FUNCTION track_emit_context_attachment_event() RETURNS trigger AS $$
DECLARE
    rec RECORD;
BEGIN
    IF TG_OP = 'DELETE' THEN
        rec := OLD;
    ELSE
        IF TG_OP = 'UPDATE' THEN
            NEW.version := OLD.version + 1;
        END IF;
        rec := NEW;
    END IF;

    PERFORM pg_notify('track_events', jsonb_build_object(
        'op',           lower(TG_OP),
        'entity',       'context_attachment',
        'id',           rec.id,
        'context_id',   rec.context_id,
        'project_id',   rec.project_id,
        'version',      rec.version,
        'members_only', EXISTS (
            SELECT 1 FROM issue_context_links l
            JOIN project_context pc ON pc.id = l.context_id AND pc.scope = 'issue'
            JOIN issues i ON i.id = l.issue_id
            WHERE l.context_id = rec.context_id AND i.private
        ) OR EXISTS (
            SELECT 1 FROM project_changelog_entries c JOIN issues i ON i.id = c.issue_id
            WHERE c.entity = 'project_context' AND c.entity_id = rec.context_id AND i.private
        ),
        'ts',           to_char(now() AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.MS"Z"')
    )::text);

    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- +goose StatementBegin
-- Who a project blocked is for its members, like the block list and its
-- changelog entries.
CREATE OR REPLACE FUNCTION track_emit_project_block_event() RETURNS trigger AS $$
DECLARE
    rec RECORD;
BEGIN
    IF TG_OP = 'DELETE' THEN
        rec := OLD;
    ELSE
        IF TG_OP = 'UPDATE' THEN
            NEW.version := OLD.version + 1;
        END IF;
        rec := NEW;
    END IF;

    PERFORM pg_notify('track_events', jsonb_build_object(
        'op',           lower(TG_OP),
        'entity',       'project_block',
        'id',           rec.id,
        'project_id',   rec.project_id,
        'version',      rec.version,
        'members_only', true,
        'ts',           to_char(now() AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.MS"Z"')
    )::text);

    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- +goose Down
-- Entries about deleted comments stay with members: which of them were shared
-- before isn't recorded, and hiding them again is always safe.

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION track_emit_context_attachment_event() RETURNS trigger AS $$
DECLARE
    rec RECORD;
BEGIN
    IF TG_OP = 'DELETE' THEN
        rec := OLD;
    ELSE
        IF TG_OP = 'UPDATE' THEN
            NEW.version := OLD.version + 1;
        END IF;
        rec := NEW;
    END IF;

    PERFORM pg_notify('track_events', jsonb_build_object(
        'op',         lower(TG_OP),
        'entity',     'context_attachment',
        'id',         rec.id,
        'context_id', rec.context_id,
        'project_id', rec.project_id,
        'version',    rec.version,
        'ts',         to_char(now() AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.MS"Z"')
    )::text);

    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION track_emit_project_block_event() RETURNS trigger AS $$
DECLARE
    rec RECORD;
BEGIN
    IF TG_OP = 'DELETE' THEN
        rec := OLD;
    ELSE
        IF TG_OP = 'UPDATE' THEN
            NEW.version := OLD.version + 1;
        END IF;
        rec := NEW;
    END IF;

    PERFORM pg_notify('track_events', jsonb_build_object(
        'op',         lower(TG_OP),
        'entity',     'project_block',
        'id',         rec.id,
        'project_id', rec.project_id,
        'version',    rec.version,
        'ts',         to_char(now() AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.MS"Z"')
    )::text);

    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd
