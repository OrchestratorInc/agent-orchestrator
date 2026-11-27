-- +goose Up
-- An orchestrator marks a message as a worker's new task (`ao send --task`) and
-- may say what that task needs from the worker's earlier work (`--keep`). Only a
-- marked task can compact the worker first, and the hint steers the summary.
-- Both travel with a message that waits in the send queue or in an interface
-- transition's outbox, so a restart or a hibernated worker keeps them.
ALTER TABLE conversation_messages ADD COLUMN new_task INTEGER NOT NULL DEFAULT 0;
ALTER TABLE conversation_messages ADD COLUMN keep_for_task TEXT NOT NULL DEFAULT '';
ALTER TABLE session_interface_transition_messages ADD COLUMN new_task INTEGER NOT NULL DEFAULT 0;
ALTER TABLE session_interface_transition_messages ADD COLUMN keep_for_task TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE session_interface_transition_messages DROP COLUMN keep_for_task;
ALTER TABLE session_interface_transition_messages DROP COLUMN new_task;
ALTER TABLE conversation_messages DROP COLUMN keep_for_task;
ALTER TABLE conversation_messages DROP COLUMN new_task;
