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
	changed   bool
	database  string
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
	state.database = file
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
	stamp, err := timestampDatabaseStamp(file)
	if err != nil {
		return nil, err
	}
	state.changed = (state.rows["_database"] != "" && state.rows["_database"] != stamp) || state.rows["_untrusted"] == "1"
	if !state.changed {
		for key, value := range state.rows {
			if strings.HasPrefix(key, "_complete/") {
				state.completed[strings.TrimPrefix(key, "_complete/")] = value
			}
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
	return fmt.Sprintf("%s/%v", table, values[n]), fmt.Sprintf("%x", sha256.Sum256(data))
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

// A retry-time update can change neither rowid nor change_log. File stamps
// detect those writes too, including a commit still present only in the WAL.
func timestampDatabaseStamp(path string) (string, error) {
	var stamp strings.Builder
	for _, file := range []string{path, path + "-wal"} {
		info, err := os.Stat(file)
		if errors.Is(err, os.ErrNotExist) && file != path {
			continue
		}
		if err != nil {
			return "", err
		}
		if file != path && info.Size() <= 32 {
			continue
		}
		fmt.Fprintf(&stamp, "%d:%d;", info.Size(), info.ModTime().UnixNano())
	}
	return stamp.String(), nil
}

func (s *timestampRepairState) checkpoint(stamp string, trusted bool) error {
	for key := range s.rows {
		if strings.HasPrefix(key, "_complete/") {
			delete(s.rows, key)
		}
	}
	if trusted {
		for table, columns := range s.completed {
			s.rows["_complete/"+table] = columns
		}
		delete(s.rows, "_untrusted")
	} else {
		s.rows["_untrusted"] = "1"
	}
	s.rows["_database"] = stamp
	s.dirty = true
	return s.save()
}
