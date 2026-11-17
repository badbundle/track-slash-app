-- +goose Up
-- The person answering a form can attach files to an answer. They are
-- form_attachments rows like the asker's media, told apart by
-- answer_question_id: NULL for a file the asker put in the form (every row
-- before this), and the question's id for a file the assignee attached to
-- their answer to that question.
-- +goose StatementBegin
ALTER TABLE form_attachments
    ADD COLUMN answer_question_id TEXT;
-- +goose StatementEnd

-- "The files of this form's answer to this question". The asker's rows have a
-- NULL question and are left out.
-- +goose StatementBegin
CREATE INDEX form_attachments_form_answer
    ON form_attachments(form_id, answer_question_id)
    WHERE answer_question_id IS NOT NULL;
-- +goose StatementEnd

-- A download grant is what a file's download URL stands for. Only the SHA-256
-- of the key in the URL is stored. The URL serves one file of one form, for a
-- short time, and counts as the member who made it.
-- +goose StatementBegin
CREATE TABLE form_file_grants (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id        UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    form_id           UUID NOT NULL,
    storage_object_id UUID NOT NULL,
    created_by_id     UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    key_hash          BYTEA NOT NULL,
    expires_at        TIMESTAMPTZ NOT NULL,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT form_file_grants_key_hash_key UNIQUE (key_hash),
    CONSTRAINT form_file_grants_key_hash_length_check CHECK (octet_length(key_hash) = 32),
    CONSTRAINT form_file_grants_form_project_fk
        FOREIGN KEY (project_id, form_id) REFERENCES forms(project_id, id) ON DELETE CASCADE,
    CONSTRAINT form_file_grants_object_project_fk
        FOREIGN KEY (project_id, storage_object_id) REFERENCES storage_objects(project_id, id) ON DELETE CASCADE
);

CREATE INDEX form_file_grants_expires
    ON form_file_grants(expires_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS form_file_grants;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX IF EXISTS form_attachments_form_answer;
-- +goose StatementEnd

-- Files attached to answers go with the column: only the asker's media stays.
-- Their objects are soft-deleted first, which queues the bytes for deletion
-- and keeps them from showing up as ordinary project objects.
-- +goose StatementBegin
UPDATE storage_objects
SET deleted_at = now(),
    updated_at = GREATEST(clock_timestamp(), updated_at + interval '1 microsecond')
WHERE deleted_at IS NULL
  AND id IN (SELECT storage_object_id FROM form_attachments WHERE answer_question_id IS NOT NULL);
-- +goose StatementEnd

-- +goose StatementBegin
DELETE FROM form_attachments WHERE answer_question_id IS NOT NULL;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE form_attachments
    DROP COLUMN IF EXISTS answer_question_id;
-- +goose StatementEnd
