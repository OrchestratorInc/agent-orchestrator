-- +goose Up
CREATE INDEX idx_deleted_session_account_usage_day ON deleted_session_account_usage(day, account_id, model_id);

-- +goose Down
DROP INDEX IF EXISTS idx_deleted_session_account_usage_day;
