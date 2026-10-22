package workerexec

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func writeExecutable(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func testAdapter(home string, onPath map[string]string, install func(context.Context, string, string) error) *claudeACPAdapter {
	return &claudeACPAdapter{
		lookPath: func(name string) (string, error) {
			if path, ok := onPath[name]; ok {
				return path, nil
			}
			return "", exec.ErrNotFound
		},
		homeDir: func() (string, error) { return home, nil },
		install: install,
	}
}

func TestClaudeACPAdapterPrefersTheWorkspaceInstall(t *testing.T) {
	installed := false
	adapter := testAdapter(t.TempDir(), map[string]string{"claude-agent-acp": "/usr/bin/claude-agent-acp", "npm": "/usr/bin/npm"},
		func(context.Context, string, string) error { installed = true; return nil })
	path, err := adapter.ensure(context.Background())
	if err != nil || path != "/usr/bin/claude-agent-acp" {
		t.Fatalf("ensure = %q, %v", path, err)
	}
	if installed {
		t.Fatal("installed although the workspace already has the adapter")
	}
}

func TestClaudeACPAdapterReusesAPreviousInstall(t *testing.T) {
	home := t.TempDir()
	local := filepath.Join(home, ".ao", "tools", "bin", "claude-agent-acp")
	writeExecutable(t, local)
	adapter := testAdapter(home, map[string]string{"npm": "/usr/bin/npm"},
		func(context.Context, string, string) error { t.Fatal("must not reinstall"); return nil })
	if path, err := adapter.ensure(context.Background()); err != nil || path != local {
		t.Fatalf("ensure = %q, %v", path, err)
	}
}

func TestClaudeACPAdapterInstallsOnceIntoItsOwnPrefix(t *testing.T) {
	home := t.TempDir()
	var installs atomic.Int32
	adapter := testAdapter(home, map[string]string{"npm": "/usr/bin/npm"}, func(_ context.Context, npm, prefix string) error {
		installs.Add(1)
		if npm != "/usr/bin/npm" || prefix != filepath.Join(home, ".ao", "tools") {
			t.Errorf("install(%q, %q)", npm, prefix)
		}
		time.Sleep(50 * time.Millisecond)
		writeExecutable(t, filepath.Join(prefix, "bin", "claude-agent-acp"))
		return nil
	})
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if path, err := adapter.ensure(context.Background()); err != nil || filepath.Base(path) != "claude-agent-acp" {
				t.Errorf("ensure = %q, %v", path, err)
			}
		}()
	}
	wg.Wait()
	if got := installs.Load(); got != 1 {
		t.Fatalf("installs = %d, want 1 for concurrent callers", got)
	}
}

func TestClaudeACPAdapterWithoutNpmIsUnavailable(t *testing.T) {
	adapter := testAdapter(t.TempDir(), map[string]string{}, nil)
	if _, err := adapter.ensure(context.Background()); !errors.Is(err, ErrChatAdapterUnavailable) {
		t.Fatalf("err = %v, want ErrChatAdapterUnavailable", err)
	}
}

func TestClaudeACPAdapterFailedInstallIsUnavailable(t *testing.T) {
	var installs atomic.Int32
	adapter := testAdapter(t.TempDir(), map[string]string{"npm": "/usr/bin/npm"},
		func(context.Context, string, string) error {
			installs.Add(1)
			return errors.New("registry unreachable")
		})
	for i := 0; i < 3; i++ {
		if _, err := adapter.ensure(context.Background()); !errors.Is(err, ErrChatAdapterUnavailable) {
			t.Fatalf("err = %v, want ErrChatAdapterUnavailable", err)
		}
	}
	if got := installs.Load(); got != 1 {
		t.Fatalf("installs = %d, want 1 until the retry delay passes", got)
	}
	adapter.mu.Lock()
	adapter.failedAt = time.Now().Add(-claudeACPRetryAfter)
	adapter.mu.Unlock()
	_, _ = adapter.ensure(context.Background())
	if got := installs.Load(); got != 2 {
		t.Fatalf("installs = %d, want a retry after the delay", got)
	}
}

// A missing npm is looked up again on every call, even within the retry delay
// of an earlier failed install, so a template that adds npm in the background
// is seen by the next caller (the Chat start settle) instead of a cached "no
// npm".
func TestClaudeACPAdapterLooksForNpmAgain(t *testing.T) {
	onPath := map[string]string{}
	var installs atomic.Int32
	adapter := testAdapter(t.TempDir(), onPath, func(_ context.Context, _, prefix string) error {
		installs.Add(1)
		writeExecutable(t, filepath.Join(prefix, "bin", "claude-agent-acp"))
		return nil
	})
	// An earlier install failed moments ago; npm has since gone missing.
	adapter.failedAt = time.Now()
	_, err := adapter.ensure(context.Background())
	if !errors.Is(err, ErrChatAdapterUnavailable) || !strings.Contains(err.Error(), "npm is not installed") {
		t.Fatalf("err = %v, want unavailable without npm", err)
	}
	onPath["npm"] = "/usr/bin/npm"
	if path, err := adapter.ensure(context.Background()); err != nil || filepath.Base(path) != "claude-agent-acp" {
		t.Fatalf("ensure after npm appeared = %q, %v", path, err)
	}
	if got := installs.Load(); got != 1 {
		t.Fatalf("installs = %d, want 1", got)
	}
}

// A short request deadline reports "installing" but does not cancel the install,
// so the next request finds the adapter.
func TestClaudeACPAdapterSlowInstallOutlivesARequestDeadline(t *testing.T) {
	home := t.TempDir()
	release := make(chan struct{})
	adapter := testAdapter(home, map[string]string{"npm": "/usr/bin/npm"}, func(ctx context.Context, _, prefix string) error {
		<-release
		if ctx.Err() != nil {
			return ctx.Err()
		}
		writeExecutable(t, filepath.Join(prefix, "bin", "claude-agent-acp"))
		return nil
	})
	short, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := adapter.ensure(short); !errors.Is(err, ErrChatAdapterInstalling) {
		t.Fatalf("err = %v, want ErrChatAdapterInstalling", err)
	}
	close(release)
	if path, err := adapter.ensure(context.Background()); err != nil || filepath.Base(path) != "claude-agent-acp" {
		t.Fatalf("after install: %q, %v", path, err)
	}
}

// A session about to start in Chat asks, without waiting, whether Claude's
// Chat can run: an adapter on PATH or an install that has started can, a
// workspace without the adapter or npm cannot.
func TestClaudeACPAdapterStartable(t *testing.T) {
	onPath := testAdapter(t.TempDir(), map[string]string{"claude-agent-acp": "/usr/bin/claude-agent-acp"}, nil)
	if err := onPath.startable(); err != nil {
		t.Fatalf("adapter on PATH: %v", err)
	}
	noNpm := testAdapter(t.TempDir(), map[string]string{}, nil)
	if err := noNpm.startable(); !errors.Is(err, ErrChatAdapterUnavailable) {
		t.Fatalf("no npm: err = %v, want ErrChatAdapterUnavailable", err)
	}
	release := make(chan struct{})
	installing := testAdapter(t.TempDir(), map[string]string{"npm": "/usr/bin/npm"}, func(_ context.Context, _, prefix string) error {
		<-release
		writeExecutable(t, filepath.Join(prefix, "bin", "claude-agent-acp"))
		return nil
	})
	if err := installing.startable(); err != nil {
		t.Fatalf("install in progress: %v", err)
	}
	close(release)
	if path, err := installing.ensure(context.Background()); err != nil || filepath.Base(path) != "claude-agent-acp" {
		t.Fatalf("after install: %q, %v", path, err)
	}
}

// The worker installs the pinned adapter into its own tools prefix with the
// workspace's npm, without root and without touching global packages.
func TestNpmInstallClaudeACPAdapterUsesThePinnedPackageAndPrefix(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args")
	npm := filepath.Join(dir, "npm")
	// The fake npm records its arguments and creates the binary npm would link
	// under --prefix/bin.
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + argsFile + "\n" +
		"prefix=''\nwhile [ $# -gt 0 ]; do if [ \"$1\" = --prefix ]; then prefix=$2; fi; shift; done\n" +
		"mkdir -p \"$prefix/bin\" && printf '#!/bin/sh\\n' > \"$prefix/bin/claude-agent-acp\" && chmod +x \"$prefix/bin/claude-agent-acp\"\n"
	if err := os.WriteFile(npm, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	adapter := &claudeACPAdapter{
		lookPath: func(name string) (string, error) {
			if name == "npm" {
				return npm, nil
			}
			return "", exec.ErrNotFound
		},
		homeDir: func() (string, error) { return home, nil },
		install: npmInstallClaudeACPAdapter,
	}
	path, err := adapter.ensure(context.Background())
	want := filepath.Join(home, ".ao", "tools", "bin", "claude-agent-acp")
	if err != nil || path != want {
		t.Fatalf("ensure = %q, %v; want %q", path, err, want)
	}
	recorded, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	args := string(recorded)
	for _, part := range []string{"install\n", "--global\n", "--prefix\n" + filepath.Join(home, ".ao", "tools") + "\n",
		claudeACPAdapterPackage + "@" + claudeACPAdapterVersion + "\n"} {
		if !strings.Contains(args, part) {
			t.Fatalf("npm args %q lack %q", args, part)
		}
	}
}
