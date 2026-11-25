-- A PR only stacks on another PR whose head is a private branch in the same
-- repository. Record when a head is not: it lives in a fork, so its branch name
-- says nothing about this repository's branches, or it is the repository's
-- default branch, as in a main -> release sync PR.

-- +goose Up
ALTER TABLE pr ADD COLUMN head_from_fork BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE pr ADD COLUMN head_is_default_branch BOOLEAN NOT NULL DEFAULT FALSE;

-- +goose Down
ALTER TABLE pr DROP COLUMN head_is_default_branch;
ALTER TABLE pr DROP COLUMN head_from_fork;
