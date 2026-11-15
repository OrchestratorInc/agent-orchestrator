//go:build !windows

package session

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// On a Unix daemon a Windows path is not absolute to filepath, so without an
// explicit check it would be cleaned into a relative path and reported as a
// workspace file.
func TestResolveWorkspacePathTreatsWindowsAbsolutePathsAsOutside(t *testing.T) {
	svc, workspace, _ := resolveFixture(t)
	// A real relative file that a mis-classified drive path would land on.
	if err := os.MkdirAll(filepath.Join(workspace, "C:", "repo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "C:", "repo", "file.ts"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{`C:/repo/file.ts`, `C:\repo\file.ts`, `c:/repo/file.ts`, `\\server\share\file.ts`, `//server/share/file.ts`} {
		got, err := svc.ResolveWorkspacePath(context.Background(), "ao-1", path)
		if err != nil {
			t.Fatalf("ResolveWorkspacePath(%q): %v", path, err)
		}
		if got.Kind != WorkspacePathKindOutside || got.Path != "" {
			t.Fatalf("ResolveWorkspacePath(%q) = {%s %q}, want outside", path, got.Kind, got.Path)
		}
	}
}
