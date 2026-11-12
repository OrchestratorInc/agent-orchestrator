-- name: DeleteWorkspaceReposByProject :exec
DELETE FROM workspace_repos WHERE project_id = ?;

-- name: UpsertWorkspaceRepo :exec
INSERT INTO workspace_repos (project_id, name, relative_path, repo_origin_url, default_branch, registered_at, git_status)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (project_id, name) DO UPDATE SET
    relative_path = excluded.relative_path,
    repo_origin_url = excluded.repo_origin_url,
    default_branch = excluded.default_branch,
    registered_at = excluded.registered_at,
    git_status = excluded.git_status;

-- name: ListWorkspaceRepos :many
SELECT project_id, name, relative_path, repo_origin_url, default_branch, registered_at, git_status
FROM workspace_repos
WHERE project_id = ?
ORDER BY name;

-- name: UpsertSessionWorktree :exec
INSERT INTO session_worktrees (session_id, repo_name, branch, base_sha, base_ref, creation_sha, worktree_path, preserved_ref, state, project_id, repo_path, worktree_root)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (session_id, repo_name) DO UPDATE SET
    branch = excluded.branch,
    base_sha = excluded.base_sha,
    base_ref = excluded.base_ref,
    creation_sha = excluded.creation_sha,
    worktree_path = excluded.worktree_path,
    preserved_ref = excluded.preserved_ref,
    state = excluded.state,
    project_id = CASE WHEN excluded.project_id = '' THEN session_worktrees.project_id ELSE excluded.project_id END,
    repo_path = CASE WHEN excluded.repo_path = '' THEN session_worktrees.repo_path ELSE excluded.repo_path END,
    -- The snapshot records where a worktree was actually created. A later upsert
    -- that cannot name the root must not erase the snapshot restore depends on.
    worktree_root = CASE WHEN excluded.worktree_root = '' THEN session_worktrees.worktree_root ELSE excluded.worktree_root END;

-- name: GetSessionWorktree :one
SELECT session_id, repo_name, branch, base_sha, worktree_path, preserved_ref, state, base_ref, creation_sha, project_id, repo_path, worktree_root
FROM session_worktrees
WHERE session_id = ? AND repo_name = ?;

-- name: ListSessionWorktrees :many
SELECT session_id, repo_name, branch, base_sha, worktree_path, preserved_ref, state, base_ref, creation_sha, project_id, repo_path, worktree_root
FROM session_worktrees
WHERE session_id = ?
ORDER BY CASE WHEN repo_name = '__root__' THEN 0 ELSE 1 END, repo_name;

-- name: DeleteSessionWorktrees :exec
DELETE FROM session_worktrees WHERE session_id = ?;

-- name: SetSessionWorktreeBranch :exec
-- A task's background branch rename records the branch each repository holds.
UPDATE session_worktrees SET branch = sqlc.arg(branch)
WHERE session_id = sqlc.arg(session_id) AND repo_name = sqlc.arg(repo_name);
