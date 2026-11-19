-- +goose Up
DROP TABLE session_account_pins;

-- +goose Down
CREATE TABLE session_account_pins (
    session_id TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE
);
