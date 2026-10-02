package sessionmanager

import (
	"testing"
	"time"

	codexagent "github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/codex"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/pkg/contract"
)

type reportCodexAgent struct{ recordingAgent }

func (*reportCodexAgent) InspectTerminalSurface(output string) ports.TerminalSurfaceObservation {
	return codexagent.New().InspectTerminalSurface(output)
}

func TestAccountsManagerReportedTerminalBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, screen string
		policy       domain.SessionInterfaceTransitionPolicy
		wantPhase    domain.AccountsManagerSwitchPhase
		wantCode     string
	}{
		{"drain-idle-control", "\x1b[1m›\x1b[0m \x1b[2mWrite tests\x1b[0m\n\ngpt-5 low · <WORKSPACE>\n", domain.SessionInterfaceTransitionDrain, domain.AccountsManagerSwitchReady, ""},
		{"drain-unknown-screen", "screen unavailable", domain.SessionInterfaceTransitionDrain, domain.AccountsManagerSwitchFailed, "SOURCE_NOT_QUIESCENT"},
		{"interrupt-unknown-screen", "screen unavailable", domain.SessionInterfaceTransitionInterrupt, domain.AccountsManagerSwitchRecoveryRequired, "TARGET_NOT_READY"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, st, rt, _, rec, cfg := accountSwitchFixture(t)
			rec.Harness = domain.HarnessCodex
			rec.Activity.State = domain.ActivityIdle
			rec.FirstSignalAt = time.Now()
			if err := st.UpdateSession(t.Context(), rec); err != nil {
				t.Fatal(err)
			}
			binding, _, err := st.GetOrCreateAccountsManagerSessionRoute(t.Context(), domain.AccountsManagerSessionRoute{
				SessionID: rec.ID, Provider: domain.AccountsManagerProviderCodex, Mode: domain.AccountsManagerManaged, AccountID: "fixture-source",
			})
			if err != nil {
				t.Fatal(err)
			}
			cfg.ExpectedRevision, cfg.AccountID, cfg.Policy = binding.Revision, "fixture-target", tc.policy
			m.agents = singleAgent{agent: &reportCodexAgent{}}
			m.interfaceTransition.idleSettle = time.Millisecond
			m.interfaceTransition.staleIdleLimit = 20 * time.Millisecond
			m.switchTargetStartWait = 500 * time.Millisecond
			rt.outputs = []string{tc.screen}
			if _, err := m.StartAccountsManagerSwitch(t.Context(), rec.ID, cfg); err != nil {
				t.Fatal(err)
			}
			op := waitAccountSwitch(t, m, st, cfg.OperationID)
			if op.Phase != tc.wantPhase || op.ErrorCode != tc.wantCode {
				t.Fatalf("phase=%s code=%s", op.Phase, op.ErrorCode)
			}
			current, _, err := st.GetAccountsManagerSessionRoute(t.Context(), rec.ID, binding.Provider)
			if err != nil {
				t.Fatal(err)
			}
			if op.Phase == domain.AccountsManagerSwitchFailed {
				if current.AccountID != binding.AccountID || current.Revision != binding.Revision || current.Blocked || rt.destroyed != 0 || rt.created != 0 {
					t.Fatal("failed drain changed source binding or runtime")
				}
			} else if current.AccountID != cfg.AccountID || current.Revision <= binding.Revision || current.Revision != op.TargetRevision || rt.created != 1 {
				t.Fatal("target binding was not committed with the recorded revision")
			}
			if op.Phase == domain.AccountsManagerSwitchRecoveryRequired {
				for range 2 {
					previous := op
					if _, err := m.RetryAccountsManagerSwitch(t.Context(), rec.ID, op.ID); err != nil {
						t.Fatal(err)
					}
					op = waitAccountSwitch(t, m, st, cfg.OperationID)
					current, _, err = st.GetAccountsManagerSessionRoute(t.Context(), rec.ID, binding.Provider)
					if err != nil {
						t.Fatal(err)
					}
					if op.Phase != tc.wantPhase || op.ErrorCode != tc.wantCode || op.TargetRevision <= previous.TargetRevision || op.TargetGeneration == previous.TargetGeneration || current.AccountID != cfg.AccountID || current.Revision != op.TargetRevision {
						t.Fatal("retry did not preserve target choice and rotate its fenced generation")
					}
					if release, allowed := m.AcquireSessionInput(rec.ID); allowed {
						release()
						t.Fatal("unready target admitted input")
					}
				}
			}
			after, _, err := st.GetSession(t.Context(), rec.ID)
			if err != nil || after.Metadata.Prompt != rec.Metadata.Prompt {
				t.Fatal("saved task was lost")
			}
			if op.TargetRevision != 0 {
				if !after.FirstSignalAt.IsZero() {
					t.Fatal("target inherited a stale source activity receipt")
				}
				status := contract.DeriveStatus(contract.SessionFacts{
					Activity: contract.ActivityState(after.Activity.State), LastActivityAt: after.Activity.LastActivityAt,
					HasSignal: !after.FirstSignalAt.IsZero(), SignalExpected: true,
				}, nil, after.Activity.LastActivityAt.Add(91*time.Second), 90*time.Second)
				if string(status) != string(domain.StatusNoSignal) {
					t.Fatal("a silent target did not derive no_signal after the grace period")
				}
			}
			t.Logf("phase=%s code=%s sourceRevision=%d targetRevision=%d creates=%d", op.Phase, op.ErrorCode, binding.Revision, op.TargetRevision, rt.created)
		})
	}
}
