package sessionmanager

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/lifecycle"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

type accountGenerationRuntime struct {
	accountSwitchRuntime
	generation  string
	cleanupFail bool
}

func (r *accountGenerationRuntime) LaunchHandles(id domain.SessionID) ([]ports.RuntimeHandle, error) {
	return []ports.RuntimeHandle{{ID: string(id)}}, nil
}

func (r *accountGenerationRuntime) Create(ctx context.Context, cfg ports.RuntimeConfig) (ports.RuntimeHandle, error) {
	if r.generation != "" {
		return ports.RuntimeHandle{}, errors.New("existing runtime still owns the slot")
	}
	r.createIDs = []string{string(cfg.SessionID)}
	handle, err := r.fakeRuntime.Create(ctx, cfg)
	if err == nil {
		r.generation = cfg.Env[EnvRuntimeLaunchID]
	}
	return handle, err
}

func (r *accountGenerationRuntime) Destroy(ctx context.Context, handle ports.RuntimeHandle) error {
	if r.cleanupFail {
		return errors.New("cleanup unavailable")
	}
	if err := r.fakeRuntime.Destroy(ctx, handle); err != nil {
		return err
	}
	r.generation = ""
	return nil
}

func (r *accountGenerationRuntime) ProbeFencedRuntime(_ context.Context, ref ports.FencedRuntimeRef) ports.FencedProbeResult {
	r.fencedRefs = append(r.fencedRefs, ref)
	if r.generation == "" {
		return ports.FencedProbeResult{Liveness: ports.FencedDead, Reason: ports.FencedReasonExactAbsent}
	}
	if ref.Handle.ID != string(ref.SessionID) || ref.Generation != r.generation {
		return ports.FencedProbeResult{Liveness: ports.FencedUnknown, Reason: ports.FencedReasonGenerationMismatch}
	}
	return ports.FencedProbeResult{Liveness: ports.FencedAlive, Reason: ports.FencedReasonExactMatch}
}

func TestAccountsManagerSwitchColdRetryRetiresSurvivingReservedTarget(t *testing.T) {
	for _, boundary := range []string{"publication and cleanup failure", "crash before publication", "unrelated generation"} {
		t.Run(boundary, func(t *testing.T) {
			foreign := boundary == "unrelated generation"
			m, st, fake, agent, rec, cfg := accountSwitchFixture(t)
			rec.Metadata.RuntimeHandleID = string(rec.ID)
			if err := st.UpdateSession(t.Context(), rec); err != nil {
				t.Fatal(err)
			}
			rt := &accountGenerationRuntime{accountSwitchRuntime: accountSwitchRuntime{fake}, generation: rec.Metadata.RuntimeLaunchID}
			fake.aliveByHandle = map[string]bool{string(rec.ID): true}
			m.runtime = rt
			fake.outputs, m.switchTargetStartWait = []string{ambiguousTerminalOutput}, 30*time.Millisecond
			if _, err := m.StartAccountsManagerSwitch(t.Context(), rec.ID, cfg); err != nil {
				t.Fatal(err)
			}
			first := waitAccountSwitch(t, m, st, cfg.OperationID)
			if first.Phase != domain.AccountsManagerSwitchRecoveryRequired {
				t.Fatal(first.Phase)
			}
			var failed domain.AccountsManagerSwitch
			if boundary == "crash before publication" {
				stored, _, err := st.GetSession(t.Context(), rec.ID)
				if err != nil {
					t.Fatal(err)
				}
				if err := rt.Destroy(t.Context(), runtimeHandle(stored.Metadata)); err != nil {
					t.Fatal(err)
				}
				failed, err = st.RetryAccountsManagerSwitch(t.Context(), first.ID, "unpublished-reservation", stored.ControllerOwner())
				if err != nil {
					t.Fatal(err)
				}
				failed, err = st.AdvanceAccountsManagerSwitch(t.Context(), failed.ID, failed.Phase, domain.AccountsManagerSwitchStarting, "")
				if err != nil {
					t.Fatal(err)
				}
				launch := fake.lastCfg
				launch.Env[EnvRuntimeLaunchID] = failed.TargetGeneration
				if _, err := rt.Create(t.Context(), launch); err != nil {
					t.Fatal(err)
				}
				// This crash cut deliberately runs neither publication nor cleanup.
			} else {
				m.lcm = &accountSwitchSurvivingPublication{Manager: lifecycle.New(st, nil), runtime: rt}
				if _, err := m.RetryAccountsManagerSwitch(t.Context(), rec.ID, cfg.OperationID); err != nil {
					t.Fatal(err)
				}
				failed = waitAccountSwitch(t, m, st, cfg.OperationID)
			}
			if failed.TargetGeneration == first.TargetGeneration || rt.generation != failed.TargetGeneration {
				t.Fatal("fixture did not preserve the unpublished reserved runtime")
			}
			stored, _, err := st.GetSession(t.Context(), rec.ID)
			if err != nil || stored.Metadata.RuntimeLaunchID != first.TargetGeneration {
				t.Fatal("retired owner was not preserved", err)
			}
			project, err := m.loadProject(t.Context(), rec.ProjectID)
			if err != nil {
				t.Fatal(err)
			}
			if err := st.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := sqlite.OpenPreMigrated(project.Path)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			rt.cleanupFail, fake.outputs = false, []string{idleTerminalOutput}
			if foreign {
				rt.generation = "foreign-generation"
			}
			created, destroyed := fake.created, fake.destroyed
			restarted := New(Deps{Store: reopened, Runtime: rt, Lifecycle: lifecycle.New(reopened, nil), AccountsManager: &accountSwitchRouter{store: reopened},
				Agents: singleAgent{agent: agent}, DataDir: t.TempDir(), LookPath: func(string) (string, error) { return "/bin/true", nil }, BackgroundContext: t.Context()})
			restarted.switchTargetStartWait = time.Second
			if err := restarted.ReconcileStartupSafety(t.Context()); err != nil {
				t.Fatal(err)
			}
			if _, err := restarted.RetryAccountsManagerSwitch(t.Context(), rec.ID, cfg.OperationID); err != nil {
				t.Fatal(err)
			}
			final := waitAccountSwitch(t, restarted, reopened, cfg.OperationID)
			if foreign {
				if final.Phase != domain.AccountsManagerSwitchRecoveryRequired || fake.created != created || fake.destroyed != destroyed || final.TargetRevision != failed.TargetRevision {
					t.Fatal("unknown owner was destroyed or rotated")
				}
				return
			}
			if final.Phase != domain.AccountsManagerSwitchReady || final.TargetRevision <= failed.TargetRevision {
				t.Fatalf("surviving reserved target stranded: phase=%s code=%s", final.Phase, final.ErrorCode)
			}
			if fake.destroyed != destroyed+1 || fake.created != created+1 || agent.lastLaunch.Prompt != "" {
				t.Fatal("retry did not retire exactly one runtime and launch passively")
			}
		})
	}
}

type accountSwitchSurvivingPublication struct {
	*lifecycle.Manager
	runtime *accountGenerationRuntime
}

func (l *accountSwitchSurvivingPublication) MarkSpawned(context.Context, domain.SessionID, domain.SessionMetadata) error {
	l.runtime.cleanupFail = true
	return errors.New("publication unavailable")
}
