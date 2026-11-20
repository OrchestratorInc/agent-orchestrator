package devin

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
)

// The schema comes from the verified installed CLI, never from source SQL.
// Every copied table is scoped to one native session; global app state and
// other conversations never enter the prepared store.
func copySelectedImport(ctx context.Context, source *sql.DB, root string, s importSession, cwd string) error {
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(filepath.Join(root, "cli", "sessions.db"))}
	target, err := sql.Open("sqlite", u.String()+"?mode=rw")
	if err != nil {
		return err
	}
	defer func() { _ = target.Close() }()
	if err := sourceImportSchema(ctx, target); err != nil {
		return err
	}
	var sessions int
	if err := target.QueryRowContext(ctx, "SELECT COUNT(*) FROM sessions").Scan(&sessions); err != nil {
		return err
	}
	if sessions != 0 {
		return errors.New("devin: private initialization was not empty")
	}
	tx, err := target.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var held int64
	for _, table := range []string{"sessions", "message_nodes", "tool_call_state", "prompt_history", "rendered_commits", "live_row_times"} {
		if err := copyImportTable(ctx, source, tx, table, s, cwd, &held); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	var result string
	if err := target.QueryRowContext(ctx, "PRAGMA quick_check").Scan(&result); err != nil {
		return err
	}
	if result != "ok" {
		return errors.New("devin: private native store failed integrity validation")
	}
	return nil
}

func quoteImportColumn(name string) string { return `"` + strings.ReplaceAll(name, `"`, `""`) + `"` }

func copyImportTable(ctx context.Context, source *sql.DB, target *sql.Tx, table string, s importSession, cwd string, held *int64) error {
	switch table {
	case "sessions", "message_nodes", "tool_call_state", "prompt_history", "rendered_commits", "live_row_times":
	default:
		return errors.New("devin: invalid native copy table")
	}
	key := "session_id"
	if table == "sessions" {
		key = "id"
	}
	// table/key are exclusively the static names in copySelectedImport.
	rows, err := source.QueryContext(ctx, "SELECT * FROM "+table+" WHERE "+key+"=?", s.id) //nolint:gosec // Table is allowlisted above; key is one of two fixed identifiers.
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	columns, err := rows.Columns()
	if err != nil {
		return err
	}
	shape, err := target.QueryContext(ctx, "SELECT * FROM "+table+" LIMIT 0") //nolint:gosec // Table is allowlisted above.
	if err != nil {
		return err
	}
	defer func() { _ = shape.Close() }()
	expected, err := shape.Columns()
	_ = shape.Close()
	if err != nil {
		return err
	}
	if !slices.Equal(columns, expected) {
		return errors.New("devin: unsupported native table layout")
	}
	quoted := make([]string, len(columns))
	marks := make([]string, len(columns))
	for i, col := range columns {
		quoted[i] = quoteImportColumn(col)
		marks[i] = "?"
	}
	statement, err := target.PrepareContext(ctx, "INSERT INTO "+table+" ("+strings.Join(quoted, ",")+") VALUES ("+strings.Join(marks, ",")+")") //nolint:gosec // Table is allowlisted; column identifiers are quoted and match the installed native schema.
	if err != nil {
		return err
	}
	defer func() { _ = statement.Close() }()
	for rows.Next() {
		values := make([]any, len(columns))
		dest := make([]any, len(columns))
		for i := range values {
			dest[i] = &values[i]
		}
		if err := rows.Scan(dest...); err != nil {
			return err
		}
		for i, col := range columns {
			var text string
			switch value := values[i].(type) {
			case string:
				text = value
			case []byte:
				text = string(value)
			}
			*held += int64(len(text))
			if *held > 256<<20 {
				return errors.New("devin: selected native rows exceed import budget")
			}
			if table == "sessions" {
				switch col {
				case "working_directory":
					values[i] = cwd
				case "workspace_dirs":
					values[i], err = remapImportDirectories(s.dirs, s.cwd, cwd)
					if err != nil {
						return err
					}
				}
			}
			if table == "message_nodes" && col == "chat_message" {
				var m importNativeMessage
				if len(text) > 16<<20 || json.Unmarshal([]byte(text), &m) != nil || m.ID == "" {
					return errors.New("devin: invalid native branch message")
				}
				if len(m.Images) != 0 && string(m.Images) != "null" && string(m.Images) != "[]" {
					return errors.New("devin: native branch attachments are unsupported")
				}
			}
		}
		if _, err := statement.ExecContext(ctx, values...); err != nil {
			return err
		}
	}
	return rows.Err()
}
