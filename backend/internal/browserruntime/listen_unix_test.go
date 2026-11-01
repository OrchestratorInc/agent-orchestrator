//go:build !windows

package browserruntime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestListenUnixUsesShortAliasForLongDataPath(t *testing.T) {
	runtimeDir := filepath.Join(t.TempDir(), strings.Repeat("long-data-directory-", 6))
	runFilePath := filepath.Join(runtimeDir, "running.json")
	// t.TempDir inherits TMPDIR, which can itself exceed sockaddr_un's limit in
	// a normal AO worktree. Use the same deliberately short spelling as Listen.
	aliasRoot, err := os.MkdirTemp("/tmp", "ao-br-test-")
	if err != nil {
		t.Fatalf("create short alias root: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(aliasRoot) })

	ln, address, err := listenUnix(runFilePath, aliasRoot, "browser.sock")
	if err != nil {
		t.Fatalf("listenUnix: %v", err)
	}
	aliasPath := filepath.Dir(address)
	if len([]byte(address)) > maxUnixSocketPathBytes {
		t.Fatalf("socket path = %d bytes, maximum %d: %q", len([]byte(address)), maxUnixSocketPathBytes, address)
	}
	if strings.Contains(address, runtimeDir) {
		t.Fatalf("socket address retained long runtime path: %q", address)
	}
	if target, err := os.Readlink(aliasPath); err != nil || target != runtimeDir {
		t.Fatalf("alias target = %q, err=%v; want %q", target, err, runtimeDir)
	}

	if err := ln.Close(); err != nil {
		t.Fatalf("close listener: %v", err)
	}
	if _, err := os.Lstat(aliasPath); !os.IsNotExist(err) {
		t.Fatalf("runtime alias was not removed on close: %v", err)
	}
}

func TestCleanupStaleRuntimeAliasesRemovesOnlyDeadOwnedLinks(t *testing.T) {
	root, err := os.MkdirTemp("/tmp", "ao-br-cleanup-test-")
	if err != nil {
		t.Fatalf("create short alias root: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	runtimeDir := filepath.Join(t.TempDir(), "runtime")
	foreignDir := filepath.Join(t.TempDir(), "runtime")
	if err := os.MkdirAll(runtimeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(foreignDir, 0o700); err != nil {
		t.Fatal(err)
	}
	links := map[string]string{
		"ao-brd-101-aaaaaaaaaaaaaaaa": runtimeDir,
		"ao-brd-202-bbbbbbbbbbbbbbbb": runtimeDir,
		"ao-brd-303-cccccccccccccccc": foreignDir,
		"ao-br-not-owned":             runtimeDir,
	}
	for name, target := range links {
		if err := os.Symlink(target, filepath.Join(root, name)); err != nil {
			t.Fatalf("create alias %s: %v", name, err)
		}
	}

	cleanupStaleRuntimeAliases(root, runtimeDir, func(pid int) bool { return pid == 202 })

	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(entries))
	for _, entry := range entries {
		got = append(got, entry.Name())
	}
	want := []string{
		"ao-br-not-owned",
		"ao-brd-202-bbbbbbbbbbbbbbbb",
		"ao-brd-303-cccccccccccccccc",
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("remaining aliases = %v, want %v", got, want)
	}
}
