-- The SCM proxy setting (Settings, General): outbound proxy URL for the
-- daemon's GitHub/GitLab clients. Desktop daemons spawned from Dock/Finder
-- never inherit shell proxy env (#110), so the persisted preference — not the
-- launch environment — has to be the source of truth. Empty means "no explicit
-- proxy": the clients keep the standard ProxyFromEnvironment behavior.
-- +goose Up
ALTER TABLE app_settings ADD COLUMN scm_proxy_url TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE app_settings DROP COLUMN scm_proxy_url;
