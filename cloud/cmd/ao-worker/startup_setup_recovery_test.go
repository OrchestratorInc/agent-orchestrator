package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/pkg/tasknaming"
	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
)

// Only durable state survives between these separate worker processes.
func TestStartupSetupRecoveryWorkerProcess(t *testing.T) {
	if os.Getenv("AO_TEST_SETUP_RECOVERY") != "1" {
		return
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	dataDir, repository := os.Getenv("AO_TEST_SETUP_DATA"), os.Getenv("AO_TEST_SETUP_REPOSITORY")
	c := &client{baseURL: os.Getenv("AO_TEST_SETUP_URL"), http: &http.Client{Timeout: time.Second}, tokenFile: filepath.Join(dataDir, "worker-token")}
	_, tokenErr := os.Stat(c.tokenFile)
	firstStart := errors.Is(tokenErr, os.ErrNotExist)
	bootstrap, err := c.connect(ctx, logger, "test-ticket")
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv("AO_TEST_SETUP_EXPECT_RESTART") == "1" && firstStart {
		t.Fatal("restart did not retain the worker credential")
	}
	if err := (worker.SetupRunner{DataDir: dataDir, FirstStart: firstStart}).Prepare(ctx, bootstrap.Launch.PostCreate); err != nil {
		t.Fatal(err)
	}
	if _, err := c.checkoutGrant(ctx); err != nil {
		if os.Getenv("AO_TEST_SETUP_EXPECT_FAILURE") != "1" {
			t.Fatal(err)
		}
		return
	}
	workspace, err := finalizeTaskWorkspace(ctx, logger, c, &bootstrap, repository, dataDir, false, tasknaming.Name{Title: "README clarity", Slug: "readme-clarity"})
	if os.Getenv("AO_TEST_SETUP_EXPECT_FAILURE") == "1" {
		if err == nil {
			t.Fatal("expected startup failure")
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	if failed := runProjectSetup(ctx, logger, c, dataDir, firstStart, bootstrap.Launch.PostCreate, workspace,
		worker.SessionScriptEnvironment{SessionID: bootstrap.SessionID, WorkspaceDir: workspace}); failed != nil {
		t.Fatalf("Setup failed after recovery: %+v", failed)
	}
}

func TestStartupSetupRecoversAcrossWorkerProcesses(t *testing.T) {
	for _, failure := range []string{"checkout", "reservation", "completion"} {
		t.Run(failure, func(t *testing.T) {
			repository, _ := namingRepository(t)
			dataDir := t.TempDir()
			var failing atomic.Bool
			failing.Store(true)
			var done atomic.Bool
			var failureReached atomic.Bool
			var setupEvents atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/worker/bootstrap", "/worker/reconnect":
					launch := worker.LaunchContext{SessionID: "session-1", ProjectID: "project", Kind: "worker", Branch: "ao/session-1/root", DefaultBranch: "main", TaskNamingState: domain.TaskNamingPending,
						PostCreate: []string{"printf 'setup\\n' >> setup-runs"}}
					if done.Load() {
						launch.TaskNamingState, launch.TaskNameSlug, launch.Branch = domain.TaskNamingDone, "readme-clarity", "ao/readme-clarity/root"
					}
					_ = json.NewEncoder(w).Encode(worker.BootstrapResponse{SessionID: "session-1", WorkerToken: "test-token", Launch: launch})
				case "/worker/heartbeat":
					_ = json.NewEncoder(w).Encode(worker.HeartbeatResponse{WorkerToken: "test-token"})
				case "/worker/checkout-grant":
					if failure == "checkout" && failing.Load() {
						failureReached.Store(true)
						http.Error(w, "checkout unavailable", http.StatusServiceUnavailable)
						return
					}
					_ = json.NewEncoder(w).Encode(worker.CheckoutGrantResponse{CloneURL: "https://github.com/acme/app"})
				case "/worker/task-name":
					var request domain.TaskNameRequest
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
						t.Error(err)
					}
					if failing.Load() && (failure == "reservation" && !request.Complete || failure == "completion" && request.Complete) {
						failureReached.Store(true)
						http.Error(w, "naming unavailable", http.StatusServiceUnavailable)
						return
					}
					result := domain.TaskNameResult{State: domain.TaskNamingReserved, Title: "README clarity", Slug: "readme-clarity", Branch: "ao/readme-clarity/root"}
					if request.Complete {
						result.State = domain.TaskNamingDone
						done.Store(true)
					}
					_ = json.NewEncoder(w).Encode(result)
				case "/worker/events":
					setupEvents.Add(1)
					_, _ = io.WriteString(w, `{"ok":true}`)
				default:
					t.Errorf("unexpected request %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			run := func(expectFailure, expectRestart string) {
				t.Helper()
				ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestStartupSetupRecoveryWorkerProcess$")
				cmd.Env = append(os.Environ(), "AO_TEST_SETUP_RECOVERY=1", "AO_TEST_SETUP_DATA="+dataDir, "AO_TEST_SETUP_REPOSITORY="+repository,
					"AO_TEST_SETUP_URL="+server.URL, "AO_TEST_SETUP_EXPECT_FAILURE="+expectFailure, "AO_TEST_SETUP_EXPECT_RESTART="+expectRestart)
				if output, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("worker process failed: %v: %s", err, output)
				}
			}
			run("1", "0")
			if !failureReached.Load() {
				t.Fatalf("worker did not reach the intended %s failure", failure)
			}
			if setupEvents.Load() != 0 {
				t.Fatal("Setup ran before the workspace was prepared")
			}
			failing.Store(false)
			run("0", "1")
			run("0", "1")
			trace, err := os.ReadFile(filepath.Join(repository, ".ao-task-worktrees", "project", "readme-clarity", "setup-runs"))
			if err != nil || strings.TrimSpace(string(trace)) != "setup" || setupEvents.Load() != 1 {
				t.Fatalf("Setup must run exactly once after recovery: trace=%q, events=%d, error=%v", trace, setupEvents.Load(), err)
			}
		})
	}
}
