-- +goose Up
-- When a session was archived, for the opt-in retention that deletes archived
-- sessions after a number of days. The trigger keeps it in step with
-- is_terminated however the row is written, and clears it on restore. Rows
-- archived before this have none; retention counts those from their last update.
ALTER TABLE sessions ADD COLUMN terminated_at TIMESTAMP;

-- +goose StatementBegin
CREATE TRIGGER sessions_terminated_at
AFTER UPDATE OF is_terminated ON sessions
WHEN OLD.is_terminated <> NEW.is_terminated
BEGIN
    UPDATE sessions
    SET terminated_at = CASE WHEN NEW.is_terminated THEN CURRENT_TIMESTAMP END
    WHERE id = NEW.id;
END;
-- +goose StatementEnd

-- Days an archived session is kept before the daemon deletes it; 0 keeps it.
ALTER TABLE app_settings ADD COLUMN session_retention_days INTEGER NOT NULL DEFAULT 0
    CHECK (session_retention_days >= 0);

-- +goose Down
ALTER TABLE app_settings DROP COLUMN session_retention_days;
DROP TRIGGER sessions_terminated_at;
ALTER TABLE sessions DROP COLUMN terminated_at;
