-- +goose NO TRANSACTION
-- +goose Up
-- ClaimGitHubWebhook ran two full scans of ao_github_webhook_deliveries per
-- claim: its OR'd 'processing' branch kept the planner off the pending/retry
-- index, and the installation-order check scanned the table once per waiting
-- delivery. Eight workers claiming every second saturated the production
-- database. Both indexes hold only in-flight rows, so they stay tiny as the
-- table grows. Built concurrently so writes keep flowing; production already
-- has them (created by hand on 2026-10-23), so IF NOT EXISTS is a no-op there.
CREATE INDEX CONCURRENTLY IF NOT EXISTS ao_github_webhook_install_order_idx
    ON ao_github_webhook_deliveries (github_installation_id, received_at, github_delivery_id)
    WHERE event IN ('installation', 'installation_repositories')
      AND status IN ('pending', 'retry', 'processing');
CREATE INDEX CONCURRENTLY IF NOT EXISTS ao_github_webhook_processing_idx
    ON ao_github_webhook_deliveries (lease_until)
    WHERE status = 'processing';
-- Issuing a worker bootstrap ticket invalidates the session's earlier ones, and
-- a retried bootstrap checks for a newer ticket: both looked tickets up by
-- session with a full scan of ao_access_tickets, which is never purged.
CREATE INDEX CONCURRENTLY IF NOT EXISTS ao_access_tickets_session_purpose_idx
    ON ao_access_tickets (org_id, session_id, purpose);

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS ao_access_tickets_session_purpose_idx;
DROP INDEX CONCURRENTLY IF EXISTS ao_github_webhook_processing_idx;
DROP INDEX CONCURRENTLY IF EXISTS ao_github_webhook_install_order_idx;
