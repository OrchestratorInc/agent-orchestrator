-- +goose Up
-- A harness update that ran while this session's process was live leaves that
-- process on the previous release. The mark names the launch it applies to
-- (the Chat controller generation or terminal launch id at marking time), so
-- any later launch reads as current without a write: staleness is derived when
-- the row is read, never cleared by hand.
ALTER TABLE sessions ADD COLUMN stale_binary_since TIMESTAMP;
ALTER TABLE sessions ADD COLUMN stale_binary_version TEXT NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN stale_binary_launch TEXT NOT NULL DEFAULT '';
-- Set when an automatic move to the new release failed; the session keeps its
-- queued messages and the user resumes it.
ALTER TABLE sessions ADD COLUMN stale_binary_switch_failed_at TIMESTAMP;

-- Like the hibernation marker, the mark is not board recency, so it only sends
-- the invalidation event the Chat header needs.
-- +goose StatementBegin
CREATE TRIGGER sessions_stale_binary_cdc_update
AFTER UPDATE OF stale_binary_since, stale_binary_launch, stale_binary_switch_failed_at ON sessions
WHEN OLD.stale_binary_since IS NOT NEW.stale_binary_since
    OR OLD.stale_binary_launch IS NOT NEW.stale_binary_launch
    OR OLD.stale_binary_switch_failed_at IS NOT NEW.stale_binary_switch_failed_at
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (NEW.project_id, NEW.id, 'session_updated',
        json_object('id', NEW.id, 'conversationId',
            (SELECT id FROM conversations WHERE current_session_id = NEW.id LIMIT 1)),
        CURRENT_TIMESTAMP);
END;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER sessions_stale_binary_cdc_update;
ALTER TABLE sessions DROP COLUMN stale_binary_switch_failed_at;
ALTER TABLE sessions DROP COLUMN stale_binary_launch;
ALTER TABLE sessions DROP COLUMN stale_binary_version;
ALTER TABLE sessions DROP COLUMN stale_binary_since;
