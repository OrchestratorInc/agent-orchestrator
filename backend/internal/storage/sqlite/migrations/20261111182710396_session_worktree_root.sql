-- +goose Up

-- worktree_root snapshots the root each worktree was actually created under, so
-- restore, observation, cleanup, and PreRemove keep validating against the
-- directory that exists on disk after a project's worktreeRoot changes. Rows
-- written before this column existed carry '', which readers resolve to the
-- installation default (<AO_DATA_DIR>/worktrees).
ALTER TABLE session_worktrees ADD COLUMN worktree_root TEXT NOT NULL DEFAULT '';

-- +goose Down

ALTER TABLE session_worktrees DROP COLUMN worktree_root;
