//go:build !windows

package project_test

import (
	"context"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	projectsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/project"
)

func cloneTestGit(t *testing.T, cloneScript string) string {
	t.Helper()
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	count := filepath.Join(dir, "attempts")
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }
	script := "#!/bin/sh\nif [ \"$1\" = clone ]; then\n" +
		"n=0; if [ -f " + quote(count) + " ]; then n=$(cat " + quote(count) + "); fi\n" +
		"n=$((n+1)); echo \"$n\" > " + quote(count) + "\n" + cloneScript + "\nfi\nexec " + quote(realGit) + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return count
}

func TestCloneRetriesTemporaryFailureWithoutChangingDestination(t *testing.T) {
	m := newManager(t)
	source := gitRepo(t)
	count := cloneTestGit(t, "if [ \"$n\" -lt 3 ]; then echo 'fatal: connection reset' >&2; exit 128; fi")
	parent := t.TempDir()
	result, err := m.PrepareClone(t.Context(), projectsvc.CloneInput{RemoteURL: (&url.URL{Scheme: "file", Path: source}).String(), DestinationParent: parent})
	if err != nil {
		t.Fatal(err)
	}
	if result.Path != filepath.Join(parent, filepath.Base(source)) {
		t.Fatalf("unexpected destination: %s", result.Path)
	}
	if data, _ := os.ReadFile(count); strings.TrimSpace(string(data)) != "3" {
		t.Fatalf("attempts = %s, want 3", data)
	}
	if temporary, _ := filepath.Glob(filepath.Join(parent, ".ao-clone-*")); len(temporary) != 0 {
		t.Fatalf("left temporary checkouts: %v", temporary)
	}
}

func TestCloneDoesNotRetryAuthenticationFailure(t *testing.T) {
	m := newManager(t)
	source := gitRepo(t)
	count := cloneTestGit(t, "echo 'fatal: Authentication failed for https://user:secret@example.test' >&2; exit 128")
	_, err := m.PrepareClone(t.Context(), projectsvc.CloneInput{RemoteURL: (&url.URL{Scheme: "file", Path: source}).String(), DestinationParent: t.TempDir()})
	wantCode(t, err, "GIT_CLONE_FAILED")
	if strings.Contains(err.Error(), "secret") {
		t.Fatal("Git output leaked into error")
	}
	if data, _ := os.ReadFile(count); strings.TrimSpace(string(data)) != "1" {
		t.Fatalf("attempts = %s, want 1", data)
	}
}

func TestCloneStopsAfterThreeNetworkFailures(t *testing.T) {
	m := newManager(t)
	source := gitRepo(t)
	count := cloneTestGit(t, "echo 'fatal: The requested URL returned error: 503' >&2; exit 128")
	parent := t.TempDir()
	_, err := m.PrepareClone(t.Context(), projectsvc.CloneInput{RemoteURL: (&url.URL{Scheme: "file", Path: source}).String(), DestinationParent: parent})
	wantCode(t, err, "GIT_CLONE_NETWORK_FAILED")
	if data, _ := os.ReadFile(count); strings.TrimSpace(string(data)) != "3" {
		t.Fatalf("attempts = %s, want 3", data)
	}
	if entries, _ := os.ReadDir(parent); len(entries) != 0 {
		t.Fatalf("failed clone left files: %v", entries)
	}
}

func TestCloneCancellationStopsRetryAndRemovesStaging(t *testing.T) {
	m := newManager(t)
	source := gitRepo(t)
	count := cloneTestGit(t, "echo 'fatal: connection reset' >&2; exit 128")
	parent := t.TempDir()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := m.PrepareClone(ctx, projectsvc.CloneInput{RemoteURL: (&url.URL{Scheme: "file", Path: source}).String(), DestinationParent: parent})
		done <- err
	}()
	deadline := time.After(5 * time.Second)
	for {
		if _, err := os.Stat(count); err == nil {
			break
		}
		select {
		case <-deadline:
			t.Fatal("clone did not start")
		case <-time.After(10 * time.Millisecond):
		}
	}
	cancel()
	select {
	case err := <-done:
		wantCode(t, err, "GIT_CLONE_CANCELLED")
	case <-time.After(5 * time.Second):
		t.Fatal("cancel did not stop cloning")
	}
	if entries, _ := os.ReadDir(parent); len(entries) != 0 {
		t.Fatalf("cancel left files: %v", entries)
	}
}
