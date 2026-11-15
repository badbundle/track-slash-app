-- +goose Up
-- +goose StatementBegin
ALTER TABLE user_push_notification_preferences
    ADD COLUMN forms BOOLEAN NOT NULL DEFAULT true;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE push_notification_deliveries
    ALTER COLUMN issue_id DROP NOT NULL,
    ADD COLUMN form_id UUID REFERENCES forms(id) ON DELETE CASCADE,
    DROP CONSTRAINT push_notification_deliveries_category_check,
    ADD CONSTRAINT push_notification_deliveries_category_check
        CHECK (category IN ('mentions', 'assignments', 'comments', 'status_changes', 'due_date_changes', 'forms')),
    ADD CONSTRAINT push_notification_deliveries_one_target_check
        CHECK ((issue_id IS NULL) <> (form_id IS NULL));
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION track_enqueue_push_notification_event() RETURNS trigger AS $$
BEGIN
    IF (NEW.issue_id IS NOT NULL AND (
        (NEW.entity = 'issue' AND NEW.op IN ('insert', 'update')) OR
        (NEW.entity = 'comment' AND NEW.op = 'insert')
    )) OR (NEW.entity = 'form' AND NEW.op = 'insert') THEN
        INSERT INTO push_notification_events (changelog_id) VALUES (NEW.id);
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION track_enqueue_push_notification_event() RETURNS trigger AS $$
BEGIN
    IF NEW.issue_id IS NOT NULL AND (
        (NEW.entity = 'issue' AND NEW.op IN ('insert', 'update')) OR
        (NEW.entity = 'comment' AND NEW.op = 'insert')
    ) THEN
        INSERT INTO push_notification_events (changelog_id) VALUES (NEW.id);
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- +goose StatementBegin
DELETE FROM push_notification_deliveries WHERE form_id IS NOT NULL;
DELETE FROM push_notification_events e
    USING project_changelog_entries c
    WHERE c.id = e.changelog_id AND c.entity = 'form';
ALTER TABLE push_notification_deliveries
    DROP CONSTRAINT push_notification_deliveries_one_target_check,
    DROP CONSTRAINT push_notification_deliveries_category_check,
    DROP COLUMN form_id,
    ALTER COLUMN issue_id SET NOT NULL,
    ADD CONSTRAINT push_notification_deliveries_category_check
        CHECK (category IN ('mentions', 'assignments', 'comments', 'status_changes', 'due_date_changes'));
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE user_push_notification_preferences DROP COLUMN forms;
-- +goose StatementEnd
