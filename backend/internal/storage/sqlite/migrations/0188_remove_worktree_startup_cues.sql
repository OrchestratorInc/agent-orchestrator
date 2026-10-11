-- +goose Up
DROP INDEX IF EXISTS cues_one_worktree_startup;
ALTER TABLE cues DROP COLUMN run_on_worktree_creation;
DROP TABLE IF EXISTS startup_cue_messages;
DROP TRIGGER IF EXISTS sessions_startup_cue_cdc;
ALTER TABLE sessions DROP COLUMN startup_cue_json;

-- +goose Down
ALTER TABLE cues ADD COLUMN run_on_worktree_creation INTEGER NOT NULL DEFAULT 0 CHECK (run_on_worktree_creation IN (0, 1));
ALTER TABLE sessions ADD COLUMN startup_cue_json TEXT NOT NULL DEFAULT '';
