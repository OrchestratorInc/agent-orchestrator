package crush

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/ports"
)

// preparedCrushCopy recovers the window between native publication and AO's
// identity checkpoint. Its proof lives inside the atomically published DB.
func preparedCrushCopy(ctx context.Context, r ports.ImportedSessionRelocation) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	id, source, err := crushImportIdentity(r)
	if err != nil {
		return false, err
	}
	dir := filepath.Join(r.LaunchCWD, ".crush")
	if info, err := os.Lstat(dir); errors.Is(err, os.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, err
	} else if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return false, errors.New("crush destination must be a real workspace directory")
	}
	target := filepath.Join(dir, "crush.db")
	info, err := os.Lstat(target)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() {
		return false, errors.New("crush destination must be a regular database")
	}
	db, err := crushNativeDB(target)
	if err != nil {
		return false, err
	}
	defer func() { _ = db.Close() }()
	var version int
	var native, original, workspace string
	if err := db.QueryRowContext(ctx, `SELECT version, native_id, source_path, launch_cwd FROM ao_import_provenance WHERE singleton = 1`).Scan(&version, &native, &original, &workspace); err != nil {
		return false, errors.New("existing crush database has no valid AO import proof")
	}
	if version != 1 || native != id || original != filepath.Clean(source) || workspace != filepath.Clean(r.LaunchCWD) {
		return false, errors.New("existing crush database belongs to a different import")
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions WHERE id = ? AND (parent_session_id IS NULL OR parent_session_id = '')`, id).Scan(&count); err != nil {
		return false, err
	}
	if count != 1 {
		return false, errors.New("prepared crush conversation is missing")
	}
	return true, nil
}
