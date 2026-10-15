package workerexec

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func testClaudeCode(home string, onPath map[string]string, versions map[string]string, install func(context.Context, string, string) error) *claudeCode {
	return &claudeCode{
		lookPath: func(name string) (string, error) {
			if path, ok := onPath[name]; ok {
				return path, nil
			}
			return "", exec.ErrNotFound
		},
		homeDir: func() (string, error) { return home, nil },
		version: func(_ context.Context, path string) (string, error) {
			if version, ok := versions[path]; ok {
				return version, nil
			}
			return "", errors.New("no such executable")
		},
		install: install,
	}
}

func TestClaudeCodeKeepsACurrentWorkspaceInstall(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "usr", "bin", "claude")
	writeExecutable(t, workspace)
	code := testClaudeCode(t.TempDir(),
		map[string]string{"claude": workspace, "npm": "/usr/bin/npm"},
		map[string]string{workspace: "2.1.296"},
		func(context.Context, string, string) error {
			t.Fatal("must not install over a current release")
			return nil
		})
	if path := code.ensure(context.Background()); path != workspace {
		t.Fatalf("ensure = %q, want the workspace install", path)
	}
}

// A workspace image that bakes an older release would offer models the model
// API refuses for that client ("version 2.1.280 or newer is required").
func TestClaudeCodeUpgradesAnOlderInstallIntoItsOwnPrefix(t *testing.T) {
	home := t.TempDir()
	local := filepath.Join(home, ".ao", "tools", "bin", "claude")
	versions := map[string]string{"/usr/bin/claude": "2.1.228"}
	var installs atomic.Int32
	code := testClaudeCode(home,
		map[string]string{"claude": "/usr/bin/claude", "npm": "/usr/bin/npm"},
		versions,
		func(_ context.Context, npm, prefix string) error {
			installs.Add(1)
			if npm != "/usr/bin/npm" || prefix != filepath.Join(home, ".ao", "tools") {
				t.Errorf("install(%q, %q)", npm, prefix)
			}
			writeExecutable(t, local)
			versions[local] = ClaudeCodeVersion
			return nil
		})
	if before := code.current(); before != "claude" {
		t.Fatalf("current before the upgrade = %q, want the bare name", before)
	}
	if path := code.ensure(context.Background()); path != local {
		t.Fatalf("ensure = %q, want the upgraded %q", path, local)
	}
	if path := code.ensure(context.Background()); path != local || installs.Load() != 1 {
		t.Fatalf("second ensure = %q after %d installs, want one install", path, installs.Load())
	}
	if code.current() != local {
		t.Fatalf("current = %q, want the upgraded install", code.current())
	}
}

func TestClaudeCodeReusesAnUpgradeFromAPreviousWorker(t *testing.T) {
	home := t.TempDir()
	local := filepath.Join(home, ".ao", "tools", "bin", "claude")
	writeExecutable(t, local)
	code := testClaudeCode(home,
		map[string]string{"claude": "/usr/bin/claude", "npm": "/usr/bin/npm"},
		map[string]string{"/usr/bin/claude": "2.1.228", local: ClaudeCodeVersion},
		func(context.Context, string, string) error { t.Fatal("must not reinstall"); return nil })
	if path := code.ensure(context.Background()); path != local {
		t.Fatalf("ensure = %q, want the earlier upgrade", path)
	}
}

func TestClaudeCodeFallsBackWhenTheUpgradeFails(t *testing.T) {
	code := testClaudeCode(t.TempDir(),
		map[string]string{"claude": "/usr/bin/claude", "npm": "/usr/bin/npm"},
		map[string]string{"/usr/bin/claude": "2.1.228"},
		func(context.Context, string, string) error { return errors.New("registry unreachable") })
	if path := code.ensure(context.Background()); path != "claude" {
		t.Fatalf("ensure = %q, want the existing install on PATH", path)
	}
	// A failure is not retried on every turn.
	code.install = func(context.Context, string, string) error { t.Fatal("retried immediately"); return nil }
	if path := code.ensure(context.Background()); path != "claude" {
		t.Fatalf("ensure after failure = %q", path)
	}
}

// With npm unreachable, a retry must not hold every Chat turn for the turn
// wait again, and retries back off instead of running once a minute forever.
func TestClaudeCodeRetriesAFailedUpgradeInTheBackgroundWithBackoff(t *testing.T) {
	var installs atomic.Int32
	release := make(chan struct{})
	code := testClaudeCode(t.TempDir(),
		map[string]string{"claude": "/usr/bin/claude", "npm": "/usr/bin/npm"},
		map[string]string{"/usr/bin/claude": "2.1.228"},
		func(context.Context, string, string) error {
			if installs.Add(1) > 1 {
				<-release
			}
			return errors.New("registry unreachable")
		})
	if path := code.ensure(context.Background()); path != "claude" {
		t.Fatalf("first ensure = %q", path)
	}
	code.mu.Lock()
	code.failedAt = time.Now().Add(-claudeCodeRetryAfter)
	code.mu.Unlock()
	returned := make(chan string, 1)
	go func() { returned <- code.ensure(context.Background()) }()
	select {
	case path := <-returned:
		if path != "claude" {
			t.Fatalf("retry ensure = %q", path)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a turn waited on a retry of a failed upgrade")
	}
	close(release)
	deadline := time.Now().Add(2 * time.Second)
	for {
		code.mu.Lock()
		failures, pending := code.failures, code.pending
		code.mu.Unlock()
		if failures == 2 && pending == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("retry did not settle: failures=%d", failures)
		}
		time.Sleep(5 * time.Millisecond)
	}
	// Two failures: the next retry waits twice as long.
	code.mu.Lock()
	code.failedAt = time.Now().Add(-claudeCodeRetryAfter - time.Second)
	code.mu.Unlock()
	_ = code.ensure(context.Background())
	if got := installs.Load(); got != 2 {
		t.Fatalf("installs = %d, want no retry inside the doubled backoff", got)
	}
	if got := claudeCodeRetryBackoff(20); got != claudeCodeRetryMax {
		t.Fatalf("backoff after 20 failures = %s, want the %s cap", got, claudeCodeRetryMax)
	}
}

func TestClaudeCodeEnsureDoesNotOutwaitItsCaller(t *testing.T) {
	release := make(chan struct{})
	code := testClaudeCode(t.TempDir(),
		map[string]string{"claude": "/usr/bin/claude", "npm": "/usr/bin/npm"},
		map[string]string{"/usr/bin/claude": "2.1.228"},
		func(context.Context, string, string) error { <-release; return errors.New("late") })
	defer close(release)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if path := code.ensure(ctx); path != "claude" {
		t.Fatalf("ensure = %q, want the existing install while the upgrade runs", path)
	}
}

func TestCompareReleaseVersions(t *testing.T) {
	cases := []struct {
		left, right string
		want        int
	}{
		{"2.1.228", "2.1.287", -1},
		{"2.1.287", "2.1.287", 0},
		{"2.1.296", "2.1.287", 1},
		{"2.2.0", "2.1.999", 1},
		{"v2.1.287", "2.1.287", 0},
		{"unknown", "2.1.287", -1},
	}
	for _, tc := range cases {
		if got := compareReleaseVersions(tc.left, tc.right); got != tc.want {
			t.Errorf("compareReleaseVersions(%q, %q) = %d, want %d", tc.left, tc.right, got, tc.want)
		}
	}
}
