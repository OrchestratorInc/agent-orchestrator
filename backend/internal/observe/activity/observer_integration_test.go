package activity

import (
	"context"
	"flag"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/codex"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/droid"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/runtime/tmux"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/lifecycle"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

func TestObserverIntegrationReconcilesRealTmuxOutputIntoSQLite(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux unavailable")
	}

	tests := []struct {
		name      string
		output    string
		wantState domain.ActivityState
	}{
		{
			name:      "idle composer repairs missed stop",
			output:    "\n› Write tests for @filename\n\ngpt-5.6-sol low · ~/project\n",
			wantState: domain.ActivityIdle,
		},
		{
			name:      "working marker prevents demotion",
			output:    "\n• Working (3m 10s • esc to interrupt)\n› Add tests\n\ngpt-5.6-sol low · ~/project\n",
			wantState: domain.ActivityActive,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			store, err := sqlitetest.Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })

			workspace := t.TempDir()
			now := time.Now().UTC()
			staleAt := now.Add(-3 * time.Minute)
			projectID := domain.ProjectID("ao3115e2e")
			if err := store.UpsertProject(ctx, domain.ProjectRecord{
				ID:           string(projectID),
				Path:         workspace,
				RegisteredAt: staleAt,
			}); err != nil {
				t.Fatal(err)
			}
			session, err := store.CreateSession(ctx, domain.SessionRecord{
				ProjectID:     projectID,
				Kind:          domain.KindWorker,
				Harness:       domain.HarnessCodex,
				Activity:      domain.Activity{State: domain.ActivityActive, LastActivityAt: staleAt},
				FirstSignalAt: staleAt,
				CreatedAt:     staleAt,
				UpdatedAt:     staleAt,
			})
			if err != nil {
				t.Fatal(err)
			}

			runtime := tmux.New(tmux.Options{Timeout: 5 * time.Second, ReapGrace: 100 * time.Millisecond})
			handle, err := runtime.Create(ctx, ports.RuntimeConfig{
				SessionID:     session.ID,
				WorkspacePath: workspace,
				Argv:          []string{"sh", "-c", "printf '%s' " + shellQuote(tt.output) + "; sleep 30"},
			})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = runtime.Destroy(context.Background(), handle) })

			waitForTerminalOutput(t, runtime, handle, "gpt-5.6-sol low · ~/project")
			session.Metadata.RuntimeHandleID = handle.ID
			session.Metadata.RuntimeLaunchID = "launch-1"
			if err := store.UpdateSession(ctx, session); err != nil {
				t.Fatal(err)
			}

			manager := lifecycle.New(store, nil)
			observer := New(
				store,
				manager,
				runtime,
				fakeAgents{domain.HarnessCodex: codex.New()},
				Config{
					StaleAfter: time.Minute,
					Clock:      func() time.Time { return now },
					Logger:     testLogger(),
				},
			)
			if err := observer.Poll(ctx); err != nil {
				t.Fatal(err)
			}

			got, ok, err := store.GetSession(ctx, session.ID)
			if err != nil || !ok {
				t.Fatalf("GetSession: ok=%v err=%v", ok, err)
			}
			if got.Activity.State != tt.wantState {
				t.Fatalf("activity state = %q, want %q", got.Activity.State, tt.wantState)
			}
		})
	}
}

// TestQuietSupervisorHelper stands in for AO's agent supervisor. The frozen
// terminal test launches this binary with the supervisor's command line, and it
// runs the agent after "--" as its child, so tmux counts the real process tree.
func TestQuietSupervisorHelper(t *testing.T) {
	if os.Getenv("AO_QUIET_SUPERVISOR_HELPER") != "1" {
		return
	}
	args := flag.Args()
	for i, arg := range args {
		if arg == "--" && i+1 < len(args) {
			agent := exec.Command(args[i+1], args[i+2:]...)
			agent.Stdin, agent.Stdout, agent.Stderr = os.Stdin, os.Stdout, os.Stderr
			_ = agent.Run()
			return
		}
	}
}

func TestObserverIntegrationMovesFrozenTerminalWithoutDetectorToWaitingInput(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux unavailable")
	}

	const frozen = "printf '>\\n  ? for help'; sleep 60"
	tests := []struct {
		name         string
		script       string
		unsupervised bool
		wantState    domain.ActivityState
	}{
		{
			name:      "prompt left after a lost stop hook",
			script:    frozen,
			wantState: domain.ActivityWaitingInput,
		},
		{
			name:      "turn timer keeps it working",
			script:    "printf '? for help\\n'; i=0; while :; do i=$((i+1)); printf '\\r- Thinking (%ds)' \"$i\"; sleep 0.1; done",
			wantState: domain.ActivityActive,
		},
		{
			// Without a supervisor the running commands can't be counted, so
			// the session fails closed and stays working.
			name:         "pane without a supervisor stays working",
			script:       frozen,
			unsupervised: true,
			wantState:    domain.ActivityActive,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			store, err := sqlitetest.Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })

			workspace := t.TempDir()
			now := time.Now().UTC()
			staleAt := now.Add(-3 * time.Minute)
			projectID := domain.ProjectID("ao7486e2e")
			if err := store.UpsertProject(ctx, domain.ProjectRecord{ID: string(projectID), Path: workspace, RegisteredAt: staleAt}); err != nil {
				t.Fatal(err)
			}
			session, err := store.CreateSession(ctx, domain.SessionRecord{
				ProjectID:     projectID,
				Kind:          domain.KindWorker,
				Harness:       domain.HarnessDroid,
				Activity:      domain.Activity{State: domain.ActivityActive, LastActivityAt: staleAt},
				FirstSignalAt: staleAt,
				CreatedAt:     staleAt,
				UpdatedAt:     staleAt,
			})
			if err != nil {
				t.Fatal(err)
			}

			cfg := ports.RuntimeConfig{SessionID: session.ID, WorkspacePath: workspace, Argv: []string{"sh", "-c", tt.script}}
			if !tt.unsupervised {
				cfg.Argv = append([]string{os.Args[0], "-test.run=TestQuietSupervisorHelper", "--",
					"agent-process", "supervise", "--session", string(session.ID), "--launch", "launch-1", "--"}, cfg.Argv...)
				cfg.Env = map[string]string{"AO_QUIET_SUPERVISOR_HELPER": "1"}
			}
			runtime := tmux.New(tmux.Options{Timeout: 5 * time.Second, ReapGrace: 100 * time.Millisecond})
			handle, err := runtime.Create(ctx, cfg)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = runtime.Destroy(context.Background(), handle) })

			waitForTerminalOutput(t, runtime, handle, "? for help")
			session.Metadata.RuntimeHandleID = handle.ID
			session.Metadata.RuntimeLaunchID = "launch-1"
			if err := store.UpdateSession(ctx, session); err != nil {
				t.Fatal(err)
			}

			clock := now
			observer := New(store, lifecycle.New(store, nil), runtime, fakeAgents{domain.HarnessDroid: droid.New()},
				Config{StaleAfter: time.Minute, Clock: func() time.Time { return clock }, Logger: testLogger()})
			for at := now; !at.After(now.Add(quietAfter)); at = at.Add(DefaultTickInterval) {
				clock = at
				if err := observer.Poll(ctx); err != nil {
					t.Fatal(err)
				}
				// Real time between ticks, so a running timer redraws.
				time.Sleep(250 * time.Millisecond)
			}

			got, ok, err := store.GetSession(ctx, session.ID)
			if err != nil || !ok {
				t.Fatalf("GetSession: ok=%v err=%v", ok, err)
			}
			if got.Activity.State != tt.wantState {
				t.Fatalf("activity state = %q, want %q", got.Activity.State, tt.wantState)
			}
		})
	}
}

func waitForTerminalOutput(t *testing.T, runtime *tmux.Runtime, handle ports.RuntimeHandle, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		output, err := runtime.GetOutput(context.Background(), handle, 40)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(output, want) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("terminal output never contained %q", want)
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
