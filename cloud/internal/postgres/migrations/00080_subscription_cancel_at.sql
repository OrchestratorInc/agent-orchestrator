-- +goose Up

-- When a subscription set to cancel (from the Customer Portal) ends, so the
-- desktop app can say when the organization drops to Free. NULL while the
-- subscription renews. Idempotent: this was first written as 00079, a number
-- 00079_activity_events.sql already holds.
ALTER TABLE ao_organizations ADD COLUMN IF NOT EXISTS subscription_cancel_at TIMESTAMPTZ;

-- +goose Down
ALTER TABLE ao_organizations DROP COLUMN IF EXISTS subscription_cancel_at;
