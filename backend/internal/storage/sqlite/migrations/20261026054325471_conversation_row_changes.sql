-- +goose Up
-- One durable clock shared by per-row cursors. Unlike timeline sequence, this
-- advances on rewrites, including settled rows and writes from any adapter.
CREATE TABLE conversation_change_clock (id INTEGER PRIMARY KEY CHECK (id = 1), cursor INTEGER NOT NULL);
INSERT INTO conversation_change_clock VALUES (1, 1);
CREATE TABLE conversation_row_changes (
    conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    kind TEXT NOT NULL,
    row_id TEXT NOT NULL,
    changed_at INTEGER NOT NULL,
    PRIMARY KEY (conversation_id, kind, row_id)
);
CREATE INDEX conversation_row_changes_cursor ON conversation_row_changes(conversation_id, changed_at);
INSERT INTO conversation_row_changes SELECT conversation_id, 'message', id, 1 FROM conversation_messages;

-- +goose StatementBegin
CREATE TRIGGER conversation_messages_change_insert AFTER INSERT ON conversation_messages
BEGIN
    UPDATE conversation_change_clock SET cursor = cursor + 1 WHERE id = 1;
    INSERT INTO conversation_row_changes VALUES (NEW.conversation_id, 'message', NEW.id, (SELECT cursor FROM conversation_change_clock WHERE id = 1))
    ON CONFLICT(conversation_id, kind, row_id) DO UPDATE SET changed_at = excluded.changed_at;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER conversation_messages_change_update AFTER UPDATE ON conversation_messages
BEGIN
    UPDATE conversation_change_clock SET cursor = cursor + 1 WHERE id = 1;
    INSERT INTO conversation_row_changes VALUES (NEW.conversation_id, 'message', NEW.id, (SELECT cursor FROM conversation_change_clock WHERE id = 1))
    ON CONFLICT(conversation_id, kind, row_id) DO UPDATE SET changed_at = excluded.changed_at;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER conversation_messages_change_delete AFTER DELETE ON conversation_messages
WHEN EXISTS (SELECT 1 FROM conversations WHERE id = OLD.conversation_id)
BEGIN
    UPDATE conversation_change_clock SET cursor = cursor + 1 WHERE id = 1;
    INSERT INTO conversation_row_changes VALUES (OLD.conversation_id, 'reset', OLD.conversation_id, (SELECT cursor FROM conversation_change_clock WHERE id = 1))
    ON CONFLICT(conversation_id, kind, row_id) DO UPDATE SET changed_at = excluded.changed_at;
END;
-- +goose StatementEnd
INSERT INTO conversation_row_changes SELECT conversation_id, 'activity', id, 1 FROM conversation_activities;

-- +goose StatementBegin
CREATE TRIGGER conversation_activities_change_insert AFTER INSERT ON conversation_activities
BEGIN
    UPDATE conversation_change_clock SET cursor = cursor + 1 WHERE id = 1;
    INSERT INTO conversation_row_changes VALUES (NEW.conversation_id, 'activity', NEW.id, (SELECT cursor FROM conversation_change_clock WHERE id = 1))
    ON CONFLICT(conversation_id, kind, row_id) DO UPDATE SET changed_at = excluded.changed_at;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER conversation_activities_change_update AFTER UPDATE ON conversation_activities
BEGIN
    UPDATE conversation_change_clock SET cursor = cursor + 1 WHERE id = 1;
    INSERT INTO conversation_row_changes VALUES (NEW.conversation_id, 'activity', NEW.id, (SELECT cursor FROM conversation_change_clock WHERE id = 1))
    ON CONFLICT(conversation_id, kind, row_id) DO UPDATE SET changed_at = excluded.changed_at;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER conversation_activities_change_delete AFTER DELETE ON conversation_activities
WHEN EXISTS (SELECT 1 FROM conversations WHERE id = OLD.conversation_id)
BEGIN
    UPDATE conversation_change_clock SET cursor = cursor + 1 WHERE id = 1;
    INSERT INTO conversation_row_changes VALUES (OLD.conversation_id, 'reset', OLD.conversation_id, (SELECT cursor FROM conversation_change_clock WHERE id = 1))
    ON CONFLICT(conversation_id, kind, row_id) DO UPDATE SET changed_at = excluded.changed_at;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER conversation_turns_change_reset AFTER UPDATE OF rolled_back_at, promoted_to_turn_id, state ON conversation_turns
WHEN NEW.rolled_back_at IS NOT OLD.rolled_back_at OR NEW.promoted_to_turn_id IS NOT OLD.promoted_to_turn_id OR (NEW.state = 'cancelled' AND OLD.state <> 'cancelled')
BEGIN
    UPDATE conversation_change_clock SET cursor = cursor + 1 WHERE id = 1;
    INSERT INTO conversation_row_changes VALUES (NEW.conversation_id, 'reset', NEW.conversation_id, (SELECT cursor FROM conversation_change_clock WHERE id = 1))
    ON CONFLICT(conversation_id, kind, row_id) DO UPDATE SET changed_at = excluded.changed_at;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER conversation_turns_change_insert AFTER INSERT ON conversation_turns
BEGIN
    UPDATE conversation_change_clock SET cursor = cursor + 1 WHERE id = 1;
    INSERT INTO conversation_row_changes VALUES (NEW.conversation_id, 'turn', NEW.id, (SELECT cursor FROM conversation_change_clock WHERE id = 1))
    ON CONFLICT(conversation_id, kind, row_id) DO UPDATE SET changed_at = excluded.changed_at;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER conversation_turns_change_update AFTER UPDATE ON conversation_turns
BEGIN
    UPDATE conversation_change_clock SET cursor = cursor + 1 WHERE id = 1;
    INSERT INTO conversation_row_changes VALUES (NEW.conversation_id, 'turn', NEW.id, (SELECT cursor FROM conversation_change_clock WHERE id = 1))
    ON CONFLICT(conversation_id, kind, row_id) DO UPDATE SET changed_at = excluded.changed_at;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER conversation_turns_change_delete AFTER DELETE ON conversation_turns
WHEN EXISTS (SELECT 1 FROM conversations WHERE id = OLD.conversation_id)
BEGIN
    UPDATE conversation_change_clock SET cursor = cursor + 1 WHERE id = 1;
    INSERT INTO conversation_row_changes VALUES (OLD.conversation_id, 'reset', OLD.conversation_id, (SELECT cursor FROM conversation_change_clock WHERE id = 1))
    ON CONFLICT(conversation_id, kind, row_id) DO UPDATE SET changed_at = excluded.changed_at;
END;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER conversation_turns_change_insert;
DROP TRIGGER conversation_turns_change_update;
DROP TRIGGER conversation_turns_change_delete;
DROP TRIGGER conversation_turns_change_reset;
DROP TRIGGER conversation_messages_change_insert;
DROP TRIGGER conversation_messages_change_update;
DROP TRIGGER conversation_messages_change_delete;
DROP TRIGGER conversation_activities_change_insert;
DROP TRIGGER conversation_activities_change_update;
DROP TRIGGER conversation_activities_change_delete;
DROP TABLE conversation_row_changes;
DROP TABLE conversation_change_clock;
