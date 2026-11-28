-- +goose Up
-- +goose StatementBegin
ALTER TABLE app_settings ADD COLUMN account_handoff INTEGER NOT NULL DEFAULT 0
    CHECK (account_handoff IN (0, 1));

CREATE TABLE session_account_pins (
    session_id TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE
);

CREATE INDEX idx_conversation_turns_failed_recent
    ON conversation_turns(completed_at) WHERE state = 'failed';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX idx_conversation_turns_failed_recent;
DROP TABLE session_account_pins;
ALTER TABLE app_settings DROP COLUMN account_handoff;
-- +goose StatementEnd
