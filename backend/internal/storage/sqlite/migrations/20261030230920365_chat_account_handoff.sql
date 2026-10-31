-- +goose Up
-- +goose StatementBegin
-- Off by default: when on, a Chat turn refused for a usage limit moves its
-- session to another signed-in account of the same harness with quota, and the
-- failed prompt is retried there once (Settings, Agent accounts).
ALTER TABLE app_settings ADD COLUMN account_handoff INTEGER NOT NULL DEFAULT 0
    CHECK (account_handoff IN (0, 1));

-- "Stay on this account": a session whose account the user chose to keep is
-- never moved automatically.
CREATE TABLE session_account_pins (
    session_id TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
    pinned_at  TIMESTAMP NOT NULL
);

-- One automatic handoff per refused turn. The row is the claim, the saga's
-- checkpoint across restarts, and the retry's one-time consent: the retry
-- transaction moves it from 'moved' to 'retried'.
CREATE TABLE chat_account_handoffs (
    source_turn_id    TEXT PRIMARY KEY,
    session_id        TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    conversation_id   TEXT NOT NULL,
    source_account_id TEXT NOT NULL,
    target_account_id TEXT NOT NULL DEFAULT '',
    transition_id     TEXT NOT NULL DEFAULT '',
    phase             TEXT NOT NULL CHECK (phase IN (
        'selecting', 'moving', 'moved', 'retried', 'stayed', 'rolled_back', 'skipped'
    )),
    detail            TEXT NOT NULL DEFAULT '',
    source_resets_at  TIMESTAMP,
    created_at        TIMESTAMP NOT NULL,
    updated_at        TIMESTAMP NOT NULL
);
CREATE INDEX idx_chat_account_handoffs_active
    ON chat_account_handoffs(phase) WHERE phase IN ('selecting', 'moving', 'moved');
CREATE INDEX idx_chat_account_handoffs_left
    ON chat_account_handoffs(session_id, source_account_id, created_at);

-- The handoff sweep reads recent failures only while the setting is on.
CREATE INDEX idx_conversation_turns_failed_recent
    ON conversation_turns(completed_at) WHERE state = 'failed';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX idx_conversation_turns_failed_recent;
DROP TABLE chat_account_handoffs;
DROP TABLE session_account_pins;
ALTER TABLE app_settings DROP COLUMN account_handoff;
-- +goose StatementEnd
