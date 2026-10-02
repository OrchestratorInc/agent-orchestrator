package sessionmanager

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/lifecycle"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

func TestAccountsManagerSwitchStartupPreservesCancellationBoundary(t *testing.T) {
	for _, phase := range []domain.AccountsManagerSwitchPhase{domain.AccountsManagerSwitchRequested, domain.AccountsManagerSwitchWaiting, domain.AccountsManagerSwitchStopping} {
		t.Run(string(phase), func(t *testing.T) {
			m, st, rt, _, rec, cfg := accountSwitchFixture(t)
			op, _, err := st.CreateAccountsManagerSwitch(t.Context(), domain.AccountsManagerSwitch{ID: cfg.OperationID, SessionID: rec.ID,
				Provider: domain.AccountsManagerProviderClaude, SourceMode: domain.AccountsManagerManaged, SourceAccountID: "account-a", SourceRevision: cfg.ExpectedRevision,
				SourceOwner: rec.ControllerOwner(), SourceRuntimeHandleID: rec.Metadata.RuntimeHandleID, TargetMode: cfg.Mode, TargetAccountID: cfg.AccountID,
				TargetGeneration: "reserved", Policy: domain.SessionInterfaceTransitionDrain, NewConversation: true})
			if err != nil {
				t.Fatal(err)
			}
			if phase != domain.AccountsManagerSwitchRequested {
				op, err = st.AdvanceAccountsManagerSwitch(t.Context(), op.ID, op.Phase, domain.AccountsManagerSwitchWaiting, "")
				if err != nil {
					t.Fatal(err)
				}
			}
			if phase == domain.AccountsManagerSwitchStopping {
				if _, err := st.PrepareAccountsManagerSwitchStop(t.Context(), op.ID, false, "", rec.ControllerOwner()); err != nil {
					t.Fatal(err)
				}
			}
			m.accountsManager.(*accountSwitchRouter).validate = func(context.Context) error {
				t.Error("cancellation attempted to validate an unavailable target")
				return errors.New("target unavailable")
			}
			gate := &transitionInputGate{acquired: make(chan string, 1), released: make(chan string, 1)}
			m.SetTerminalInputGate(gate)
			if err := m.ReconcileStartupSafety(t.Context()); err != nil {
				t.Fatal(err)
			}
			select {
			case <-gate.acquired:
			default:
				t.Fatal("raw terminal intake was not fenced")
			}
			if release, ok := m.AcquireSessionInput(rec.ID); ok {
				release()
				t.Fatal("session intake was not fenced")
			}
			_, err = m.CancelAccountsManagerSwitch(t.Context(), rec.ID, op.ID)
			if phase == domain.AccountsManagerSwitchStopping {
				if !errors.Is(err, domain.ErrAccountsManagerSwitchConflict) {
					t.Fatal("uncertain stop became cancellable", err)
				}
				select {
				case <-gate.released:
					t.Fatal("uncertain stop released raw intake")
				default:
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			select {
			case <-gate.released:
			default:
				t.Fatal("cancellation retained the raw input fence")
			}
			if release, ok := m.AcquireSessionInput(rec.ID); !ok {
				t.Fatal("cancellation retained the operation fence")
			} else {
				release()
			}
			binding, _, err := st.GetAccountsManagerSessionRoute(t.Context(), rec.ID, op.Provider)
			if err != nil || binding.Revision != cfg.ExpectedRevision || binding.AccountID != "account-a" || binding.Blocked || rt.created != 0 || rt.destroyed != 0 {
				t.Fatal("cancellation changed the source or authorization", err)
			}
		})
	}
}

type accountStopBoundaryStore struct {
	*sqlite.Store
	before  bool
	entered chan struct{}
	release chan struct{}
}

func (s *accountStopBoundaryStore) PrepareAccountsManagerSwitchStop(ctx context.Context, id string, empty bool, native string, owner domain.SessionControllerOwner) (domain.AccountsManagerSwitch, error) {
	var op domain.AccountsManagerSwitch
	var err error
	if !s.before {
		op, err = s.Store.PrepareAccountsManagerSwitchStop(ctx, id, empty, native, owner)
	}
	close(s.entered)
	select {
	case <-ctx.Done():
		return op, ctx.Err()
	case <-s.release:
	}
	if s.before {
		return s.Store.PrepareAccountsManagerSwitchStop(ctx, id, empty, native, owner)
	}
	return op, err
}

func TestAccountsManagerSwitchStartupRetryCancellationRace(t *testing.T) {
	for _, phase := range []domain.AccountsManagerSwitchPhase{domain.AccountsManagerSwitchRequested, domain.AccountsManagerSwitchWaiting} {
		for _, outcome := range []string{"resume", "cancel wins", "stop wins"} {
			t.Run(string(phase)+"/"+outcome, func(t *testing.T) {
				m, st, rt, agent, rec, cfg := accountSwitchFixture(t)
				op, _, err := st.CreateAccountsManagerSwitch(t.Context(), domain.AccountsManagerSwitch{ID: cfg.OperationID, SessionID: rec.ID,
					Provider: domain.AccountsManagerProviderClaude, SourceMode: domain.AccountsManagerManaged, SourceAccountID: "account-a", SourceRevision: cfg.ExpectedRevision,
					SourceOwner: rec.ControllerOwner(), SourceRuntimeHandleID: rec.Metadata.RuntimeHandleID, TargetMode: cfg.Mode, TargetAccountID: cfg.AccountID,
					TargetGeneration: "reserved", Policy: domain.SessionInterfaceTransitionInterrupt, NewConversation: true})
				if err != nil {
					t.Fatal(err)
				}
				if phase == domain.AccountsManagerSwitchWaiting {
					if _, err := st.AdvanceAccountsManagerSwitch(t.Context(), op.ID, op.Phase, phase, ""); err != nil {
						t.Fatal(err)
					}
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
				m.store, m.lcm, m.accountsManager = reopened, lifecycle.New(reopened, nil), &accountSwitchRouter{store: reopened}
				if err := m.ReconcileStartupSafety(t.Context()); err != nil {
					t.Fatal(err)
				}
				var boundary *accountStopBoundaryStore
				var once sync.Once
				release := func() {
					if boundary != nil {
						once.Do(func() { close(boundary.release) })
					}
				}
				t.Cleanup(release)
				if outcome != "resume" {
					boundary = &accountStopBoundaryStore{Store: reopened, before: outcome == "cancel wins", entered: make(chan struct{}), release: make(chan struct{})}
					m.store = boundary
				}
				if _, err := m.RetryAccountsManagerSwitch(t.Context(), rec.ID, cfg.OperationID); err != nil {
					t.Fatal(err)
				}
				if boundary != nil {
					select {
					case <-boundary.entered:
					case <-time.After(3 * time.Second):
						t.Fatal("retry did not reach the durable stop boundary")
					}
					_, err := m.CancelAccountsManagerSwitch(t.Context(), rec.ID, cfg.OperationID)
					if outcome == "cancel wins" && err != nil {
						t.Fatal(err)
					}
					if outcome == "stop wins" && !errors.Is(err, domain.ErrAccountsManagerSwitchConflict) {
						t.Fatal("durable stopping became cancellable", err)
					}
					release()
				}
				final := waitAccountSwitch(t, m, reopened, cfg.OperationID)
				binding, _, err := reopened.GetAccountsManagerSessionRoute(t.Context(), rec.ID, op.Provider)
				if err != nil || binding.Blocked {
					t.Fatal("settled operation retained authorization fence", err)
				}
				if outcome == "cancel wins" {
					if final.Phase != domain.AccountsManagerSwitchCancelled || rt.created != 0 || rt.destroyed != 0 || binding.Revision != cfg.ExpectedRevision || binding.AccountID != "account-a" {
						t.Fatal("cancellation lost the pre-stop race")
					}
				} else if final.Phase != domain.AccountsManagerSwitchReady || rt.created != 1 || rt.destroyed != 1 || binding.AccountID != cfg.AccountID || agent.lastLaunch.Prompt != "" {
					t.Fatalf("cold retry did not resume passively: phase=%s code=%s", final.Phase, final.ErrorCode)
				}
			})
		}
	}
}
