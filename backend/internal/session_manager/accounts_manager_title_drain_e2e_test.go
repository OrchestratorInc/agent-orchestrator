//go:build e2e && (linux || darwin)

package sessionmanager

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/terminalui"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type accountTitleAgent struct{ accountRetainedPaneAgent }

func (*accountTitleAgent) InspectTerminalSurface(output string) ports.TerminalSurfaceObservation {
	if !strings.Contains(output, "frame-ready") {
		return (transitionSurfaceAgent{}).InspectTerminalSurface(strings.TrimSpace(output))
	}
	observation := ports.TerminalSurfaceObservation{Work: ports.TerminalSurfaceWorkIdle}
	switch terminalui.LastBorderedPromptComposerState(output, "❯") {
	case terminalui.ComposerEmpty:
		observation.Composer = ports.TerminalComposerEmpty
	case terminalui.ComposerDraft:
		observation.Composer = ports.TerminalComposerDraft
	}
	return observation
}

type accountTitleRuntime struct {
	*accountRealHandoffOwnership
	sampled chan struct{}
	once    sync.Once
}

func (r *accountTitleRuntime) GetStyledOutput(ctx context.Context, handle ports.RuntimeHandle, lines int) (string, error) {
	output, err := r.accountRealHandoffOwnership.GetStyledOutput(ctx, handle, lines)
	if err == nil && strings.Contains(output, "frame-ready") {
		r.once.Do(func() { close(r.sampled) })
	}
	return output, err
}

func TestAccountsManagerDrainRealTitleOutput(t *testing.T) {
	for _, fallback := range []bool{false, true} {
		for _, draft := range []string{"", "real unsent draft"} {
			name := "direct"
			if fallback {
				name = "fallback"
			}
			t.Run(name+"/"+draft, func(t *testing.T) {
				m, st, runtime, _, rec, cfg := realSelectedAccountRuntimeFixture(t, fallback)
				if err := runtime.Destroy(t.Context(), runtimeHandle(rec.Metadata)); err != nil {
					t.Fatal(err)
				}
				binary, err := m.executable()
				if err != nil {
					t.Fatal(err)
				}
				frame := "\x1b[2J\x1b[H────────────────────────────────\r\n❯ " + draft + "\r\n────────────────────────────────\x1b[2;3H\x1b]0;✳ synthetic-finished-turn\a\x1b[4;1Hframe-ready"
				handle, err := runtime.Create(t.Context(), ports.RuntimeConfig{SessionID: rec.ID, WorkspacePath: rec.Metadata.WorkspacePath,
					Argv: []string{binary, "agent-process", "supervise", "--session", string(rec.ID), "--launch", rec.Metadata.RuntimeLaunchID, "--",
						"/bin/sh", "-c", `printf '%s' "$1"; sleep 30`, "title-fixture", frame},
					Env: map[string]string{EnvSupervisedProcess: "1", EnvRuntimeLaunchID: rec.Metadata.RuntimeLaunchID, EnvDataDir: m.dataDir, EnvRunFile: m.runFilePath}})
				if err != nil {
					t.Fatal(err)
				}
				ref := ports.FencedRuntimeRef{SessionID: rec.ID, Handle: handle, Generation: rec.Metadata.RuntimeLaunchID}
				waitSelectedAccountProbe(t, runtime, ref, ports.FencedAlive)
				rec.Metadata.RuntimeHandleID = handle.ID
				rec.Activity.State = domain.ActivityActive
				if err := st.UpdateSession(t.Context(), rec); err != nil {
					t.Fatal(err)
				}
				observed := &accountTitleRuntime{accountRealHandoffOwnership: &accountRealHandoffOwnership{Runtime: runtime}, sampled: make(chan struct{})}
				m.runtime, m.agents = observed, singleAgent{agent: &accountTitleAgent{}}
				cfg.Policy = domain.SessionInterfaceTransitionDrain
				if _, err := m.StartAccountsManagerSwitch(t.Context(), rec.ID, cfg); err != nil {
					t.Fatal(err)
				}
				select {
				case <-observed.sampled:
				case <-time.After(4 * time.Second):
					t.Fatal("drain did not observe the live source")
				}
				binding, _, err := st.GetAccountsManagerSessionRoute(t.Context(), rec.ID, domain.AccountsManagerProviderClaude)
				if err != nil || binding.AccountID != "account-a" {
					t.Fatal("account committed before active work ended", err)
				}
				rec.Activity.State, rec.Activity.LastActivityAt = domain.ActivityIdle, time.Now()
				if err := st.UpdateSession(t.Context(), rec); err != nil {
					t.Fatal(err)
				}
				op := waitAccountSwitch(t, m, st, cfg.OperationID)
				binding, _, err = st.GetAccountsManagerSessionRoute(t.Context(), rec.ID, domain.AccountsManagerProviderClaude)
				if err != nil || observed.interrupts != 0 || observed.inputs != 0 {
					t.Fatal("drain sent input or lost the binding", err)
				}
				if draft != "" {
					if op.TargetRevision != 0 || binding.AccountID != "account-a" || observed.destroys != 0 || observed.creates != 0 {
						t.Fatal("real draft was discarded", op.Phase, op.ErrorCode)
					}
					waitSelectedAccountProbe(t, runtime, ref, ports.FencedAlive)
				} else if op.Phase != domain.AccountsManagerSwitchReady || binding.AccountID != "account-b" || observed.destroys == 0 || observed.creates != 1 {
					t.Fatal("finished turn did not drain through production capture", op.Phase, op.ErrorCode)
				}
			})
		}
	}
}
