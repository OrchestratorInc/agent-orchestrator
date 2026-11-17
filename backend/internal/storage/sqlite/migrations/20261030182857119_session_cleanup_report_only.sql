-- +goose Up
-- A first cleanup result recorded by `ao session cleanup` or the daemon's
-- background pass, for a session nobody archived in this generation, is
-- report-only: the CLI and API list its failure, but it does not raise the
-- desktop app's cleanup error. Every existing failure row came from an archive
-- or a retry of one.
ALTER TABLE session_cleanup_facts ADD COLUMN report_only INTEGER NOT NULL DEFAULT 0
    CHECK (report_only IN (0, 1));

-- +goose Down
ALTER TABLE session_cleanup_facts DROP COLUMN report_only;
