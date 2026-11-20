package sqlite

import (
	"database/sql"
	"log/slog"
)

type timestampRepairResult struct{ Repaired, Skipped, Deleted int }
type timestampRepairState struct {
	timestampRepairResult
	reader *sql.DB
	retry  map[string]bool
}

func newTimestampRepairState() *timestampRepairState {
	return &timestampRepairState{retry: map[string]bool{}}
}

func (s *timestampRepairState) skip(table string, values []any, n int, err error) {
	if !preserveRejectedTimestamp(err) {
		s.retry[table] = true
	}
	s.Skipped++
	slog.Warn("timestamp repair skipped row", "table", table, "rowid", values[n], "error", err)
}
