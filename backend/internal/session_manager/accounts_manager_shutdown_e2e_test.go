//go:build e2e && (linux || darwin)

package sessionmanager

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/runtime/runtimeselect"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/lifecycle"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

func TestAccountsManagerSwitchShutdownRealProductionRecovery(t *testing.T) {
	for _, fallback := range []bool{false, true} {
		name := "direct"
		if fallback {
			name = "fallback"
		}
		for _, action := range []string{"retry", "cancel"} {
			t.Run(name+"/"+action, func(t *testing.T) {
				m, st, rt, agent, rec, cfg := realSelectedAccountRuntimeFixture(t, fallback)
				observed := &accountRealHandoffOwnership{Runtime: rt}
				m.runtime = observed
				shutdownCtx, shutdown := context.WithCancel(t.Context())
				defer shutdown()
				m.backgroundContext = shutdownCtx
				boundary := &accountStopBoundaryStore{Store: st, before: true, entered: make(chan struct{}), release: make(chan struct{})}
				m.store = boundary
				if _, err := m.StartAccountsManagerSwitch(t.Context(), rec.ID, cfg); err != nil {
					t.Fatal(err)
				}
				select {
				case <-boundary.entered:
				case <-time.After(5 * time.Second):
					t.Fatal("production switch did not reach pre-stop boundary")
				}
				shutdown()
				joinCtx, joinCancel := context.WithTimeout(t.Context(), 5*time.Second)
				defer joinCancel()
				if err := m.WaitAgentSwitchWorkers(joinCtx); err != nil {
					t.Fatal(err)
				}
				op := waitAccountSwitch(t, m, st, cfg.OperationID)
				if op.Phase != domain.AccountsManagerSwitchWaiting || observed.creates != 0 || observed.destroys != 0 || observed.interrupts != 0 || observed.inputs != 0 {
					t.Fatalf("shutdown changed execution or intent: phase=%s code=%s", op.Phase, op.ErrorCode)
				}
				if err := st.Close(); err != nil {
					t.Fatal(err)
				}
				reopened, err := sqlite.OpenPreMigrated(rec.Metadata.WorkspacePath)
				if err != nil {
					t.Fatal(err)
				}
				defer reopened.Close()
				cold := &accountRealHandoffOwnership{Runtime: runtimeselect.New(nil, m.runFilePath)}
				restarted := New(Deps{Store: reopened, Runtime: cold, Lifecycle: lifecycle.New(reopened, nil), AccountsManager: &accountSwitchRouter{store: reopened},
					Agents: m.agents, DataDir: m.dataDir, RunFilePath: m.runFilePath, Executable: m.executable, LookPath: m.lookPath, BackgroundContext: t.Context()})
				restarted.switchTargetStartWait = 3 * time.Second
				restarted.interfaceTransition.pollInterval = time.Millisecond
				defer func() {
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					if err := restarted.WaitAgentSwitchWorkers(ctx); err != nil {
						t.Error(err)
					}
				}()
				if err := restarted.ReconcileStartupSafety(t.Context()); err != nil {
					t.Fatal(err)
				}
				if release, ok := restarted.AcquireSessionInput(rec.ID); ok {
					release()
					t.Fatal("restart did not fence the recovered operation")
				}
				if action == "cancel" {
					_, err = restarted.CancelAccountsManagerSwitch(t.Context(), rec.ID, op.ID)
				} else {
					_, err = restarted.RetryAccountsManagerSwitch(t.Context(), rec.ID, op.ID)
				}
				if err != nil {
					t.Fatal(err)
				}
				final := waitAccountSwitch(t, restarted, reopened, op.ID)
				binding, found, err := reopened.GetAccountsManagerSessionRoute(t.Context(), rec.ID, op.Provider)
				if err != nil || !found || binding.Blocked {
					t.Fatal("settled production operation retained authorization fence", err)
				}
				if action == "cancel" {
					if final.Phase != domain.AccountsManagerSwitchCancelled || cold.creates != 0 || cold.destroys != 0 || cold.interrupts != 0 || cold.inputs != 0 || binding.Revision != cfg.ExpectedRevision || binding.AccountID != "account-a" {
						t.Fatal("cold cancellation mutated the production source")
					}
					alive, err := cold.IsAlive(t.Context(), runtimeHandle(rec.Metadata))
					if err != nil || !alive {
						t.Fatal("cold cancellation destroyed the retained source runtime", err)
					}
					return
				}
				if final.Phase != domain.AccountsManagerSwitchReady || binding.AccountID != cfg.AccountID || cold.creates != 1 || agent.lastLaunch.Prompt != "" {
					t.Fatalf("cold production retry did not settle: phase=%s code=%s", final.Phase, final.ErrorCode)
				}
				target, found, err := reopened.GetSession(t.Context(), rec.ID)
				if err != nil || !found || target.Metadata.RuntimeLaunchID != final.TargetGeneration {
					t.Fatal("cold retry lost the reserved target generation", err)
				}
				waitSelectedAccountProbe(t, cold.Runtime, ports.FencedRuntimeRef{SessionID: rec.ID, Handle: runtimeHandle(target.Metadata), Generation: final.TargetGeneration}, ports.FencedAlive)
			})
		}
	}
}
