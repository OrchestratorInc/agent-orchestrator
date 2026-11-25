package store

import (
	"context"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Chat attachment leases record which staged files belong only to an unsent
// draft. Accepting a message that names a file deletes its lease inside the
// accepting write (see the chat_attachment_leases migration triggers), so a
// file named by history never has one. Only a leased file is ever reclaimed.

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

// CommitChatAttachmentLeases drops the live leases of id whose names appear
// in any of texts. It is the explicit commit for accepted messages that are
// not stored in conversation tables (side chats).
func (s *Store) CommitChatAttachmentLeases(ctx context.Context, id domain.SessionID, texts []string) error {
	if len(texts) == 0 {
		return nil
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("commit chat attachment leases %s: %w", id, err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, text := range texts {
		if text == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
DELETE FROM chat_attachment_leases
WHERE session_id = ? AND released_at_ms IS NULL AND instr(?, name) > 0`, string(id), text); err != nil {
			return fmt.Errorf("commit chat attachment leases %s: %w", id, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit chat attachment leases %s: %w", id, err)
	}
	return nil
}

// ReleaseChatAttachmentLeases marks the live leases of names released. A name
// without a live lease (never leased, already committed by an accepted
// message, or already released) is left alone, so a release that loses a race
// with acceptance cannot reclaim the accepted file.
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
WHERE released_at_ms IS NOT NULL AND (? = '' OR session_id = ?)
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

// ForgetChatAttachmentLease deletes a released lease once both copies of its
// file are gone. A live lease is never deleted here.
func (s *Store) ForgetChatAttachmentLease(ctx context.Context, id domain.SessionID, name string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if _, err := s.writeDB.ExecContext(ctx, `
DELETE FROM chat_attachment_leases
WHERE session_id = ? AND name = ? AND released_at_ms IS NOT NULL`, string(id), name); err != nil {
		return fmt.Errorf("forget chat attachment lease %s/%s: %w", id, name, err)
	}
	return nil
}
