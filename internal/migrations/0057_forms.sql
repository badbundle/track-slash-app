-- +goose Up
-- A form is a set of questions an agent asks a person, with a suggested answer
-- for each. The questions (definition) are written once and read whole, and the
-- answers are saved as the person makes them, so both are JSONB documents that
-- internal/model validates. Forms are numbered per project and a number is
-- never reused.
-- +goose StatementBegin
ALTER TABLE projects
    ADD COLUMN next_form_number INT NOT NULL DEFAULT 1;

CREATE TYPE form_status AS ENUM ('open', 'submitted', 'acknowledged', 'cancelled');

CREATE TABLE forms (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id      UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    number          INT NOT NULL CHECK (number >= 1),
    issue_id        UUID REFERENCES issues(id) ON DELETE SET NULL,
    title           TEXT NOT NULL CHECK (length(title) BETWEEN 1 AND 200),
    description     TEXT NOT NULL DEFAULT '' CHECK (length(description) <= 20000),
    definition      JSONB NOT NULL CHECK (jsonb_typeof(definition) = 'object'),
    answers         JSONB NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(answers) = 'object'),
    comment         TEXT NOT NULL DEFAULT '' CHECK (length(comment) <= 10000),
    status          form_status NOT NULL DEFAULT 'open',
    assignee_id     UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    created_by_id   UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    origin_label    TEXT NOT NULL DEFAULT '' CHECK (length(origin_label) <= 200),
    origin_url      TEXT NOT NULL DEFAULT '' CHECK (length(origin_url) <= 2000),
    submitted_via   TEXT CHECK (submitted_via IN ('web', 'api')),
    submitted_at    TIMESTAMPTZ,
    acknowledged_at TIMESTAMPTZ,
    cancelled_at    TIMESTAMPTZ,
    version         BIGINT NOT NULL DEFAULT 1,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT forms_project_number_key UNIQUE (project_id, number),
    -- The timestamps follow the status. A form cancelled after it was
    -- submitted keeps its submitted time.
    CONSTRAINT forms_open_not_submitted CHECK (status <> 'open' OR submitted_at IS NULL),
    CONSTRAINT forms_submitted_has_time CHECK (status NOT IN ('submitted', 'acknowledged') OR submitted_at IS NOT NULL),
    CONSTRAINT forms_submitted_via_with_time CHECK ((submitted_at IS NULL) = (submitted_via IS NULL)),
    CONSTRAINT forms_acknowledged_has_time CHECK ((status = 'acknowledged') = (acknowledged_at IS NOT NULL)),
    CONSTRAINT forms_cancelled_has_time CHECK ((status = 'cancelled') = (cancelled_at IS NOT NULL))
);

-- The forms waiting on one person, longest-waiting first: the Inbox and its count.
CREATE INDEX forms_assignee_open
    ON forms(assignee_id, created_at, id)
    WHERE status = 'open';

-- The forms one person has dealt with, most recently changed first.
CREATE INDEX forms_assignee_done
    ON forms(assignee_id, updated_at DESC, id DESC)
    WHERE status <> 'open';

CREATE INDEX forms_issue
    ON forms(issue_id)
    WHERE issue_id IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS forms;
DROP TYPE IF EXISTS form_status;
ALTER TABLE projects DROP COLUMN IF EXISTS next_form_number;
-- +goose StatementEnd
