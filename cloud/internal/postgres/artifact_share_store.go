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

// RecordArtifactShareUpload queues an uploaded share object version that no
// share serves yet. The sweeper deletes it after ArtifactUploadTTL unless
// creating or updating a share claims it first.
func (s *Store) RecordArtifactShareUpload(ctx context.Context, userID, s3Key, versionID string, size int64) error {
	return s.withUser(ctx, userID, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`INSERT INTO ao_artifact_object_versions (
				user_id, s3_key, version_id, reason, size, collect_after
			) VALUES ($1, $2, $3, 'upload', $4, now() + $5::interval)
			ON CONFLICT (s3_key, version_id) DO NOTHING`,
			userID, s3Key, versionID, size, domain.ArtifactUploadTTL.String(),
		); err != nil {
			return fmt.Errorf("record artifact share upload: %w", normalizeConstraintError(err))
		}
		return nil
	})
}

// DiscardArtifactShareUpload makes an unclaimed share upload due for the
// sweeper now.
func (s *Store) DiscardArtifactShareUpload(ctx context.Context, userID, s3Key, versionID string) error {
	return s.withUser(ctx, userID, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`UPDATE ao_artifact_object_versions SET collect_after = now()
			WHERE user_id = $1 AND s3_key = $2 AND version_id = $3 AND reason = 'upload'`,
			userID, s3Key, versionID,
		); err != nil {
			return fmt.Errorf("discard artifact share upload: %w", err)
		}
		return nil
	})
}

// claimShareUploadTx removes a recorded upload from the sweeper's queue once
// a share serves it. It returns ErrArtifactVersionUnknown when the upload is
// not queued: never recorded, or already swept.
func claimShareUploadTx(ctx context.Context, tx pgx.Tx, userID, s3Key, versionID string) error {
	tag, err := tx.Exec(ctx,
		`DELETE FROM ao_artifact_object_versions
		WHERE user_id = $1 AND s3_key = $2 AND version_id = $3 AND reason = 'upload'`,
		userID, s3Key, versionID,
	)
	if err != nil {
		return fmt.Errorf("claim artifact share upload: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrArtifactVersionUnknown
	}
	return nil
}

// queueReplacedShareVersionTx queues a version no share serves any more. The
// sweeper deletes it after ArtifactReplacedGrace, so a read that started
// before the change can finish.
func queueReplacedShareVersionTx(ctx context.Context, tx pgx.Tx, userID, s3Key, versionID string, size int64) error {
	if _, err := tx.Exec(ctx,
		`INSERT INTO ao_artifact_object_versions (
			user_id, s3_key, version_id, reason, size, collect_after
		) VALUES ($1, $2, $3, 'replaced', $4, now() + $5::interval)
		ON CONFLICT (s3_key, version_id) DO NOTHING`,
		userID, s3Key, versionID, size, domain.ArtifactReplacedGrace.String(),
	); err != nil {
		return fmt.Errorf("queue replaced artifact share version: %w", err)
	}
	return nil
}

// CreateArtifactShare inserts a new share link for share.UserID under the
// first free slug of baseSlug, baseSlug-2, baseSlug-3, and so on. It returns
// ErrArtifactShareLimit, and inserts nothing, when the user already holds
// limit links. The share claims its upload, recorded by
// RecordArtifactShareUpload, from the sweeper's queue. A per-user advisory lock makes the count and the insert one
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
			return claimShareUploadTx(ctx, tx, share.UserID, share.S3Key, share.VersionID)
		}
		return ErrConflict
	})
	return created, err
}

// UpdateArtifactShare points one of the user's share links at a new object
// version recorded by RecordArtifactShareUpload, and queues the version it
// replaced for the sweeper. A link the user does not own is ErrNotFound.
func (s *Store) UpdateArtifactShare(ctx context.Context, share domain.ArtifactShare) (domain.ArtifactShare, error) {
	var updated domain.ArtifactShare
	err := s.withUser(ctx, share.UserID, func(tx pgx.Tx) error {
		var s3Key, previousVersion string
		var previousSize int64
		if err := tx.QueryRow(ctx,
			`SELECT s3_key, version_id, size FROM ao_artifact_shares
			WHERE id = $1 AND user_id = $2 FOR UPDATE`,
			share.ID, share.UserID,
		).Scan(&s3Key, &previousVersion, &previousSize); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("lock artifact share: %w", err)
		}
		if err := claimShareUploadTx(ctx, tx, share.UserID, s3Key, share.VersionID); err != nil {
			return err
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
		if err != nil {
			return err
		}
		if previousVersion == share.VersionID {
			return nil
		}
		return queueReplacedShareVersionTx(ctx, tx, share.UserID, s3Key, previousVersion, previousSize)
	})
	return updated, err
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

// DeleteArtifactShare removes one of the user's share links, queues the
// version it served for the sweeper, and returns the deleted row, or
// ErrNotFound.
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
		if err != nil {
			return err
		}
		return queueReplacedShareVersionTx(ctx, tx, userID, deleted.S3Key, deleted.VersionID, deleted.Size)
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
