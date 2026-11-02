package mimocode

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var _ ports.AgentImportedSessionRelocator = (*Plugin)(nil)

// MiMo checks session.directory again before executing a prompt. Copy the
// selected conversation to a private database, preserve its ID, and bind that
// database through MIMOCODE_DB when resuming in the worktree.
func (p *Plugin) RelocateImportedSession(ctx context.Context, r ports.ImportedSessionRelocation) error {
	if !mimoCodeSessionID.MatchString(r.NativeSessionID) || !filepath.IsAbs(r.ConfigDir) || !filepath.IsAbs(r.LaunchCWD) {
		return errors.New("invalid mimo-code import relocation")
	}
	cwd, err := filepath.EvalSymlinks(r.LaunchCWD)
	if err != nil {
		return err
	}
	source := filepath.Join(r.ConfigDir, "mimocode.db")
	dir := filepath.Join(r.LaunchCWD, ".mimocode")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if info, err := os.Lstat(dir); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("mimo-code destination must be a real workspace directory")
	}
	target := filepath.Join(dir, "mimocode.db")
	if resolved, err := filepath.EvalSymlinks(source); err == nil {
		if parent, err := filepath.EvalSymlinks(dir); err == nil && filepath.Join(parent, "mimocode.db") == resolved {
			return errors.New("mimo-code import destination is the source database")
		}
	}
	if info, err := os.Lstat(target); err == nil {
		if !info.Mode().IsRegular() {
			return errors.New("mimo-code destination is not a regular database")
		}
		return mimocodeCopyHasSession(ctx, target, r.NativeSessionID, cwd)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	file, err := os.CreateTemp(dir, ".ao-import-*.db")
	if err != nil {
		return err
	}
	tmp := file.Name()
	if err := file.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	defer func() { _ = os.Remove(tmp) }()
	if err := copyMiMoCodeConversation(ctx, source, tmp, r.NativeSessionID, cwd); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		return err
	}
	if _, err := os.Lstat(target); err == nil {
		return errors.New("mimo-code destination appeared during import; retry")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.Rename(tmp, target)
}

func mimocodeNativeDB(path string) (*sql.DB, error) {
	uri := url.URL{Scheme: "file", Path: filepath.ToSlash(path)}
	return sql.Open("sqlite", uri.String()+"?mode=ro&_pragma=busy_timeout(2000)")
}

func mimocodeCopyHasSession(ctx context.Context, path, id, cwd string) error {
	db, err := mimocodeNativeDB(path)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	var directory string
	if err := db.QueryRowContext(ctx, `SELECT directory FROM session WHERE id = ?`, id).Scan(&directory); err != nil {
		return err
	}
	if directory != cwd {
		return errors.New("existing mimocode workspace database does not contain the imported conversation")
	}
	return nil
}

func copyMiMoCodeConversation(ctx context.Context, source, target, id, cwd string) error {
	src, err := mimocodeNativeDB(source)
	if err != nil {
		return err
	}
	defer func() { _ = src.Close() }()
	read, err := src.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	defer func() { _ = read.Rollback() }()
	var exists int
	if err := read.QueryRowContext(ctx, `SELECT COUNT(*) FROM session WHERE id = ? AND (parent_id IS NULL OR parent_id = '')`, id).Scan(&exists); err != nil {
		return err
	}
	if exists != 1 {
		return errors.New("mimocode source conversation is missing")
	}
	dst, err := sql.Open("sqlite", target)
	if err != nil {
		return err
	}
	defer func() { _ = dst.Close() }()
	write, err := dst.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = write.Rollback() }()
	rows, err := read.QueryContext(ctx, `SELECT type, name, sql FROM sqlite_master WHERE sql IS NOT NULL AND name NOT LIKE 'sqlite_%' AND name NOT IN (SELECT name FROM pragma_table_list WHERE type = 'shadow') ORDER BY rowid`)
	if err != nil {
		return err
	}
	type schemaItem struct{ kind, name, sql string }
	var schema []schemaItem
	for rows.Next() {
		var item schemaItem
		if err := rows.Scan(&item.kind, &item.name, &item.sql); err != nil {
			_ = rows.Close()
			return err
		}
		schema = append(schema, item)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return err
	}
	// Preserve schema and provider migration versions, but never copy another
	// conversation. Triggers are installed after rows to preserve source times
	// and counters instead of treating the copy as newly generated messages.
	for _, item := range schema {
		if item.kind != "table" {
			continue
		}
		if _, err := write.ExecContext(ctx, item.sql); err != nil {
			return err
		}
		filter := ""
		var args []any
		switch item.name {
		case "session":
			filter, args = " WHERE id = ?", []any{id}
		case "project":
			filter, args = " WHERE id = (SELECT project_id FROM session WHERE id = ?)", []any{id}
		case "__drizzle_migrations":
		default:
			columns, err := mimocodeImportColumns(ctx, read, item.name)
			if err != nil {
				return err
			}
			if !slices.Contains(columns, "session_id") {
				continue
			}
			filter, args = " WHERE session_id = ?", []any{id}
		}
		if err := copyMiMoCodeRows(ctx, read, write, item.name, filter, args); err != nil {
			return err
		}
	}
	for _, item := range schema {
		if item.kind != "table" {
			if _, err := write.ExecContext(ctx, item.sql); err != nil {
				return err
			}
		}
	}
	columns, err := mimocodeImportColumns(ctx, read, "session")
	if err != nil {
		return err
	}
	update := "UPDATE session SET directory = ?"
	if slices.Contains(columns, "workspace_id") {
		update += ", workspace_id = NULL"
	}
	if _, err := write.ExecContext(ctx, update+" WHERE id = ?", cwd, id); err != nil {
		return err
	}
	return write.Commit()
}

func copyMiMoCodeRows(ctx context.Context, read, write *sql.Tx, table, filter string, args []any) error {
	quoted := `"` + strings.ReplaceAll(table, `"`, `""`) + `"`
	rows, err := read.QueryContext(ctx, "SELECT * FROM "+quoted+filter, args...)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	columns, err := rows.Columns()
	if err != nil {
		return err
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(columns)), ",")
	for rows.Next() {
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			return err
		}
		if _, err := write.ExecContext(ctx, "INSERT INTO "+quoted+" VALUES ("+placeholders+")", values...); err != nil {
			return fmt.Errorf("copy mimocode %s: %w", table, err)
		}
	}
	return rows.Err()
}

func mimocodeImportColumns(ctx context.Context, read *sql.Tx, table string) ([]string, error) {
	quoted := `"` + strings.ReplaceAll(table, `"`, `""`) + `"`
	rows, err := read.QueryContext(ctx, "SELECT * FROM "+quoted+" LIMIT 0")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return rows.Columns()
}
