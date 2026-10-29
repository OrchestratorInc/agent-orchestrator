-- name: UpsertPRDetails :exec
INSERT INTO pr_details (pr_url, description, description_truncated, labels, requested_reviewers, commits, commit_count, observed_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (pr_url) DO UPDATE SET
    description = excluded.description,
    description_truncated = excluded.description_truncated,
    labels = excluded.labels,
    requested_reviewers = excluded.requested_reviewers,
    commits = excluded.commits,
    commit_count = excluded.commit_count,
    observed_at = excluded.observed_at;

-- name: GetPRDetails :one
SELECT pr_url, description, description_truncated, labels, requested_reviewers, commits, commit_count, observed_at
FROM pr_details WHERE pr_url = ?;
