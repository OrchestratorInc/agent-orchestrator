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

-- The handoff sweep reads recent failures only while the setting is on.
CREATE INDEX idx_conversation_turns_failed_recent
    ON conversation_turns(completed_at) WHERE state = 'failed';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX idx_conversation_turns_failed_recent;
DROP TABLE session_account_pins;
ALTER TABLE app_settings DROP COLUMN account_handoff;
-- +goose StatementEnd
