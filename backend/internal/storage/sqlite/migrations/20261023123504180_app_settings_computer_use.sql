-- +goose Up
-- Off by default: when on, new chat sessions get tools to drive desktop apps
-- through Cua Driver (Settings > Computer use agents).
ALTER TABLE app_settings ADD COLUMN computer_use INTEGER NOT NULL DEFAULT 0
    CHECK (computer_use IN (0, 1));

-- +goose Down
ALTER TABLE app_settings DROP COLUMN computer_use;
