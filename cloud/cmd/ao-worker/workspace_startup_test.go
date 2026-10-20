package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
)

// workerEventRecorder is a control plane that records the worker events it
// receives.
type workerEventRecorder struct {
	mu     sync.Mutex
	events []worker.EventRequest
}

func (r *workerEventRecorder) server(t *testing.T) *client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/worker/events" {
			http.NotFound(w, req)
			return
		}
		var event struct {
			Type    string          `json:"type"`
			Payload json.RawMessage `json:"payload"`
		}
		body, _ := io.ReadAll(req.Body)
		if err := json.Unmarshal(body, &event); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		r.mu.Lock()
		r.events = append(r.events, worker.EventRequest{Type: event.Type, Payload: event.Payload})
		r.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	t.Cleanup(server.Close)
	return &client{baseURL: server.URL, http: server.Client()}
}

func (r *workerEventRecorder) startup(t *testing.T) []worker.WorkspaceStartupEvent {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	var reports []worker.WorkspaceStartupEvent
	for _, event := range r.events {
		if event.Type != worker.WorkspaceStartupEventType {
			continue
		}
		var report worker.WorkspaceStartupEvent
		if err := json.Unmarshal(event.Payload.(json.RawMessage), &report); err != nil {
			t.Fatal(err)
		}
		reports = append(reports, report)
	}
	return reports
}

// A failed Setup step becomes the session's startup error: the step and its
// exit status as the message, the command's output as the detail.
func TestFailedSetupIsReportedAsTheWorkspaceStartupError(t *testing.T) {
	recorder := &workerEventRecorder{}
	c := recorder.server(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	workspace := t.TempDir()
	failed := runProjectSetup(context.Background(), logger, c, t.TempDir(), true,
		[]string{"echo starting stack; echo missing secret >&2; exit 4", "echo never"}, workspace,
		worker.SessionScriptEnvironment{SessionID: "session-1", WorkspaceDir: workspace})
	if failed == nil {
		t.Fatal("setup reported success")
	}
	reportWorkspaceStartup(context.Background(), logger, c, setupStartupFailure(*failed))

	reports := recorder.startup(t)
	if len(reports) != 1 {
		t.Fatalf("startup reports = %+v", reports)
	}
	report := reports[0]
	if report.Status != worker.WorkspaceStartupFailed || report.Code != domain.WorkspaceStartupErrorProjectSetup ||
		!strings.HasPrefix(report.Message, "The project's Setup script failed at step 1 (exit status 4), so the agent did not start.") ||
		!strings.Contains(report.Message, "Project settings > Scripts") ||
		!strings.Contains(report.Detail, "missing secret") || strings.Contains(report.Message, "missing secret") {
		t.Fatalf("report = %+v", report)
	}
}

// A checkout failure names its cause: the Coder connection's working
// directory, or the repository checkout with the redacted error as detail.
func TestCheckoutFailuresHaveTheirOwnStartupErrors(t *testing.T) {
	directory := checkoutStartupFailure(fmt.Errorf("%w: /home/ubuntu/work/alice is not a checkout of the repository", worker.ErrWorkingDirectoryUnusable))
	if directory.Code != domain.WorkspaceStartupErrorWorkingDirectory ||
		!strings.Contains(directory.Message, "/home/ubuntu/work/alice is not a checkout of the repository") {
		t.Fatalf("working directory failure = %+v", directory)
	}
	checkout := checkoutStartupFailure(errors.New("git fetch: https://x-access-token:ghs_abcdefghijklmnopqrstuvwxyz0123456789@github.com/acme/app: 403"))
	if checkout.Code != domain.WorkspaceStartupErrorCheckout || checkout.Status != worker.WorkspaceStartupFailed ||
		!strings.Contains(checkout.Detail, "403") || strings.Contains(checkout.Detail, "abcdefghijklmnop") {
		t.Fatalf("checkout failure = %+v", checkout)
	}
	branch := checkoutStartupFailure(fmt.Errorf("configure: %w: feature/x exists on the repository but could not be checked out from origin/feature/x: README.md would be overwritten", worker.ErrSessionBranchSwitch))
	if branch.Status != worker.WorkspaceStartupFailed || !strings.Contains(branch.Message, "session's branch") ||
		!strings.Contains(branch.Detail, "feature/x") || !strings.Contains(branch.Detail, "README.md") {
		t.Fatalf("session branch failure = %+v", branch)
	}
}

// A workspace that is ready is reported so a failure from an earlier attempt
// is cleared.
func TestReadyWorkspaceIsReported(t *testing.T) {
	recorder := &workerEventRecorder{}
	c := recorder.server(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if failed := runProjectSetup(context.Background(), logger, c, t.TempDir(), true, []string{"true"}, t.TempDir(),
		worker.SessionScriptEnvironment{SessionID: "session-1"}); failed != nil {
		t.Fatalf("setup failed: %+v", failed)
	}
	reportWorkspaceStartup(context.Background(), logger, c, worker.WorkspaceStartupEvent{Status: worker.WorkspaceStartupReady})
	reports := recorder.startup(t)
	if len(reports) != 1 || reports[0].Status != worker.WorkspaceStartupReady || reports[0].Code != "" {
		t.Fatalf("reports = %+v", reports)
	}
}

// A session opened while Setup was still running must not count as a retry
// once that Setup fails, or Setup silently runs again and the agent starts.
func TestAnOpenSavedBeforeAFailedStartIsNotARetry(t *testing.T) {
	reopen := make(chan struct{}, 1)
	dropPendingReopen(reopen) // nothing saved: returns at once
	reopen <- struct{}{}      // opened while the failed attempt ran
	dropPendingReopen(reopen)
	select {
	case <-reopen:
		t.Fatal("an open saved before the failure was read as a retry")
	default:
	}
}
