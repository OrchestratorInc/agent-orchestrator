-- +goose Up
ALTER TABLE automations ADD COLUMN template_id TEXT NOT NULL DEFAULT 'custom'
    CHECK (template_id IN ('custom', 'daily_pr_review_v1', 'daily_issue_triage_v1', 'weekly_release_notes_v1'));
ALTER TABLE automations ADD COLUMN use_case TEXT NOT NULL DEFAULT 'unknown'
    CHECK (use_case IN ('pr_review', 'issue_triage', 'repo_digest', 'ci_triage', 'release_notes', 'security_audit', 'dependency_maintenance', 'docs_maintenance', 'other', 'unknown'));

-- +goose Down
ALTER TABLE automations DROP COLUMN use_case;
ALTER TABLE automations DROP COLUMN template_id;
