-- The process-footprint feature (status bar, orphan kill, stop-idle, `ao ps`)
-- is back from its park. The runtime gate is the AO_NO_PROCINVENTORY
-- environment variable (#93 convention: behavioral flags are env vars, not UI
-- settings), read by the daemon at startup. This column restores the feature's
-- schema footprint so databases from the parked line — which applied it as
-- fork migration 9002, later renumbered 9003, both numbers burned since — and
-- fresh databases converge on one shape. Default 1 keeps the feature on,
-- matching the env default (nothing to hide without AO_NO_PROCINVENTORY).
-- +goose Up
-- +goose StatementBegin
ALTER TABLE app_settings
    ADD COLUMN process_inventory INTEGER NOT NULL DEFAULT 1
        CHECK (process_inventory IN (0, 1));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE app_settings DROP COLUMN process_inventory;
-- +goose StatementEnd
