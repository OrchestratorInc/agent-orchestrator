package agentlaunch

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"sync/atomic"
)

var stableExecutable atomic.Pointer[string]

func Executable() (string, error) {
	if link := stableExecutable.Load(); link != nil {
		return *link, nil
	}
	return os.Executable()
}

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
