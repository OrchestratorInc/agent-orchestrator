-- +goose Up
ALTER TABLE conversation_turns ADD COLUMN usage_limit_resets_at TIMESTAMP;
ALTER TABLE conversation_turns ADD COLUMN usage_limit_auto_resume INTEGER NOT NULL DEFAULT 0 CHECK (usage_limit_auto_resume IN (0, 1));
ALTER TABLE conversation_turns ADD COLUMN usage_limit_resume_error TEXT NOT NULL DEFAULT '';

CREATE INDEX conversation_turns_auto_resume ON conversation_turns(usage_limit_resets_at) WHERE usage_limit_auto_resume = 1;

-- +goose StatementBegin
CREATE TRIGGER conversation_turns_auto_resume_cdc_update
AFTER UPDATE OF usage_limit_auto_resume, usage_limit_resume_error ON conversation_turns
WHEN OLD.usage_limit_auto_resume <> NEW.usage_limit_auto_resume OR OLD.usage_limit_resume_error <> NEW.usage_limit_resume_error
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    SELECT s.project_id, s.id, 'session_updated',
        json_object('id', s.id, 'sessionId', s.id, 'conversationId', NEW.conversation_id), CURRENT_TIMESTAMP
    FROM conversations c JOIN sessions s ON s.id = c.current_session_id
    WHERE c.id = NEW.conversation_id;
END;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER conversation_turns_auto_resume_cdc_update;
DROP INDEX conversation_turns_auto_resume;
ALTER TABLE conversation_turns DROP COLUMN usage_limit_resume_error;
ALTER TABLE conversation_turns DROP COLUMN usage_limit_auto_resume;
ALTER TABLE conversation_turns DROP COLUMN usage_limit_resets_at;
