-- +goose Up
-- 旧记录保持空归属，恢复时闭锁，不能从可替换的 Worktree 推断授权仓库。
ALTER TABLE session_worktrees ADD COLUMN project_id TEXT NOT NULL DEFAULT '';
ALTER TABLE session_worktrees ADD COLUMN repo_path TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE session_worktrees DROP COLUMN repo_path;
ALTER TABLE session_worktrees DROP COLUMN project_id;
