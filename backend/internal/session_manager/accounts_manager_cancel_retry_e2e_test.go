//go:build e2e && (linux || darwin)

package sessionmanager

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type accountRealCancelledRetry struct {
	*accountRealHandoffOwnership
	uncertain   atomic.Bool
	probes      atomic.Int64
	interrupted chan struct{}
}

func (r *accountRealCancelledRetry) ProbeFencedRuntime(ctx context.Context, ref ports.FencedRuntimeRef) ports.FencedProbeResult {
	r.probes.Add(1)
	if r.uncertain.Load() {
		return ports.FencedProbeResult{Liveness: ports.FencedUnknown, Reason: ports.FencedReasonProbeFailed}
	}
	return r.Runtime.ProbeFencedRuntime(ctx, ref)
}

func (r *accountRealCancelledRetry) Interrupt(ctx context.Context, handle ports.RuntimeHandle) error {
	r.interrupted <- struct{}{}
	return r.accountRealHandoffOwnership.Interrupt(ctx, handle)
}

func TestAccountsManagerSwitchRealProductionCancelledRetry(t *testing.T) {
	for _, fallback := range []bool{false, true} {
		for _, cold := range []bool{false, true} {
			name := "direct"
			if fallback {
				name = "fallback"
			}
			if cold {
				name += "/startup"
			} else {
				name += "/initial"
			}
			t.Run(name, func(t *testing.T) {
				m, st, rt, _, rec, cfg := realSelectedAccountRuntimeFixture(t, fallback)
				rec.Activity.State = domain.ActivityActive
				if err := st.UpdateSession(t.Context(), rec); err != nil {
					t.Fatal(err)
				}
				ref := ports.FencedRuntimeRef{SessionID: rec.ID, Handle: runtimeHandle(rec.Metadata), Generation: rec.Metadata.RuntimeLaunchID}
				if err := rt.Destroy(t.Context(), ref.Handle); err != nil {
					t.Fatal(err)
				}
				binary, err := m.executable()
				if err != nil {
					t.Fatal(err)
				}
				handle, err := rt.Create(t.Context(), ports.RuntimeConfig{SessionID: rec.ID, WorkspacePath: rec.Metadata.WorkspacePath,
					Argv: []string{binary, "agent-process", "supervise", "--session", string(rec.ID), "--launch", ref.Generation, "--", "/bin/sleep", "60"},
					Env:  map[string]string{EnvSupervisedProcess: "1", EnvRuntimeLaunchID: ref.Generation, EnvDataDir: m.dataDir, EnvRunFile: m.runFilePath}})
				if err != nil || handle != ref.Handle {
					t.Fatal("source did not occupy its recorded slot", err)
				}
				waitSelectedAccountProbe(t, rt, ref, ports.FencedAlive)
				observed := &accountRealCancelledRetry{accountRealHandoffOwnership: &accountRealHandoffOwnership{Runtime: rt}, interrupted: make(chan struct{}, 1)}
				observed.uncertain.Store(true)
				m.runtime = observed
				var probes int64
				exerciseAccountCancelledRetry(t, m, st, rec, cfg, cold, func() {
					probes = observed.probes.Load()
					observed.uncertain.Store(false)
				}, observed.interrupted)
				if observed.probes.Load() != probes || observed.interrupts != 0 || observed.inputs != 0 || observed.destroys != 0 || observed.creates != 0 {
					t.Errorf("cancelled retry reached a real process: probes=%d interrupts=%d input=%d destroy=%d launch=%d", observed.probes.Load()-probes, observed.interrupts, observed.inputs, observed.destroys, observed.creates)
				}
				alive, err := observed.IsExactSupervisedProcessAlive(t.Context(), ref.Handle, ports.SupervisedProcessRef{SessionID: rec.ID, LaunchID: ref.Generation})
				if err != nil || !alive {
					t.Fatal("cancelled source workload was interrupted", err)
				}
			})
		}
	}
}
