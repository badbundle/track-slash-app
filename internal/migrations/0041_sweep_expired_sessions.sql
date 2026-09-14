-- +goose Up
-- +goose StatementBegin
-- Sessions are issued with an expiry, but the sweep only looked at age, so a
-- session stayed unrevoked for three years after it stopped working. Anything
-- reading revoked_at to mean "usable" was wrong for the intervening years.
CREATE INDEX auth_tokens_live_sessions_by_expiry
    ON auth_tokens(kind, expires_at)
    WHERE revoked_at IS NULL;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION track_sweep_expired_sessions() RETURNS trigger AS $$
BEGIN
    -- One sweep per interval, claimed by updating the single sweep row. Two
    -- concurrent refreshes serialise on that row lock, and under READ COMMITTED
    -- the loser re-evaluates the predicate against the winner's committed value
    -- and matches nothing, so exactly one of them pays for the sweep.
    UPDATE auth_token_sweeps
    SET last_swept_at = now()
    WHERE id
      AND last_swept_at < now() - INTERVAL '1 hour';
    IF NOT FOUND THEN
        RETURN NULL;
    END IF;

    -- Sessions only: API tokens are long-lived by design. A session goes when
    -- its own expiry has passed; the age rule stays as the backstop for
    -- sessions carrying no expiry at all, which nothing would otherwise clear.
    -- The token being refreshed is spared so an active session is never revoked
    -- out from under the request that is using it; the next sweep reconsiders
    -- it. The limit bounds how long a single refresh can be made to wait.
    UPDATE auth_tokens
    SET revoked_at = now()
    WHERE id IN (
        SELECT id
        FROM auth_tokens
        WHERE kind = 'session'
          AND revoked_at IS NULL
          AND (expires_at < now() OR created_at < now() - INTERVAL '3 years')
          AND id <> NEW.id
        LIMIT 1000
    );
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION track_sweep_expired_sessions() RETURNS trigger AS $$
BEGIN
    UPDATE auth_token_sweeps
    SET last_swept_at = now()
    WHERE id
      AND last_swept_at < now() - INTERVAL '1 hour';
    IF NOT FOUND THEN
        RETURN NULL;
    END IF;

    UPDATE auth_tokens
    SET revoked_at = now()
    WHERE id IN (
        SELECT id
        FROM auth_tokens
        WHERE kind = 'session'
          AND revoked_at IS NULL
          AND created_at < now() - INTERVAL '3 years'
          AND id <> NEW.id
        LIMIT 1000
    );
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX IF EXISTS auth_tokens_live_sessions_by_expiry;
-- +goose StatementEnd
