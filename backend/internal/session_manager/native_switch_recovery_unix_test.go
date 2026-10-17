//go:build darwin || linux

package sessionmanager

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/runtime/conpty"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func waitForNativeTestFile(t *testing.T, path, want string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		raw, _ := os.ReadFile(path)
		if strings.Contains(string(raw), want) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %q in %s; got %q", want, path, raw)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Recovery of a switch interrupted in starting_target probes the target
// generation. A real native supervisor that has not yet started its agent is
// healthy; recovery must retain the switch for later instead of treating the
// launch as definitely dead, destroying it, and rolling back to the source.
func TestReconcileStartingTargetRetainsStartingNativeSupervisor(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	rt := conpty.New(conpty.Options{RunFilePath: filepath.Join(dir, "running.json")})
	manager, store, messenger := newSwitchTestManager(t, rt)
	target := manager.agents.(switchTestAgents)[domain.HarnessCodex].(*switchTestAgent)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	starting := filepath.Join(dir, "supervisor.starting")
	gate := filepath.Join(dir, "supervisor.start")
	ready := filepath.Join(dir, "agent.ready")
	handle, err := rt.Create(ctx, ports.RuntimeConfig{
		SessionID: "proj-1", WorkspacePath: dir,
		Argv: []string{exe, "agent-process", "supervise", "--session", "proj-1", "--launch", "target-generation", "--",
			"/bin/sh", "-c", `printf ready > "$AO_TEST_AGENT_READY"; while IFS= read -r line; do :; done`},
		Env: map[string]string{
			"AO_RUNTIME_LAUNCH_ID":          "target-generation",
			"AO_TEST_SUPERVISOR_STARTING":   starting,
			"AO_TEST_SUPERVISOR_START_GATE": gate,
			"AO_TEST_AGENT_READY":           ready,
		},
	})
	if err != nil {
		t.Fatalf("create native target: %v", err)
	}
	t.Cleanup(func() {
		_ = os.WriteFile(gate, nil, 0o600)
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = rt.Destroy(cleanupCtx, handle)
	})
	waitForNativeTestFile(t, starting, "starting")

	now := time.Now().UTC()
	targetRef := domain.AgentNativeSessionID("native-target")
	sw := domain.AgentSwitch{
		ID: "switch-target-starting", SessionID: "proj-1", IdempotencyKey: "target-starting",
		RequestFingerprint: domain.ComputeAgentSwitchRequestFingerprint("proj-1", domain.HarnessCodex, ""),
		FromHarness:        domain.HarnessClaudeCode, TargetHarness: domain.HarnessCodex,
		TargetNativeSessionRef: &targetRef, TargetStartMode: domain.AgentSwitchTargetStartFresh,
		State: domain.AgentSwitchStartingTarget, AgentHandoffStatus: domain.AgentHandoffUnavailable,
		SourceGenerationID: "source-generation", TargetGenerationID: "target-generation",
		TargetRuntimeHandleID: handle.ID, RequestedAt: now, UpdatedAt: now,
	}
	store.switches[sw.ID] = sw
	sessionBefore := store.sessions["proj-1"]

	resolved, err := manager.reconcileRetainedAgentSwitchOnce(ctx, store, sw.SessionID)
	if err == nil || resolved {
		t.Fatalf("recovery = resolved %v err %v, want an unresolved ownership error", resolved, err)
	}
	if !strings.Contains(err.Error(), string(ports.FencedReasonWorkloadUnconfirmed)) {
		t.Fatalf("recovery error %q does not name the unconfirmed workload", err)
	}
	got := store.switches[sw.ID]
	if got.State != domain.AgentSwitchStartingTarget || got.ErrorCode != domain.AgentSwitchErrorTargetStartUnconfirmed || got.TargetRuntimeHandleID != handle.ID {
		t.Fatalf("switch = state %q code %q handle %q, want retained starting target", got.State, got.ErrorCode, got.TargetRuntimeHandleID)
	}
	if alive, err := rt.IsChildAlive(ctx, handle); err != nil || !alive {
		t.Fatalf("recovery destroyed the starting target: alive=%v err=%v", alive, err)
	}
	if target.cleanupCalls != 0 || len(messenger.msgs) != 0 {
		t.Fatalf("recovery side effects: cleanups=%d messages=%d", target.cleanupCalls, len(messenger.msgs))
	}
	if after := store.sessions["proj-1"]; after.Harness != sessionBefore.Harness || after.Metadata.RuntimeLaunchID != sessionBefore.Metadata.RuntimeLaunchID {
		t.Fatalf("recovery rolled the session back: before %+v after %+v", sessionBefore.Metadata, after.Metadata)
	}
	if !manager.SessionMutationInProgress(sw.SessionID) {
		t.Fatal("unconfirmed target ownership released the operation gate")
	}

	// The same launch then starts its agent: it was healthy all along.
	if err := os.WriteFile(gate, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	waitForNativeTestFile(t, ready, "ready")
	ref := ports.FencedRuntimeRef{Handle: handle, SessionID: "proj-1", Generation: "target-generation"}
	if probe := rt.ProbeFencedRuntime(ctx, ref); probe.Liveness != ports.FencedAlive {
		t.Fatalf("started target probe = %+v, want alive", probe)
	}
}
