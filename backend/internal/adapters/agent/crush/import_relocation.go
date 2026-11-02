package crush

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hookutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// RelocateImportedSession copies the selected native conversation to the
// worktree's nearest .crush store. Crush's --session lookup is database-local;
// creating this directory makes its existing cwd/data-directory lookup find
// the copy. All later opens use the same ResumeCwd and must retain AO's turns.
func (p *Plugin) RelocateImportedSession(ctx context.Context, r ports.ImportedSessionRelocation) error {
	id, err := uuid.Parse(r.NativeSessionID)
	if err != nil || !filepath.IsAbs(r.LaunchCWD) || filepath.Base(r.TranscriptPath) != id.String() {
		return errors.New("invalid crush import relocation")
	}
	source := filepath.Dir(r.TranscriptPath)
	if filepath.Base(source) != "crush.db" || !filepath.IsAbs(source) {
		return errors.New("invalid crush source database")
	}
	dir := filepath.Join(r.LaunchCWD, ".crush")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if info, err := os.Lstat(dir); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("crush destination must be a real workspace directory")
	}
	target := filepath.Join(dir, "crush.db")
	if resolved, err := filepath.EvalSymlinks(source); err == nil {
		if parent, err := filepath.EvalSymlinks(dir); err == nil && filepath.Join(parent, "crush.db") == resolved {
			return errors.New("crush import destination is the source database")
		}
	}
	if info, err := os.Lstat(target); err == nil {
		if !info.Mode().IsRegular() {
			return errors.New("crush destination is not a regular database")
		}
		if err := crushCopyHasSession(ctx, target, id.String()); err != nil {
			return err
		}
		return pinCrushImportStore(r.LaunchCWD, dir)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	// A temp file in the destination directory keeps the final rename atomic.
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
	if err := copyCrushConversation(ctx, source, tmp, id.String()); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		return err
	}
	if _, err := os.Stat(target); err == nil {
		if err := crushCopyHasSession(ctx, target, id.String()); err != nil {
			return err
		}
		return pinCrushImportStore(r.LaunchCWD, dir)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Rename(tmp, target); err != nil {
		return err
	}
	return pinCrushImportStore(r.LaunchCWD, dir)
}

// Local config wins over a user-level data_directory override. Preserve the
// hook-installed context and model settings while pinning only this workspace.
func pinCrushImportStore(workspace, dir string) error {
	root, err := os.OpenRoot(workspace)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	config := map[string]any{}
	file, err := root.Open(".crush.json")
	if err == nil {
		err = json.NewDecoder(io.LimitReader(file, 4<<20)).Decode(&config)
		_ = file.Close()
		if err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	options, ok := config["options"].(map[string]any)
	if !ok {
		if value, exists := config["options"]; exists && value != nil {
			return errors.New("crush workspace options must be an object")
		}
		options = map[string]any{}
	}
	options["data_directory"] = dir
	config["options"] = options
	data, err := json.Marshal(config)
	if err != nil {
		return err
	}
	return hookutil.AtomicWriteFile(filepath.Join(workspace, ".crush.json"), data, 0o600)
}

func crushCopyHasSession(ctx context.Context, path, id string) error {
	db, err := crushNativeDB(path)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions WHERE id = ?`, id).Scan(&count); err != nil {
		return err
	}
	if count != 1 {
		return errors.New("existing crush workspace database does not contain the imported conversation")
	}
	return nil
}

func copyCrushConversation(ctx context.Context, source, target, id string) error {
	src, err := crushNativeDB(source)
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
	if err := read.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions WHERE id = ? AND (parent_session_id IS NULL OR parent_session_id = '')`, id).Scan(&exists); err != nil {
		return err
	}
	if exists != 1 {
		return errors.New("crush source conversation is missing")
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
	rows, err := read.QueryContext(ctx, `SELECT type, name, sql FROM sqlite_master WHERE sql IS NOT NULL AND name NOT LIKE 'sqlite_%' ORDER BY rowid`)
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
		case "sessions":
			filter, args = " WHERE id = ?", []any{id}
		case "messages", "files", "read_files":
			filter, args = " WHERE session_id = ?", []any{id}
		case "goose_db_version":
		default:
			continue
		}
		if err := copyCrushRows(ctx, read, write, item.name, filter, args); err != nil {
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
	return write.Commit()
}

func copyCrushRows(ctx context.Context, read, write *sql.Tx, table, filter string, args []any) error {
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
			return fmt.Errorf("copy crush %s: %w", table, err)
		}
	}
	return rows.Err()
}
