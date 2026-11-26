-- +goose Up

-- Reading a forked conversation keeps each turn only when one of its messages or
-- activities sits at or before the fork point. That per-turn check filters on
-- turn_id, conversation_id and sequence. Without statistics, which AO never
-- collects, the planner range-scans the conversation's history once per turn
-- instead of using the turn_id indexes, so large forked conversations took
-- longer than a resume's deadline to load (#7545).
--
-- IF NOT EXISTS: a build of this change's draft (version 20261101115405806)
-- already created these indexes.
CREATE INDEX IF NOT EXISTS idx_conversation_messages_turn_lineage
    ON conversation_messages(turn_id, conversation_id, sequence);
CREATE INDEX IF NOT EXISTS idx_conversation_activities_turn_lineage
    ON conversation_activities(turn_id, conversation_id, sequence);

-- +goose Down
DROP INDEX IF EXISTS idx_conversation_activities_turn_lineage;
DROP INDEX IF EXISTS idx_conversation_messages_turn_lineage;
