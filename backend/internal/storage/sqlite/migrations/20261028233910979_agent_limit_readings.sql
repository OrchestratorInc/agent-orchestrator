-- +goose Up
CREATE TABLE agent_limit_readings (
    account_id       TEXT    NOT NULL CHECK (trim(account_id) <> ''),
    provider         TEXT    NOT NULL CHECK (provider IN ('claude', 'codex')),
    email            TEXT    NOT NULL DEFAULT '',
    plan             TEXT    NOT NULL DEFAULT '',
    window_id        TEXT    NOT NULL CHECK (trim(window_id) <> ''),
    label            TEXT    NOT NULL DEFAULT '',
    duration_minutes INTEGER NOT NULL DEFAULT 0,
    used_percent     REAL    NOT NULL,
    resets_at_ms     INTEGER,
    read_at_ms       INTEGER NOT NULL,
    PRIMARY KEY (account_id, window_id, read_at_ms)
) WITHOUT ROWID;

CREATE INDEX agent_limit_readings_read_at ON agent_limit_readings (read_at_ms);

-- +goose Down
DROP TABLE agent_limit_readings;
