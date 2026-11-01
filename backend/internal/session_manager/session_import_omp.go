package sessionmanager

import (
	"context"
	"io/fs"
	"path/filepath"
	"strings"
)

// ompSessionLocked looks for OMP's per-transcript OS lock. A released lock file
// can survive its owner, so only an actively held lock is ownership evidence.
// Search both the original cwd folder and any AO copy by the same native id.
func ompSessionLocked(ctx context.Context, configDir, nativeID string) bool {
	if strings.TrimSpace(configDir) == "" || nativeID == "" {
		return false
	}
	held := false
	suffix := "_" + nativeID + ".jsonl.lock.os"
	_ = filepath.WalkDir(filepath.Join(configDir, "sessions"), func(path string, entry fs.DirEntry, err error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err != nil {
			return nil //nolint:nilerr // Unreadable paths do not prove another owner exists.
		}
		if entry.Type().IsRegular() && strings.HasPrefix(entry.Name(), ".") && strings.HasSuffix(entry.Name(), suffix) && providerLockHeld(path) {
			held = true
			return fs.SkipAll
		}
		return nil
	})
	return held
}
