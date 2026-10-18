-- Summary: record which lines of each instructions revision the user typed or edited, so dreaming may rewrite its own lines but never the user's.
-- +goose Up
-- Newline-separated. A dream's revision carries the user's lines forward unchanged.
ALTER TABLE instruction_revisions ADD COLUMN user_lines TEXT NOT NULL DEFAULT '';
UPDATE instruction_revisions SET user_lines = body WHERE dreamed = 0;

-- +goose Down
ALTER TABLE instruction_revisions DROP COLUMN user_lines;
