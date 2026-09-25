-- Sprint membership history for project insights.
--
-- issues.sprint_id only says where an issue is now, and sprint completion
-- moves unfinished issues on without a changelog row per issue. Sprint
-- burn-up scope and velocity commitment need to know which issues were in a
-- sprint at any instant, so every change of issues.sprint_id is recorded here
-- by trigger, whichever code path made it.
-- +goose Up
-- +goose StatementBegin
CREATE TABLE sprint_issue_memberships (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    sprint_id  UUID NOT NULL,
    issue_id   UUID NOT NULL,
    added_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    removed_at TIMESTAMPTZ,
    -- Rows written by this migration for memberships that predate it. Their
    -- added_at is an estimate, so insights treat sprints that carry them as
    -- having no recorded commitment.
    backfilled BOOLEAN NOT NULL DEFAULT false,
    CONSTRAINT sprint_issue_memberships_sprint_project_fk
        FOREIGN KEY (project_id, sprint_id) REFERENCES sprints(project_id, id) ON DELETE CASCADE,
    CONSTRAINT sprint_issue_memberships_issue_project_fk
        FOREIGN KEY (project_id, issue_id) REFERENCES issues(project_id, id) ON DELETE CASCADE,
    CONSTRAINT sprint_issue_memberships_interval
        CHECK (removed_at IS NULL OR removed_at >= added_at)
);

CREATE INDEX sprint_issue_memberships_sprint
    ON sprint_issue_memberships(sprint_id, added_at);

CREATE UNIQUE INDEX sprint_issue_memberships_open_issue
    ON sprint_issue_memberships(issue_id) WHERE removed_at IS NULL;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION track_record_sprint_membership() RETURNS trigger AS $$
BEGIN
    IF TG_OP = 'UPDATE' THEN
        IF OLD.sprint_id IS NOT DISTINCT FROM NEW.sprint_id THEN
            RETURN NEW;
        END IF;
        IF OLD.sprint_id IS NOT NULL THEN
            UPDATE sprint_issue_memberships
            SET removed_at = GREATEST(now(), added_at)
            WHERE issue_id = OLD.id AND sprint_id = OLD.sprint_id AND removed_at IS NULL;
        END IF;
    END IF;
    IF NEW.sprint_id IS NOT NULL THEN
        INSERT INTO sprint_issue_memberships (project_id, sprint_id, issue_id, added_at)
        VALUES (NEW.project_id, NEW.sprint_id, NEW.id, now());
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER issues_sprint_membership
    AFTER INSERT OR UPDATE OF sprint_id ON issues
    FOR EACH ROW EXECUTE FUNCTION track_record_sprint_membership();
-- +goose StatementEnd

-- Backfill what is still knowable: issues currently in a sprint, plus issues a
-- completed sprint captured at completion but has since moved on. Neither
-- source says when the issue joined, so the later of the issue and sprint
-- creation times stands in for it.
-- +goose StatementBegin
INSERT INTO sprint_issue_memberships (project_id, sprint_id, issue_id, added_at, removed_at, backfilled)
SELECT i.project_id, i.sprint_id, i.id, GREATEST(i.created_at, s.created_at), NULL, true
FROM issues i
JOIN sprints s ON s.id = i.sprint_id AND s.project_id = i.project_id;

INSERT INTO sprint_issue_memberships (project_id, sprint_id, issue_id, added_at, removed_at, backfilled)
SELECT sis.project_id, sis.sprint_id, sis.issue_id,
       GREATEST(i.created_at, s.created_at),
       GREATEST(COALESCE(s.completed_at, sis.snapshotted_at), i.created_at, s.created_at),
       true
FROM sprint_issue_snapshots sis
JOIN issues i ON i.id = sis.issue_id AND i.project_id = sis.project_id
JOIN sprints s ON s.id = sis.sprint_id AND s.project_id = sis.project_id
WHERE i.sprint_id IS DISTINCT FROM sis.sprint_id;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS issues_sprint_membership ON issues;
DROP FUNCTION IF EXISTS track_record_sprint_membership();
DROP TABLE IF EXISTS sprint_issue_memberships;
-- +goose StatementEnd
