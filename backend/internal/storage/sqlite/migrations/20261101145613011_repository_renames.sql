-- +goose Up
-- A renamed or transferred repository's old name and the provider's current
-- name for it, learned from SCM PR listings. Names are lowercase. A PR link
-- built from the old name then matches the PR tracked under the current one.
CREATE TABLE repository_renames (
    host     TEXT NOT NULL,
    old_name TEXT NOT NULL,
    name     TEXT NOT NULL,
    PRIMARY KEY (host, old_name),
    CHECK (old_name <> name)
);

-- +goose Down
DROP TABLE repository_renames;
