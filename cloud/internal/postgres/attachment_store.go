package postgres

import (
	"context"
	"errors"
	"github.com/aoagents/agent-orchestrator/cloud/internal/attachments"
	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var ErrImageWorkerUpgrade = errors.New("worker must be upgraded for image attachments")

const attachmentColumns = `id, org_id, project_id, COALESCE(session_id::text,''), created_by_user_id, filename, size, mime_type, sha256, status`

func scanAttachment(row pgx.Row, a *domain.Attachment) error {
	return row.Scan(&a.ID, &a.OrgID, &a.ProjectID, &a.SessionID, &a.CreatorID, &a.Filename, &a.Size, &a.MIMEType, &a.SHA256, &a.Status)
}

func (s *Store) withAttachmentAccess(ctx context.Context, p domain.Principal, org, session string, write bool, fn tenantFn) error {
	if session == "" {
		return s.withTenant(ctx, p, org, fn)
	}
	return s.withSessionAccess(ctx, p, org, session, func(tx pgx.Tx, a sessionAccess) error {
		if write && (a.Role == "viewer" || a.ModeCap == "read-only" || len(a.DeniedCommands) > 0) {
			return ErrForbidden
		}
		return fn(tx)
	})
}
func (s *Store) PrepareAttachment(ctx context.Context, p domain.Principal, org, key string, input domain.PrepareAttachment) (domain.Attachment, error) {
	if attachments.ValidateMetadata(input.Metadata) != nil || key == "" {
		return domain.Attachment{}, ErrInvalid
	}
	var a domain.Attachment
	err := s.withAttachmentAccess(ctx, p, org, input.SessionID, true, func(tx pgx.Tx) error {
		if input.SessionID != "" {
			var project string
			if err := tx.QueryRow(ctx, `SELECT project_id FROM ao_sessions WHERE org_id=$1 AND id=$2 AND is_terminated=false`, org, input.SessionID).Scan(&project); err != nil {
				return ErrNotFound
			}
			if project != input.ProjectID {
				return ErrInvalid
			}
		}
		err := scanAttachment(tx.QueryRow(ctx, `INSERT INTO ao_attachments (org_id,project_id,session_id,created_by_user_id,idempotency_key,filename,size,mime_type,sha256) VALUES ($1,$2,NULLIF($3,'')::uuid,$4,$5,$6,$7,$8,$9) ON CONFLICT (org_id,created_by_user_id,idempotency_key) DO NOTHING RETURNING `+attachmentColumns, org, input.ProjectID, input.SessionID, p.UserID, key, input.Filename, input.Size, input.MIMEType, input.SHA256), &a)
		if errors.Is(err, pgx.ErrNoRows) {
			err = scanAttachment(tx.QueryRow(ctx, `SELECT `+attachmentColumns+` FROM ao_attachments WHERE org_id=$1 AND created_by_user_id=$2 AND idempotency_key=$3`, org, p.UserID, key), &a)
			if err == nil && (a.ProjectID != input.ProjectID || a.SessionID != input.SessionID || a.Filename != input.Filename || a.Size != input.Size || a.MIMEType != input.MIMEType || a.SHA256 != input.SHA256) {
				return ErrIdempotencyMismatch
			}
		}

		if err == nil {
			_, err = tx.Exec(ctx, `UPDATE ao_attachments SET upload_expires_at=now()+interval '10 minutes', upload_cleaned=false WHERE org_id=$1 AND id=$2`, org, a.ID)
		}
		return normalizeConstraintError(err)
	})
	return a, err
}

// Authorize through the bound session, or through the creator's project before
// binding. Looking up metadata never substitutes for checking session access.
func (s *Store) GetAttachment(ctx context.Context, p domain.Principal, org, id string) (domain.Attachment, error) {
	var a domain.Attachment
	err := s.withOrg(ctx, org, func(tx pgx.Tx) error {
		return scanAttachment(tx.QueryRow(ctx, `SELECT `+attachmentColumns+` FROM ao_attachments WHERE org_id=$1 AND id=$2 AND status <> 'expired' AND (expires_at>now() OR preparation_expires_at>now() OR retained)`, org, id), &a)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return a, ErrNotFound
	}
	if err != nil {
		return a, err
	}
	err = s.withAttachmentAccess(ctx, p, org, a.SessionID, false, func(tx pgx.Tx) error {
		if a.SessionID == "" && a.CreatorID != p.UserID {
			return ErrNotFound
		}
		return nil
	})
	if err == nil || a.SessionID == "" || !errors.Is(err, ErrForbidden) {
		return a, err
	}
	// Forwarded messages authorize readers through the destination session too.
	var sessions []string
	if err := s.withOrg(ctx, org, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT DISTINCT session_id FROM ao_message_attachments WHERE org_id=$1 AND attachment_id=$2`, org, id)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var session string
			if err := rows.Scan(&session); err != nil {
				return err
			}
			sessions = append(sessions, session)
		}
		return rows.Err()
	}); err != nil {
		return a, err
	}
	for _, session := range sessions {
		err = s.withAttachmentAccess(ctx, p, org, session, false, func(pgx.Tx) error { return nil })
		if err == nil || !errors.Is(err, ErrForbidden) {
			return a, err
		}
	}
	return a, ErrForbidden
}

// FinalizeAttachment holds the attachment row lock across the canonical write.
// Cleanup must acquire the same lock before it can expire or delete the object.
// Failed writes or commits leave metadata for a later retry or cleanup pass.
func (s *Store) FinalizeAttachment(ctx context.Context, p domain.Principal, org, id string, finalize func(context.Context, domain.Attachment) error) (domain.Attachment, error) {
	a, err := s.GetAttachment(ctx, p, org, id)
	if err != nil {
		return a, err
	}
	if a.CreatorID != p.UserID {
		return a, ErrForbidden
	}
	err = s.withAttachmentAccess(ctx, p, org, a.SessionID, true, func(tx pgx.Tx) error {
		if err := scanAttachment(tx.QueryRow(ctx, `SELECT `+attachmentColumns+` FROM ao_attachments WHERE org_id=$1 AND id=$2 AND status <> 'expired' AND (expires_at>now() OR preparation_expires_at>now() OR retained) FOR UPDATE`, org, id), &a); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		if a.Status == "ready" {
			return nil
		}
		if err := finalize(ctx, a); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE ao_attachments SET status='ready' WHERE org_id=$1 AND id=$2`, org, id)
		if err == nil {
			a.Status = "ready"
		}
		return err
	})
	return a, err
}

// source is supplied only after the caller authorizes the parent/child route.
// Forwarding adds a message reference and leaves the original session binding.
func attachmentMetadataTx(ctx context.Context, tx pgx.Tx, org, session string, ids []string, project, creator, source string) ([]attachments.Metadata, error) {
	if len(ids) > attachments.MaxCount {
		return nil, ErrInvalid
	}
	if len(ids) > 0 {
		var oldWorker bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM ao_worker_connections WHERE org_id=$1 AND session_id=$2 AND disconnected_at IS NULL AND NOT(capabilities ? 'attachments.images.v1'))`, org, session).Scan(&oldWorker); err != nil {
			return nil, err
		}
		if oldWorker {
			return nil, ErrImageWorkerUpgrade
		}
	}
	result := make([]attachments.Metadata, 0, len(ids))
	seen := map[string]bool{}
	var total int64
	for _, id := range ids {
		parsed, err := uuid.Parse(id)
		if err != nil {
			return nil, ErrInvalid
		}
		id = parsed.String()
		if seen[id] {
			return nil, ErrInvalid
		}
		seen[id] = true
		var a domain.Attachment
		err = scanAttachment(tx.QueryRow(ctx, `SELECT `+attachmentColumns+` FROM ao_attachments WHERE org_id=$1 AND id=$2 AND status='ready' AND (expires_at>now() OR preparation_expires_at>now() OR retained) FOR UPDATE`, org, id), &a)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrInvalid
		}
		if err != nil {
			return nil, err
		}
		allowed := a.SessionID == session || (a.SessionID == "" && a.CreatorID == creator && a.ProjectID == project) || (source != "" && a.SessionID == source)
		if !allowed {
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM ao_message_attachments WHERE org_id=$1 AND attachment_id=$2 AND (session_id=$3 OR session_id=NULLIF($4,'')::uuid))`, org, id, session, source).Scan(&allowed); err != nil {
				return nil, err
			}
		}
		if !allowed {
			return nil, ErrInvalid
		}
		total += a.Size
		if total > attachments.MaxTotalBytes {
			return nil, ErrInvalid
		}
		if a.SessionID == "" {
			if _, err := tx.Exec(ctx, `UPDATE ao_attachments SET session_id=$3 WHERE org_id=$1 AND id=$2`, org, id, session); err != nil {
				return nil, err
			}
		}
		result = append(result, a.Metadata)
	}
	return result, nil
}
func linkAttachmentsTx(ctx context.Context, tx pgx.Tx, org, session string, seq int64, metadata []attachments.Metadata) error {
	for _, a := range metadata {
		if _, err := tx.Exec(ctx, `UPDATE ao_attachments SET retained=true WHERE org_id=$1 AND id=$2`, org, a.ID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO ao_message_attachments (org_id,session_id,event_sequence,attachment_id) VALUES ($1,$2,$3,$4) ON CONFLICT DO NOTHING`, org, session, seq, a.ID); err != nil {
			return err
		}
	}
	return nil
}
func (s *Store) WorkerAttachments(ctx context.Context, org, session, worker string, epoch int64) ([]attachments.Metadata, error) {
	result := []attachments.Metadata{}
	err := s.withOrg(ctx, org, func(tx pgx.Tx) error {
		if err := requireCurrentWorker(ctx, tx, org, session, worker, epoch); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT id,filename,size,mime_type,sha256,status FROM ao_attachments WHERE org_id=$1 AND status='ready' AND ((session_id=$2 AND (retained OR preparation_expires_at>now())) OR EXISTS(SELECT 1 FROM ao_message_attachments ref WHERE ref.org_id=$1 AND ref.attachment_id=ao_attachments.id AND ref.session_id=$2)) ORDER BY created_at,id`, org, session)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var a attachments.Metadata
			if err := rows.Scan(&a.ID, &a.Filename, &a.Size, &a.MIMEType, &a.SHA256, &a.Status); err != nil {
				return err
			}
			result = append(result, a)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		rows.Close()
		if len(result) > 0 {
			var capable bool
			if err := tx.QueryRow(ctx, `SELECT capabilities ? 'attachments.images.v1' FROM ao_worker_connections WHERE org_id=$1 AND session_id=$2 AND worker_id=$3 AND epoch=$4 AND disconnected_at IS NULL`, org, session, worker, epoch).Scan(&capable); err != nil {
				return err
			}
			if !capable {
				return ErrImageWorkerUpgrade
			}
		}
		return nil
	})
	return result, err
}

// LeaseAttachments permits worker reads during preparation without permanent
// retention. Failed, cancelled and stale-worker requests expire with this lease.
func (s *Store) LeaseAttachments(ctx context.Context, p domain.Principal, org, session string, ids []string) ([]attachments.Metadata, error) {
	var metadata []attachments.Metadata
	err := s.withAttachmentAccess(ctx, p, org, session, true, func(tx pgx.Tx) error {
		var err error
		metadata, err = attachmentMetadataTx(ctx, tx, org, session, ids, "", p.UserID, "")
		if err != nil {
			return err
		}
		for _, a := range metadata {
			if _, err := tx.Exec(ctx, `UPDATE ao_attachments SET preparation_expires_at=now()+interval '10 minutes' WHERE org_id=$1 AND id=$2`, org, a.ID); err != nil {
				return err
			}
		}
		return nil
	})
	return metadata, err
}

// RetainAttachments promotes an acknowledged terminal preparation. The worker
// lock fences replacement through the retention commit, not just an HTTP check.
func (s *Store) RetainAttachments(ctx context.Context, p domain.Principal, org, session string, ids []string, worker string, epoch int64) error {
	return s.withAttachmentAccess(ctx, p, org, session, true, func(tx pgx.Tx) error {
		var current string
		err := tx.QueryRow(ctx, `SELECT worker_id FROM ao_worker_connections WHERE org_id=$1 AND session_id=$2 AND worker_id=$3 AND epoch=$4 AND disconnected_at IS NULL FOR SHARE`, org, session, worker, epoch).Scan(&current)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrStaleWorker
		}
		if err != nil {
			return err
		}
		metadata, err := attachmentMetadataTx(ctx, tx, org, session, ids, "", p.UserID, "")
		if err != nil {
			return err
		}
		for _, a := range metadata {
			tag, err := tx.Exec(ctx, `UPDATE ao_attachments SET retained=true,preparation_expires_at=NULL WHERE org_id=$1 AND id=$2 AND (retained OR preparation_expires_at>now())`, org, a.ID)
			if err != nil {
				return err
			}
			if tag.RowsAffected() == 0 {
				return ErrInvalid
			}
		}
		return nil
	})
}

// CleanupAttachments locks out new links before deleting expired bytes. Failed
// storage deletions leave expired rows available for the next cleanup pass.
func (s *Store) CleanupAttachments(ctx context.Context, storage attachments.Storage) error {
	type candidate struct{ ID, Org string }
	var items []candidate
	if err := s.withService(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE ao_attachments SET status='expired' WHERE NOT retained AND expires_at<=now() AND (preparation_expires_at IS NULL OR preparation_expires_at<=now()) AND status<>'expired'`); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT id,org_id FROM ao_attachments WHERE status='expired' OR (status='ready' AND NOT upload_cleaned AND upload_expires_at<=now()) ORDER BY (status='expired') DESC,created_at LIMIT 100`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var c candidate
			if err := rows.Scan(&c.ID, &c.Org); err != nil {
				return err
			}
			items = append(items, c)
		}
		return rows.Err()
	}); err != nil {
		return err
	}
	for _, c := range items {
		if err := s.withOrg(ctx, c.Org, func(tx pgx.Tx) error {
			var expired, cleanUpload bool
			err := tx.QueryRow(ctx, `SELECT status='expired',status='ready' AND NOT upload_cleaned AND upload_expires_at<=now() FROM ao_attachments WHERE org_id=$1 AND id=$2 FOR UPDATE`, c.Org, c.ID).Scan(&expired, &cleanUpload)
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			if err != nil {
				return err
			}
			if !expired && !cleanUpload {
				return nil
			}
			if err := storage.Delete(ctx, "upload-"+c.ID); err != nil {
				return err
			}
			if !expired {
				_, err := tx.Exec(ctx, `UPDATE ao_attachments SET upload_cleaned=true WHERE org_id=$1 AND id=$2`, c.Org, c.ID)
				return err
			}
			if err := storage.Delete(ctx, "image-"+c.ID); err != nil {
				return err
			}
			_, err = tx.Exec(ctx, `DELETE FROM ao_attachments WHERE org_id=$1 AND id=$2 AND status='expired' AND NOT retained`, c.Org, c.ID)
			return err
		}); err != nil {
			return err
		}
	}
	return nil
}
