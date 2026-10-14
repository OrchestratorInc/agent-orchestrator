package hooksjson

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func confineManager() Manager {
	return Manager{
		Label:         "test",
		CommandPrefix: "ao hooks test ",
		Timeout:       30,
		Path: func(ws string) string {
			return filepath.Join(ws, ".claude", "settings.local.json")
		},
		Managed: []HookSpec{{Event: "Notification", Command: "ao hooks test notification"}},
	}
}

func skipIfNoSymlinks(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on windows")
	}
}

func TestInstallDoesNotFollowSymlinkedSettingsFile(t *testing.T) {
	skipIfNoSymlinks(t)
	ws, outside := t.TempDir(), t.TempDir()
	secret := filepath.Join(outside, "host.json")
	if err := os.WriteFile(secret, []byte(`{"secret":"S"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(ws, ".claude"), 0o750); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(ws, ".claude", "settings.local.json")
	if err := os.Symlink(secret, target); err != nil {
		t.Fatal(err)
	}

	if err := confineManager().Install(t.Context(), ws); err != nil {
		t.Fatal(err)
	}

	if got, _ := os.ReadFile(secret); string(got) != `{"secret":"S"}` {
		t.Fatalf("outside file modified: %q", got)
	}
	fi, err := os.Lstat(target)
	if err != nil || !fi.Mode().IsRegular() {
		t.Fatalf("worktree settings not a regular file: %v %v", fi, err)
	}
	got, _ := os.ReadFile(target)
	if strings.Contains(string(got), `"S"`) || !strings.Contains(string(got), "ao hooks test notification") {
		t.Fatalf("unexpected settings content: %s", got)
	}
}

func TestInstallDoesNotFollowSymlinkedSettingsDir(t *testing.T) {
	skipIfNoSymlinks(t)
	ws, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(ws, ".claude")); err != nil {
		t.Fatal(err)
	}

	if err := confineManager().Install(t.Context(), ws); err != nil {
		t.Fatal(err)
	}

	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Fatalf("outside dir was written to: %v", entries)
	}
	fi, err := os.Lstat(filepath.Join(ws, ".claude"))
	if err != nil || !fi.IsDir() {
		t.Fatalf(".claude is not a real dir: %v %v", fi, err)
	}
	if _, err := os.Lstat(filepath.Join(ws, ".claude", "settings.local.json")); err != nil {
		t.Fatal(err)
	}
}

func TestInstallReplacesEmptyDirSettingsFileAndRejectsNonEmpty(t *testing.T) {
	ws := t.TempDir()
	target := filepath.Join(ws, ".claude", "settings.local.json")
	if err := os.MkdirAll(target, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := confineManager().Install(t.Context(), ws); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(target); err != nil || !fi.Mode().IsRegular() {
		t.Fatalf("empty dir not replaced: %v %v", fi, err)
	}

	ws2 := t.TempDir()
	target2 := filepath.Join(ws2, ".claude", "settings.local.json")
	if err := os.MkdirAll(target2, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target2, "keep"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := confineManager().Install(t.Context(), ws2); err == nil {
		t.Fatal("expected an error for a non-empty directory")
	}
	if _, err := os.Stat(filepath.Join(target2, "keep")); err != nil {
		t.Fatalf("non-empty dir contents lost: %v", err)
	}
}

func TestUninstallAndDetectDoNotFollowSymlinks(t *testing.T) {
	skipIfNoSymlinks(t)
	managed := `{"hooks":{"Notification":[{"hooks":[{"type":"command","command":"ao hooks test notification"}]}]}}`

	// Symlinked settings file pointing at a host file carrying AO hooks.
	ws, outside := t.TempDir(), t.TempDir()
	host := filepath.Join(outside, "host.json")
	if err := os.WriteFile(host, []byte(managed), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(ws, ".claude"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(host, filepath.Join(ws, ".claude", "settings.local.json")); err != nil {
		t.Fatal(err)
	}
	m := confineManager()
	if ok, err := m.AreInstalled(t.Context(), ws); err != nil || ok {
		t.Fatalf("AreInstalled through symlink = %v, %v; want false, nil", ok, err)
	}
	if err := m.Uninstall(t.Context(), ws); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(host); string(got) != managed {
		t.Fatalf("outside file modified by uninstall: %q", got)
	}

	// Symlinked settings dir pointing at an outside dir carrying AO hooks.
	ws2, outside2 := t.TempDir(), t.TempDir()
	hostFile := filepath.Join(outside2, "settings.local.json")
	if err := os.WriteFile(hostFile, []byte(managed), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside2, filepath.Join(ws2, ".claude")); err != nil {
		t.Fatal(err)
	}
	if ok, err := m.AreInstalled(t.Context(), ws2); err != nil || ok {
		t.Fatalf("AreInstalled through symlinked dir = %v, %v; want false, nil", ok, err)
	}
	if err := m.Uninstall(t.Context(), ws2); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(hostFile); string(got) != managed {
		t.Fatalf("outside file modified by uninstall: %q", got)
	}
}

func TestUninstallMissingFileIsNoOp(t *testing.T) {
	ws := t.TempDir()
	if err := confineManager().Uninstall(t.Context(), ws); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(ws, ".claude")); !os.IsNotExist(err) {
		t.Fatalf("uninstall created files: %v", err)
	}
	if err := confineManager().Uninstall(t.Context(), filepath.Join(ws, "missing")); err != nil {
		t.Fatal(err)
	}
}

func TestInstallRejectsPathOutsideWorkspace(t *testing.T) {
	ws, other := t.TempDir(), t.TempDir()
	m := confineManager()
	m.Path = func(string) string { return filepath.Join(other, "settings.json") }
	if err := m.Install(t.Context(), ws); err == nil {
		t.Fatal("expected error for hooks path outside the workspace")
	}
	if entries, _ := os.ReadDir(other); len(entries) != 0 {
		t.Fatalf("wrote outside workspace: %v", entries)
	}
}
