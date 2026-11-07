package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/timestamps"
)

// A readable legacy time still sorts by its local clock in SQL. Normalize the
// guard fields in the caller's transaction before it compares or takes MAX.
func normalizeSessionGuardTimes(ctx context.Context, db gen.DBTX, id domain.SessionID) error {
	columns := []string{"updated_at", "latest_user_prompt_at", "latest_interaction_at"}
	fields, values, dest := make([]string, 3), make([]sql.NullString, 3), make([]any, 3)
	for i, column := range columns {
		fields[i] = fmt.Sprintf("CASE WHEN typeof(%s)='text' THEN CAST(%s AS TEXT) END", column, column)
		dest[i] = &values[i]
	}
	err := db.QueryRowContext(ctx, "SELECT "+strings.Join(fields, ",")+" FROM sessions WHERE id=?", id).Scan(dest...)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read session timestamp guards: %w", err)
	}
	var sets, checks []string
	var args, before []any
	for i, value := range values {
		if !value.Valid || strings.HasSuffix(value.String, " +0000 UTC") {
			continue
		}
		if at, ok := timestamps.ParseLegacy(value.String); ok && at.String() != value.String {
			sets = append(sets, columns[i]+"=?")
			args = append(args, at)
			checks = append(checks, fmt.Sprintf("typeof(%s)='text' AND CAST(%s AS TEXT)=?", columns[i], columns[i]))
			before = append(before, value.String)
		}
	}
	if len(sets) == 0 {
		return nil
	}
	args = append(append(args, id), before...)
	result, err := db.ExecContext(ctx, "UPDATE sessions SET "+strings.Join(sets, ",")+" WHERE id=? AND "+strings.Join(checks, " AND "), args...)
	if err != nil {
		return fmt.Errorf("normalize session timestamp guards: %w", err)
	}
	affected, err := result.RowsAffected()
	if err == nil && affected != 1 {
		return errors.New("session timestamp guards changed during normalization")
	}
	return err
}
