-- Summary: remove unused Cue descriptions while preserving executable definitions.
-- +goose Up
ALTER TABLE cues DROP COLUMN description;

-- +goose Down
ALTER TABLE cues ADD COLUMN description TEXT NOT NULL DEFAULT '';
