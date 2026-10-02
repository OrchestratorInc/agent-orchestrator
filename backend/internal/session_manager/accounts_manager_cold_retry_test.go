package sessionmanager

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/lifecycle"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

func coldRetryJournal(t *testing.T, st *sqlite.Store, rec domain.SessionRecord, cfg AccountsManagerSwitchConfig, phase domain.AccountsManagerSwitchPhase) domain.AccountsManagerSwitch {
	t.Helper()
	provider, supported := accountsManagerProvider(rec.Harness)
	if !supported {
		t.Fatal("fixture has no managed account provider")
	}
	op, _, err := st.CreateAccountsManagerSwitch(t.Context(), domain.AccountsManagerSwitch{
		ID: cfg.OperationID, SessionID: rec.ID, Provider: provider, SourceMode: domain.AccountsManagerManaged,
		SourceAccountID: "account-a", SourceRevision: cfg.ExpectedRevision, SourceOwner: rec.ControllerOwner(),
		SourceRuntimeHandleID: rec.Metadata.RuntimeHandleID, TargetMode: cfg.Mode, TargetAccountID: cfg.AccountID,
		TargetGeneration: "reserved", Policy: cfg.Policy, NewConversation: cfg.NewConversation,
	})
	if err != nil {
		t.Fatal(err)
	}
	if phase != op.Phase {
		op, err = st.AdvanceAccountsManagerSwitch(t.Context(), op.ID, op.Phase, phase, "")
		if err != nil {
			t.Fatal(err)
		}
	}
	return op
}

func reopenColdRetryManager(t *testing.T, previous *Manager, st *sqlite.Store, path string, validate func(context.Context) error) (*Manager, *sqlite.Store, *transitionInputGate) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	if err := previous.WaitAgentSwitchWorkers(ctx); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err := sqlite.OpenPreMigrated(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	m := New(Deps{Store: st, Runtime: previous.runtime, Lifecycle: lifecycle.New(st, nil),
		AccountsManager: &accountSwitchRouter{store: st, validate: validate}, Agents: previous.agents,
		DataDir: previous.dataDir, RunFilePath: previous.runFilePath, Executable: previous.executable,
		LookPath: previous.lookPath, BackgroundContext: t.Context()})
	m.switchTargetStartWait = time.Second
	m.interfaceTransition.pollInterval = time.Millisecond
	t.Cleanup(func() {
		joinCtx, joinCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer joinCancel()
		if err := m.WaitAgentSwitchWorkers(joinCtx); err != nil {
			t.Error(err)
		}
	})
	gate := &transitionInputGate{acquired: make(chan string, 1), released: make(chan string, 1)}
	m.SetTerminalInputGate(gate)
	if err := m.ReconcileStartupSafety(t.Context()); err != nil {
		t.Fatal(err)
	}
	return m, st, gate
}

type coldRetryFailureStore struct {
	*sqlite.Store
	entered chan struct{}
	release chan struct{}
}

func (s *coldRetryFailureStore) AdvanceAccountsManagerSwitch(ctx context.Context, id string, expected, next domain.AccountsManagerSwitchPhase, code string) (domain.AccountsManagerSwitch, error) {
	if code == "TARGET_REVALIDATION_UNAVAILABLE" {
		close(s.entered)
		select {
		case <-s.release:
		case <-ctx.Done():
			return domain.AccountsManagerSwitch{}, ctx.Err()
		}
	}
	return s.Store.AdvanceAccountsManagerSwitch(ctx, id, expected, next, code)
}

func TestAccountsManagerColdRetryCancellationWins(t *testing.T) {
	for _, prefix := range []string{"ptyhost-v1:", ""} {
		for _, phase := range []domain.AccountsManagerSwitchPhase{domain.AccountsManagerSwitchRequested, domain.AccountsManagerSwitchWaiting} {
			for _, boundary := range []string{"validation", "error-write"} {
				t.Run(prefix+"/"+string(phase)+"/"+boundary, func(t *testing.T) {
					m, st, rt, _, rec, cfg := accountSwitchFixture(t)
					rec.Metadata.RuntimeHandleID = prefix + string(rec.ID)
					if err := st.UpdateSession(t.Context(), rec); err != nil {
						t.Fatal(err)
					}
					rt.aliveByHandle = map[string]bool{rec.Metadata.RuntimeHandleID: true}
					op := coldRetryJournal(t, st, rec, cfg, phase)
					entered, release := make(chan struct{}), make(chan struct{})
					defer func() {
						select {
						case <-release:
						default:
							close(release)
						}
						waitAccountSwitch(t, m, st, op.ID)
					}()
					m, st, gate := reopenColdRetryManager(t, m, st, rec.Metadata.WorkspacePath, func(context.Context) error {
						if boundary == "validation" {
							close(entered)
							<-release
						}
						return errors.New("target unavailable")
					})
					if boundary == "error-write" {
						m.store = &coldRetryFailureStore{Store: st, entered: entered, release: release}
					}
					if _, err := m.RetryAccountsManagerSwitch(t.Context(), rec.ID, op.ID); err != nil {
						t.Fatal(err)
					}
					select {
					case <-entered:
					case <-time.After(3 * time.Second):
						t.Fatal("retry did not reach the cancellation barrier")
					}
					if _, err := m.CancelAccountsManagerSwitch(t.Context(), rec.ID, op.ID); err != nil {
						t.Fatal(err)
					}
					close(release)
					final := waitAccountSwitch(t, m, st, op.ID)
					if final.Phase != domain.AccountsManagerSwitchCancelled || final.ErrorCode != "" || final.TargetRevision != 0 || len(gate.released) != 1 {
						t.Fatal("late retry outcome replaced cancellation or retained its fence")
					}
					if _, err := m.RetryAccountsManagerSwitch(t.Context(), rec.ID, op.ID); !errors.Is(err, domain.ErrAccountsManagerSwitchConflict) {
						t.Fatal("cancelled retry restarted", err)
					}
					m, st, gate = reopenColdRetryManager(t, m, st, rec.Metadata.WorkspacePath, nil)
					if _, err := m.RetryAccountsManagerSwitch(t.Context(), rec.ID, op.ID); !errors.Is(err, domain.ErrAccountsManagerSwitchConflict) || len(gate.acquired) != 0 {
						t.Fatal("restart revived a cancelled operation", err)
					}
					if rt.created != 0 || rt.destroyed != 0 || len(rt.interrupts) != 0 {
						t.Fatal("cancelled retry changed runtime execution")
					}
				})
			}
		}
	}
}

func TestAccountsManagerColdRetryPhaseControls(t *testing.T) {
	for _, phase := range []domain.AccountsManagerSwitchPhase{domain.AccountsManagerSwitchFailed, domain.AccountsManagerSwitchCancelled, domain.AccountsManagerSwitchStopping} {
		t.Run(string(phase), func(t *testing.T) {
			m, st, rt, _, rec, cfg := accountSwitchFixture(t)
			op := coldRetryJournal(t, st, rec, cfg, domain.AccountsManagerSwitchWaiting)
			var err error
			if phase == domain.AccountsManagerSwitchStopping {
				_, err = st.PrepareAccountsManagerSwitchStop(t.Context(), op.ID, false, "", rec.ControllerOwner())
			} else {
				_, err = st.AdvanceAccountsManagerSwitch(t.Context(), op.ID, op.Phase, phase, "")
			}
			if err != nil {
				t.Fatal(err)
			}
			m, st, gate := reopenColdRetryManager(t, m, st, rec.Metadata.WorkspacePath, func(context.Context) error { return errors.New("target unavailable") })
			_, err = m.RetryAccountsManagerSwitch(t.Context(), rec.ID, op.ID)
			if phase.Terminal() {
				if !errors.Is(err, domain.ErrAccountsManagerSwitchConflict) || len(gate.acquired) != 0 {
					t.Fatal("historical terminal journal was reopened", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			final := waitAccountSwitch(t, m, st, op.ID)
			if final.Phase != domain.AccountsManagerSwitchRecoveryRequired || final.TargetRevision != 0 || len(gate.released) != 0 {
				t.Fatal("post-stop outage softened the irreversible boundary")
			}
			if _, err := m.CancelAccountsManagerSwitch(t.Context(), rec.ID, op.ID); !errors.Is(err, domain.ErrAccountsManagerSwitchConflict) {
				t.Fatal("post-stop outage became cancellable", err)
			}
			if rt.created != 0 || rt.destroyed != 0 || len(rt.interrupts) != 0 {
				t.Fatal("post-stop outage changed runtime ownership")
			}
		})
	}
}

func TestAccountsManagerColdRetryTargetOutage(t *testing.T) {
	for _, prefix := range []string{"ptyhost-v1:", ""} {
		for _, phase := range []domain.AccountsManagerSwitchPhase{domain.AccountsManagerSwitchRequested, domain.AccountsManagerSwitchWaiting} {
			for _, action := range []string{"retry", "cancel"} {
				t.Run(prefix+"/"+string(phase)+"/"+action, func(t *testing.T) {
					m, st, rt, agent, rec, cfg := accountSwitchFixture(t)
					rec.Metadata.RuntimeHandleID = prefix + string(rec.ID)
					if err := st.UpdateSession(t.Context(), rec); err != nil {
						t.Fatal(err)
					}
					rt.aliveByHandle = map[string]bool{rec.Metadata.RuntimeHandleID: true}
					original := coldRetryJournal(t, st, rec, cfg, phase)
					var gate *transitionInputGate
					for range 2 {
						m, st, gate = reopenColdRetryManager(t, m, st, rec.Metadata.WorkspacePath, func(context.Context) error {
							return errors.New("target runner unavailable")
						})
						for range 2 {
							if _, err := m.RetryAccountsManagerSwitch(t.Context(), rec.ID, original.ID); err != nil {
								t.Fatal(err)
							}
							op := waitAccountSwitch(t, m, st, original.ID)
							if op.Phase != domain.AccountsManagerSwitchWaiting || op.ErrorCode != "TARGET_REVALIDATION_UNAVAILABLE" {
								t.Fatalf("target outage discarded safe recovery: phase=%s code=%s", op.Phase, op.ErrorCode)
							}
							if !m.AccountsManagerSwitchCanRetry(op) || !op.SameRequest(original) || op.SourceOwner != original.SourceOwner || op.TargetGeneration != original.TargetGeneration || op.TargetRevision != 0 {
								t.Fatal("target outage lost retry eligibility or recorded intent")
							}
							if rt.created != 0 || rt.destroyed != 0 || len(rt.interrupts) != 0 || len(gate.released) != 0 {
								t.Fatal("target outage changed execution or released raw input")
							}
							if release, ok := m.AcquireSessionInput(rec.ID); ok {
								release()
								t.Fatal("target outage released session intake")
							}
							if release, ok := m.AcquireSessionInput("unrelated-session"); !ok {
								t.Fatal("target outage fenced unrelated work")
							} else {
								release()
							}
							binding, found, err := st.GetAccountsManagerSessionRoute(t.Context(), rec.ID, original.Provider)
							if err != nil || !found || binding.AccountID != original.SourceAccountID || binding.Revision != original.SourceRevision {
								t.Fatal("target outage changed the committed binding", err)
							}
						}
					}
					if action == "cancel" {
						if _, err := m.CancelAccountsManagerSwitch(t.Context(), rec.ID, original.ID); err != nil {
							t.Fatal(err)
						}
					} else {
						m.accountsManager.(*accountSwitchRouter).validate = nil
						if _, err := m.RetryAccountsManagerSwitch(t.Context(), rec.ID, original.ID); err != nil {
							t.Fatal(err)
						}
					}
					final := waitAccountSwitch(t, m, st, original.ID)
					if len(gate.released) != 1 || m.AccountsManagerSwitchCanRetry(final) {
						t.Fatal("settled recovery kept a fence or exposed another retry")
					}
					binding, _, err := st.GetAccountsManagerSessionRoute(t.Context(), rec.ID, original.Provider)
					if err != nil || binding.Blocked {
						t.Fatal("settled recovery retained authorization fence", err)
					}
					if action == "cancel" {
						if final.Phase != domain.AccountsManagerSwitchCancelled || binding.AccountID != original.SourceAccountID || binding.Revision != original.SourceRevision || rt.created != 0 || rt.destroyed != 0 {
							t.Fatal("cancellation changed the source")
						}
					} else if final.Phase != domain.AccountsManagerSwitchReady || binding.AccountID != cfg.AccountID || rt.created != 1 || rt.destroyed != 1 || agent.lastLaunch.Prompt != "" {
						t.Fatalf("explicit recovery did not settle: phase=%s code=%s", final.Phase, final.ErrorCode)
					}
				})
			}
		}
	}
}
