-- +goose Up
-- The session artifacts a turn created or edited, as JSON: each entry's path
-- inside the session's artifact directory and whether the turn added it or
-- changed it. Recorded by AO from the artifact directory itself, so it covers
-- files an agent wrote through a shell command, which no provider diff lists.
-- Turns from before this column existed stay '' and show no artifacts.
ALTER TABLE conversation_turns ADD COLUMN artifacts_json TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE conversation_turns DROP COLUMN artifacts_json;
