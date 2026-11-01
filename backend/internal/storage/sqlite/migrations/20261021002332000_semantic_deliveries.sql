-- +goose Up
CREATE TABLE semantic_deliveries (
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    runtime_launch_id TEXT NOT NULL,
    delivery_id TEXT NOT NULL,
    registered_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    accepted_at TIMESTAMP NULL,
    PRIMARY KEY (session_id, runtime_launch_id, delivery_id)
);

-- +goose Down
DROP TABLE semantic_deliveries;
