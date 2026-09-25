-- +goose Up
-- Whiteboard pages are free-form project notes. They live apart from project
-- context, are never linked to issues, and are soft-deleted so page refs are
-- never reused.
-- +goose StatementBegin
ALTER TABLE projects
    ADD COLUMN next_whiteboard_page_number INT NOT NULL DEFAULT 1;

CREATE TABLE whiteboard_pages (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id    UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    number        INT NOT NULL CHECK (number >= 1),
    title         TEXT NOT NULL CHECK (length(title) BETWEEN 1 AND 200),
    body          TEXT NOT NULL DEFAULT '' CHECK (length(body) <= 100000),
    created_by_id UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    updated_by_id UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    version       BIGINT NOT NULL DEFAULT 1,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at    TIMESTAMPTZ,
    CONSTRAINT whiteboard_pages_project_number_key UNIQUE (project_id, number)
);

CREATE INDEX whiteboard_pages_project_updated_alive
    ON whiteboard_pages(project_id, updated_at DESC, id DESC)
    WHERE deleted_at IS NULL;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION track_emit_whiteboard_page_event() RETURNS trigger AS $$
DECLARE
    rec    RECORD;
    rec_op TEXT;
BEGIN
    IF TG_OP = 'DELETE' THEN
        rec := OLD;
    ELSE
        IF TG_OP = 'UPDATE' THEN
            NEW.version := OLD.version + 1;
        END IF;
        rec := NEW;
    END IF;

    rec_op := lower(TG_OP);
    IF TG_OP = 'UPDATE' THEN
        IF OLD.deleted_at IS NULL AND NEW.deleted_at IS NOT NULL THEN
            rec_op := 'delete';
        END IF;
    END IF;

    PERFORM pg_notify('track_events', jsonb_build_object(
        'op',         rec_op,
        'entity',     'whiteboard_page',
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

CREATE TRIGGER whiteboard_pages_events
    BEFORE INSERT OR UPDATE OR DELETE ON whiteboard_pages
    FOR EACH ROW EXECUTE FUNCTION track_emit_whiteboard_page_event();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS whiteboard_pages_events ON whiteboard_pages;
DROP FUNCTION IF EXISTS track_emit_whiteboard_page_event();
DROP TABLE IF EXISTS whiteboard_pages;
ALTER TABLE projects DROP COLUMN IF EXISTS next_whiteboard_page_number;
-- +goose StatementEnd
