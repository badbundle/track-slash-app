-- +goose Up
-- A form can carry files: images, audio, video or anything else. An agent
-- uploads them first, through a short-lived upload URL that holds its own
-- secret, and then names them when it creates the form.
--
-- A storage object belongs to at most one attachment flow. Between upload and
-- form creation it is a pending upload (form_uploads); creating the form moves
-- it to form_attachments.

-- form_attachments points at a form of the same project, as issue_attachments
-- does at an issue.
-- +goose StatementBegin
ALTER TABLE forms
    ADD CONSTRAINT forms_project_id_id_key UNIQUE (project_id, id);
-- +goose StatementEnd

-- An upload grant is what an upload URL stands for. Only the SHA-256 of the
-- key in the URL is stored. files_used counts the files taken so far.
-- +goose StatementBegin
CREATE TABLE form_upload_grants (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id    UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    created_by_id UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    key_hash      BYTEA NOT NULL,
    expires_at    TIMESTAMPTZ NOT NULL,
    max_files     INT NOT NULL CHECK (max_files >= 1),
    files_used    INT NOT NULL DEFAULT 0,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT form_upload_grants_key_hash_key UNIQUE (key_hash),
    CONSTRAINT form_upload_grants_key_hash_length_check CHECK (octet_length(key_hash) = 32),
    CONSTRAINT form_upload_grants_files_used_check CHECK (files_used >= 0),
    CONSTRAINT form_upload_grants_files_within_max_check CHECK (files_used <= max_files)
);

CREATE INDEX form_upload_grants_expires
    ON form_upload_grants(expires_at);
-- +goose StatementEnd

-- A pending upload: a storage object uploaded for a form that doesn't exist
-- yet. grant_id is NULL for an upload made with an account token.
-- +goose StatementBegin
CREATE TABLE form_uploads (
    storage_object_id UUID PRIMARY KEY REFERENCES storage_objects(id) ON DELETE CASCADE,
    project_id        UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    created_by_id     UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    grant_id          UUID REFERENCES form_upload_grants(id) ON DELETE SET NULL,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT form_uploads_object_project_fk
        FOREIGN KEY (project_id, storage_object_id) REFERENCES storage_objects(project_id, id) ON DELETE CASCADE
);

CREATE INDEX form_uploads_project_creator
    ON form_uploads(project_id, created_by_id);

CREATE INDEX form_uploads_created
    ON form_uploads(created_at);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE form_attachments (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id        UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    form_id           UUID NOT NULL,
    storage_object_id UUID NOT NULL,
    created_by_id     UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT form_attachments_form_project_fk
        FOREIGN KEY (project_id, form_id) REFERENCES forms(project_id, id) ON DELETE CASCADE,
    CONSTRAINT form_attachments_object_project_fk
        FOREIGN KEY (project_id, storage_object_id) REFERENCES storage_objects(project_id, id) ON DELETE CASCADE,
    CONSTRAINT form_attachments_form_object_key UNIQUE (form_id, storage_object_id),
    CONSTRAINT form_attachments_storage_object_key UNIQUE (storage_object_id)
);

CREATE INDEX form_attachments_form
    ON form_attachments(form_id, created_at, id);

CREATE INDEX form_attachments_project
    ON form_attachments(project_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS form_attachments;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS form_uploads;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS form_upload_grants;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE forms
    DROP CONSTRAINT forms_project_id_id_key;
-- +goose StatementEnd
