-- +goose Up
-- Session numbers come from MAX(num) + 1, so deleting the newest session of a
-- project would hand its number, id, and branch name to the next spawn. Session
-- deletion records the highest number it removed here, keyed by the id prefix
-- (the project id, or "standalone"), and allocation never goes at or below it.
CREATE TABLE session_num_floors (
    id_prefix TEXT PRIMARY KEY,
    max_num   INTEGER NOT NULL
);

-- +goose Down
DROP TABLE session_num_floors;
