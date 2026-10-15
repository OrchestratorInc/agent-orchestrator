-- +goose Up
-- +goose StatementBegin
-- Every completed turn asks, inside its projection transaction on the single
-- writer, whether the active branch archived a matching turn.completed event
-- (SelectVerifiedAuthenticationTurn). Through idx_conversation_provider_events_branch
-- that lookup walks every archive row of the branch and reads each one to test
-- method, and the archive is mostly streamed deltas. Indexing only the
-- turn.completed rows leaves the lookup one entry per completed turn to walk,
-- and no other event pays an index write for it.
CREATE INDEX idx_conversation_provider_events_turn_completed
    ON conversation_provider_events(branch_id, id)
    WHERE method = 'turn.completed';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_conversation_provider_events_turn_completed;
-- +goose StatementEnd
