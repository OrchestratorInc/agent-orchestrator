-- +goose Up
-- Extends the usage measurements from 20261026185532680 without changing what
-- their existing counts mean.

-- pending_count stays "direct human Chat messages". These count the human
-- sends it does not see (steers into a running turn, and `/send`: `ao send`
-- outside a session or a TUI session's composer) and split every counted
-- message by the client it came from.
ALTER TABLE usage_message_summary ADD COLUMN steer_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE usage_message_summary ADD COLUMN send_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE usage_message_summary ADD COLUMN desktop_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE usage_message_summary ADD COLUMN mobile_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE usage_message_summary ADD COLUMN cli_count INTEGER NOT NULL DEFAULT 0;

-- A worker PR closed without merging is reported once, like a merge.
-- merged_via records the client when AO itself performed the merge; empty
-- means the merge was only observed (made on the provider or elsewhere).
ALTER TABLE usage_pr_measurement ADD COLUMN closed_reported INTEGER NOT NULL DEFAULT 0;
ALTER TABLE usage_pr_measurement ADD COLUMN merged_via TEXT NOT NULL DEFAULT '';

-- PRs already closed when this ships are history, not new outcomes.
UPDATE usage_pr_measurement SET closed_reported = 1 WHERE pr_key IN (
    SELECT CASE WHEN provider_id <> '' AND provider <> ''
        THEN provider || ':' || host || ':' || provider_id ELSE url END
    FROM pr WHERE pr_state = 'closed'
);

-- +goose Down
ALTER TABLE usage_pr_measurement DROP COLUMN merged_via;
ALTER TABLE usage_pr_measurement DROP COLUMN closed_reported;
ALTER TABLE usage_message_summary DROP COLUMN cli_count;
ALTER TABLE usage_message_summary DROP COLUMN mobile_count;
ALTER TABLE usage_message_summary DROP COLUMN desktop_count;
ALTER TABLE usage_message_summary DROP COLUMN send_count;
ALTER TABLE usage_message_summary DROP COLUMN steer_count;
