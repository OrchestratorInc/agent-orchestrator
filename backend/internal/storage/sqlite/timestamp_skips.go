package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
)

type timestampRepairResult struct{ Repaired, Skipped int }
type timestampRepairState struct {
	timestampRepairResult
	path  string
	rows  map[string]string
	dirty bool
}

func loadTimestampSkips(ctx context.Context, db *sql.DB) (*timestampRepairState, error) {
	state := &timestampRepairState{rows: map[string]string{}}
	var file string
	if err := db.QueryRowContext(ctx, `SELECT file FROM pragma_database_list WHERE name='main'`).Scan(&file); err != nil {
		return nil, err
	}
	if file == "" {
		return state, nil
	}
	state.path = filepath.Join(filepath.Dir(file), "timestamp-repair-skips.json")
	data, err := os.ReadFile(state.path)
	if errors.Is(err, os.ErrNotExist) {
		return state, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &state.rows); err != nil {
		return nil, err
	}
	if state.rows == nil {
		return nil, errors.New("invalid timestamp skip ledger")
	}
	return state, nil
}
func (s *timestampRepairState) save() error {
	if !s.dirty || s.path == "" {
		return nil
	}
	data, err := json.Marshal(s.rows)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(s.path), ".timestamp-repair-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(file.Name()) }()
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), s.path)
}
func timestampSkipKey(table string, values []any, n int) string {
	return fmt.Sprintf("%s/%v", table, values[n])
}

// A skipped row is retried on every pass, because the conflict that rejected it
// can be resolved later. The ledger remembers what the row looked like and why
// it was rejected, so an unchanged rejection stays quiet and a new cause is
// counted and logged again.
func (s *timestampRepairState) skip(table string, values []any, n int, err error) {
	key := timestampSkipKey(table, values, n)
	cause := fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%#v|%v", values[n+1:], err))))
	if s.rows[key] == cause {
		return
	}
	s.rows[key] = cause
	s.dirty = true
	s.Skipped++
	slog.Warn("timestamp repair skipped row", "table", table, "rowid", values[n], "error", err)
}
