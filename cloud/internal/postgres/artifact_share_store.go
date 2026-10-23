package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/jackc/pgx/v5"
)

const artifactShareColumns = `id::text, user_id::text, slug, name, content_type,
	size, sha256, s3_key, version_id, created_at, updated_at, revoked_at`

// ArtifactSharePublishAllowed reports whether the user can publish the
// document: it already has a link, which publishing replaces (or restores,
// when revoked), or the user has used fewer than limit link slots. Revoked
// links keep their slot. PublishArtifactShare enforces the same rule
// exactly; this check only avoids storing an object that would be refused.
func (s *Store) ArtifactSharePublishAllowed(ctx context.Context, userID, documentKey string, limit int) (bool, error) {
	var allowed bool
	err := s.withUser(ctx, userID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT EXISTS (
				SELECT 1 FROM ao_artifact_shares WHERE user_id = $1 AND document_key = $2
			) OR (SELECT count(*) FROM ao_artifact_shares WHERE user_id = $1) < $3`,
			userID, documentKey, limit,
		).Scan(&allowed)
	})
	return allowed, err
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

// PublishArtifactShare publishes share.UserID's document. When the user
// already has a link for documentKey, that link now serves the new version,
// which keeps its URL and slot, and replaced is true; a revoked link is
// restored, and the version an active link served is queued for the sweeper.
// Otherwise it inserts a new link under the first free slug of baseSlug,
// baseSlug-2, baseSlug-3, and so on, returning ErrArtifactShareLimit, and
// inserting nothing, when the user has used limit link slots. Revoked links
// keep their slot. Either way the link claims its upload, recorded by
// RecordArtifactShareUpload, from the sweeper's queue. A per-user advisory
// lock makes the lookup, the count and the write one step, so concurrent
// requests can neither pass the limit nor make two links for one document.
func (s *Store) PublishArtifactShare(
	ctx context.Context,
	share domain.ArtifactShare,
	baseSlug, documentKey string,
	limit int,
) (published domain.ArtifactShare, replaced bool, err error) {
	err = s.withUser(ctx, share.UserID, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`SELECT pg_advisory_xact_lock(hashtextextended('ao_artifact_shares:' || $1, 0))`,
			share.UserID,
		); err != nil {
			return fmt.Errorf("lock artifact shares: %w", err)
		}
		var existingID, previousKey, previousVersion string
		var previousSize int64
		var wasRevoked bool
		// FOR UPDATE: the advisory lock serializes publishes only, so this also
		// waits for a PUT or revoke on the row and reads the version it left.
		err := tx.QueryRow(ctx,
			`SELECT id::text, s3_key, version_id, size, revoked_at IS NOT NULL FROM ao_artifact_shares
			WHERE user_id = $1 AND document_key = $2 FOR UPDATE`,
			share.UserID, documentKey,
		).Scan(&existingID, &previousKey, &previousVersion, &previousSize, &wasRevoked)
		switch {
		case err == nil:
			replaced = true
			if err := claimShareUploadTx(ctx, tx, share.UserID, share.S3Key, share.VersionID); err != nil {
				return err
			}
			rows, err := tx.Query(ctx,
				`UPDATE ao_artifact_shares
				SET content_type = $3, size = $4, sha256 = $5, s3_key = $6,
					version_id = $7, updated_at = now(), revoked_at = NULL
				WHERE id = $1 AND user_id = $2
				RETURNING `+artifactShareColumns,
				existingID, share.UserID, share.ContentType, share.Size, share.SHA256,
				share.S3Key, share.VersionID,
			)
			if err != nil {
				return fmt.Errorf("replace artifact share version: %w", normalizeConstraintError(err))
			}
			published, err = pgx.CollectExactlyOneRow(rows, scanArtifactShare)
			if err != nil || wasRevoked {
				// Revoking already queued the version a revoked link served.
				return err
			}
			return queueReplacedShareVersionTx(ctx, tx, share.UserID, previousKey, previousVersion, previousSize)
		case !errors.Is(err, pgx.ErrNoRows):
			return fmt.Errorf("find published artifact document: %w", err)
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
					id, user_id, slug, name, document_key, content_type, size, sha256, s3_key, version_id
				) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
				ON CONFLICT (slug) DO NOTHING
				RETURNING `+artifactShareColumns,
				share.ID, share.UserID, domain.ArtifactShareSlugCandidate(baseSlug, attempt),
				share.Name, documentKey, share.ContentType, share.Size, share.SHA256, share.S3Key, share.VersionID,
			)
			if err != nil {
				return fmt.Errorf("insert artifact share: %w", normalizeConstraintError(err))
			}
			published, err = pgx.CollectExactlyOneRow(rows, scanArtifactShare)
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
	return published, replaced, err
}

// UpdateArtifactShare points one of the user's share links at a new object
// version recorded by RecordArtifactShareUpload, and queues the version it
// replaced for the sweeper. The file name must stay the same, because it is
// part of the link's document key; another name is
// ErrArtifactShareNameChanged. A link the user does not own is ErrNotFound.
func (s *Store) UpdateArtifactShare(ctx context.Context, share domain.ArtifactShare) (domain.ArtifactShare, error) {
	var updated domain.ArtifactShare
	err := s.withUser(ctx, share.UserID, func(tx pgx.Tx) error {
		var name, s3Key, previousVersion string
		var previousSize int64
		if err := tx.QueryRow(ctx,
			`SELECT name, s3_key, version_id, size FROM ao_artifact_shares
			WHERE id = $1 AND user_id = $2 AND revoked_at IS NULL FOR UPDATE`,
			share.ID, share.UserID,
		).Scan(&name, &s3Key, &previousVersion, &previousSize); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("lock artifact share: %w", err)
		}
		if share.Name != name {
			return ErrArtifactShareNameChanged
		}
		if err := claimShareUploadTx(ctx, tx, share.UserID, s3Key, share.VersionID); err != nil {
			return err
		}
		rows, err := tx.Query(ctx,
			`UPDATE ao_artifact_shares
			SET content_type = $3, size = $4, sha256 = $5,
				version_id = $6, updated_at = now()
			WHERE id = $1 AND user_id = $2
			RETURNING `+artifactShareColumns,
			share.ID, share.UserID, share.ContentType, share.Size,
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

// GetArtifactShare returns one of the user's active share links, or
// ErrNotFound.
func (s *Store) GetArtifactShare(ctx context.Context, userID, shareID string) (domain.ArtifactShare, error) {
	var share domain.ArtifactShare
	err := s.withUser(ctx, userID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT `+artifactShareColumns+` FROM ao_artifact_shares
			WHERE id = $1 AND user_id = $2 AND revoked_at IS NULL`,
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

// ListArtifactShares returns the user's share links, revoked ones included
// because they still use a slot, newest first.
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

// RevokeArtifactShare stops one of the user's active links serving and queues
// the version it served for the sweeper. The row stays, so the link keeps
// using its slot. A link the user does not own, or already revoked, is
// ErrNotFound.
func (s *Store) RevokeArtifactShare(ctx context.Context, userID, shareID string) (domain.ArtifactShare, error) {
	var revoked domain.ArtifactShare
	err := s.withUser(ctx, userID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`UPDATE ao_artifact_shares SET revoked_at = now(), updated_at = now()
			WHERE id = $1 AND user_id = $2 AND revoked_at IS NULL
			RETURNING `+artifactShareColumns,
			shareID, userID,
		)
		if err != nil {
			return fmt.Errorf("revoke artifact share: %w", err)
		}
		revoked, err = pgx.CollectExactlyOneRow(rows, scanArtifactShare)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		return queueReplacedShareVersionTx(ctx, tx, userID, revoked.S3Key, revoked.VersionID, revoked.Size)
	})
	return revoked, err
}

// ArtifactShareBySlug returns the active share link a public subdomain names,
// or ErrNotFound.
func (s *Store) ArtifactShareBySlug(ctx context.Context, slug string) (domain.ArtifactShare, error) {
	var share domain.ArtifactShare
	err := s.withService(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT `+artifactShareColumns+` FROM ao_artifact_shares
			WHERE slug = $1 AND revoked_at IS NULL`, slug,
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
		&share.CreatedAt, &share.UpdatedAt, &share.RevokedAt,
	)
	return share, err
}
