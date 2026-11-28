-- +goose Up
-- Off by default: sessions get the user's skills their agent does not load itself.
ALTER TABLE app_settings ADD COLUMN skill_sharing INTEGER NOT NULL DEFAULT 0
    CHECK (skill_sharing IN (0, 1));

-- +goose Down
ALTER TABLE app_settings DROP COLUMN skill_sharing;
