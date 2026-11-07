-- +goose Up

-- A session delete cascades through its turns and reviews. For each deleted
-- turn SQLite sets turn_id to NULL in activities and messages, and for each
-- deleted review it does the same to review_id in the provider archive.
-- Without these indexes each of those lookups is a full table scan on the
-- only writer connection: about 90 ms per turn and 4 s per review on a real
-- 2 GB database.
CREATE INDEX idx_conversation_activities_turn
    ON conversation_activities(turn_id) WHERE turn_id IS NOT NULL;
CREATE INDEX idx_conversation_messages_turn
    ON conversation_messages(turn_id) WHERE turn_id IS NOT NULL;
CREATE INDEX idx_conversation_provider_events_review
    ON conversation_provider_events(review_id) WHERE review_id IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS idx_conversation_provider_events_review;
DROP INDEX IF EXISTS idx_conversation_messages_turn;
DROP INDEX IF EXISTS idx_conversation_activities_turn;
