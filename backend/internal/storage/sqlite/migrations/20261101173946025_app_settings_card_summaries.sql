-- +goose Up
-- Off by default: when on, new workers are asked to keep a one-line summary on
-- their kanban card with `ao summary`, and the board shows it. The desktop app
-- turns it on with Developer mode.
ALTER TABLE app_settings ADD COLUMN card_summaries INTEGER NOT NULL DEFAULT 0
    CHECK (card_summaries IN (0, 1));

-- +goose Down
ALTER TABLE app_settings DROP COLUMN card_summaries;
