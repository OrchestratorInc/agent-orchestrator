package sessionmanager

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

type accountCancelReadKey struct{}

type accountCancelPhaseStore struct {
	*sqlite.Store
	read *accountRetryBarrier
}

func (s *accountCancelPhaseStore) GetAccountsManagerSwitch(ctx context.Context, id string) (domain.AccountsManagerSwitch, bool, error) {
	op, found, err := s.Store.GetAccountsManagerSwitch(ctx, id)
	if ctx.Value(accountCancelReadKey{}) != nil {
		s.read.pause()
	}
	return op, found, err
}

func TestAccountsManagerSwitchCancelAcrossPreStopPhase(t *testing.T) {
	for _, prefix := range []string{"ptyhost-v1:", ""} {
		for _, next := range []domain.AccountsManagerSwitchPhase{domain.AccountsManagerSwitchWaiting, domain.AccountsManagerSwitchStopping, domain.AccountsManagerSwitchCancelled} {
			t.Run(prefix+"/"+string(next), func(t *testing.T) {
				m, st, runtime, _, rec, cfg := accountSwitchFixture(t)
				rec.Metadata.RuntimeHandleID = prefix + string(rec.ID)
				rec.Activity.State = domain.ActivityActive
				if err := st.UpdateSession(t.Context(), rec); err != nil {
					t.Fatal(err)
				}
				guarded := &accountHandoffOwnershipRuntime{accountTeardownSlots: &accountTeardownSlots{
					accountSlotsRuntime: accountSlotsRuntime{accountSwitchRuntime: accountSwitchRuntime{runtime},
						generations: map[string]string{rec.Metadata.RuntimeHandleID: rec.Metadata.RuntimeLaunchID}},
					handles: []ports.RuntimeHandle{{ID: rec.Metadata.RuntimeHandleID}},
				}}
				m.runtime = guarded
				provider, _ := accountsManagerProvider(rec.Harness)
				op, _, err := st.CreateAccountsManagerSwitch(t.Context(), domain.AccountsManagerSwitch{
					ID: cfg.OperationID, SessionID: rec.ID, Provider: provider, SourceMode: domain.AccountsManagerManaged,
					SourceAccountID: "account-a", SourceRevision: cfg.ExpectedRevision, SourceOwner: rec.ControllerOwner(),
					SourceRuntimeHandleID: rec.Metadata.RuntimeHandleID, TargetMode: cfg.Mode, TargetAccountID: cfg.AccountID,
					TargetGeneration: "reserved", Policy: cfg.Policy, NewConversation: true,
				})
				if err != nil {
					t.Fatal(err)
				}
				gate := &transitionInputGate{acquired: make(chan string, 1), released: make(chan string, 1)}
				m.SetTerminalInputGate(gate)
				store := &accountCancelPhaseStore{Store: st, read: newAccountRetryBarrier(t)}
				m.store = store
				if err := m.ReconcileStartupSafety(t.Context()); err != nil {
					t.Fatal(err)
				}
				result := make(chan error, 1)
				go func() {
					_, err := m.CancelAccountsManagerSwitch(context.WithValue(t.Context(), accountCancelReadKey{}, true), rec.ID, op.ID)
					result <- err
				}()
				store.read.wait(t)
				if _, err := st.AdvanceAccountsManagerSwitch(t.Context(), op.ID, op.Phase, domain.AccountsManagerSwitchWaiting, ""); err != nil {
					t.Fatal(err)
				}
				switch next {
				case domain.AccountsManagerSwitchStopping:
					if _, err := st.PrepareAccountsManagerSwitchStop(t.Context(), op.ID, false, "", rec.ControllerOwner()); err != nil {
						t.Fatal(err)
					}
				case domain.AccountsManagerSwitchCancelled:
					if _, err := m.CancelAccountsManagerSwitch(t.Context(), rec.ID, op.ID); err != nil {
						t.Fatal(err)
					}
				}
				store.read.release()
				select {
				case err = <-result:
				case <-time.After(3 * time.Second):
					t.Fatal("cancellation did not settle")
				}
				if next == domain.AccountsManagerSwitchStopping {
					if !errors.Is(err, domain.ErrAccountsManagerSwitchConflict) {
						t.Fatalf("stop-wins cancellation = %v, want conflict", err)
					}
				} else if err != nil {
					t.Errorf("cancellable pre-stop transition rejected cancellation: %v", err)
				}
				current, _, err := st.GetAccountsManagerSwitch(t.Context(), op.ID)
				want := domain.AccountsManagerSwitchCancelled
				if next == domain.AccountsManagerSwitchStopping {
					want = next
				}
				if err != nil || current.Phase != want || current.TargetGeneration != op.TargetGeneration || current.TargetRevision != 0 {
					t.Errorf("wrong durable cancellation outcome: phase=%s err=%v", current.Phase, err)
				}
				if runtime.created != 0 || runtime.destroyed != 0 || len(runtime.interrupts) != 0 || guarded.inputs != 0 {
					t.Error("cancellation signaled, stopped or launched a runtime")
				}
				if next != domain.AccountsManagerSwitchStopping {
					if len(gate.released) != 1 {
						t.Error("successful cancellation retained the terminal fence")
					}
					if release, ok := m.AcquireSessionInput(rec.ID); !ok {
						t.Error("successful cancellation retained session intake")
					} else {
						release()
					}
					if _, err := m.RetryAccountsManagerSwitch(t.Context(), rec.ID, op.ID); !errors.Is(err, domain.ErrAccountsManagerSwitchConflict) {
						t.Errorf("cancelled operation admitted retry: %v", err)
					}
				}
			})
		}
	}
}

var _ ports.AccountsManagerSwitchStore = (*accountCancelPhaseStore)(nil)

type accountCancelLostResponseStore struct {
	*sqlite.Store
	committed bool
	loseRead  bool
}

func (s *accountCancelLostResponseStore) AdvanceAccountsManagerSwitch(ctx context.Context, id string, expected, next domain.AccountsManagerSwitchPhase, code string) (domain.AccountsManagerSwitch, error) {
	op, err := s.Store.AdvanceAccountsManagerSwitch(ctx, id, expected, next, code)
	if err == nil && next == domain.AccountsManagerSwitchCancelled {
		s.committed = true
		return domain.AccountsManagerSwitch{}, errors.New("synthetic committed response loss")
	}
	return op, err
}

func (s *accountCancelLostResponseStore) GetAccountsManagerSwitch(ctx context.Context, id string) (domain.AccountsManagerSwitch, bool, error) {
	if s.committed && s.loseRead {
		s.loseRead = false
		return domain.AccountsManagerSwitch{}, false, errors.New("synthetic outcome read failure")
	}
	return s.Store.GetAccountsManagerSwitch(ctx, id)
}

func TestAccountsManagerSwitchCancelLostResponseReleasesOnlyConfirmedRun(t *testing.T) {
	for _, loseRead := range []bool{false, true} {
		name := "response lost"
		if loseRead {
			name = "response and first read lost"
		}
		t.Run(name, func(t *testing.T) {
			m, st, runtime, _, rec, cfg := accountSwitchFixture(t)
			op := accountHandoffJournal(t, st, rec, cfg, domain.AccountsManagerSwitchWaiting)
			gate := &transitionInputGate{acquired: make(chan string, 1), released: make(chan string, 1)}
			m.SetTerminalInputGate(gate)
			m.store = &accountCancelLostResponseStore{Store: st, loseRead: loseRead}
			if err := m.ReconcileStartupSafety(t.Context()); err != nil {
				t.Fatal(err)
			}
			cancelled, err := m.CancelAccountsManagerSwitch(t.Context(), rec.ID, op.ID)
			if !loseRead && (err != nil || cancelled.Phase != domain.AccountsManagerSwitchCancelled) {
				t.Errorf("confirmed cancellation not recovered after response loss: %s %v", cancelled.Phase, err)
			}
			if loseRead && err == nil {
				t.Error("unobserved cancellation reported success")
			}
			if cancelled, err = m.CancelAccountsManagerSwitch(t.Context(), rec.ID, op.ID); err != nil || cancelled.Phase != domain.AccountsManagerSwitchCancelled {
				t.Errorf("repeated cancellation did not observe its durable result: %s %v", cancelled.Phase, err)
			}
			if len(gate.released) != 1 {
				t.Error("durable cancellation retained the old terminal fence")
			}
			if release, ok := m.AcquireSessionInput(rec.ID); !ok {
				t.Error("durable cancellation retained session intake")
			} else {
				release()
			}
			if _, err := m.RetryAccountsManagerSwitch(t.Context(), rec.ID, op.ID); !errors.Is(err, domain.ErrAccountsManagerSwitchConflict) {
				t.Errorf("cancelled operation admitted retry: %v", err)
			}
			if runtime.created != 0 || runtime.destroyed != 0 || len(runtime.interrupts) != 0 {
				t.Error("cancellation touched the runtime")
			}
		})
	}
}
