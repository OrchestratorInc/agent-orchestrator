-- +goose Up
-- +goose StatementBegin
-- pr_details holds the slow-changing, view-only part of a pull request: its
-- description, labels, requested reviewers and commits. It lives beside pr, not
-- in it, so the many pr reads stay as narrow as they are; it is written in the
-- same transaction as the pr row and cascades from it.
CREATE TABLE pr_details (
    pr_url                TEXT PRIMARY KEY REFERENCES pr (url) ON DELETE CASCADE,
    description           TEXT NOT NULL DEFAULT '',
    description_truncated INTEGER NOT NULL DEFAULT 0,
    labels                TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(labels)),
    requested_reviewers   TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(requested_reviewers)),
    commits               TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(commits)),
    commit_count          INTEGER NOT NULL DEFAULT 0,
    observed_at           TIMESTAMP NOT NULL
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS pr_details;
-- +goose StatementEnd
