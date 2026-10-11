package workerexec

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
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
