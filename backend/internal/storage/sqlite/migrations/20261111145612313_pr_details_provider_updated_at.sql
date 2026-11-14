-- +goose Up
-- The provider's updatedAt of the PR state a pr_details row was read from. The
-- observer reads details again only once the PR's updatedAt differs from it,
-- so an unchanged PR costs the light batch query. Rows from before this column
-- stay NULL, which reads as unknown and gets them read again once.
ALTER TABLE pr_details ADD COLUMN provider_updated_at TIMESTAMP;

-- +goose Down
ALTER TABLE pr_details DROP COLUMN provider_updated_at;
