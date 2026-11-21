-- +goose Up

-- The earlier agent-switching tables (agent_switches, agent_native_sessions,
-- agent_switch_failure_*) are no longer used but stay for one release: an
-- older build reads them at startup, so dropping them now would stop a
-- downgrade from opening the database. A later migration drops them.

-- One row per Terminal UI turn, fed by the prompt-submit and stop hooks. A
-- terminal agent owns its transcript, so this is AO's only record of its turns.
CREATE TABLE session_terminal_turns (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    harness TEXT NOT NULL,
    native_session_id TEXT NOT NULL DEFAULT '',
    user_request TEXT NOT NULL,
    agent_response TEXT NOT NULL DEFAULT '',
    requested_at TIMESTAMP NOT NULL,
    responded_at TIMESTAMP
);
CREATE INDEX idx_session_terminal_turns_session ON session_terminal_turns (session_id, id);

-- The note a Chat switch gave the branch's provider, re-applied when it restarts.
ALTER TABLE conversation_branches ADD COLUMN handoff_note TEXT NOT NULL DEFAULT '';
-- When Claude Code compacts on its own: '' leaves it to Claude Code.
ALTER TABLE conversations ADD COLUMN auto_compact_window TEXT NOT NULL DEFAULT '';
-- A terminal agent's launch choices as JSON (fast tier, Ultracode, auto-compact window).
ALTER TABLE sessions ADD COLUMN launch_options TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE sessions DROP COLUMN launch_options;
ALTER TABLE conversations DROP COLUMN auto_compact_window;
ALTER TABLE conversation_branches DROP COLUMN handoff_note;
DROP TABLE session_terminal_turns;
