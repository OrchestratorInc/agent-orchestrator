package sessionmanager

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// A TUI switch can park on source restore before any target handle was
// recorded. Restore must retry the source rather than reclassify the switch as
// an unconfirmed target start, which would also end Kill's takeover eligibility.
func TestRestoreRetriesSourceForStartingTargetWithoutHandle(t *testing.T) {
	for _, restoreSucceeds := range []bool{false, true} {
		name := "restore fails and kill still works"
		if restoreSucceeds {
			name = "restore succeeds"
		}
		t.Run(name, func(t *testing.T) {
			runtime := &switchRollbackCancellationRuntime{
				fakeRestartRuntime: &fakeRestartRuntime{fakeRuntime: &fakeRuntime{}},
				createCalls:        1,
			}
			if !restoreSucceeds {
				runtime.rollbackErr = errors.New("source relaunch unavailable")
			}
			manager, store, _ := newSwitchTestManager(t, runtime)
			manager.newLaunchID = func() string { return "source-restore-generation" }
			now := time.Now().UTC()
			sw := domain.AgentSwitch{
				ID: "switch-no-target", SessionID: "proj-1", IdempotencyKey: "no-target",
				RequestFingerprint: domain.ComputeAgentSwitchRequestFingerprint("proj-1", domain.HarnessCodex, ""),
				FromHarness:        domain.HarnessClaudeCode, TargetHarness: domain.HarnessCodex,
				State: domain.AgentSwitchStartingTarget, ErrorCode: domain.AgentSwitchErrorSourceRestoreUnconfirmed,
				AgentHandoffStatus: domain.AgentHandoffUnavailable, SourceGenerationID: "source-generation",
				TargetGenerationID: "target-generation", RequestedAt: now, UpdatedAt: now,
			}
			store.switches[sw.ID] = sw
			_, _ = manager.reconcileRetainedAgentSwitchOnce(context.Background(), store, sw.SessionID)

			if _, err := manager.RecoverAgentSwitch(context.Background(), sw.SessionID, sw.ID); err != nil {
				t.Fatalf("RecoverAgentSwitch: %v", err)
			}
			if !restoreSucceeds {
				waitForCondition(t, "failed restore to re-retain the gate", func() bool {
					return manager.agentSwitchRetained(sw.SessionID)
				})
				got, _, _ := store.GetAgentSwitch(context.Background(), sw.ID)
				if !got.RequiresSourceRestore() {
					t.Fatalf("failed restore = %s (%s), want source restore still required", got.State, got.ErrorCode)
				}
				if _, err := manager.Kill(context.Background(), sw.SessionID); err != nil {
					t.Fatalf("Kill after failed restore: %v", err)
				}
				return
			}
			waitForCondition(t, "restore to settle", func() bool {
				got, _, _ := store.GetAgentSwitch(context.Background(), sw.ID)
				return got.State.Terminal() && !manager.SessionMutationInProgress(sw.SessionID)
			})
			got, _, _ := store.GetAgentSwitch(context.Background(), sw.ID)
			if got.State != domain.AgentSwitchFailed || got.ErrorCode == domain.AgentSwitchErrorTargetStartUnconfirmed {
				t.Fatalf("restored switch = %s (%s), want failed after source restore", got.State, got.ErrorCode)
			}
			rec, _, _ := store.GetSession(context.Background(), sw.SessionID)
			if rec.Harness != domain.HarnessClaudeCode || rec.Activity.State == domain.ActivityExited {
				t.Fatalf("restored session = harness %q activity %q, want running Claude", rec.Harness, rec.Activity.State)
			}
		})
	}
}
