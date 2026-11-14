-- +goose Up
-- +goose StatementBegin
-- conversation holds a PR's newest top-level conversation comments (not review
-- thread comments, which live in pr_comment), as a capped JSON list written
-- with the rest of pr_details.
ALTER TABLE pr_details ADD COLUMN conversation TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(conversation));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE pr_details DROP COLUMN conversation;
-- +goose StatementEnd
