-- +goose Up
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
