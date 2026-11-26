-- +goose Up

-- Model catalogs are device-wide: one row per agent under the empty scope, plus
-- cloud credential scopes ('@cred:<type>'). Rows older builds wrote per project,
-- and their retired Settings browse scope, are never read again.
DELETE FROM agent_model_catalog
WHERE project_id <> '' AND project_id NOT LIKE '@cred:%';

-- +goose Down
-- The deleted rows were a cache; older builds rediscover them. Forward-only.
