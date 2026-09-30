//go:build e2e && (linux || darwin)

package sessionmanager

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/runtime/runtimeselect"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestAccountsManagerColdRetryRealTargetOutage(t *testing.T) {
	for _, fallback := range []bool{false, true} {
		name := "direct"
		if fallback {
			name = "fallback"
		}
		for _, action := range []string{"retry", "cancel"} {
			t.Run(name+"/"+action, func(t *testing.T) {
				m, st, rt, agent, rec, cfg := realSelectedAccountRuntimeFixture(t, fallback)
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
					t.Fatal("fixture did not start the exact source in its reserved slot", err)
				}
				waitSelectedAccountProbe(t, rt, ref, ports.FencedAlive)
				rec.Activity.State = domain.ActivityActive
				if err := st.UpdateSession(t.Context(), rec); err != nil {
					t.Fatal(err)
				}
				op := coldRetryJournal(t, st, rec, cfg, domain.AccountsManagerSwitchWaiting)
				var observed *accountRealHandoffOwnership
				var gate *transitionInputGate
				for range 2 {
					observed = &accountRealHandoffOwnership{Runtime: runtimeselect.New(nil, m.runFilePath)}
					m.runtime = observed
					m, st, gate = reopenColdRetryManager(t, m, st, rec.Metadata.WorkspacePath, func(context.Context) error { return errors.New("target unavailable") })
					m.switchTargetStartWait = 3 * time.Second
					if _, err := m.RetryAccountsManagerSwitch(t.Context(), rec.ID, op.ID); err != nil {
						t.Fatal(err)
					}
					pending := waitAccountSwitch(t, m, st, op.ID)
					if pending.Phase != domain.AccountsManagerSwitchWaiting || !m.AccountsManagerSwitchCanRetry(pending) || pending.TargetRevision != 0 || len(gate.released) != 0 {
						t.Fatalf("production outage discarded pre-stop recovery: phase=%s code=%s", pending.Phase, pending.ErrorCode)
					}
					if observed.interrupts != 0 || observed.inputs != 0 || observed.destroys != 0 || observed.creates != 0 {
						t.Fatal("production outage changed the original source")
					}
					waitSelectedAccountProbe(t, observed.Runtime, ref, ports.FencedAlive)
					alive, err := observed.IsExactSupervisedProcessAlive(t.Context(), ref.Handle, ports.SupervisedProcessRef{SessionID: rec.ID, LaunchID: ref.Generation})
					if err != nil || !alive {
						t.Fatal("exact source process did not survive cold retry outage", err)
					}
				}
				if action == "cancel" {
					_, err = m.CancelAccountsManagerSwitch(t.Context(), rec.ID, op.ID)
				} else {
					m.accountsManager.(*accountSwitchRouter).validate = nil
					_, err = m.RetryAccountsManagerSwitch(t.Context(), rec.ID, op.ID)
				}
				if err != nil {
					t.Fatal(err)
				}
				m.accountSwitchMu.Lock()
				run := m.accountSwitches[rec.ID]
				var done <-chan struct{}
				if run != nil {
					done = run.done
				}
				m.accountSwitchMu.Unlock()
				if done != nil {
					select {
					case <-done:
					case <-time.After(15 * time.Second):
						t.Fatal("production recovery worker did not settle")
					}
				}
				final := waitAccountSwitch(t, m, st, op.ID)
				binding, _, err := st.GetAccountsManagerSessionRoute(t.Context(), rec.ID, op.Provider)
				if err != nil || binding.Blocked || len(gate.released) != 1 {
					t.Fatal("settled production recovery retained a fence", err)
				}
				if action == "cancel" {
					if final.Phase != domain.AccountsManagerSwitchCancelled || binding.Revision != op.SourceRevision || binding.AccountID != op.SourceAccountID || observed.interrupts != 0 || observed.destroys != 0 || observed.creates != 0 || observed.inputs != 0 {
						t.Fatal("cancellation changed execution or the source account")
					}
					waitSelectedAccountProbe(t, observed.Runtime, ref, ports.FencedAlive)
				} else {
					if final.Phase != domain.AccountsManagerSwitchReady || binding.AccountID != cfg.AccountID || observed.creates != 1 || observed.interrupts != 1 || observed.inputs != 0 || agent.lastLaunch.Prompt != "" {
						t.Fatalf("explicit production retry failed: phase=%s code=%s", final.Phase, final.ErrorCode)
					}
					target, _, err := st.GetSession(t.Context(), rec.ID)
					if err != nil {
						t.Fatal(err)
					}
					waitSelectedAccountProbe(t, observed.Runtime, ports.FencedRuntimeRef{SessionID: rec.ID, Handle: runtimeHandle(target.Metadata), Generation: final.TargetGeneration}, ports.FencedAlive)
				}
			})
		}
	}
}
