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

type accountRealHandoffOwnership struct {
	runtimeselect.Runtime
	interrupts, inputs, destroys, creates int
}

func (r *accountRealHandoffOwnership) LaunchHandles(id domain.SessionID) ([]ports.RuntimeHandle, error) {
	resolver, ok := r.Runtime.(ports.RuntimeLaunchHandleResolver)
	if !ok {
		return nil, ErrInterfaceHandoffUnsupported
	}
	return resolver.LaunchHandles(id)
}

func (r *accountRealHandoffOwnership) GetStyledOutput(ctx context.Context, handle ports.RuntimeHandle, lines int) (string, error) {
	reader, ok := r.Runtime.(ports.StyledTerminalOutputReader)
	if !ok {
		return "", ErrInterfaceHandoffUnsupported
	}
	return reader.GetStyledOutput(ctx, handle, lines)
}

func (r *accountRealHandoffOwnership) IsExactSupervisedProcessAlive(ctx context.Context, handle ports.RuntimeHandle, ref ports.SupervisedProcessRef) (bool, error) {
	inspector, ok := r.Runtime.(ports.ExactSupervisedProcessInspector)
	if !ok {
		return false, ErrInterfaceHandoffUnsupported
	}
	return inspector.IsExactSupervisedProcessAlive(ctx, handle, ref)
}

func (r *accountRealHandoffOwnership) Interrupt(ctx context.Context, handle ports.RuntimeHandle) error {
	r.interrupts++
	return r.Runtime.Interrupt(ctx, handle)
}

func (r *accountRealHandoffOwnership) SendInput(ctx context.Context, handle ports.RuntimeHandle, input string) error {
	r.inputs++
	return r.Runtime.SendInput(ctx, handle, input)
}

func (r *accountRealHandoffOwnership) SendMessage(ctx context.Context, handle ports.RuntimeHandle, input string) error {
	r.inputs++
	return r.Runtime.SendMessage(ctx, handle, input)
}

func (r *accountRealHandoffOwnership) Create(ctx context.Context, cfg ports.RuntimeConfig) (ports.RuntimeHandle, error) {
	r.creates++
	return r.Runtime.Create(ctx, cfg)
}

func (r *accountRealHandoffOwnership) Destroy(ctx context.Context, handle ports.RuntimeHandle) error {
	r.destroys++
	return r.Runtime.Destroy(ctx, handle)
}

func TestAccountsManagerSwitchRealProductionHandoffOwnership(t *testing.T) {
	for _, fallback := range []bool{false, true} {
		name := "direct"
		if fallback {
			name = "fallback"
		}
		for _, phase := range []domain.AccountsManagerSwitchPhase{domain.AccountsManagerSwitchWaiting, domain.AccountsManagerSwitchStopping} {
			for _, owner := range []string{"foreign", "same", "absent"} {
				t.Run(name+"/"+string(phase)+"/"+owner, func(t *testing.T) {
					m, st, rt, agent, rec, cfg := realSelectedAccountRuntimeFixture(t, fallback)
					rec.Activity.State = domain.ActivityActive
					if err := st.UpdateSession(t.Context(), rec); err != nil {
						t.Fatal(err)
					}
					before := accountHandoffJournal(t, st, rec, cfg, phase)
					ref := ports.FencedRuntimeRef{SessionID: rec.ID, Handle: runtimeHandle(rec.Metadata), Generation: rec.Metadata.RuntimeLaunchID}
					if err := rt.Destroy(t.Context(), ref.Handle); err != nil {
						t.Fatal(err)
					}
					if owner != "absent" {
						if owner == "foreign" {
							ref.Generation = "foreign-generation"
						}
						binary, err := m.executable()
						if err != nil {
							t.Fatal(err)
						}
						handle, err := rt.Create(t.Context(), ports.RuntimeConfig{SessionID: rec.ID, WorkspacePath: rec.Metadata.WorkspacePath,
							Argv: []string{binary, "agent-process", "supervise", "--session", string(rec.ID), "--launch", ref.Generation, "--", "/bin/sleep", "30"},
							Env:  map[string]string{EnvSupervisedProcess: "1", EnvRuntimeLaunchID: ref.Generation, EnvDataDir: m.dataDir, EnvRunFile: m.runFilePath}})
						if err != nil || handle != ref.Handle {
							t.Fatal("fixture did not occupy the intended runtime slot", err)
						}
						waitSelectedAccountProbe(t, rt, ref, ports.FencedAlive)
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
						Agents: singleAgent{agent: agent}, DataDir: m.dataDir, RunFilePath: m.runFilePath, LookPath: m.lookPath, BackgroundContext: t.Context()})
					restarted.executable, restarted.switchTargetStartWait = m.executable, 3*time.Second
					restarted.interfaceTransition.pollInterval = time.Millisecond
					gate := &transitionInputGate{acquired: make(chan string, 1), released: make(chan string, 1)}
					restarted.SetTerminalInputGate(gate)
					if err := restarted.ReconcileStartupSafety(t.Context()); err != nil {
						t.Fatal(err)
					}
					attempts := 1
					if owner == "foreign" {
						attempts = 2
					}
					for range attempts {
						if _, err := restarted.RetryAccountsManagerSwitch(t.Context(), rec.ID, cfg.OperationID); err != nil {
							t.Fatal(err)
						}
						restarted.accountSwitchMu.Lock()
						run := restarted.accountSwitches[rec.ID]
						var done <-chan struct{}
						if run != nil {
							done = run.done
						}
						restarted.accountSwitchMu.Unlock()
						if done != nil {
							select {
							case <-done:
							case <-time.After(15 * time.Second):
								t.Fatal("handoff worker did not settle")
							}
						}
						after := waitAccountSwitch(t, restarted, reopened, cfg.OperationID)
						if owner == "foreign" {
							if cold.interrupts != 0 || cold.inputs != 0 || cold.destroys != 0 || cold.creates != 0 {
								t.Fatalf("foreign runtime received effects: interrupts=%d input=%d destroy=%d launch=%d", cold.interrupts, cold.inputs, cold.destroys, cold.creates)
							}
							wantPhase := domain.AccountsManagerSwitchWaiting
							if phase == domain.AccountsManagerSwitchStopping {
								wantPhase = domain.AccountsManagerSwitchRecoveryRequired
							}
							binding, _, err := reopened.GetAccountsManagerSessionRoute(t.Context(), rec.ID, before.Provider)
							if err != nil || binding.Revision != before.SourceRevision || binding.AccountID != before.SourceAccountID ||
								after.Phase != wantPhase || after.TargetRevision != 0 || after.TargetGeneration != before.TargetGeneration {
								t.Fatal("foreign runtime allowed rotation or acknowledgement")
							}
							if release, ok := restarted.AcquireSessionInput(rec.ID); ok {
								release()
								t.Fatal("foreign runtime recovery released input")
							}
							if len(gate.acquired) != 1 || len(gate.released) != 0 {
								t.Fatal("foreign runtime recovery released raw input")
							}
							waitSelectedAccountProbe(t, cold.Runtime, ref, ports.FencedAlive)
							alive, err := cold.IsExactSupervisedProcessAlive(t.Context(), ref.Handle, ports.SupervisedProcessRef{SessionID: rec.ID, LaunchID: ref.Generation})
							if err != nil || !alive {
								t.Fatal("foreign workload did not survive recovery", err)
							}
							continue
						}
						wantInterrupts := 0
						if phase == domain.AccountsManagerSwitchWaiting && owner == "same" {
							wantInterrupts = 1
						}
						if cold.interrupts != wantInterrupts || cold.inputs != 0 || cold.creates != 1 || after.Phase != domain.AccountsManagerSwitchReady {
							t.Fatalf("valid handoff failed: interrupts=%d want=%d launches=%d phase=%s code=%s", cold.interrupts, wantInterrupts, cold.creates, after.Phase, after.ErrorCode)
						}
					}
				})
			}
		}
	}
}
