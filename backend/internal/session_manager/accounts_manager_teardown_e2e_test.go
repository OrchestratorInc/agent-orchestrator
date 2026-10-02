//go:build e2e && (linux || darwin)

package sessionmanager

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/runtime/runtimeselect"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/lifecycle"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

type accountRealDestroyTakeover struct {
	runtimeselect.Runtime
	handle   ports.RuntimeHandle
	replace  func(context.Context) error
	destroys int
	creates  int
}

func (r *accountRealDestroyTakeover) LaunchHandles(id domain.SessionID) ([]ports.RuntimeHandle, error) {
	return r.Runtime.(ports.RuntimeLaunchHandleResolver).LaunchHandles(id)
}

func (r *accountRealDestroyTakeover) Create(ctx context.Context, cfg ports.RuntimeConfig) (ports.RuntimeHandle, error) {
	r.creates++
	return r.Runtime.Create(ctx, cfg)
}

func (r *accountRealDestroyTakeover) Destroy(ctx context.Context, handle ports.RuntimeHandle) error {
	if handle == r.handle {
		r.destroys++
		if r.destroys == 1 {
			return r.replace(ctx)
		}
	}
	return r.Runtime.Destroy(ctx, handle)
}

func TestAccountsManagerSwitchRealProductionDestroyTakeover(t *testing.T) {
	for _, fallback := range []bool{false, true} {
		name := "direct"
		if fallback {
			name = "fallback"
		}
		for _, boundary := range []string{"source", "reserved target"} {
			t.Run(name+"/"+boundary, func(t *testing.T) {
				m, st, _, agent, rec, cfg := realSelectedAccountRuntimeFixture(t, fallback)
				var before domain.AccountsManagerSwitch
				if boundary == "reserved target" {
					m.switchTargetStartWait = time.Millisecond
					if _, err := m.StartAccountsManagerSwitch(t.Context(), rec.ID, cfg); err != nil {
						t.Fatal(err)
					}
					before = waitAccountSwitch(t, m, st, cfg.OperationID)
					if before.Phase != domain.AccountsManagerSwitchRecoveryRequired || before.TargetRevision == 0 {
						t.Fatal("fixture did not retain an unready target")
					}
				}
				stored, _, err := st.GetSession(t.Context(), rec.ID)
				if err != nil {
					t.Fatal(err)
				}
				ref := ports.FencedRuntimeRef{SessionID: rec.ID, Handle: runtimeHandle(stored.Metadata), Generation: "foreign-generation"}
				binary, err := m.executable()
				if err != nil {
					t.Fatal(err)
				}
				rt := &accountRealDestroyTakeover{Runtime: runtimeselect.New(nil, m.runFilePath), handle: ref.Handle}
				replaced := false
				rt.replace = func(ctx context.Context) error {
					if err := rt.Runtime.Destroy(ctx, ref.Handle); err != nil {
						return err
					}
					handle, err := rt.Runtime.Create(ctx, ports.RuntimeConfig{SessionID: rec.ID, WorkspacePath: rec.Metadata.WorkspacePath,
						Argv: []string{binary, "agent-process", "supervise", "--session", string(rec.ID), "--launch", ref.Generation, "--", "/bin/sleep", "30"},
						Env:  map[string]string{EnvSupervisedProcess: "1", EnvRuntimeLaunchID: ref.Generation, EnvDataDir: m.dataDir, EnvRunFile: m.runFilePath}})
					if err != nil {
						return err
					}
					if handle != ref.Handle {
						return errors.New("replacement did not occupy the original slot")
					}
					probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
					defer cancel()
					tick := time.NewTicker(10 * time.Millisecond)
					defer tick.Stop()
					for {
						if rt.Runtime.ProbeFencedRuntime(probeCtx, ref).Liveness == ports.FencedAlive {
							replaced = true
							return errors.New("destroy response unavailable after replacement")
						}
						select {
						case <-probeCtx.Done():
							return probeCtx.Err()
						case <-tick.C:
						}
					}
				}
				restart := func() {
					t.Helper()
					if err := st.Close(); err != nil {
						t.Fatal(err)
					}
					st, err = sqlite.OpenPreMigrated(rec.Metadata.WorkspacePath)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { _ = st.Close() })
					rt.Runtime = runtimeselect.New(nil, m.runFilePath)
					cold := New(Deps{Store: st, Runtime: rt, Lifecycle: lifecycle.New(st, nil), AccountsManager: &accountSwitchRouter{store: st},
						Agents: singleAgent{agent: agent}, DataDir: m.dataDir, RunFilePath: m.runFilePath, LookPath: m.lookPath, BackgroundContext: t.Context()})
					cold.executable, cold.switchTargetStartWait = m.executable, 3*time.Second
					m = cold
					if err := m.ReconcileStartupSafety(t.Context()); err != nil {
						t.Fatal(err)
					}
				}
				restart()
				if boundary == "source" {
					before, err = m.StartAccountsManagerSwitch(t.Context(), rec.ID, cfg)
				} else {
					_, err = m.RetryAccountsManagerSwitch(t.Context(), rec.ID, cfg.OperationID)
				}
				if err != nil {
					t.Fatal(err)
				}
				assertHeld := func() {
					t.Helper()
					m.accountSwitchMu.Lock()
					done := m.accountSwitches[rec.ID].done
					m.accountSwitchMu.Unlock()
					select {
					case <-done:
					case <-time.After(15 * time.Second):
						t.Fatal("takeover worker did not settle")
					}
					after := waitAccountSwitch(t, m, st, cfg.OperationID)
					if !replaced || rt.destroys != 1 || rt.creates != 0 {
						t.Fatalf("replacement=%v destructive calls=%d launches=%d", replaced, rt.destroys, rt.creates)
					}
					if after.Phase != domain.AccountsManagerSwitchRecoveryRequired || after.TargetRevision != before.TargetRevision || after.TargetGeneration != before.TargetGeneration {
						t.Fatal("replacement allowed rotation or acknowledgement", after.Phase)
					}
					waitSelectedAccountProbe(t, rt.Runtime, ref, ports.FencedAlive)
					if release, ok := m.AcquireSessionInput(rec.ID); ok {
						release()
						t.Fatal("replacement reopened input")
					}
				}
				assertHeld()
				restart()
				for range 2 {
					if _, err := m.RetryAccountsManagerSwitch(t.Context(), rec.ID, cfg.OperationID); err != nil {
						t.Fatal(err)
					}
					assertHeld()
				}
			})
		}
	}
}

func TestAccountsManagerSwitchRealProductionTwoLiveSlots(t *testing.T) {
	m, st, rt, agent, rec, cfg := realSelectedAccountRuntimeFixture(t, true)
	m.switchTargetStartWait = time.Millisecond
	if _, err := m.StartAccountsManagerSwitch(t.Context(), rec.ID, cfg); err != nil {
		t.Fatal(err)
	}
	before := waitAccountSwitch(t, m, st, cfg.OperationID)
	if before.Phase != domain.AccountsManagerSwitchRecoveryRequired || before.TargetRevision == 0 {
		t.Fatal("fixture did not retain an unready fallback target")
	}
	stored, _, err := st.GetSession(t.Context(), rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	fallback := ports.FencedRuntimeRef{SessionID: rec.ID, Handle: runtimeHandle(stored.Metadata), Generation: before.TargetGeneration}
	waitSelectedAccountProbe(t, rt, fallback, ports.FencedAlive)
	setAccountHostFailure(t, false)
	binary, err := m.executable()
	if err != nil {
		t.Fatal(err)
	}
	direct, err := rt.Create(t.Context(), ports.RuntimeConfig{SessionID: rec.ID, WorkspacePath: rec.Metadata.WorkspacePath,
		Argv: []string{binary, "agent-process", "supervise", "--session", string(rec.ID), "--launch", before.TargetGeneration, "--", "/bin/sleep", "30"},
		Env:  map[string]string{EnvSupervisedProcess: "1", EnvRuntimeLaunchID: before.TargetGeneration, EnvDataDir: m.dataDir, EnvRunFile: m.runFilePath}})
	if err != nil || direct == fallback.Handle {
		t.Fatal("fixture did not create both live runtime slots", err)
	}
	waitSelectedAccountProbe(t, rt, ports.FencedRuntimeRef{SessionID: rec.ID, Handle: direct, Generation: before.TargetGeneration}, ports.FencedAlive)
	waitSelectedAccountProbe(t, rt, fallback, ports.FencedAlive)
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := sqlite.OpenPreMigrated(rec.Metadata.WorkspacePath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	cold := runtimeselect.New(nil, m.runFilePath)
	restarted := New(Deps{Store: reopened, Runtime: cold, Lifecycle: lifecycle.New(reopened, nil), AccountsManager: &accountSwitchRouter{store: reopened},
		Agents: singleAgent{agent: agent}, DataDir: m.dataDir, RunFilePath: m.runFilePath, LookPath: m.lookPath, BackgroundContext: t.Context()})
	restarted.executable, restarted.switchTargetStartWait = m.executable, 3*time.Second
	if err := restarted.ReconcileStartupSafety(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.RetryAccountsManagerSwitch(t.Context(), rec.ID, cfg.OperationID); err != nil {
		t.Fatal(err)
	}
	after := waitAccountSwitch(t, restarted, reopened, cfg.OperationID)
	if after.Phase != domain.AccountsManagerSwitchReady || after.TargetRevision <= before.TargetRevision || after.TargetGeneration == before.TargetGeneration {
		t.Fatal("two-slot recovery did not retire both owners before launching", after.Phase, after.ErrorCode)
	}
	waitSelectedAccountProbe(t, cold, fallback, ports.FencedDead)
	waitSelectedAccountProbe(t, cold, ports.FencedRuntimeRef{SessionID: rec.ID, Handle: direct, Generation: after.TargetGeneration}, ports.FencedAlive)
	if agent.lastLaunch.Prompt != "" {
		t.Fatal("two-slot recovery replayed the task")
	}
}
