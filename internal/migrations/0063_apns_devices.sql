-- +goose Up
-- +goose StatementBegin
ALTER TABLE push_subscriptions
    ADD COLUMN kind TEXT NOT NULL DEFAULT 'web' CHECK (kind IN ('web', 'apns'));
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE push_subscriptions
    DROP CONSTRAINT push_subscriptions_p256dh_check,
    DROP CONSTRAINT push_subscriptions_auth_secret_check;
-- +goose StatementEnd

-- +goose StatementBegin
-- A browser subscription carries its keys. An iOS device is only its APNs
-- token, kept in endpoint, so it has none.
ALTER TABLE push_subscriptions
    ADD CONSTRAINT push_subscriptions_keys_check CHECK (
        length(p256dh) <= 1024 AND length(auth_secret) <= 1024
        AND (kind = 'apns' OR (length(p256dh) >= 1 AND length(auth_secret) >= 1))
    );
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM push_subscriptions WHERE kind = 'apns';
ALTER TABLE push_subscriptions DROP CONSTRAINT push_subscriptions_keys_check;
ALTER TABLE push_subscriptions
    ADD CONSTRAINT push_subscriptions_p256dh_check CHECK (length(p256dh) BETWEEN 1 AND 1024),
    ADD CONSTRAINT push_subscriptions_auth_secret_check CHECK (length(auth_secret) BETWEEN 1 AND 1024);
ALTER TABLE push_subscriptions DROP COLUMN kind;
-- +goose StatementEnd
