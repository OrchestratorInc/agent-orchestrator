-- name: LoadProviderAccountState :one
SELECT revision, facts, pending FROM provider_account_state WHERE id = 1;
-- name: SaveProviderAccountIntent :execrows
UPDATE provider_account_state SET pending = ? WHERE id = 1 AND revision = ? AND pending IS NULL;
-- name: CommitProviderAccountIntent :execrows
UPDATE provider_account_state SET facts = json_extract(pending,'$.next'), revision = json_extract(pending,'$.next.revision')
WHERE id = 1 AND revision = ? AND pending IS NOT NULL;
-- name: FinishProviderAccountIntent :execrows
UPDATE provider_account_state SET pending = NULL WHERE id = 1 AND revision = ? AND pending IS NOT NULL;
