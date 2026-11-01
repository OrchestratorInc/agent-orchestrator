-- +goose Up
-- The one-line card summary shown on a kanban card. It has its own
-- column rather than sharing latest_assistant_update, which lifecycle hooks
-- rewrite on every stop and which handoff and chat replay read as the agent's
-- last message. No full-row UPDATE lists this column, so those writers cannot
-- overwrite it.
ALTER TABLE sessions ADD COLUMN card_summary TEXT NOT NULL DEFAULT '';

-- A summary-only write must still invalidate the board.
-- +goose StatementBegin
CREATE TRIGGER sessions_card_summary_cdc
AFTER UPDATE OF card_summary ON sessions
WHEN NEW.card_summary <> OLD.card_summary
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (NEW.project_id, NEW.id, 'session_updated', json_object('id', NEW.id), NEW.updated_at);
END;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER IF EXISTS sessions_card_summary_cdc;
ALTER TABLE sessions DROP COLUMN card_summary;
