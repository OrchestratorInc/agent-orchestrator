-- +goose Up

-- Reading a forked conversation keeps each turn only when one of its messages or
-- activities sits at or before the fork point. That per-turn check filters on
-- turn_id, conversation_id and sequence. Without statistics, which AO never
-- collects, the planner range-scans the conversation's history once per turn
-- instead of using the turn_id indexes, so large forked conversations took
-- longer than a resume's deadline to load (#7545). These indexes answer the
-- whole check.
--
-- They lead on turn_id, so they also serve the session delete's turn_id lookups
-- and replace the turn_id-only indexes added for it: each insert keeps
-- maintaining one turn_id index, not two. A build of this change's draft
-- (version 20261101115405806) created full indexes under these names; they are
-- rebuilt as partial ones.
DROP INDEX IF EXISTS idx_conversation_messages_turn_lineage;
DROP INDEX IF EXISTS idx_conversation_activities_turn_lineage;
CREATE INDEX idx_conversation_messages_turn_lineage
    ON conversation_messages(turn_id, conversation_id, sequence) WHERE turn_id IS NOT NULL;
CREATE INDEX idx_conversation_activities_turn_lineage
    ON conversation_activities(turn_id, conversation_id, sequence) WHERE turn_id IS NOT NULL;
DROP INDEX IF EXISTS idx_conversation_messages_turn;
DROP INDEX IF EXISTS idx_conversation_activities_turn;

-- +goose Down
CREATE INDEX IF NOT EXISTS idx_conversation_activities_turn
    ON conversation_activities(turn_id) WHERE turn_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_conversation_messages_turn
    ON conversation_messages(turn_id) WHERE turn_id IS NOT NULL;
DROP INDEX IF EXISTS idx_conversation_activities_turn_lineage;
DROP INDEX IF EXISTS idx_conversation_messages_turn_lineage;
