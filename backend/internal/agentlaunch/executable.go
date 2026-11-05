package agentlaunch

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"sync/atomic"
)

// stableExecutable is the ao AO-owned commands run instead of this process's
// own path when that path dies with the app: an AppImage mounts the app under
// a new /tmp/.mount_* directory on every launch, so a session that outlives an
// app restart would otherwise call a binary that no longer exists.
var stableExecutable atomic.Pointer[string]

// Executable returns the ao that sessions, hooks and terminals should run:
// the link UseStableExecutable keeps, or else this process's executable.
func Executable() (string, error) {
	if link := stableExecutable.Load(); link != nil {
		return *link, nil
	}
	return os.Executable()
}

// UseStableExecutable makes Executable return <dataDir>/runtime/ao-current/ao
// when this daemon runs from an AppImage (the desktop sets AO_APPIMAGE), and
// points that link at the current mount. Every start moves it, so sessions
// started before a restart keep reaching the running app's ao. Anywhere else
// Executable returns this process's own path.
func UseStableExecutable(dataDir string, getenv func(string) string) error {
	if getenv("AO_APPIMAGE") == "" {
		stableExecutable.Store(nil)
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	link := filepath.Join(dataDir, "runtime", "ao-current", aoBinaryName)
	if err := os.MkdirAll(filepath.Dir(link), 0o700); err != nil {
		return err
	}
	if current, err := os.Readlink(link); err != nil || current != exe {
		if err := swapSymlink(link, exe); err != nil {
			return err
		}
	}
	stableExecutable.Store(&link)
	return nil
}

// swapSymlink atomically replaces link with a symlink to target.
func swapSymlink(link, target string) error {
	suffix := make([]byte, 8)
	if _, err := rand.Read(suffix); err != nil {
		return err
	}
	temp := filepath.Join(filepath.Dir(link), ".ao-link-"+hex.EncodeToString(suffix))
	if err := os.Symlink(target, temp); err != nil {
		return err
	}
	if err := os.Rename(temp, link); err != nil {
		_ = os.Remove(temp)
		return err
	}
	return nil
}
