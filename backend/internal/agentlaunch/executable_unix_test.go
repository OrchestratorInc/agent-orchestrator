//go:build !windows

package agentlaunch

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUseStableExecutableKeepsDurableInstallsOnTheirOwnPath(t *testing.T) {
	t.Cleanup(func() { stableExecutable.Store(nil) })
	dataDir := t.TempDir()
	if err := UseStableExecutable(dataDir, func(string) string { return "" }); err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if got, err := Executable(); err != nil || got != self {
		t.Fatalf("Executable() = %q, %v; want this process's own path %q", got, err, self)
	}
	if _, err := os.Lstat(filepath.Join(dataDir, "runtime", "ao-current")); !os.IsNotExist(err) {
		t.Fatalf("link directory created outside an AppImage (stat err %v)", err)
	}
}

// TestUseStableExecutableFollowsTheAppImageMount covers an AppImage restart:
// the link a session pinned last launch points at the old, gone mount, and the
// new daemon moves it to its own, so that session reaches the running app.
func TestUseStableExecutableFollowsTheAppImageMount(t *testing.T) {
	t.Cleanup(func() { stableExecutable.Store(nil) })
	dataDir := t.TempDir()
	link := filepath.Join(dataDir, "runtime", "ao-current", "ao")
	if err := os.MkdirAll(filepath.Dir(link), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/tmp/.mount_OldRun/resources/daemon/ao", link); err != nil {
		t.Fatal(err)
	}
	appImage := func(key string) string { return map[string]string{"AO_APPIMAGE": "/home/me/AO.AppImage"}[key] }
	if err := UseStableExecutable(dataDir, appImage); err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if target, err := os.Readlink(link); err != nil || target != self {
		t.Fatalf("%s -> %q (%v), want this mount's ao %q", link, target, err, self)
	}
	if got, err := Executable(); err != nil || got != link {
		t.Fatalf("Executable() = %q, %v; want the stable link %q", got, err, link)
	}
	// Sessions pin the link's directory, which holds nothing but ao.
	if got := PinnedDir(Executable, dataDir); got != filepath.Dir(link) {
		t.Fatalf("PinnedDir = %q, want %q", got, filepath.Dir(link))
	}
	entries, err := os.ReadDir(filepath.Dir(link))
	if err != nil || len(entries) != 1 {
		t.Fatalf("link directory holds %v (%v), want only ao", entries, err)
	}
}
