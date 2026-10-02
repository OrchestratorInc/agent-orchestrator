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

type accountHandoffOwnershipRuntime struct {
	*accountTeardownSlots
	unknown ports.FencedProbeReason
	inputs  int
}

func (r *accountHandoffOwnershipRuntime) ProbeFencedRuntime(ctx context.Context, ref ports.FencedRuntimeRef) ports.FencedProbeResult {
	r.fencedRefs = append(r.fencedRefs, ref)
	if r.unknown != "" {
		return ports.FencedProbeResult{Liveness: ports.FencedUnknown, Reason: r.unknown}
	}
	return r.accountTeardownSlots.ProbeFencedRuntime(ctx, ref)
}

func (r *accountHandoffOwnershipRuntime) SendInput(context.Context, ports.RuntimeHandle, string) error {
	r.inputs++
	return nil
}

func (r *accountHandoffOwnershipRuntime) SendMessage(context.Context, ports.RuntimeHandle, string) error {
	r.inputs++
	return nil
}

type accountOwnershipFinishStore struct {
	*sqlite.Store
	beforeUncertainWrite func()
}

func (s *accountOwnershipFinishStore) AdvanceAccountsManagerSwitch(ctx context.Context, id string, expected, next domain.AccountsManagerSwitchPhase, code string) (domain.AccountsManagerSwitch, error) {
	if expected == domain.AccountsManagerSwitchWaiting && next == expected && code == "SOURCE_OWNERSHIP_UNCONFIRMED" {
		s.beforeUncertainWrite()
	}
	return s.Store.AdvanceAccountsManagerSwitch(ctx, id, expected, next, code)
}

func TestAccountsManagerSwitchHandoffCancellationWinsUncertainWrite(t *testing.T) {
	for _, prefix := range []string{"ptyhost-v1:", ""} {
		t.Run(prefix, func(t *testing.T) {
			m, st, fake, _, rec, cfg := accountSwitchFixture(t)
			rec.Metadata.RuntimeHandleID, rec.Activity.State = prefix+string(rec.ID), domain.ActivityActive
			if err := st.UpdateSession(t.Context(), rec); err != nil {
				t.Fatal(err)
			}
			rt := &accountHandoffOwnershipRuntime{accountTeardownSlots: &accountTeardownSlots{
				accountSlotsRuntime: accountSlotsRuntime{accountSwitchRuntime: accountSwitchRuntime{fake}},
				handles:             []ports.RuntimeHandle{{ID: rec.Metadata.RuntimeHandleID}}}, unknown: ports.FencedReasonProbeFailed}
			m.runtime = rt
			gate := &transitionInputGate{acquired: make(chan string, 1), released: make(chan string, 1)}
			m.SetTerminalInputGate(gate)
			m.store = &accountOwnershipFinishStore{Store: st, beforeUncertainWrite: func() {
				if _, err := m.CancelAccountsManagerSwitch(t.Context(), rec.ID, cfg.OperationID); err != nil {
					t.Error(err)
				}
			}}
			if _, err := m.StartAccountsManagerSwitch(t.Context(), rec.ID, cfg); err != nil {
				t.Fatal(err)
			}
			after := waitAccountSwitch(t, m, st, cfg.OperationID)
			if after.Phase != domain.AccountsManagerSwitchCancelled || len(gate.released) != 1 {
				t.Fatal("winning cancellation retained the raw input fence", after.Phase)
			}
			if release, ok := m.AcquireSessionInput(rec.ID); !ok {
				t.Fatal("winning cancellation retained session intake")
			} else {
				release()
			}
			if len(fake.interrupts) != 0 || rt.inputs != 0 || fake.destroyed != 0 || fake.created != 0 {
				t.Fatal("winning cancellation mutated an unproven owner")
			}
		})
	}
}

func accountHandoffJournal(t *testing.T, st *sqlite.Store, rec domain.SessionRecord, cfg AccountsManagerSwitchConfig, phase domain.AccountsManagerSwitchPhase) domain.AccountsManagerSwitch {
	t.Helper()
	provider, supported := accountsManagerProvider(rec.Harness)
	if !supported {
		t.Fatal("fixture harness does not support managed accounts")
	}
	op, _, err := st.CreateAccountsManagerSwitch(t.Context(), domain.AccountsManagerSwitch{ID: cfg.OperationID, SessionID: rec.ID,
		Provider: provider, SourceMode: domain.AccountsManagerManaged, SourceAccountID: "account-a", SourceRevision: cfg.ExpectedRevision,
		SourceOwner: rec.ControllerOwner(), SourceRuntimeHandleID: rec.Metadata.RuntimeHandleID, TargetMode: cfg.Mode, TargetAccountID: cfg.AccountID,
		TargetGeneration: "reserved", Policy: domain.SessionInterfaceTransitionInterrupt, NewConversation: true})
	if err != nil {
		t.Fatal(err)
	}
	op, err = st.AdvanceAccountsManagerSwitch(t.Context(), op.ID, op.Phase, domain.AccountsManagerSwitchWaiting, "")
	if err != nil {
		t.Fatal(err)
	}
	if phase == domain.AccountsManagerSwitchWaiting {
		return op
	}
	op, err = st.PrepareAccountsManagerSwitchStop(t.Context(), op.ID, false, "", rec.ControllerOwner())
	if err != nil {
		t.Fatal(err)
	}
	return op
}

func TestAccountsManagerSwitchHandoffRequiresRecordedOwner(t *testing.T) {
	for _, prefix := range []string{"ptyhost-v1:", ""} {
		for _, stage := range []string{"initial", "cold waiting", "cold stopping"} {
			for _, owner := range []string{"foreign", "missing identity", "probe error", "unknown", "same", "absent"} {
				t.Run(prefix+"/"+stage+"/"+owner, func(t *testing.T) {
					m, st, fake, agent, rec, cfg := accountSwitchFixture(t)
					handle := ports.RuntimeHandle{ID: prefix + string(rec.ID)}
					rec.Metadata.RuntimeHandleID, rec.Activity.State = handle.ID, domain.ActivityActive
					if owner == "same" || owner == "absent" {
						rec.Activity.State = domain.ActivityIdle
					}
					if err := st.UpdateSession(t.Context(), rec); err != nil {
						t.Fatal(err)
					}
					rt := &accountHandoffOwnershipRuntime{accountTeardownSlots: &accountTeardownSlots{accountSlotsRuntime: accountSlotsRuntime{
						accountSwitchRuntime: accountSwitchRuntime{fake}, generations: map[string]string{handle.ID: rec.Metadata.RuntimeLaunchID}},
						handles: []ports.RuntimeHandle{{ID: "ptyhost-v1:" + string(rec.ID)}, {ID: string(rec.ID)}}, active: handle}}
					fake.aliveByHandle = map[string]bool{handle.ID: true}
					unsafe := owner != "same" && owner != "absent"
					if unsafe {
						fake.interruptErr = errors.New("interrupt reached an unproven owner")
					}
					switch owner {
					case "foreign":
						rt.generations[handle.ID] = "foreign-generation"
					case "missing identity":
						rt.unknown = ports.FencedReasonIdentityMissing
					case "probe error":
						rt.unknown = ports.FencedReasonProbeFailed
					case "unknown":
						rt.unknown = ports.FencedReasonRegistryUnreadable
					case "absent":
						delete(rt.generations, handle.ID)
						fake.aliveByHandle[handle.ID] = false
					}
					m.runtime = rt
					fake.onInterrupt = func(got ports.RuntimeHandle) {
						want := ports.FencedRuntimeRef{SessionID: rec.ID, Handle: handle, Generation: rec.Metadata.RuntimeLaunchID}
						if got != handle || len(rt.fencedRefs) == 0 || rt.fencedRefs[len(rt.fencedRefs)-1] != want {
							t.Error("interrupt lacked an exact source ownership probe")
						}
					}
					gate := &transitionInputGate{acquired: make(chan string, 1), released: make(chan string, 1)}
					m.SetTerminalInputGate(gate)
					restart := func() {
						t.Helper()
						if err := st.Close(); err != nil {
							t.Fatal(err)
						}
						reopened, err := sqlite.OpenPreMigrated(rec.Metadata.WorkspacePath)
						if err != nil {
							t.Fatal(err)
						}
						t.Cleanup(func() { _ = reopened.Close() })
						st = reopened
						m = New(Deps{Store: st, Runtime: rt, Lifecycle: lifecycle.New(st, nil), AccountsManager: &accountSwitchRouter{store: st},
							Agents: singleAgent{agent: agent}, DataDir: m.dataDir, LookPath: m.lookPath, BackgroundContext: t.Context()})
						m.interfaceTransition.pollInterval, m.switchTargetStartWait = time.Millisecond, time.Second
						gate = &transitionInputGate{acquired: make(chan string, 1), released: make(chan string, 1)}
						m.SetTerminalInputGate(gate)
						if err := m.ReconcileStartupSafety(t.Context()); err != nil {
							t.Fatal(err)
						}
					}
					var before domain.AccountsManagerSwitch
					if stage != "initial" {
						phase := domain.AccountsManagerSwitchWaiting
						if stage == "cold stopping" {
							phase = domain.AccountsManagerSwitchStopping
						}
						before = accountHandoffJournal(t, st, rec, cfg, phase)
						restart()
						if _, err := m.RetryAccountsManagerSwitch(t.Context(), rec.ID, cfg.OperationID); err != nil {
							t.Fatal(err)
						}
					} else {
						var err error
						before, err = m.StartAccountsManagerSwitch(t.Context(), rec.ID, cfg)
						if err != nil {
							t.Fatal(err)
						}
					}
					assertOutcome := func() {
						t.Helper()
						after := waitAccountSwitch(t, m, st, cfg.OperationID)
						if unsafe {
							if len(fake.interrupts) != 0 || rt.inputs != 0 || fake.destroyed != 0 || fake.created != 0 {
								t.Fatalf("unproven owner received effects: interrupts=%d input=%d destroy=%d launch=%d", len(fake.interrupts), rt.inputs, fake.destroyed, fake.created)
							}
							binding, _, err := st.GetAccountsManagerSessionRoute(t.Context(), rec.ID, before.Provider)
							if err != nil || binding.Revision != before.SourceRevision || binding.AccountID != "account-a" || after.TargetRevision != 0 || after.TargetGeneration != before.TargetGeneration {
								t.Fatal("unproven owner allowed revision rotation", err)
							}
							wantPhase := domain.AccountsManagerSwitchWaiting
							if stage == "cold stopping" {
								wantPhase = domain.AccountsManagerSwitchRecoveryRequired
							}
							if after.Phase != wantPhase || after.ErrorCode == "" {
								t.Fatal("uncertainty lost its durable boundary or error", after.Phase, after.ErrorCode)
							}
							if release, ok := m.AcquireSessionInput(rec.ID); ok {
								release()
								t.Fatal("uncertainty released session intake")
							}
							if len(gate.acquired) != 1 || len(gate.released) != 0 {
								t.Fatal("uncertainty did not preserve the raw input fence")
							}
							return
						}
						wantInterrupts := 0
						if owner == "same" && stage != "cold stopping" {
							wantInterrupts = 1
						}
						if len(fake.interrupts) != wantInterrupts || rt.inputs != 0 || after.Phase != domain.AccountsManagerSwitchReady || fake.created != 1 || fake.destroyed != 1 {
							t.Fatalf("valid recovery failed: interrupts=%d want=%d phase=%s code=%s", len(fake.interrupts), wantInterrupts, after.Phase, after.ErrorCode)
						}
					}
					assertOutcome()
					if unsafe {
						if _, err := m.RetryAccountsManagerSwitch(t.Context(), rec.ID, cfg.OperationID); err != nil {
							t.Fatal(err)
						}
						assertOutcome()
						restart()
						if _, err := m.RetryAccountsManagerSwitch(t.Context(), rec.ID, cfg.OperationID); err != nil {
							t.Fatal(err)
						}
						assertOutcome()
						_, err := m.CancelAccountsManagerSwitch(t.Context(), rec.ID, cfg.OperationID)
						if stage == "cold stopping" {
							if !errors.Is(err, domain.ErrAccountsManagerSwitchConflict) {
								t.Fatal("uncertain teardown became cancellable", err)
							}
							assertOutcome()
						} else {
							if err != nil || len(gate.released) != 1 {
								t.Fatal("pre-stop cancellation did not release raw intake", err)
							}
							if release, ok := m.AcquireSessionInput(rec.ID); !ok {
								t.Fatal("pre-stop cancellation did not release session intake")
							} else {
								release()
							}
							if len(fake.interrupts) != 0 || rt.inputs != 0 || fake.destroyed != 0 || fake.created != 0 {
								t.Fatal("pre-stop cancellation mutated an unproven runtime")
							}
						}
					}
				})
			}
		}
	}
}
