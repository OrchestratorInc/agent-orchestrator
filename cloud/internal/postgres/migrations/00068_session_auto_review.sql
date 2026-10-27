-- +goose Up

ALTER TABLE ao_sessions
	ADD COLUMN IF NOT EXISTS auto_review_enabled BOOLEAN NOT NULL DEFAULT false;

-- +goose Down

ALTER TABLE ao_sessions DROP COLUMN IF EXISTS auto_review_enabled;
