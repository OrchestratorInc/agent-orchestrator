-- +goose Up
-- A deleted session's own change_log rows have to go before it (they reference
-- it), so one project-level event without a session id tells clients to
-- refetch the list the session left. Task preparations are never listed.
-- +goose StatementBegin
CREATE TRIGGER sessions_cdc_delete
AFTER DELETE ON sessions
WHEN OLD.is_task_preparation = 0
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (OLD.project_id, NULL, 'session_updated', json_object('deletedSessionId', OLD.id), datetime('now'));
END;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER sessions_cdc_delete;
