-- +goose Up
-- +goose StatementBegin
-- Card summaries are a user preference: each one is a small model call, so it is
-- off until the user turns it on in Settings. Persisted daemon-side like every
-- preference so the desktop, mobile, and headless spawns agree.
ALTER TABLE app_settings
    ADD COLUMN card_summaries INTEGER NOT NULL DEFAULT 0
        CHECK (card_summaries IN (0, 1));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE app_settings DROP COLUMN card_summaries;
-- +goose StatementEnd
