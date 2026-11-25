package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Chat attachment leases record which staged files belong only to an unsent
// draft. Accepting a message that names a file deletes its lease inside the
// accepting write (see the chat_attachment_leases migration triggers), so a
// file named by history never has one. Only a leased file is ever reclaimed,
// and only after reclamation has claimed its row; acceptance of a claimed file
// is refused, so an accepted message never names a deleted file.

// ErrChatAttachmentDiscarded reports a message naming a staged file that was
// discarded and is being or has been deleted.
var ErrChatAttachmentDiscarded = errors.New("chat attachment was discarded")

// LeaseChatAttachment records name as staged for an unsent draft of id.
func (s *Store) LeaseChatAttachment(ctx context.Context, id domain.SessionID, name string, now time.Time) error {
	at := now.UnixMilli()
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if _, err := s.writeDB.ExecContext(ctx, `
INSERT INTO chat_attachment_leases (session_id, name, created_at_ms, renewed_at_ms)
VALUES (?, ?, ?, ?)`, string(id), name, at, at); err != nil {
		return fmt.Errorf("lease chat attachment %s/%s: %w", id, name, err)
	}
	return nil
}

// HoldChatAttachmentLeases is the first step of the explicit commit for
// messages accepted outside the conversation tables (side questions). It holds
// the unclaimed leases of id whose names appear in any of texts, including one
// a racing release already released, so reclamation cannot claim them while
// the message is being recorded, and returns their names for
// SettleChatAttachmentHolds. If any named file has already been claimed for
// reclamation it holds nothing and returns ErrChatAttachmentDiscarded.
func (s *Store) HoldChatAttachmentLeases(ctx context.Context, id domain.SessionID, texts []string) ([]string, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("hold chat attachment leases %s: %w", id, err)
	}
	defer func() { _ = tx.Rollback() }()
	held := map[string]bool{}
	var names []string
	for _, text := range texts {
		if text == "" {
			continue
		}
		var claimed int
		if err := tx.QueryRowContext(ctx, `
SELECT COUNT(*) FROM chat_attachment_leases
WHERE session_id = ? AND reclaiming_at_ms IS NOT NULL AND instr(?, name) > 0`, string(id), text).Scan(&claimed); err != nil {
			return nil, fmt.Errorf("hold chat attachment leases %s: %w", id, err)
		}
		if claimed > 0 {
			return nil, ErrChatAttachmentDiscarded
		}
		rows, err := tx.QueryContext(ctx, `
SELECT name FROM chat_attachment_leases
WHERE session_id = ? AND reclaiming_at_ms IS NULL AND instr(?, name) > 0`, string(id), text)
		if err != nil {
			return nil, fmt.Errorf("hold chat attachment leases %s: %w", id, err)
		}
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				_ = rows.Close()
				return nil, fmt.Errorf("hold chat attachment leases %s: %w", id, err)
			}
			if !held[name] {
				held[name] = true
				names = append(names, name)
			}
		}
		if err := rows.Close(); err != nil {
			return nil, fmt.Errorf("hold chat attachment leases %s: %w", id, err)
		}
	}
	for _, name := range names {
		if _, err := tx.ExecContext(ctx, `
UPDATE chat_attachment_leases SET accepting = accepting + 1
WHERE session_id = ? AND name = ?`, string(id), name); err != nil {
			return nil, fmt.Errorf("hold chat attachment lease %s/%s: %w", id, name, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("hold chat attachment leases %s: %w", id, err)
	}
	return names, nil
}

// SettleChatAttachmentHolds finishes HoldChatAttachmentLeases. If the message
// was recorded (accepted), the held files are committed for good: their rows
// are deleted. If it was not, only this message's hold is dropped and each
// file returns to whatever its draft did meanwhile (live, or released and
// reclaimable). A row another message accepted in the meantime is already
// gone, so dropping a hold never re-leases an accepted file.
func (s *Store) SettleChatAttachmentHolds(ctx context.Context, id domain.SessionID, names []string, accepted bool) error {
	if len(names) == 0 {
		return nil
	}
	query := `
UPDATE chat_attachment_leases SET accepting = accepting - 1
WHERE session_id = ? AND name = ? AND accepting > 0`
	if accepted {
		query = `
DELETE FROM chat_attachment_leases
WHERE session_id = ? AND name = ? AND reclaiming_at_ms IS NULL`
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("settle chat attachment holds %s: %w", id, err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, name := range names {
		if _, err := tx.ExecContext(ctx, query, string(id), name); err != nil {
			return fmt.Errorf("settle chat attachment hold %s/%s: %w", id, name, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("settle chat attachment holds %s: %w", id, err)
	}
	return nil
}

// ReleaseChatAttachmentLeases marks the live leases of names released. A name
// without a live lease (never leased, already committed by an accepted
// message, or already released) is left alone. A released lease can still be
// rescued by an acceptance until reclamation claims it.
func (s *Store) ReleaseChatAttachmentLeases(ctx context.Context, id domain.SessionID, names []string, now time.Time) error {
	if len(names) == 0 {
		return nil
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("release chat attachment leases %s: %w", id, err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, name := range names {
		if _, err := tx.ExecContext(ctx, `
UPDATE chat_attachment_leases SET released_at_ms = ?
WHERE session_id = ? AND name = ? AND released_at_ms IS NULL`, now.UnixMilli(), string(id), name); err != nil {
			return fmt.Errorf("release chat attachment lease %s/%s: %w", id, name, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("release chat attachment leases %s: %w", id, err)
	}
	return nil
}

// RenewChatAttachmentLeases keeps the live leases of names from expiring and
// returns the names whose lease was already released: their files are being
// or have been reclaimed.
func (s *Store) RenewChatAttachmentLeases(ctx context.Context, id domain.SessionID, names []string, now time.Time) ([]string, error) {
	if len(names) == 0 {
		return nil, nil
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("renew chat attachment leases %s: %w", id, err)
	}
	defer func() { _ = tx.Rollback() }()
	var released []string
	for _, name := range names {
		if _, err := tx.ExecContext(ctx, `
UPDATE chat_attachment_leases SET renewed_at_ms = ?
WHERE session_id = ? AND name = ? AND released_at_ms IS NULL`, now.UnixMilli(), string(id), name); err != nil {
			return nil, fmt.Errorf("renew chat attachment lease %s/%s: %w", id, name, err)
		}
		var count int
		if err := tx.QueryRowContext(ctx, `
SELECT COUNT(*) FROM chat_attachment_leases
WHERE session_id = ? AND name = ? AND released_at_ms IS NOT NULL`, string(id), name).Scan(&count); err != nil {
			return nil, fmt.Errorf("renew chat attachment lease %s/%s: %w", id, name, err)
		}
		if count > 0 {
			released = append(released, name)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("renew chat attachment leases %s: %w", id, err)
	}
	return released, nil
}

// ExpireChatAttachmentLeases releases every live lease last renewed before
// cutoff. An empty id expires leases of every session.
func (s *Store) ExpireChatAttachmentLeases(ctx context.Context, id domain.SessionID, cutoff, now time.Time) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if _, err := s.writeDB.ExecContext(ctx, `
UPDATE chat_attachment_leases SET released_at_ms = ?
WHERE released_at_ms IS NULL AND renewed_at_ms < ? AND (? = '' OR session_id = ?)`,
		now.UnixMilli(), cutoff.UnixMilli(), string(id), string(id)); err != nil {
		return fmt.Errorf("expire chat attachment leases: %w", err)
	}
	return nil
}

// ReleasedChatAttachmentLeases lists released leases whose files still have
// to be reclaimed, by session. An empty id lists every session's.
func (s *Store) ReleasedChatAttachmentLeases(ctx context.Context, id domain.SessionID) (map[domain.SessionID][]string, error) {
	rows, err := s.writeDB.QueryContext(ctx, `
SELECT session_id, name FROM chat_attachment_leases
WHERE released_at_ms IS NOT NULL AND reclaimed_at_ms IS NULL AND (? = '' OR session_id = ?)
ORDER BY session_id, name`, string(id), string(id))
	if err != nil {
		return nil, fmt.Errorf("list released chat attachment leases: %w", err)
	}
	defer func() { _ = rows.Close() }()
	released := map[domain.SessionID][]string{}
	for rows.Next() {
		var session, name string
		if err := rows.Scan(&session, &name); err != nil {
			return nil, fmt.Errorf("list released chat attachment leases: %w", err)
		}
		released[domain.SessionID(session)] = append(released[domain.SessionID(session)], name)
	}
	return released, rows.Err()
}

// ClaimChatAttachmentLease takes a released lease for reclamation and reports
// whether its files may now be deleted. It is false when an accepted message
// rescued the file after the list of released leases was read, or while a
// side question holding the file is being recorded. From the claim
// on, any acceptance naming the file is refused. Re-claiming a row a previous
// run claimed but did not finish succeeds.
func (s *Store) ClaimChatAttachmentLease(ctx context.Context, id domain.SessionID, name string, now time.Time) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	res, err := s.writeDB.ExecContext(ctx, `
UPDATE chat_attachment_leases SET reclaiming_at_ms = COALESCE(reclaiming_at_ms, ?)
WHERE session_id = ? AND name = ? AND released_at_ms IS NOT NULL AND reclaimed_at_ms IS NULL
  AND (accepting = 0 OR reclaiming_at_ms IS NOT NULL)`,
		now.UnixMilli(), string(id), name)
	if err != nil {
		return false, fmt.Errorf("claim chat attachment lease %s/%s: %w", id, name, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("claim chat attachment lease %s/%s: %w", id, name, err)
	}
	return n > 0, nil
}

// MarkChatAttachmentReclaimed records that both copies of a claimed file are
// gone. The row stays as a tombstone so a late reference is still refused.
func (s *Store) MarkChatAttachmentReclaimed(ctx context.Context, id domain.SessionID, name string, now time.Time) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if _, err := s.writeDB.ExecContext(ctx, `
UPDATE chat_attachment_leases SET reclaimed_at_ms = ?
WHERE session_id = ? AND name = ? AND reclaiming_at_ms IS NOT NULL AND reclaimed_at_ms IS NULL`,
		now.UnixMilli(), string(id), name); err != nil {
		return fmt.Errorf("mark chat attachment reclaimed %s/%s: %w", id, name, err)
	}
	return nil
}

// PruneChatAttachmentTombstones deletes the rows of files reclaimed before
// cutoff.
func (s *Store) PruneChatAttachmentTombstones(ctx context.Context, cutoff time.Time) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if _, err := s.writeDB.ExecContext(ctx, `
DELETE FROM chat_attachment_leases WHERE reclaimed_at_ms IS NOT NULL AND reclaimed_at_ms < ?`,
		cutoff.UnixMilli()); err != nil {
		return fmt.Errorf("prune chat attachment tombstones: %w", err)
	}
	return nil
}
