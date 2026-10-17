package hookutil

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestEnsureWorkspaceGitignoreDoesNotFollowSymlinkedGitignore(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on windows")
	}
	dir, outside := t.TempDir(), t.TempDir()
	host := filepath.Join(outside, "host")
	if err := os.WriteFile(host, []byte(GitignoreSentinel+"\nHOST\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(host, filepath.Join(dir, ".gitignore")); err != nil {
		t.Fatal(err)
	}
	if err := EnsureWorkspaceGitignore(dir, "hooks.json"); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(host); string(got) != GitignoreSentinel+"\nHOST\n" {
		t.Fatalf("outside file modified: %q", got)
	}
	fi, err := os.Lstat(filepath.Join(dir, ".gitignore"))
	if err != nil || !fi.Mode().IsRegular() {
		t.Fatalf(".gitignore not a regular file: %v %v", fi, err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if strings.Contains(string(got), "HOST") || !strings.Contains(string(got), "/hooks.json\n") {
		t.Fatalf("unexpected content %q", got)
	}
}

func TestEnsureWorkspaceGitignoreSkipsSymlinkedDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on windows")
	}
	base, outside := t.TempDir(), t.TempDir()
	link := filepath.Join(base, ".codex")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if err := EnsureWorkspaceGitignore(link, "hooks.json"); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Fatalf("outside dir written to: %v", entries)
	}
}

// A repository that commits .cursor -> config/cursor still gets AO's
// .gitignore, written in the link's in-repo target; the link is kept.
func TestEnsureWorkspaceGitignoreWritesThroughInRepoDirLink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on windows")
	}
	ws := t.TempDir()
	target := filepath.Join(ws, "config", "cursor")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(ws, ".cursor")
	if err := os.Symlink(filepath.Join("config", "cursor"), link); err != nil {
		t.Fatal(err)
	}
	if err := EnsureWorkspaceGitignore(link, "hooks.json"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(target, ".gitignore"))
	if err != nil || !strings.Contains(string(got), GitignoreSentinel) || !strings.Contains(string(got), "/hooks.json\n") {
		t.Fatalf("gitignore in link target = %q, %v", got, err)
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("committed link replaced: %v %v", fi, err)
	}
}

// A settings file committed as a link to another in-repo file is read
// through, so the user's settings survive the merge; one that escapes the
// root is still absent.
func TestReadRootFileFollowsOnlyInRootFileLinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on windows")
	}
	ws, outside := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(ws, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(ws, ".gemini"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "config", "gemini.json"), []byte(`{"user":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "secret.json"), []byte(`{"secret":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("..", "config", "gemini.json"), filepath.Join(ws, ".gemini", "settings.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.json"), filepath.Join(ws, ".gemini", "escape.json")); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(ws)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()

	data, present, err := ReadRootFile(root, filepath.Join(".gemini", "settings.json"), 1<<20)
	if err != nil || !present || string(data) != `{"user":true}` {
		t.Fatalf("in-repo link: data=%q present=%v err=%v, want the target's settings", data, present, err)
	}
	data, present, err = ReadRootFile(root, filepath.Join(".gemini", "escape.json"), 1<<20)
	if err != nil || present || data != nil {
		t.Fatalf("escaping link: data=%q present=%v err=%v, want absent", data, present, err)
	}
}

func TestEnsureGitignoreInRootReplacesSymlinkedDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on windows")
	}
	ws, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(ws, ".claude")); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(ws)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	if err := EnsureGitignoreInRoot(root, ".claude", "settings.local.json"); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Fatalf("outside dir written to: %v", entries)
	}
	fi, err := os.Lstat(filepath.Join(ws, ".claude", ".gitignore"))
	if err != nil || !fi.Mode().IsRegular() {
		t.Fatalf("gitignore not created in real dir: %v %v", fi, err)
	}
}

// EnsureParentDirs keeps a link that resolves to a directory inside the root
// and replaces links that escape, dangle or name a file.
func TestEnsureParentDirsFollowsOnlyInRootDirLinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on windows")
	}
	ws, outside := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(ws, "real"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "file"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	// "hop" escapes but is never repaired, so "chained" escapes through it
	// whatever order the components are checked in.
	if err := os.Symlink(outside, filepath.Join(ws, "hop")); err != nil {
		t.Fatal(err)
	}
	links := map[string]string{
		"inroot":   "real",
		"escapes":  outside,
		"dangling": "missing",
		"tofile":   "file",
		"chained":  "hop",
	}
	for name, dst := range links {
		if err := os.Symlink(dst, filepath.Join(ws, name)); err != nil {
			t.Fatal(err)
		}
	}
	root, err := os.OpenRoot(ws)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	for name := range links {
		if err := EnsureParentDirs(root, filepath.Join(name, "x.json")); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		fi, err := os.Lstat(filepath.Join(ws, name))
		if err != nil {
			t.Fatal(err)
		}
		isLink := fi.Mode()&os.ModeSymlink != 0
		if name == "inroot" && !isLink {
			t.Fatalf("in-root dir link %s was replaced", name)
		}
		if name != "inroot" && (isLink || !fi.IsDir()) {
			t.Fatalf("%s = %v, want a real directory", name, fi.Mode())
		}
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Fatalf("outside dir written to: %v", entries)
	}
}

func TestEnsureWorkspaceGitignoreLeavesOversizedFileUntouched(t *testing.T) {
	dir := t.TempDir()
	big := strings.Repeat("x", gitignoreMaxBytes+10) + GitignoreSentinel
	path := filepath.Join(dir, ".gitignore")
	if err := os.WriteFile(path, []byte(big), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := EnsureWorkspaceGitignore(dir, "hooks.json"); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); string(got) != big {
		t.Fatal("oversized .gitignore was modified")
	}
}
