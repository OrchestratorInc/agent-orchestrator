-- +goose Up

-- Reading a forked conversation keeps each turn only when one of its messages or
-- activities sits at or before the fork point. That per-turn check filters on
-- turn_id, conversation_id and sequence; without an index leading on turn_id it
-- range-scans the conversation's history once per turn, so large forked
-- conversations took longer than a resume's deadline to load (#7545).
CREATE INDEX idx_conversation_messages_turn_lineage
    ON conversation_messages(turn_id, conversation_id, sequence);
CREATE INDEX idx_conversation_activities_turn_lineage
    ON conversation_activities(turn_id, conversation_id, sequence);

-- +goose Down
DROP INDEX IF EXISTS idx_conversation_activities_turn_lineage;
DROP INDEX IF EXISTS idx_conversation_messages_turn_lineage;
