-- +goose Up

-- The chat settings a session's turns run with: model, reasoning effort,
-- permission mode and approval mode. The session's owner (its creator, or an
-- organization owner or admin) sets them; everyone else in a shared session
-- sees them and their turns run with them. Empty means each turn's own
-- settings, as before sessions were shared.
ALTER TABLE ao_sessions
    ADD COLUMN chat_settings JSONB NOT NULL DEFAULT '{}'::jsonb
        CHECK (jsonb_typeof(chat_settings) = 'object' AND pg_column_size(chat_settings) <= 2048);

-- A change wakes the session's live streams, so everyone sees it at once.
-- +goose StatementBegin
CREATE FUNCTION ao_notify_session_chat_settings() RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF OLD.chat_settings IS DISTINCT FROM NEW.chat_settings THEN
        PERFORM pg_notify('ao_session_live', NEW.id::text);
    END IF;
    RETURN NULL;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER ao_sessions_chat_settings_live_notify
    AFTER UPDATE OF chat_settings
    ON ao_sessions
    FOR EACH ROW EXECUTE FUNCTION ao_notify_session_chat_settings();

-- +goose Down
DROP TRIGGER IF EXISTS ao_sessions_chat_settings_live_notify ON ao_sessions;
DROP FUNCTION IF EXISTS ao_notify_session_chat_settings();
ALTER TABLE ao_sessions DROP COLUMN IF EXISTS chat_settings;
