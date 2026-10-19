-- +goose Up
ALTER TABLE conversations ADD COLUMN speed_mode TEXT
    CHECK (speed_mode IS NULL OR speed_mode IN ('normal', 'fast', 'ultrafast'));
ALTER TABLE sessions ADD COLUMN speed_mode TEXT NOT NULL DEFAULT ''
    CHECK (speed_mode IN ('', 'normal', 'fast', 'ultrafast'));

-- The conversation owns live choices; terminal restore reads the same choice
-- from the session. Generic lifecycle writes must not revert this preference.
-- +goose StatementBegin
CREATE TRIGGER conversation_speed_to_session
AFTER UPDATE OF speed_mode, current_session_id ON conversations
WHEN NEW.current_session_id IS NOT NULL AND
    (OLD.speed_mode IS NOT NEW.speed_mode OR OLD.current_session_id IS NOT NEW.current_session_id)
BEGIN
    UPDATE sessions SET speed_mode = COALESCE(NEW.speed_mode, '')
    WHERE id = NEW.current_session_id;
END;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER conversation_speed_to_session;
ALTER TABLE sessions DROP COLUMN speed_mode;
ALTER TABLE conversations DROP COLUMN speed_mode;
