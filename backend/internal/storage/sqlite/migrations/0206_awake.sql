-- +goose Up
-- One daemon owns this aggregate. IF NOT EXISTS: earlier builds of this branch shipped it as 0201/0203. Runtime/native observations are never stored.
CREATE TABLE IF NOT EXISTS awake_state (
 id INTEGER PRIMARY KEY CHECK (id = 1),
 document TEXT NOT NULL CHECK (json_valid(document))
);
INSERT OR IGNORE INTO awake_state (id, document) VALUES (1, '{}');

-- +goose Down
DROP TABLE awake_state;
