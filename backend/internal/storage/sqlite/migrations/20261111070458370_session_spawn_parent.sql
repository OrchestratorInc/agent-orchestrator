-- +goose Up
-- 保留实际创建者；父会话删除后由读取校验拒绝归属，不改写历史创建事实。
ALTER TABLE sessions ADD COLUMN spawn_parent_session_id TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE sessions DROP COLUMN spawn_parent_session_id;
