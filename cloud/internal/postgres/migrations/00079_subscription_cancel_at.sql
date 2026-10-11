-- +goose Up

-- When a subscription set to cancel (from the Customer Portal) ends, so the
-- desktop app can say when the organization drops to Free. NULL while the
-- subscription renews.
ALTER TABLE ao_organizations ADD COLUMN subscription_cancel_at TIMESTAMPTZ;

-- +goose Down
ALTER TABLE ao_organizations DROP COLUMN subscription_cancel_at;
