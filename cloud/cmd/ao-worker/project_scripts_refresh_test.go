package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
)

// projectScriptsServer is a control plane whose /worker/reconnect returns the
// project's current Setup and Cleanup commands, or fails when failing is set.
func projectScriptsServer(t *testing.T, postCreate, preRemove []string, failing *atomic.Bool) *client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/worker/reconnect":
			if failing != nil && failing.Load() {
				http.Error(w, `{"error":"unavailable"}`, http.StatusServiceUnavailable)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(worker.BootstrapResponse{
				SessionID: "session-1",
				Launch:    worker.LaunchContext{PostCreate: postCreate, PreRemove: preRemove},
			})
		case "/worker/events":
			_, _ = io.Copy(io.Discard, req.Body)
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"ok":true}`)
		default:
			http.NotFound(w, req)
		}
	}))
	t.Cleanup(server.Close)
	return &client{baseURL: server.URL, http: server.Client()}
}

// A reopened session retries Setup with the commands the project has now, so
// fixing the Setup script in Project settings > Scripts lets the retry pass.
func TestReopenedSessionRetriesSetupWithTheProjectsCurrentCommands(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	dataDir, workspace := t.TempDir(), t.TempDir()
	environment := worker.SessionScriptEnvironment{SessionID: "session-1", WorkspaceDir: workspace}
	c := projectScriptsServer(t, []string{"echo fixed > setup-ran"}, []string{"echo bye"}, nil)

	booted := projectScripts{PostCreate: []string{"exit 3"}, PreRemove: []string{"echo old-cleanup"}}
	if failed := runProjectSetup(ctx, logger, c, dataDir, true, booted.PostCreate, workspace, environment); failed == nil {
		t.Fatal("Setup with the boot-time commands succeeded, want a failure")
	}

	refreshed := refreshProjectScripts(ctx, logger, c, dataDir, workspace, environment, booted)
	if strings.Join(refreshed.PostCreate, "|") != "echo fixed > setup-ran" || strings.Join(refreshed.PreRemove, "|") != "echo bye" {
		t.Fatalf("refreshed scripts = %+v, want the project's current commands", refreshed)
	}
	if failed := runProjectSetup(ctx, logger, c, dataDir, true, refreshed.PostCreate, workspace, environment); failed != nil {
		t.Fatalf("retry with the current commands failed: %+v", *failed)
	}
	if _, err := os.Stat(filepath.Join(workspace, "setup-ran")); err != nil {
		t.Fatalf("the retry did not run the current Setup command: %v", err)
	}
	hook, err := os.ReadFile(filepath.Join(dataDir, worker.CleanupCommandsFile))
	if err != nil {
		t.Fatalf("read reinstalled cleanup commands: %v", err)
	}
	if !strings.Contains(string(hook), "echo bye") || strings.Contains(string(hook), "old-cleanup") {
		t.Fatalf("cleanup commands = %q, want the project's current Cleanup", hook)
	}
}

// A project whose Cleanup was removed leaves a hook that runs nothing.
func TestRefreshClearsTheCleanupCommandsWhenCleanupIsCleared(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	dataDir, workspace := t.TempDir(), t.TempDir()
	environment := worker.SessionScriptEnvironment{SessionID: "session-1", WorkspaceDir: workspace}
	if err := worker.WriteCleanupHook(dataDir, []string{"echo old-cleanup"}, workspace, environment); err != nil {
		t.Fatal(err)
	}
	c := projectScriptsServer(t, nil, nil, nil)
	refreshProjectScripts(ctx, logger, c, dataDir, workspace, environment, projectScripts{PreRemove: []string{"echo old-cleanup"}})
	script, err := os.ReadFile(filepath.Join(dataDir, worker.CleanupCommandsFile))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(script), "old-cleanup") {
		t.Fatalf("cleanup commands still run the removed step: %q", script)
	}
}

// When the control plane can't be read, the retry keeps the commands the
// worker already has rather than dropping Setup.
func TestRefreshKeepsTheKnownScriptsWhenTheControlPlaneFails(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	dataDir, workspace := t.TempDir(), t.TempDir()
	environment := worker.SessionScriptEnvironment{SessionID: "session-1", WorkspaceDir: workspace}
	failing := &atomic.Bool{}
	failing.Store(true)
	c := projectScriptsServer(t, []string{"echo new"}, nil, failing)
	known := projectScripts{PostCreate: []string{"echo known"}, PreRemove: []string{"echo known-cleanup"}}
	got := refreshProjectScripts(ctx, logger, c, dataDir, workspace, environment, known)
	if strings.Join(got.PostCreate, "|") != "echo known" || strings.Join(got.PreRemove, "|") != "echo known-cleanup" {
		t.Fatalf("scripts after a failed read = %+v, want the known ones", got)
	}
}
