-- +goose Up

-- Claim publication before the external GitHub POST. An interrupted claim is
-- reconciled against the review marker and is never blindly posted again.
ALTER TABLE ao_review_runs
    ADD COLUMN IF NOT EXISTS publish_state TEXT NOT NULL DEFAULT 'pending'
        CHECK (publish_state IN ('pending', 'publishing', 'uncertain', 'published')),
    ADD COLUMN IF NOT EXISTS publish_verdict TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS publish_body TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS publish_started_at TIMESTAMPTZ;

-- +goose Down

ALTER TABLE ao_review_runs
    DROP COLUMN IF EXISTS publish_started_at,
    DROP COLUMN IF EXISTS publish_body,
    DROP COLUMN IF EXISTS publish_verdict,
    DROP COLUMN IF EXISTS publish_state;
