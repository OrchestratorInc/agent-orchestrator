package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/jackc/pgx/v5"
)

const artifactShareColumns = `id::text, user_id::text, slug, name, content_type,
	size, sha256, s3_key, version_id, created_at, updated_at`

// CountArtifactShares returns how many active share links the user holds.
func (s *Store) CountArtifactShares(ctx context.Context, userID string) (int, error) {
	var count int
	err := s.withUser(ctx, userID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT count(*) FROM ao_artifact_shares WHERE user_id = $1`, userID,
		).Scan(&count)
	})
	return count, err
}

// CreateArtifactShare inserts a new share link for share.UserID under the
// first free slug of baseSlug, baseSlug-2, baseSlug-3, and so on. It returns
// ErrArtifactShareLimit, and inserts nothing, when the user already holds
// limit links. A per-user advisory lock makes the count and the insert one
// step, so concurrent requests cannot pass the limit.
func (s *Store) CreateArtifactShare(
	ctx context.Context,
	share domain.ArtifactShare,
	baseSlug string,
	limit int,
) (domain.ArtifactShare, error) {
	var created domain.ArtifactShare
	err := s.withUser(ctx, share.UserID, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`SELECT pg_advisory_xact_lock(hashtextextended('ao_artifact_shares:' || $1, 0))`,
			share.UserID,
		); err != nil {
			return fmt.Errorf("lock artifact shares: %w", err)
		}
		var count int
		if err := tx.QueryRow(ctx,
			`SELECT count(*) FROM ao_artifact_shares WHERE user_id = $1`, share.UserID,
		).Scan(&count); err != nil {
			return fmt.Errorf("count artifact shares: %w", err)
		}
		if count >= limit {
			return ErrArtifactShareLimit
		}
		for attempt := 1; attempt <= domain.MaxArtifactShareSlugAttempts; attempt++ {
			rows, err := tx.Query(ctx,
				`INSERT INTO ao_artifact_shares (
					id, user_id, slug, name, content_type, size, sha256, s3_key, version_id
				) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
				ON CONFLICT (slug) DO NOTHING
				RETURNING `+artifactShareColumns,
				share.ID, share.UserID, domain.ArtifactShareSlugCandidate(baseSlug, attempt),
				share.Name, share.ContentType, share.Size, share.SHA256, share.S3Key, share.VersionID,
			)
			if err != nil {
				return fmt.Errorf("insert artifact share: %w", normalizeConstraintError(err))
			}
			created, err = pgx.CollectExactlyOneRow(rows, scanArtifactShare)
			if errors.Is(err, pgx.ErrNoRows) {
				continue
			}
			if err != nil {
				return fmt.Errorf("insert artifact share: %w", normalizeConstraintError(err))
			}
			return nil
		}
		return ErrConflict
	})
	return created, err
}

// UpdateArtifactShare points one of the user's share links at a new object
// version and returns the updated row and the version it replaced. A link
// the user does not own is ErrNotFound.
func (s *Store) UpdateArtifactShare(
	ctx context.Context,
	share domain.ArtifactShare,
) (domain.ArtifactShare, string, error) {
	var updated domain.ArtifactShare
	var previousVersion string
	err := s.withUser(ctx, share.UserID, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx,
			`SELECT version_id FROM ao_artifact_shares WHERE id = $1 AND user_id = $2 FOR UPDATE`,
			share.ID, share.UserID,
		).Scan(&previousVersion); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("lock artifact share: %w", err)
		}
		rows, err := tx.Query(ctx,
			`UPDATE ao_artifact_shares
			SET name = $3, content_type = $4, size = $5, sha256 = $6,
				version_id = $7, updated_at = now()
			WHERE id = $1 AND user_id = $2
			RETURNING `+artifactShareColumns,
			share.ID, share.UserID, share.Name, share.ContentType, share.Size,
			share.SHA256, share.VersionID,
		)
		if err != nil {
			return fmt.Errorf("update artifact share: %w", normalizeConstraintError(err))
		}
		updated, err = pgx.CollectExactlyOneRow(rows, scanArtifactShare)
		return err
	})
	return updated, previousVersion, err
}

// GetArtifactShare returns one of the user's share links, or ErrNotFound.
func (s *Store) GetArtifactShare(ctx context.Context, userID, shareID string) (domain.ArtifactShare, error) {
	var share domain.ArtifactShare
	err := s.withUser(ctx, userID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT `+artifactShareColumns+` FROM ao_artifact_shares WHERE id = $1 AND user_id = $2`,
			shareID, userID,
		)
		if err != nil {
			return fmt.Errorf("get artifact share: %w", err)
		}
		share, err = pgx.CollectExactlyOneRow(rows, scanArtifactShare)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	})
	return share, err
}

// ListArtifactShares returns the user's share links, newest first.
func (s *Store) ListArtifactShares(ctx context.Context, userID string) ([]domain.ArtifactShare, error) {
	var shares []domain.ArtifactShare
	err := s.withUser(ctx, userID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT `+artifactShareColumns+` FROM ao_artifact_shares
			WHERE user_id = $1 ORDER BY created_at DESC, id`,
			userID,
		)
		if err != nil {
			return fmt.Errorf("list artifact shares: %w", err)
		}
		shares, err = pgx.CollectRows(rows, scanArtifactShare)
		return err
	})
	return shares, err
}

// DeleteArtifactShare removes one of the user's share links and returns the
// deleted row, or ErrNotFound.
func (s *Store) DeleteArtifactShare(ctx context.Context, userID, shareID string) (domain.ArtifactShare, error) {
	var deleted domain.ArtifactShare
	err := s.withUser(ctx, userID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`DELETE FROM ao_artifact_shares WHERE id = $1 AND user_id = $2
			RETURNING `+artifactShareColumns,
			shareID, userID,
		)
		if err != nil {
			return fmt.Errorf("delete artifact share: %w", err)
		}
		deleted, err = pgx.CollectExactlyOneRow(rows, scanArtifactShare)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	})
	return deleted, err
}

// ArtifactShareBySlug returns the share link a public subdomain names, or
// ErrNotFound.
func (s *Store) ArtifactShareBySlug(ctx context.Context, slug string) (domain.ArtifactShare, error) {
	var share domain.ArtifactShare
	err := s.withService(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT `+artifactShareColumns+` FROM ao_artifact_shares WHERE slug = $1`, slug,
		)
		if err != nil {
			return fmt.Errorf("get artifact share by slug: %w", err)
		}
		share, err = pgx.CollectExactlyOneRow(rows, scanArtifactShare)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	})
	return share, err
}

func scanArtifactShare(row pgx.CollectableRow) (domain.ArtifactShare, error) {
	var share domain.ArtifactShare
	err := row.Scan(
		&share.ID, &share.UserID, &share.Slug, &share.Name, &share.ContentType,
		&share.Size, &share.SHA256, &share.S3Key, &share.VersionID,
		&share.CreatedAt, &share.UpdatedAt,
	)
	return share, err
}
