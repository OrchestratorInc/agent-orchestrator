-- +goose Up
-- SelectVerifiedAuthenticationTurn looks for the turn.completed archive row of a
-- completed turn: inside that turn's projection transaction on the single writer,
-- and once per completed turn when a chat starts. Through
-- idx_conversation_provider_events_branch it reads every archive row of the branch
-- to test method, and the archive is mostly streamed deltas. Indexing only the
-- turn.completed rows leaves one entry per completed turn to walk, and no other
-- event pays an index write for it.
-- Building the index scans the whole table once, at the first start after upgrade.
CREATE INDEX idx_conversation_provider_events_turn_completed
    ON conversation_provider_events(branch_id, id)
    WHERE method = 'turn.completed';

-- +goose Down
DROP INDEX IF EXISTS idx_conversation_provider_events_turn_completed;
