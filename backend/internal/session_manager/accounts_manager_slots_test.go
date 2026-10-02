package sessionmanager

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type accountSlotsRuntime struct {
	accountSwitchRuntime
	generations map[string]string
	extra       string
}

func (*accountSlotsRuntime) LaunchHandles(domain.SessionID) ([]ports.RuntimeHandle, error) {
	return []ports.RuntimeHandle{{ID: "h1"}, {ID: "fallback"}}, nil
}

func (r *accountSlotsRuntime) Create(ctx context.Context, cfg ports.RuntimeConfig) (ports.RuntimeHandle, error) {
	handle, err := r.fakeRuntime.Create(ctx, cfg)
	if err == nil {
		r.generations[handle.ID] = cfg.Env[EnvRuntimeLaunchID]
		r.generations["fallback"] = r.extra
		if r.extra == "same generation" {
			r.generations["fallback"] = cfg.Env[EnvRuntimeLaunchID]
		}
	}
	return handle, err
}

func (r *accountSlotsRuntime) Destroy(ctx context.Context, handle ports.RuntimeHandle) error {
	if err := r.fakeRuntime.Destroy(ctx, handle); err != nil {
		return err
	}
	delete(r.generations, handle.ID)
	return nil
}

func (r *accountSlotsRuntime) ProbeFencedRuntime(_ context.Context, ref ports.FencedRuntimeRef) ports.FencedProbeResult {
	generation := r.generations[ref.Handle.ID]
	if generation == "unknown" {
		return ports.FencedProbeResult{Liveness: ports.FencedUnknown, Reason: ports.FencedReasonProbeFailed}
	}
	if generation == "" {
		return ports.FencedProbeResult{Liveness: ports.FencedDead, Reason: ports.FencedReasonExactAbsent}
	}
	if generation != ref.Generation {
		return ports.FencedProbeResult{Liveness: ports.FencedUnknown, Reason: ports.FencedReasonGenerationMismatch}
	}
	return ports.FencedProbeResult{Liveness: ports.FencedAlive, Reason: ports.FencedReasonExactMatch}
}

func TestAccountsManagerSwitchReadinessRequiresUnambiguousRuntime(t *testing.T) {
	for _, extra := range []string{"", "same generation", "foreign generation", "unknown"} {
		t.Run("extra="+extra, func(t *testing.T) {
			m, st, fake, _, rec, cfg := accountSwitchFixture(t)
			rt := &accountSlotsRuntime{accountSwitchRuntime: accountSwitchRuntime{fake}, generations: map[string]string{"source": rec.Metadata.RuntimeLaunchID}, extra: extra}
			m.runtime, m.switchTargetStartWait = rt, 300*time.Millisecond
			if _, err := m.StartAccountsManagerSwitch(t.Context(), rec.ID, cfg); err != nil {
				t.Fatal(err)
			}
			op := waitAccountSwitch(t, m, st, cfg.OperationID)
			if extra == "" {
				if op.Phase != domain.AccountsManagerSwitchReady {
					t.Fatalf("positive control phase=%s code=%s", op.Phase, op.ErrorCode)
				}
				return
			}
			if op.Phase != domain.AccountsManagerSwitchRecoveryRequired {
				t.Fatal("ambiguous runtime became ready", op.Phase)
			}
			if release, ok := m.AcquireSessionInput(rec.ID); ok {
				release()
				t.Fatal("ambiguous runtime reopened input")
			}
			created, destroyed := fake.created, fake.destroyed
			rt.extra = ""
			if _, err := m.RetryAccountsManagerSwitch(t.Context(), rec.ID, op.ID); err != nil {
				t.Fatal(err)
			}
			final := waitAccountSwitch(t, m, st, cfg.OperationID)
			if extra == "same generation" {
				if final.Phase != domain.AccountsManagerSwitchReady || fake.created != created+1 || fake.destroyed != destroyed+2 || final.TargetRevision <= op.TargetRevision {
					t.Fatal("retry did not retire both proven owners", final.Phase, final.ErrorCode)
				}
			} else if final.Phase != domain.AccountsManagerSwitchRecoveryRequired || fake.created != created || fake.destroyed != destroyed || final.TargetRevision != op.TargetRevision {
				t.Fatal("unknown slot allowed teardown, launch or rotation")
			}
		})
	}
}
