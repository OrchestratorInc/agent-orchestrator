package session

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
)

func killedProcessError(t *testing.T) error {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("requires POSIX signal semantics")
	}
	err := exec.Command("sh", "-c", "kill -9 $$").Run()
	if err == nil || err.Error() != "signal: killed" {
		t.Fatalf("killed process error = %v, want signal: killed", err)
	}
	return err
}

// A git or agent process killed by the spawn's expired deadline returns its
// own error, not context.DeadlineExceeded; under an expired context both are
// timeouts, and without one they keep their stage's code.
func TestKilledStagesUnderAnExpiredBudgetAreSpawnTimeouts(t *testing.T) {
	expired, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	stages := map[string]error{
		"git worktree killed": fmt.Errorf("spawn w-1: %w: git worktree add: %w", sessionmanager.ErrWorkspaceCreate, killedProcessError(t)),
		"ACP peer disconnected": fmt.Errorf("spawn w-1: %w: initialize: %w", sessionmanager.ErrChatController,
			errors.New(`request failed: {"error":"peer disconnected before response"}`)),
	}
	wantLive := map[string]string{"git worktree killed": "WORKSPACE_CREATE_FAILED", "ACP peer disconnected": "CHAT_CONTROLLER_FAILED"}
	for name, stageErr := range stages {
		relabelled := sessionmanager.RelabelSpawnTimeout(stageErr, expired)
		var apiErr *apierr.Error
		if !errors.As(toSpawnAPIError(relabelled), &apiErr) || apiErr.Code != "SPAWN_TIMEOUT" {
			t.Errorf("%s under an expired budget = %v, want SPAWN_TIMEOUT", name, toSpawnAPIError(relabelled))
		}
		if _, cause := sessionmanager.ClassifySpawnFailure(relabelled, sessionmanager.SpawnTraceFacts{}); cause != "timeout" {
			t.Errorf("%s cause = %q, want timeout", name, cause)
		}
		live := sessionmanager.RelabelSpawnTimeout(stageErr, context.Background())
		if !errors.As(toSpawnAPIError(live), &apiErr) || apiErr.Code != wantLive[name] {
			t.Errorf("%s with a live budget = %v, want %s", name, toSpawnAPIError(live), wantLive[name])
		}
	}
}

func TestSpawnFailedCarriesStepsCauseAndLaunchFacts(t *testing.T) {
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer"}
	sink := &fakeTelemetrySink{}
	gitErr := gitStderrError{cause: "branch_exists"}
	fc := &fakeCommander{
		spawnErr: fmt.Errorf("spawn mer-1: %w: %w", sessionmanager.ErrWorkspaceCreate, gitErr),
		spawnHook: func(ctx context.Context) {
			sessionmanager.NoteSpawnLaunch(ctx, domain.SessionModeChat, false)
			sessionmanager.NoteSpawnModel(ctx, "claude-opus-5-5", true)
			sessionmanager.BeginSpawnStep(ctx, sessionmanager.SpawnStepModelCheck)(nil)
			sessionmanager.BeginSpawnStep(ctx, sessionmanager.SpawnStepWorktree)(gitErr)
		},
	}
	svc := NewWithDeps(Deps{Manager: fc, Store: st, Telemetry: sink})
	_, _, _, err := svc.Spawn(context.Background(), ports.SpawnConfig{ProjectID: "mer", CreatedBy: domain.SessionCreatedByAutomation})
	if err == nil {
		t.Fatal("Spawn should fail")
	}
	if len(sink.events) != 1 || sink.events[0].Name != "ao.sessions.spawn_failed" {
		t.Fatalf("events = %#v, want one spawn_failed", sink.events)
	}
	payload := sink.events[0].Payload
	for key, want := range map[string]any{
		"failed_step": "worktree", "cause": "branch_exists", "mode": "chat", "async": false,
		"model": "custom", "created_by": "automation",
	} {
		if payload[key] != want {
			t.Errorf("spawn_failed %s = %#v, want %#v", key, payload[key], want)
		}
	}
	for _, key := range []string{"model_check_ms", "worktree_ms"} {
		if _, ok := payload[key].(int64); !ok {
			t.Errorf("spawn_failed %s = %#v, want a duration", key, payload[key])
		}
	}
	for _, key := range []string{"branch_fetch_ms", "setup_ms", "agent_start_ms"} {
		if _, ok := payload[key]; ok {
			t.Errorf("spawn_failed carries %s for a step that never ran", key)
		}
	}
	// The API error carries the classification for the request's Sentry capture.
	var tagged interface{ SentryTags() map[string]string }
	if !errors.As(err, &tagged) || tagged.SentryTags()["cause"] != "branch_exists" || tagged.SentryTags()["failed_step"] != "worktree" {
		t.Fatalf("spawn error %v does not carry failed_step and cause tags", err)
	}
}

func TestCancelledSpawnSendsSpawnCancelledNotFailed(t *testing.T) {
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer"}
	sink := &fakeTelemetrySink{}
	fc := &fakeCommander{spawnErr: fmt.Errorf("spawn: %w", context.Canceled)}
	svc := NewWithDeps(Deps{Manager: fc, Store: st, Telemetry: sink})
	if _, _, _, err := svc.Spawn(context.Background(), ports.SpawnConfig{ProjectID: "mer"}); err == nil {
		t.Fatal("Spawn should fail")
	}
	if len(sink.events) != 1 || sink.events[0].Name != "ao.sessions.spawn_cancelled" {
		t.Fatalf("events = %#v, want one spawn_cancelled", sink.events)
	}
	if got := sink.events[0].Payload["error_code"]; got != "SPAWN_CANCELLED" {
		t.Fatalf("spawn_cancelled error_code = %#v, want SPAWN_CANCELLED", got)
	}
	if _, ok := sink.events[0].Payload["failed_step"]; !ok {
		t.Fatal("spawn_cancelled lacks spawn_failed's props")
	}
}

// gitStderrError stands in for the git adapter's commandError.
type gitStderrError struct{ cause string }

func (e gitStderrError) Error() string        { return "git worktree add: exit status 128" }
func (e gitStderrError) FailureCause() string { return e.cause }

func TestLaunchPropsOmitModeBeforeSelection(t *testing.T) {
	for _, tc := range []struct {
		requested, selected domain.SessionMode
		want                string
	}{
		{"", "", ""}, {domain.SessionModeChat, "", "chat"}, {domain.SessionModeTUI, "", "terminal"}, {domain.SessionModeChat, domain.SessionModeTUI, "terminal"},
	} {
		payload := map[string]any{}
		addSpawnLaunchProps(payload, sessionmanager.SpawnTraceFacts{Mode: tc.selected}, tc.requested)
		if tc.want == "" {
			if _, ok := payload["mode"]; ok {
				t.Fatalf("unchosen mode reported: %v", payload)
			}
		} else if payload["mode"] != tc.want {
			t.Fatalf("mode = %v, want %s", payload["mode"], tc.want)
		}
	}
}
