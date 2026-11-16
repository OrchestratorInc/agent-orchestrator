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
	"runtime"
	"strings"
)

type timestampRepairResult struct{ Repaired, Skipped int }
type timestampRepairState struct {
	timestampRepairResult
	path      string
	rows      map[string]string
	dirty     bool
	reader    *sql.DB
	completed map[string]string
	retry     map[string]bool
}

func loadTimestampSkips(ctx context.Context, db *sql.DB) (*timestampRepairState, error) {
	state := &timestampRepairState{rows: map[string]string{}, completed: map[string]string{}, retry: map[string]bool{}}
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
	if err := json.Unmarshal(data, &state.rows); err != nil || state.rows == nil {
		if err == nil {
			err = errors.New("null timestamp repair ledger")
		}
		slog.Warn("invalid timestamp repair ledger; rebuilding", "error", err)
		state.rows = map[string]string{}
		state.dirty = true
	}
	for key, value := range state.rows {
		if strings.HasPrefix(key, timestampCompletionPrefix) {
			state.completed[strings.TrimPrefix(key, timestampCompletionPrefix)] = value
		}
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
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(file.Name(), s.path); err != nil {
		return err
	}
	// Windows has no directory fsync; the file itself was synced before rename.
	if runtime.GOOS == "windows" {
		return nil
	}
	dir, err := os.Open(filepath.Dir(s.path))
	if err != nil {
		return err
	}
	return errors.Join(dir.Sync(), dir.Close())
}
func timestampSkipKey(table string, values []any, n int) (string, string) {
	data := []byte(fmt.Sprintf("%#v", values[n+1:]))
	return fmt.Sprintf("%s/%v", table, values[n]), fmt.Sprintf("policy:%x", sha256.Sum256(data))
}
func (s *timestampRepairState) skip(table string, values []any, n int, err error) {
	key, hash := timestampSkipKey(table, values, n)
	if errors.Is(err, errTimestampPolicySkip) {
		s.rows[key] = hash
		s.dirty = true
	} else {
		s.retry[table] = true
	}
	s.Skipped++
	slog.Warn("timestamp repair skipped row", "table", table, "rowid", values[n], "error", err)
}

// Version 3 also requires the repaired database pages to be checkpointed.
const timestampCompletionVersion = "3"
const timestampCompletionPrefix = "_complete3/"

func (s *timestampRepairState) complete(table, signature string) {
	s.completed[table] = signature
	s.rows[timestampCompletionPrefix+table] = signature
	s.dirty = true
}

func (s *timestampRepairState) saveProgress(ctx context.Context, db *sql.DB) error {
	var err error
	if s.dirty && len(s.completed) > 0 {
		var busy, logged, checkpointed int
		err = db.QueryRowContext(ctx, "PRAGMA wal_checkpoint(FULL)").Scan(&busy, &logged, &checkpointed)
		if err != nil || busy != 0 {
			if err == nil {
				err = fmt.Errorf("timestamp repair checkpoint busy: %d", busy)
			}
			for table := range s.completed {
				delete(s.rows, timestampCompletionPrefix+table)
			}
			clear(s.completed)
			delete(s.rows, "_repaired")
		}
	}
	return errors.Join(err, s.save())
}
