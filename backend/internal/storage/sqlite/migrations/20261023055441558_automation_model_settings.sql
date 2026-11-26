-- +goose Up
ALTER TABLE automations ADD COLUMN model TEXT NOT NULL DEFAULT '';
ALTER TABLE automations ADD COLUMN mode TEXT NOT NULL DEFAULT '';
ALTER TABLE automations ADD COLUMN effort TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE automations DROP COLUMN effort;
ALTER TABLE automations DROP COLUMN mode;
ALTER TABLE automations DROP COLUMN model;
