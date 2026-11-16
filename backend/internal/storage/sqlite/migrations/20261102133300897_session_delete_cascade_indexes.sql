-- +goose Up
-- Deleting a session (the delete endpoint and the retention pass) cascades
-- through these foreign keys on the single writer connection. For every
-- deleted parent row SQLite looks up its children by the child column, and
-- without an index each lookup reads the whole child table: once per deleted
-- turn for the turn_id columns, so a 37-turn session over 300k activities held
-- the writer for minutes. Each index below leads with its foreign-key column.
-- Columns that are often NULL get a partial index, so rows without a link pay
-- no index write; the lookups compare with =, which implies IS NOT NULL.
-- Building them reads each table once, at the first start after upgrade.

-- Children of sessions. change_log is also cleared by session_id before the
-- delete; nearly all of its rows carry one, so a partial index would save nothing.
CREATE INDEX idx_change_log_session ON change_log(session_id);
CREATE INDEX idx_conversation_turns_handled_by_session
    ON conversation_turns(handled_by_session_id);
CREATE INDEX idx_conversation_branches_session ON conversation_branches(session_id);
CREATE INDEX idx_review_run_session ON review_run(session_id);
CREATE INDEX idx_notifications_session ON notifications(session_id);
CREATE INDEX idx_shell_terminals_session
    ON shell_terminals(session_id) WHERE session_id IS NOT NULL;
CREATE INDEX idx_automation_runs_session
    ON automation_runs(session_id) WHERE session_id IS NOT NULL;

-- Children of conversation_turns. conversation_messages(turn_id) and
-- conversation_activities(turn_id) are indexed by
-- 20261107181854835_session_delete_child_indexes, as is
-- conversation_provider_events(review_id) below.
CREATE INDEX idx_conversation_turns_retry_of
    ON conversation_turns(retry_of_turn_id) WHERE retry_of_turn_id IS NOT NULL;
CREATE INDEX idx_conversation_turns_promoted_to
    ON conversation_turns(promoted_to_turn_id) WHERE promoted_to_turn_id IS NOT NULL;
CREATE INDEX idx_conversation_branches_fork_after_turn
    ON conversation_branches(fork_after_turn_id) WHERE fork_after_turn_id IS NOT NULL;
CREATE INDEX idx_conversation_branches_replaced_turn
    ON conversation_branches(replaced_turn_id) WHERE replaced_turn_id IS NOT NULL;
CREATE INDEX idx_conversation_branches_replacement_turn
    ON conversation_branches(replacement_turn_id) WHERE replacement_turn_id IS NOT NULL;

-- Children of conversation_branches, review, session_interface_transitions,
-- agent_native_sessions and agent_switches, which the delete removes too.
-- parent_branch_id is sparse too, but the conversation reads walk the active
-- lineage WHERE parent_branch_id IS NOT NULL: a partial index matching that
-- filter would tempt the planner, once sqlite_stat1 exists, to scan every
-- forked branch at each step instead of looking the branch up by id.
CREATE INDEX idx_conversation_branches_parent ON conversation_branches(parent_branch_id);
CREATE INDEX idx_conversation_turns_handled_by_review
    ON conversation_turns(handled_by_review_id) WHERE handled_by_review_id IS NOT NULL;
CREATE INDEX idx_review_run_review ON review_run(review_id);
CREATE INDEX idx_session_interface_transition_messages_transition
    ON session_interface_transition_messages(transition_id);
CREATE INDEX idx_agent_switches_target_native_session
    ON agent_switches(target_native_session_ref) WHERE target_native_session_ref IS NOT NULL;
CREATE INDEX idx_agent_switch_failure_receipts_switch
    ON agent_switch_failure_receipts(switch_id);

-- +goose Down
DROP INDEX IF EXISTS idx_agent_switch_failure_receipts_switch;
DROP INDEX IF EXISTS idx_agent_switches_target_native_session;
DROP INDEX IF EXISTS idx_session_interface_transition_messages_transition;
DROP INDEX IF EXISTS idx_review_run_review;
DROP INDEX IF EXISTS idx_conversation_turns_handled_by_review;
DROP INDEX IF EXISTS idx_conversation_branches_parent;
DROP INDEX IF EXISTS idx_conversation_branches_replacement_turn;
DROP INDEX IF EXISTS idx_conversation_branches_replaced_turn;
DROP INDEX IF EXISTS idx_conversation_branches_fork_after_turn;
DROP INDEX IF EXISTS idx_conversation_turns_promoted_to;
DROP INDEX IF EXISTS idx_conversation_turns_retry_of;
DROP INDEX IF EXISTS idx_automation_runs_session;
DROP INDEX IF EXISTS idx_shell_terminals_session;
DROP INDEX IF EXISTS idx_notifications_session;
DROP INDEX IF EXISTS idx_review_run_session;
DROP INDEX IF EXISTS idx_conversation_branches_session;
DROP INDEX IF EXISTS idx_conversation_turns_handled_by_session;
DROP INDEX IF EXISTS idx_change_log_session;
