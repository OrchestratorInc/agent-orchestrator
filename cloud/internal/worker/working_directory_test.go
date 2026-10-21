package worker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRepositoryName(t *testing.T) {
	t.Parallel()
	for url, want := range map[string]string{
		"https://github.com/11x-ai/alice":     "alice",
		"https://github.com/11x-ai/Alice.git": "Alice",
		"git@github.com:acme/app.git":         "app",
		"https://github.com/acme/app/":        "app",
		"":                                    "",
		"not a url":                           "",
		"https://x-access-token:s@github.com/a/b.git": "b",
	} {
		if got := RepositoryName(url); got != want {
			t.Errorf("RepositoryName(%q) = %q, want %q", url, got, want)
		}
	}
}

func TestExpandWorkingDirectory(t *testing.T) {
	t.Parallel()
	const repo = "https://github.com/11x-ai/alice"
	for template, want := range map[string]string{
		"~/work/{repo}":       "/home/ubuntu/work/alice",
		"$HOME/work/{repo}":   "/home/ubuntu/work/alice",
		"/srv/{repo}/main/":   "/srv/alice/main",
		"/home/ubuntu/stacks": "/home/ubuntu/stacks",
	} {
		got, err := ExpandWorkingDirectory(template, "/home/ubuntu", repo)
		if err != nil || got != want {
			t.Errorf("ExpandWorkingDirectory(%q) = %q, %v; want %q", template, got, err, want)
		}
	}
	for _, template := range []string{"", "work/{repo}", "/srv/../etc", "/"} {
		if got, err := ExpandWorkingDirectory(template, "/home/ubuntu", repo); err == nil {
			t.Errorf("ExpandWorkingDirectory(%q) = %q, want an error", template, got)
		}
	}
	if _, err := ExpandWorkingDirectory("~/work", "", repo); err == nil {
		t.Error("a home-relative directory resolved without a home")
	}
	if _, err := ExpandWorkingDirectory("~/work/{repo}", "/home/ubuntu", ""); err == nil {
		t.Error("{repo} resolved without a repository")
	}
}

// The template's checkout at the configured directory is adopted onto the
// session branch, and a restarted worker keeps it without switching again.
func TestResolveWorkingDirectoryAdoptsMatchingCheckout(t *testing.T) {
	t.Parallel()
	requireGit(t)
	upstream := upstreamRepository(t)
	home := t.TempDir()
	checkout := filepath.Join(home, "work", "app")
	templateCheckout(t, upstream, checkout, "git@github.com:acme/app.git")
	// A second checkout of the same repository the heuristic search would
	// find first must be ignored: the configured directory wins.
	templateCheckout(t, upstream, filepath.Join(home, "work", "aaa"), "https://github.com/acme/app")
	dataDir := filepath.Join(home, ".ao", "worker")

	decision, err := ResolveWorkingDirectory(context.Background(), ExecGitRunner{}, "~/work/{repo}", home, dataDir,
		"https://github.com/acme/app", "session-1", "ao/session-1", "main")
	if err != nil || !decision.Reused || !decision.AdoptedNow || decision.Workspace != checkout {
		t.Fatalf("decision = %+v, %v", decision, err)
	}
	if branch := gitOutput(t, checkout, "symbolic-ref", "--short", "HEAD"); branch != "ao/session-1" {
		t.Fatalf("branch = %q", branch)
	}
	restart, err := ResolveWorkingDirectory(context.Background(), ExecGitRunner{}, "~/work/{repo}", home, dataDir,
		"https://github.com/acme/app", "session-1", "ao/session-1", "main")
	if err != nil || !restart.Reused || restart.AdoptedNow || restart.Workspace != checkout {
		t.Fatalf("restart = %+v, %v", restart, err)
	}
}

// A missing or empty directory gets AO's own clone there, remembered across
// worker restarts even once the clone exists.
func TestResolveWorkingDirectoryClonesIntoMissingOrEmptyDirectory(t *testing.T) {
	t.Parallel()
	for name, prepare := range map[string]func(string) error{
		"missing": func(string) error { return nil },
		"empty":   func(dir string) error { return os.MkdirAll(dir, 0o755) },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			home := t.TempDir()
			dir := filepath.Join(home, "work", "app")
			if err := prepare(dir); err != nil {
				t.Fatal(err)
			}
			dataDir := filepath.Join(home, ".ao", "worker")
			decision, err := ResolveWorkingDirectory(context.Background(), ExecGitRunner{}, "~/work/{repo}", home, dataDir,
				"https://github.com/acme/app", "session-1", "ao/session-1", "main")
			if err != nil || decision.Reused || decision.Workspace != dir {
				t.Fatalf("decision = %+v, %v", decision, err)
			}
			// AO's clone now exists; a restart still treats it as AO's own.
			if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
				t.Fatal(err)
			}
			restart, err := ResolveWorkingDirectory(context.Background(), ExecGitRunner{}, "~/work/{repo}", home, dataDir,
				"https://github.com/acme/app", "session-1", "ao/session-1", "main")
			if err != nil || restart.Reused || restart.Workspace != dir {
				t.Fatalf("restart = %+v, %v", restart, err)
			}
		})
	}
}

// A configured directory that holds something else fails clearly instead of
// silently working somewhere the template did not ask for.
func TestResolveWorkingDirectoryRefusesUnrelatedDirectory(t *testing.T) {
	t.Parallel()
	requireGit(t)
	upstream := upstreamRepository(t)
	home := t.TempDir()
	notes := filepath.Join(home, "work", "notes")
	if err := os.MkdirAll(notes, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(notes, "todo.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(home, "work", "other")
	templateCheckout(t, upstream, other, "https://github.com/acme/other.git")
	for template, dir := range map[string]string{"~/work/notes": notes, "~/work/other": other} {
		_, err := ResolveWorkingDirectory(context.Background(), ExecGitRunner{}, template, home,
			filepath.Join(home, ".ao", "worker-"+filepath.Base(dir)),
			"https://github.com/acme/app", "session-1", "ao/session-1", "main")
		if !errors.Is(err, ErrWorkingDirectoryUnusable) {
			t.Errorf("%s: err = %v, want ErrWorkingDirectoryUnusable", template, err)
		}
	}
}

// A dedicated-user worker uses a working folder it owns (the one the
// bootstrap created, or this session's checkout from an earlier start) and
// creates a missing one. A folder owned by someone else, a symbolic link or a
// file is occupied: the worker never takes it over or falls back.
func TestResolveOwnedCheckoutDirectory(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	uid := os.Getuid()

	missing := filepath.Join(home, "work", "alice")
	if err := resolveOwnedCheckoutDirectory(missing, home, uid); err != nil {
		t.Fatalf("missing folder: %v", err)
	}
	if info, err := os.Stat(missing); err != nil || !info.IsDir() {
		t.Fatalf("missing folder was not created: %v", err)
	}
	// A restart finds the session's own checkout, owned by the worker user.
	if err := os.MkdirAll(filepath.Join(missing, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := resolveOwnedCheckoutDirectory(missing, home, uid); err != nil {
		t.Fatalf("own checkout on restart: %v", err)
	}

	// The template's checkout belongs to the login user, not the worker.
	err := resolveOwnedCheckoutDirectory(missing, home, uid+1)
	if !errors.Is(err, ErrWorkingDirectoryOccupied) || !strings.HasSuffix(err.Error(), ": ~/work/alice") {
		t.Fatalf("someone else's folder = %v, want occupied ~/work/alice", err)
	}

	link := filepath.Join(home, "work", "link")
	if err := os.Symlink(missing, link); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(home, "work", "file")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{link, file} {
		if err := resolveOwnedCheckoutDirectory(dir, home, uid); !errors.Is(err, ErrWorkingDirectoryOccupied) {
			t.Errorf("%s = %v, want occupied", dir, err)
		}
	}

	locked := filepath.Join(home, "locked")
	if err := os.Mkdir(locked, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })
	if os.Getuid() != 0 {
		if err := resolveOwnedCheckoutDirectory(filepath.Join(locked, "alice"), home, uid); !errors.Is(err, ErrCheckoutDirectoryUnusable) ||
			!strings.Contains(err.Error(), "~/locked/alice does not exist and AO could not create it") {
			t.Errorf("uncreatable folder = %v, want unusable", err)
		}
	}
	if err := resolveOwnedCheckoutDirectory("work/alice", home, uid); !errors.Is(err, ErrCheckoutDirectoryUnusable) {
		t.Errorf("relative folder = %v, want unusable", err)
	}
}

func TestDisplayPath(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ dir, home, want string }{
		{"/home/ubuntu/work/alice", "/home/ubuntu", "~/work/alice"},
		{"/home/ubuntu/work/alice", "/home/ubuntu/", "~/work/alice"},
		{"/home/ubuntuX/work", "/home/ubuntu", "/home/ubuntuX/work"},
		{"/srv/work/alice", "", "/srv/work/alice"},
		{"/srv/work/alice", "/", "/srv/work/alice"},
	} {
		if got := DisplayPath(tc.dir, tc.home); got != tc.want {
			t.Errorf("DisplayPath(%q, %q) = %q, want %q", tc.dir, tc.home, got, tc.want)
		}
	}
}
