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

func TestAccountsManagerSwitchDestroyRetryRequiresSameOwner(t *testing.T) {
	for _, handle := range []string{"ptyhost-v1:account-session", "account-session"} {
		for _, reason := range []ports.FencedProbeReason{ports.FencedReasonGenerationMismatch, ports.FencedReasonIdentityMissing, ports.FencedReasonProbeFailed} {
			t.Run(handle+"/"+string(reason), func(t *testing.T) {
				firstErr := errors.New("destroy response unavailable")
				rt := &fakeRuntime{aliveByHandle: map[string]bool{handle: true}, destroyErrSequence: []error{firstErr}}
				ref := ports.FencedRuntimeRef{SessionID: "account-session", Handle: ports.RuntimeHandle{ID: handle}, Generation: "original-generation"}
				rt.onDestroy = func(call int, _ ports.RuntimeHandle) {
					if call == 0 {
						rt.fencedResult = ports.FencedProbeResult{Liveness: ports.FencedUnknown, Reason: reason}
					}
				}
				m := New(Deps{Runtime: rt})
				err := m.stopSourceRuntime(t.Context(), ref)
				if rt.destroyed != 1 {
					t.Fatalf("destroy called %d times after ownership changed; want exactly one", rt.destroyed)
				}
				if !errors.Is(err, ErrSwitchSourceStopUnconfirmed) || !errors.Is(err, firstErr) {
					t.Fatalf("uncertain ownership lost its stop failure: %v", err)
				}
				if !rt.aliveByHandle[handle] {
					t.Fatal("replacement owner was destroyed")
				}
				if len(rt.fencedRefs) != 1 || rt.fencedRefs[0] != ref {
					t.Fatal("retry did not probe the exact original owner")
				}
			})
		}
	}
}

func TestAccountsManagerSwitchDestroyRetryIdempotence(t *testing.T) {
	responseErr := errors.New("destroy response unavailable")
	for _, handle := range []string{"ptyhost-v1:account-session", "account-session"} {
		for _, tc := range []struct {
			name       string
			errors     []error
			absentAt   int
			unknownAt  int
			wantCalls  int
			wantProbes int
			wantError  bool
		}{
			{name: "first response succeeds", wantCalls: 1},
			{name: "first command committed", errors: []error{responseErr}, absentAt: 1, wantCalls: 1, wantProbes: 1},
			{name: "same owner retry succeeds", errors: []error{responseErr}, wantCalls: 2, wantProbes: 1},
			{name: "second command committed", errors: []error{responseErr, responseErr}, absentAt: 2, wantCalls: 2, wantProbes: 2},
			{name: "same owner remains alive", errors: []error{responseErr, responseErr}, wantCalls: 2, wantProbes: 2, wantError: true},
			{name: "second command leaves uncertainty", errors: []error{responseErr, responseErr}, unknownAt: 2, wantCalls: 2, wantProbes: 2, wantError: true},
		} {
			t.Run(handle+"/"+tc.name, func(t *testing.T) {
				rt := &fakeRuntime{aliveByHandle: map[string]bool{handle: true}, destroyErrSequence: tc.errors}
				ref := ports.FencedRuntimeRef{SessionID: "account-session", Handle: ports.RuntimeHandle{ID: handle}, Generation: "original-generation"}
				rt.onDestroy = func(call int, _ ports.RuntimeHandle) {
					if call+1 == tc.absentAt {
						rt.aliveByHandle[handle] = false
					}
					if call+1 == tc.unknownAt {
						rt.fencedResult = ports.FencedProbeResult{Liveness: ports.FencedUnknown, Reason: ports.FencedReasonProbeFailed}
					}
				}
				m := New(Deps{Runtime: rt})
				err := m.stopSourceRuntime(t.Context(), ref)
				if (err != nil) != tc.wantError || (tc.wantError && !errors.Is(err, ErrSwitchSourceStopUnconfirmed)) {
					t.Fatalf("stop error=%v wantError=%v", err, tc.wantError)
				}
				if rt.destroyed != tc.wantCalls || len(rt.fencedRefs) != tc.wantProbes {
					t.Fatalf("destroys=%d probes=%d want=%d/%d", rt.destroyed, len(rt.fencedRefs), tc.wantCalls, tc.wantProbes)
				}
				for _, got := range rt.fencedRefs {
					if got != ref {
						t.Fatal("retry changed the owner being probed")
					}
				}
			})
		}
	}
}

type accountTeardownSlots struct {
	accountSlotsRuntime
	handles []ports.RuntimeHandle
	active  ports.RuntimeHandle
}

func (r *accountTeardownSlots) LaunchHandles(domain.SessionID) ([]ports.RuntimeHandle, error) {
	return r.handles, nil
}

func (r *accountTeardownSlots) Create(ctx context.Context, cfg ports.RuntimeConfig) (ports.RuntimeHandle, error) {
	r.createIDs = []string{r.active.ID}
	handle, err := r.fakeRuntime.Create(ctx, cfg)
	if err == nil {
		r.generations[handle.ID] = cfg.Env[EnvRuntimeLaunchID]
	}
	return handle, err
}

func TestAccountsManagerSwitchDestroyTakeoverRetainsColdRecovery(t *testing.T) {
	for _, prefix := range []string{"ptyhost-v1:", ""} {
		for _, boundary := range []string{"source", "reserved target"} {
			for _, replacement := range []string{"foreign-generation", "unknown"} {
				t.Run(prefix+"/"+boundary+"/"+replacement, func(t *testing.T) {
					m, st, fake, agent, rec, cfg := accountSwitchFixture(t)
					handle := ports.RuntimeHandle{ID: prefix + string(rec.ID)}
					rec.Metadata.RuntimeHandleID = handle.ID
					if err := st.UpdateSession(t.Context(), rec); err != nil {
						t.Fatal(err)
					}
					rt := &accountTeardownSlots{accountSlotsRuntime: accountSlotsRuntime{
						accountSwitchRuntime: accountSwitchRuntime{fake}, generations: map[string]string{handle.ID: rec.Metadata.RuntimeLaunchID}},
						handles: []ports.RuntimeHandle{{ID: "ptyhost-v1:" + string(rec.ID)}, {ID: string(rec.ID)}}, active: handle}
					if prefix == "" {
						rt.handles[0], rt.handles[1] = rt.handles[1], rt.handles[0]
					}
					fake.aliveByHandle = map[string]bool{handle.ID: true}
					m.runtime = rt
					var before domain.AccountsManagerSwitch
					if boundary == "reserved target" {
						fake.outputs, m.switchTargetStartWait = []string{ambiguousTerminalOutput}, 30*time.Millisecond
						if _, err := m.StartAccountsManagerSwitch(t.Context(), rec.ID, cfg); err != nil {
							t.Fatal(err)
						}
						before = waitAccountSwitch(t, m, st, cfg.OperationID)
						if before.Phase != domain.AccountsManagerSwitchRecoveryRequired || before.TargetRevision == 0 {
							t.Fatal("fixture did not preserve an unready target")
						}
					}
					created, destroyed := fake.created, fake.destroyed
					fake.destroyErrSequence = make([]error, destroyed+1)
					fake.destroyErrSequence[destroyed] = errors.New("destroy response unavailable")
					fake.onDestroy = func(call int, got ports.RuntimeHandle) {
						if call == destroyed {
							if got != handle {
								t.Error("first destructive call targeted the wrong slot")
							}
							rt.generations[handle.ID] = replacement
						}
					}
					fake.outputs, m.switchTargetStartWait = []string{idleTerminalOutput}, time.Second
					var err error
					if boundary == "source" {
						before, err = m.StartAccountsManagerSwitch(t.Context(), rec.ID, cfg)
					} else {
						_, err = m.RetryAccountsManagerSwitch(t.Context(), rec.ID, cfg.OperationID)
					}
					if err != nil {
						t.Fatal(err)
					}
					assertHeld := func(manager *Manager, db *sqlite.Store) {
						t.Helper()
						after := waitAccountSwitch(t, manager, db, cfg.OperationID)
						if fake.destroyed != destroyed+1 || fake.created != created || rt.generations[handle.ID] != replacement {
							t.Fatalf("takeover allowed destruction or launch: destroys=%d want=%d creates=%d want=%d owner=%q", fake.destroyed, destroyed+1, fake.created, created, rt.generations[handle.ID])
						}
						if after.Phase != domain.AccountsManagerSwitchRecoveryRequired || after.TargetRevision != before.TargetRevision || after.TargetGeneration != before.TargetGeneration {
							t.Fatal("takeover allowed rotation or acknowledgement", after.Phase)
						}
						if release, ok := manager.AcquireSessionInput(rec.ID); ok {
							release()
							t.Fatal("uncertain ownership reopened input")
						}
					}
					assertHeld(m, st)
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
					restarted := New(Deps{Store: reopened, Runtime: rt, Lifecycle: lifecycle.New(reopened, nil),
						AccountsManager: &accountSwitchRouter{store: reopened}, Agents: singleAgent{agent: agent},
						DataDir: m.dataDir, LookPath: m.lookPath, BackgroundContext: t.Context()})
					if err := restarted.ReconcileStartupSafety(t.Context()); err != nil {
						t.Fatal(err)
					}
					for range 2 {
						if _, err := restarted.RetryAccountsManagerSwitch(t.Context(), rec.ID, cfg.OperationID); err != nil {
							t.Fatal(err)
						}
						assertHeld(restarted, reopened)
					}
				})
			}
		}
	}
}
