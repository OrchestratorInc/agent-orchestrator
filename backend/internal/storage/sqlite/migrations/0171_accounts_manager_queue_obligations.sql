-- +goose Up
CREATE TABLE accounts_manager_queue_obligations (
    turn_id TEXT PRIMARY KEY REFERENCES conversation_turns(id) ON DELETE CASCADE
);

-- +goose StatementBegin
CREATE TRIGGER accounts_manager_queue_on_switch AFTER INSERT ON accounts_manager_switches
BEGIN
    INSERT OR IGNORE INTO accounts_manager_queue_obligations(turn_id)
    SELECT id FROM conversation_turns WHERE handled_by_session_id = NEW.session_id AND state = 'queued';
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER accounts_manager_queue_on_settlement AFTER UPDATE OF phase ON accounts_manager_switches
BEGIN
    INSERT OR IGNORE INTO accounts_manager_queue_obligations(turn_id)
    SELECT id FROM conversation_turns WHERE handled_by_session_id = NEW.session_id AND state = 'queued';
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER accounts_manager_queue_adopted AFTER UPDATE OF state ON conversation_turns
WHEN NEW.state <> 'queued'
BEGIN
    DELETE FROM accounts_manager_queue_obligations WHERE turn_id = NEW.id;
END;
-- +goose StatementEnd

INSERT OR IGNORE INTO accounts_manager_queue_obligations(turn_id)
SELECT turn.id FROM conversation_turns AS turn
WHERE turn.state = 'queued' AND EXISTS (
    SELECT 1 FROM accounts_manager_switches AS operation
    WHERE operation.session_id = turn.handled_by_session_id
      AND turn.requested_at <= operation.updated_at
);

-- +goose Down
DROP TRIGGER accounts_manager_queue_on_switch;
DROP TRIGGER accounts_manager_queue_on_settlement;
DROP TRIGGER accounts_manager_queue_adopted;
DROP TABLE accounts_manager_queue_obligations;
