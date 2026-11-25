-- +goose Up
-- +goose StatementBegin
-- When an idle Claude Chat over 200K tokens compacts: when the next message
-- arrives after its prompt cache expired (onReturn, the default), shortly
-- before the cache expires (beforeCacheExpires, beta), or never (off).
ALTER TABLE app_settings ADD COLUMN idle_compaction TEXT NOT NULL DEFAULT 'onReturn'
    CHECK (idle_compaction IN ('onReturn', 'beforeCacheExpires', 'off'));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE app_settings DROP COLUMN idle_compaction;
-- +goose StatementEnd
