package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Storage management reads and writes raw SQL: its tables are small, private to
// the storage service, and have no generated queries.

const storageSettingsColumns = `auto_clean, offload_enabled, offload_idle_hours, offload_no_pr_grace_hours,
	offload_merged_idle_minutes, ended_worktree_days, backup_days, log_days,
	clean_dev_profiles, dev_profile_days, template_days, updated_at_ms`

// GetStorageSettings returns the storage preferences. The row is seeded by
// migration, so a missing row is a corrupted database, not a default.
func (s *Store) GetStorageSettings(ctx context.Context) (domain.StorageSettings, error) {
	var (
		out                                     domain.StorageSettings
		autoClean, offloadEnabled, cleanDev     int64
		idle, grace, merged, ended, backup, log int64
		devDays, templateDays, updatedAtMs      int64
	)
	err := s.readDB.QueryRowContext(ctx, `SELECT `+storageSettingsColumns+` FROM storage_settings WHERE id = 1`).Scan(
		&autoClean, &offloadEnabled, &idle, &grace, &merged, &ended, &backup, &log,
		&cleanDev, &devDays, &templateDays, &updatedAtMs,
	)
	if err != nil {
		return domain.StorageSettings{}, fmt.Errorf("get storage settings: %w", err)
	}
	out.AutoClean = autoClean != 0
	out.OffloadEnabled = offloadEnabled != 0
	out.OffloadIdleHours = int(idle)
	out.OffloadNoPRGraceHours = int(grace)
	out.OffloadMergedIdleMinutes = int(merged)
	out.EndedWorktreeDays = int(ended)
	out.BackupDays = int(backup)
	out.LogDays = int(log)
	out.CleanDevProfiles = cleanDev != 0
	out.DevProfileDays = int(devDays)
	out.TemplateDays = int(templateDays)
	out.UpdatedAt = msToTime(updatedAtMs)
	return out, nil
}

// SaveStorageSettings replaces the storage preferences.
func (s *Store) SaveStorageSettings(ctx context.Context, settings domain.StorageSettings, now time.Time) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.writeDB.ExecContext(ctx, `UPDATE storage_settings SET
		auto_clean = ?, offload_enabled = ?, offload_idle_hours = ?, offload_no_pr_grace_hours = ?,
		offload_merged_idle_minutes = ?, ended_worktree_days = ?, backup_days = ?, log_days = ?,
		clean_dev_profiles = ?, dev_profile_days = ?, template_days = ?, updated_at_ms = ?
		WHERE id = 1`,
		storageFlag(settings.AutoClean), storageFlag(settings.OffloadEnabled), settings.OffloadIdleHours,
		settings.OffloadNoPRGraceHours, settings.OffloadMergedIdleMinutes, settings.EndedWorktreeDays,
		settings.BackupDays, settings.LogDays, storageFlag(settings.CleanDevProfiles), settings.DevProfileDays,
		settings.TemplateDays, now.UnixMilli(),
	)
	if err != nil {
		return fmt.Errorf("save storage settings: %w", err)
	}
	return nil
}

const sessionStorageFactsColumns = `session_id, offloaded_at_ms, offloaded_bytes, offloaded_paths, template_keys, restored_at_ms, restore_error`

func scanSessionStorageFacts(scan func(...any) error) (domain.SessionStorageFacts, error) {
	var (
		id                        string
		offloadedAt, restoredAt   int64
		bytes                     int64
		paths, keys, restoreError string
	)
	if err := scan(&id, &offloadedAt, &bytes, &paths, &keys, &restoredAt, &restoreError); err != nil {
		return domain.SessionStorageFacts{}, err
	}
	facts := domain.SessionStorageFacts{
		SessionID:      domain.SessionID(id),
		OffloadedAt:    msToTime(offloadedAt),
		OffloadedBytes: bytes,
		RestoredAt:     msToTime(restoredAt),
		RestoreError:   restoreError,
	}
	// A malformed list reads as empty rather than failing the whole read: the
	// restore path falls back to the project's Setup, which needs neither.
	_ = json.Unmarshal([]byte(paths), &facts.OffloadedPaths)
	_ = json.Unmarshal([]byte(keys), &facts.TemplateKeys)
	return facts, nil
}

// GetSessionStorageFacts returns a session's offload facts, ok=false when the
// session was never offloaded.
func (s *Store) GetSessionStorageFacts(ctx context.Context, id domain.SessionID) (domain.SessionStorageFacts, bool, error) {
	row := s.readDB.QueryRowContext(ctx, `SELECT `+sessionStorageFactsColumns+` FROM session_storage_facts WHERE session_id = ?`, string(id))
	facts, err := scanSessionStorageFacts(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.SessionStorageFacts{}, false, nil
	}
	if err != nil {
		return domain.SessionStorageFacts{}, false, fmt.Errorf("get session storage facts %s: %w", id, err)
	}
	return facts, true, nil
}

// ListSessionStorageFacts returns every session's offload facts by session.
func (s *Store) ListSessionStorageFacts(ctx context.Context) (map[domain.SessionID]domain.SessionStorageFacts, error) {
	rows, err := s.readDB.QueryContext(ctx, `SELECT `+sessionStorageFactsColumns+` FROM session_storage_facts`)
	if err != nil {
		return nil, fmt.Errorf("list session storage facts: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[domain.SessionID]domain.SessionStorageFacts{}
	for rows.Next() {
		facts, err := scanSessionStorageFacts(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("scan session storage facts: %w", err)
		}
		out[facts.SessionID] = facts
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list session storage facts: %w", err)
	}
	return out, nil
}

// RecordSessionOffload records a finished offload. It keeps the last restore
// time, so the session reads as offloaded until a later restore settles it.
func (s *Store) RecordSessionOffload(ctx context.Context, facts domain.SessionStorageFacts) error {
	paths, err := json.Marshal(nonNilStrings(facts.OffloadedPaths))
	if err != nil {
		return fmt.Errorf("encode offloaded paths: %w", err)
	}
	keys := facts.TemplateKeys
	if keys == nil {
		keys = map[string]string{}
	}
	encodedKeys, err := json.Marshal(keys)
	if err != nil {
		return fmt.Errorf("encode template keys: %w", err)
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err = s.writeDB.ExecContext(ctx, `INSERT INTO session_storage_facts
		(session_id, offloaded_at_ms, offloaded_bytes, offloaded_paths, template_keys, restore_error)
		VALUES (?, ?, ?, ?, ?, '')
		ON CONFLICT (session_id) DO UPDATE SET
			offloaded_at_ms = excluded.offloaded_at_ms,
			offloaded_bytes = excluded.offloaded_bytes,
			offloaded_paths = excluded.offloaded_paths,
			template_keys = excluded.template_keys,
			restore_error = ''`,
		string(facts.SessionID), facts.OffloadedAt.UnixMilli(), facts.OffloadedBytes, string(paths), string(encodedKeys),
	)
	if err != nil {
		return fmt.Errorf("record session offload %s: %w", facts.SessionID, err)
	}
	return nil
}

// RecordSessionRestore records a restore attempt. A success settles the
// offload; a failure keeps it offloaded and records why, so the next resume
// tries again.
func (s *Store) RecordSessionRestore(ctx context.Context, id domain.SessionID, at time.Time, restoreErr string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var err error
	if restoreErr == "" {
		_, err = s.writeDB.ExecContext(ctx, `UPDATE session_storage_facts SET restored_at_ms = ?, restore_error = '' WHERE session_id = ?`,
			at.UnixMilli(), string(id))
	} else {
		_, err = s.writeDB.ExecContext(ctx, `UPDATE session_storage_facts SET restore_error = ? WHERE session_id = ?`,
			restoreErr, string(id))
	}
	if err != nil {
		return fmt.Errorf("record session restore %s: %w", id, err)
	}
	return nil
}

// InsertStorageActions appends audit-log entries.
func (s *Store) InsertStorageActions(ctx context.Context, actions []domain.StorageAction) error {
	if len(actions) == 0 {
		return nil
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("insert storage actions: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, action := range actions {
		if _, err := tx.ExecContext(ctx, `INSERT INTO storage_actions
			(at_ms, kind, target, session_id, reason, bytes, dry_run, result, error)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			action.At.UnixMilli(), string(action.Kind), action.Target, string(action.SessionID), action.Reason,
			action.Bytes, storageFlag(action.DryRun), string(action.Result), action.Error,
		); err != nil {
			return fmt.Errorf("insert storage action: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("insert storage actions: %w", err)
	}
	return nil
}

// ListStorageActions returns the newest audit-log entries first.
func (s *Store) ListStorageActions(ctx context.Context, limit int) ([]domain.StorageAction, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.readDB.QueryContext(ctx, `SELECT id, at_ms, kind, target, session_id, reason, bytes, dry_run, result, error
		FROM storage_actions ORDER BY at_ms DESC, id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("list storage actions: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []domain.StorageAction{}
	for rows.Next() {
		var (
			action                                 domain.StorageAction
			atMs, dryRun                           int64
			kind, sessionID, result, target, cause string
		)
		if err := rows.Scan(&action.ID, &atMs, &kind, &target, &sessionID, &cause, &action.Bytes, &dryRun, &result, &action.Error); err != nil {
			return nil, fmt.Errorf("scan storage action: %w", err)
		}
		action.At = msToTime(atMs)
		action.Kind = domain.StorageCleanKind(kind)
		action.Target = target
		action.SessionID = domain.SessionID(sessionID)
		action.Reason = cause
		action.DryRun = dryRun != 0
		action.Result = domain.StorageActionResult(result)
		out = append(out, action)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list storage actions: %w", err)
	}
	return out, nil
}

// PruneStorageActionsBefore deletes audit-log entries older than before.
func (s *Store) PruneStorageActionsBefore(ctx context.Context, before time.Time) (int64, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	res, err := s.writeDB.ExecContext(ctx, `DELETE FROM storage_actions WHERE at_ms < ?`, before.UnixMilli())
	if err != nil {
		return 0, fmt.Errorf("prune storage actions: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// ListSessionEndTimes returns when each terminated session ended: its
// terminated_at, or its last update for rows terminated before that column.
func (s *Store) ListSessionEndTimes(ctx context.Context) (map[domain.SessionID]time.Time, error) {
	rows, err := s.readDB.QueryContext(ctx, `SELECT id, updated_at, terminated_at FROM sessions WHERE is_terminated = 1`)
	if err != nil {
		return nil, fmt.Errorf("list session end times: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[domain.SessionID]time.Time{}
	for rows.Next() {
		var (
			id           string
			updatedAt    time.Time
			terminatedAt sql.NullTime
		)
		if err := rows.Scan(&id, &updatedAt, &terminatedAt); err != nil {
			return nil, fmt.Errorf("scan session end time: %w", err)
		}
		ended := updatedAt
		if terminatedAt.Valid {
			ended = terminatedAt.Time
		}
		out[domain.SessionID(id)] = ended
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list session end times: %w", err)
	}
	return out, nil
}

// DatabasePageStats reports the database's page size, total pages and free
// pages, enough to size the file and what a vacuum would return to the disk.
func (s *Store) DatabasePageStats(ctx context.Context) (pageSize, pageCount, freePages int64, err error) {
	for _, pragma := range []struct {
		name string
		dst  *int64
	}{{"page_size", &pageSize}, {"page_count", &pageCount}, {"freelist_count", &freePages}} {
		if scanErr := s.writeDB.QueryRowContext(ctx, "PRAGMA "+pragma.name).Scan(pragma.dst); scanErr != nil {
			return 0, 0, 0, fmt.Errorf("pragma %s: %w", pragma.name, scanErr)
		}
	}
	return pageSize, pageCount, freePages, nil
}

// DatabaseTableSizes returns the bytes each table and index occupies, largest
// first. It needs SQLite's dbstat virtual table; a build without it reports
// ok=false instead of failing the storage report.
func (s *Store) DatabaseTableSizes(ctx context.Context, limit int) (sizes []domain.StorageTableSize, ok bool, err error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.readDB.QueryContext(ctx, `SELECT name, SUM(pgsize) AS bytes FROM dbstat GROUP BY name ORDER BY bytes DESC LIMIT ?`, limit)
	if err != nil {
		if strings.Contains(err.Error(), "no such table") {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("database table sizes: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var size domain.StorageTableSize
		if err := rows.Scan(&size.Name, &size.Bytes); err != nil {
			return nil, false, fmt.Errorf("scan table size: %w", err)
		}
		sizes = append(sizes, size)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("database table sizes: %w", err)
	}
	return sizes, true, nil
}

func storageFlag(value bool) int64 {
	if value {
		return 1
	}
	return 0
}

func msToTime(ms int64) time.Time {
	if ms <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms).UTC()
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}
