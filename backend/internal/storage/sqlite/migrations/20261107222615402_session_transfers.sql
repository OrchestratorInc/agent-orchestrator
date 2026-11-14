-- +goose Up
CREATE TABLE session_transfers (
    session_id      TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
    transfer_id     TEXT NOT NULL,
    dest_host_id    TEXT NOT NULL DEFAULT '',
    dest_label      TEXT NOT NULL DEFAULT '',
    dest_session_id TEXT NOT NULL DEFAULT '',
    created_at      TIMESTAMP NOT NULL
);

-- +goose Down
DROP TABLE session_transfers;
