package sessionmove

import (
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// isolateGit points git at a throwaway global config so the developer's own
// settings (signing, hooks, identity) cannot change the result.
func isolateGit(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("symlink and file-mode cases cover macOS and Linux")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found")
	}
	home := t.TempDir()
	global := filepath.Join(home, "gitconfig")
	writeFile(t, global, "[user]\n\tname = Test User\n\temail = test@example.com\n"+
		"[init]\n\tdefaultBranch = main\n[protocol \"file\"]\n\tallow = always\n"+
		"[core]\n\tautocrlf = false\n[advice]\n\tdetachedHead = false\n", 0o644)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("GIT_CONFIG_GLOBAL", global)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
}

func gitTry(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.Output()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		err = fmt.Errorf("%w: %s", err, exit.Stderr)
	}
	return strings.TrimSpace(string(out)), err
}

func g(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := gitTry(dir, args...)
	if err != nil {
		t.Fatalf("git %s: %v", strings.Join(args, " "), err)
	}
	return out
}

func writeFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func randomBytes(t *testing.T, n int) string {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return string(b)
}

type fixture struct {
	tmp, origin, src string
	c1, c2           string // c1 is on origin's feat/x, c2 only on src
}

// newFixture makes origin (main) and src on feat/x: c1 pushed, c2 not.
func newFixture(t *testing.T) fixture {
	t.Helper()
	isolateGit(t)
	tmp := t.TempDir()
	f := fixture{tmp: tmp, origin: filepath.Join(tmp, "origin.git"), src: filepath.Join(tmp, "src")}
	g(t, tmp, "init", "-q", "--bare", f.origin)
	seed := filepath.Join(tmp, "seed")
	g(t, tmp, "init", "-q", seed)
	writeFile(t, filepath.Join(seed, "README.md"), "hello\n", 0o644)
	writeFile(t, filepath.Join(seed, "src/a.go"), "package a\n", 0o644)
	writeFile(t, filepath.Join(seed, "src/b.go"), "package b\n", 0o644)
	writeFile(t, filepath.Join(seed, "docs/old.md"), "old\n", 0o644)
	writeFile(t, filepath.Join(seed, "bin/run.sh"), "#!/bin/sh\necho hi\n", 0o755)
	writeFile(t, filepath.Join(seed, "config.yml"), "env: prod\n", 0o644)
	writeFile(t, filepath.Join(seed, ".gitignore"), ".env\n*.log\n", 0o644)
	g(t, seed, "add", "-A")
	g(t, seed, "commit", "-q", "-m", "seed")
	g(t, seed, "push", "-q", f.origin, "main")
	g(t, tmp, "clone", "-q", f.origin, f.src)
	g(t, f.src, "switch", "-q", "-c", "feat/x")
	writeFile(t, filepath.Join(f.src, "src/a.go"), "package a // c1\n", 0o644)
	g(t, f.src, "commit", "-q", "-am", "c1")
	g(t, f.src, "push", "-q", "-u", "origin", "feat/x")
	f.c1 = g(t, f.src, "rev-parse", "HEAD")
	writeFile(t, filepath.Join(f.src, "src/c.go"), "package c\n", 0o644)
	g(t, f.src, "add", "src/c.go")
	g(t, f.src, "commit", "-q", "-m", "c2")
	f.c2 = g(t, f.src, "rev-parse", "HEAD")
	return f
}

// makeDirty leaves every kind of uncommitted work in src and returns the
// paths a caller excludes (an AO project symlink to an absolute path).
func makeDirty(t *testing.T, f fixture) []string {
	t.Helper()
	src := f.src
	writeFile(t, filepath.Join(src, "src/b.go"), "package b // staged\n", 0o644)
	g(t, src, "add", "src/b.go")
	writeFile(t, filepath.Join(src, "src/b.go"), "package b // staged then edited\n", 0o644)
	writeFile(t, filepath.Join(src, "README.md"), "hello\nmore\n", 0o644)
	g(t, src, "rm", "-q", "docs/old.md")
	g(t, src, "mv", "src/a.go", "src/a2.go")
	if err := os.Chmod(filepath.Join(src, "src/c.go"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(src, "notes/new.txt"), "new notes\n", 0o644)
	writeFile(t, filepath.Join(src, "assets/logo.bin"), randomBytes(t, 64<<10), 0o644)
	writeFile(t, filepath.Join(src, ".env"), "SECRET=1\n", 0o644)
	writeFile(t, filepath.Join(src, "debug.log"), "noise\n", 0o644)
	outside := filepath.Join(f.tmp, "outside", "config.local")
	writeFile(t, outside, "local only\n", 0o644)
	if err := os.Symlink(outside, filepath.Join(src, "config.local")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("README.md", filepath.Join(src, "link.txt")); err != nil {
		t.Fatal(err)
	}
	g(t, src, "update-index", "--skip-worktree", "config.yml")
	writeFile(t, filepath.Join(src, "config.yml"), "env: local-dev\n", 0o644)
	return []string{"config.local"}
}

type fileState struct{ kind, data string }

// files maps every file and symlink under root, outside .git, to its kind
// (file, exec, link) and content or link target.
func files(t *testing.T, root string) map[string]fileState {
	t.Helper()
	m := map[string]fileState{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		if rel == ".git" {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		info, err := os.Lstat(p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(p)
			m[rel] = fileState{"link", target}
			return err
		}
		data, err := os.ReadFile(p)
		kind := "file"
		if info.Mode()&0o111 != 0 {
			kind = "exec"
		}
		m[rel] = fileState{kind, string(data)}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// expectedTarget is what the target's checkout must hold after a move of the
// makeDirty state: src's files minus ignored and excluded ones, with the
// skip-worktree file at its committed content.
func expectedTarget(t *testing.T, f fixture) map[string]fileState {
	t.Helper()
	want := files(t, f.src)
	for _, p := range []string{".env", "debug.log", "config.local"} {
		delete(want, p)
	}
	want["config.yml"] = fileState{"file", "env: prod\n"}
	return want
}

func assertSameFiles(t *testing.T, got, want map[string]fileState) {
	t.Helper()
	var diff []string
	for p, w := range want {
		if gv, ok := got[p]; !ok {
			diff = append(diff, "missing "+p)
		} else if gv != w {
			diff = append(diff, fmt.Sprintf("%s: got %s %q, want %s %q", p, gv.kind, short(gv.data), w.kind, short(w.data)))
		}
	}
	for p := range got {
		if _, ok := want[p]; !ok {
			diff = append(diff, "unexpected "+p)
		}
	}
	slices.Sort(diff)
	if len(diff) > 0 {
		t.Fatalf("target files differ from source:\n%s", strings.Join(diff, "\n"))
	}
}

func short(s string) string {
	if len(s) > 40 {
		return fmt.Sprintf("%d bytes", len(s))
	}
	return s
}

type repoState struct{ index, head, refs, status string }

// stateOf fingerprints what a capture must not change: the real index file
// bytes, HEAD, every ref, and git status.
func stateOf(t *testing.T, wt string) repoState {
	t.Helper()
	index := g(t, wt, "rev-parse", "--git-path", "index")
	if !filepath.IsAbs(index) {
		index = filepath.Join(wt, index)
	}
	data, err := os.ReadFile(index)
	if err != nil {
		t.Fatal(err)
	}
	return repoState{
		index:  fmt.Sprintf("%x", sha256.Sum256(data)),
		head:   g(t, wt, "rev-parse", "HEAD"),
		refs:   g(t, wt, "for-each-ref"),
		status: g(t, wt, "--no-optional-locks", "status", "--porcelain=v2"),
	}
}
