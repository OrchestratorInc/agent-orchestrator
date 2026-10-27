-- +goose Up

-- Wake a session's event streams the moment any path appends to its event
-- log. Without it every open stream polls PostgreSQL once a second, so a
-- shared session's cost grows with its viewers even while it is idle, and
-- assistant output reaches each viewer up to a second late. pg_notify folds
-- identical payloads within one transaction, so a burst of deltas committed
-- together wakes each stream once. Each stream's own replay query remains the
-- source of truth; a lost notification only delays it to the fallback poll.
-- +goose StatementBegin
CREATE FUNCTION ao_notify_session_event() RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    PERFORM pg_notify('ao_session_event', NEW.session_id::text);
    RETURN NULL;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER ao_events_session_notify
    AFTER INSERT
    ON ao_events
    FOR EACH ROW EXECUTE FUNCTION ao_notify_session_event();

-- +goose Down
DROP TRIGGER IF EXISTS ao_events_session_notify ON ao_events;
DROP FUNCTION IF EXISTS ao_notify_session_event();
