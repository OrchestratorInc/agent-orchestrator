-- +goose Up
CREATE TABLE task_preparation_skipped_children (
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    repo_name TEXT NOT NULL,
    worktree_path TEXT NOT NULL,
    PRIMARY KEY (session_id, repo_name)
);

-- +goose StatementBegin
CREATE TRIGGER clear_promoted_preparation_skips AFTER UPDATE OF is_task_preparation ON sessions
WHEN OLD.is_task_preparation = 1 AND NEW.is_task_preparation = 0
BEGIN
    DELETE FROM task_preparation_skipped_children WHERE session_id = NEW.id;
END;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER clear_promoted_preparation_skips;
DROP TABLE task_preparation_skipped_children;
