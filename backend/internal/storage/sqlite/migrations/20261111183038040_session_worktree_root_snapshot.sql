-- +goose Up

-- session_worktrees.worktree_root covers workspace projects, whose every repo
-- has a row. Single-repo sessions never write those rows, so their worktree root
-- is snapshotted here instead: restore and teardown validate the recorded path
-- against the root it was created under, not the project's current config.
-- Rows written before this column existed carry '', which readers resolve to the
-- installation default (<AO_DATA_DIR>/worktrees).
ALTER TABLE sessions ADD COLUMN worktree_root TEXT NOT NULL DEFAULT '';

-- +goose Down

ALTER TABLE sessions DROP COLUMN worktree_root;
