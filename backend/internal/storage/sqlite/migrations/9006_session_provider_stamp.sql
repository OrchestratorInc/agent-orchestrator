-- Issue #6096: provider stamp for running sessions. Records the gateway
-- identity (base URL + model) the session's agent process was launched with,
-- so a later gateway switch can detect which running sessions would relaunch
-- on a different provider. Internal lifecycle facts, not user-visible state.

-- +goose Up
ALTER TABLE sessions ADD COLUMN provider_base_url TEXT NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN provider_model TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE sessions DROP COLUMN provider_model;
ALTER TABLE sessions DROP COLUMN provider_base_url;
