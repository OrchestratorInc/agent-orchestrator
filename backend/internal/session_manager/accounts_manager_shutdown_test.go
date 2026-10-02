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

type accountShutdownBoundaryStore struct {
	*accountStopBoundaryStore
	phase domain.AccountsManagerSwitchPhase
}

func (s *accountShutdownBoundaryStore) AdvanceAccountsManagerSwitch(ctx context.Context, id string, expected, next domain.AccountsManagerSwitchPhase, code string) (domain.AccountsManagerSwitch, error) {
	if s.phase == domain.AccountsManagerSwitchRequested && expected == s.phase && next == domain.AccountsManagerSwitchWaiting {
		close(s.entered)
		<-ctx.Done()
		return domain.AccountsManagerSwitch{}, ctx.Err()
	}
	return s.Store.AdvanceAccountsManagerSwitch(ctx, id, expected, next, code)
}

func TestAccountsManagerSwitchShutdownPreservesWaitingIntent(t *testing.T) {
	testAccountSwitchShutdown(t, domain.AccountsManagerSwitchWaiting)
}

func TestAccountsManagerSwitchShutdownPreservesRequestedIntent(t *testing.T) {
	testAccountSwitchShutdown(t, domain.AccountsManagerSwitchRequested)
}

func TestAccountsManagerSwitchShutdownRetainsStopBoundary(t *testing.T) {
	testAccountSwitchShutdown(t, domain.AccountsManagerSwitchStopping)
}

func testAccountSwitchShutdown(t *testing.T, phase domain.AccountsManagerSwitchPhase) {
	t.Helper()
	for _, prefix := range []string{"ptyhost-v1:", ""} {
		for _, action := range []string{"retry", "cancel"} {
			t.Run(prefix+"/"+action, func(t *testing.T) {
				m, st, rt, agent, rec, cfg := accountSwitchFixture(t)
				rec.Metadata.RuntimeHandleID = prefix + string(rec.ID)
				if err := st.UpdateSession(t.Context(), rec); err != nil {
					t.Fatal(err)
				}
				rt.aliveByHandle = map[string]bool{rec.Metadata.RuntimeHandleID: true}
				shutdownCtx, shutdown := context.WithCancel(t.Context())
				defer shutdown()
				m.backgroundContext = shutdownCtx
				boundary := &accountShutdownBoundaryStore{phase: phase, accountStopBoundaryStore: &accountStopBoundaryStore{
					Store: st, before: phase != domain.AccountsManagerSwitchStopping, entered: make(chan struct{}), release: make(chan struct{})}}
				m.store = boundary
				gate := &transitionInputGate{acquired: make(chan string, 1), released: make(chan string, 1)}
				m.SetTerminalInputGate(gate)
				if _, err := m.StartAccountsManagerSwitch(t.Context(), rec.ID, cfg); err != nil {
					t.Fatal(err)
				}
				select {
				case <-boundary.entered:
				case <-time.After(3 * time.Second):
					t.Fatal("switch did not reach shutdown boundary")
				}
				shutdown()
				ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
				defer cancel()
				if err := m.WaitAgentSwitchWorkers(ctx); err != nil {
					t.Fatal(err)
				}
				op := waitAccountSwitch(t, m, st, cfg.OperationID)
				wantPhase := phase
				if phase == domain.AccountsManagerSwitchStopping {
					wantPhase = domain.AccountsManagerSwitchRecoveryRequired
				}
				if op.Phase != wantPhase {
					t.Fatalf("daemon shutdown discarded recovery intent: phase=%s want=%s code=%s", op.Phase, wantPhase, op.ErrorCode)
				}
				if len(gate.released) != 0 || rt.created != 0 || rt.destroyed != 0 || len(rt.interrupts) != 0 {
					t.Fatal("shutdown released intake or changed runtime ownership")
				}
				if release, ok := m.AcquireSessionInput(rec.ID); ok {
					release()
					t.Fatal("shutdown released the session fence")
				}
				unchanged, found, err := st.GetAccountsManagerSessionRoute(t.Context(), rec.ID, op.Provider)
				if err != nil || !found || unchanged.AccountID != "account-a" || unchanged.Revision != cfg.ExpectedRevision || op.TargetRevision != 0 {
					t.Fatal("shutdown changed the committed account", err)
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
				restarted := New(Deps{Store: reopened, Runtime: m.runtime, Lifecycle: lifecycle.New(reopened, nil), AccountsManager: &accountSwitchRouter{store: reopened},
					Agents: m.agents, DataDir: m.dataDir, LookPath: m.lookPath, BackgroundContext: t.Context()})
				restarted.switchTargetStartWait = time.Second
				restarted.interfaceTransition.pollInterval = time.Millisecond
				restoredGate := &transitionInputGate{acquired: make(chan string, 1), released: make(chan string, 1)}
				restarted.SetTerminalInputGate(restoredGate)
				defer func() {
					joinCtx, joinCancel := context.WithTimeout(context.Background(), 3*time.Second)
					defer joinCancel()
					if err := restarted.WaitAgentSwitchWorkers(joinCtx); err != nil {
						t.Error(err)
					}
				}()
				if err := restarted.ReconcileStartupSafety(t.Context()); err != nil {
					t.Fatal(err)
				}
				if len(restoredGate.acquired) != 1 || len(restoredGate.released) != 0 {
					t.Fatal("restart did not restore the raw input fence")
				}
				if release, ok := restarted.AcquireSessionInput(rec.ID); ok {
					release()
					t.Fatal("restart did not restore the operation fence")
				}
				if action == "cancel" {
					_, err = restarted.CancelAccountsManagerSwitch(t.Context(), rec.ID, op.ID)
					if phase == domain.AccountsManagerSwitchStopping {
						if !errors.Is(err, domain.ErrAccountsManagerSwitchConflict) {
							t.Fatal("shutdown made a durable stop cancellable", err)
						}
						_, err = restarted.RetryAccountsManagerSwitch(t.Context(), rec.ID, op.ID)
					}
				} else {
					_, err = restarted.RetryAccountsManagerSwitch(t.Context(), rec.ID, op.ID)
				}
				if err != nil {
					t.Fatal("restart lost the authorized recovery action", err)
				}
				final := waitAccountSwitch(t, restarted, reopened, op.ID)
				if len(restoredGate.released) != 1 {
					t.Fatal("settled operation retained the raw input fence")
				}
				if release, ok := restarted.AcquireSessionInput(rec.ID); !ok {
					t.Fatal("settled operation retained the session fence")
				} else {
					release()
				}
				binding, found, err := reopened.GetAccountsManagerSessionRoute(t.Context(), rec.ID, op.Provider)
				if err != nil || !found || binding.Blocked {
					t.Fatal("settled operation retained authorization fence", err)
				}
				if action == "cancel" && phase != domain.AccountsManagerSwitchStopping {
					if final.Phase != domain.AccountsManagerSwitchCancelled || binding.Revision != cfg.ExpectedRevision || binding.AccountID != "account-a" || rt.created != 0 || rt.destroyed != 0 {
						t.Fatal("cancellation changed the source")
					}
				} else if final.Phase != domain.AccountsManagerSwitchReady || binding.AccountID != cfg.AccountID || rt.created != 1 || rt.destroyed != 1 || agent.lastLaunch.Prompt != "" {
					t.Fatalf("retry did not resume the recorded intent: phase=%s code=%s", final.Phase, final.ErrorCode)
				}
			})
		}
	}
}

func TestAccountsManagerSwitchShutdownPreservesCancellationWinner(t *testing.T) {
	for _, prefix := range []string{"ptyhost-v1:", ""} {
		t.Run(prefix, func(t *testing.T) {
			m, st, rt, _, rec, cfg := accountSwitchFixture(t)
			rec.Metadata.RuntimeHandleID = prefix + string(rec.ID)
			if err := st.UpdateSession(t.Context(), rec); err != nil {
				t.Fatal(err)
			}
			rt.aliveByHandle = map[string]bool{rec.Metadata.RuntimeHandleID: true}
			shutdownCtx, shutdown := context.WithCancel(t.Context())
			defer shutdown()
			m.backgroundContext = shutdownCtx
			boundary := &accountStopBoundaryStore{Store: st, before: true, entered: make(chan struct{}), release: make(chan struct{})}
			m.store = boundary
			gate := &transitionInputGate{acquired: make(chan string, 1), released: make(chan string, 1)}
			m.SetTerminalInputGate(gate)
			if _, err := m.StartAccountsManagerSwitch(t.Context(), rec.ID, cfg); err != nil {
				t.Fatal(err)
			}
			select {
			case <-boundary.entered:
			case <-time.After(3 * time.Second):
				t.Fatal("switch did not reach pre-stop boundary")
			}
			if _, err := m.CancelAccountsManagerSwitch(t.Context(), rec.ID, cfg.OperationID); err != nil {
				t.Fatal(err)
			}
			shutdown()
			op := waitAccountSwitch(t, m, st, cfg.OperationID)
			if op.Phase != domain.AccountsManagerSwitchCancelled || len(gate.released) != 1 || rt.created != 0 || rt.destroyed != 0 || len(rt.interrupts) != 0 {
				t.Fatal("shutdown undid a winning cancellation")
			}
			if _, err := m.RetryAccountsManagerSwitch(t.Context(), rec.ID, op.ID); !errors.Is(err, domain.ErrAccountsManagerSwitchConflict) {
				t.Fatal("cancelled operation became retryable", err)
			}
		})
	}
}

func TestAccountsManagerSwitchShutdownControlTargetFailure(t *testing.T) {
	m, st, _, _, rec, cfg := accountSwitchFixture(t)
	m.accountsManager.(*accountSwitchRouter).validate = func(context.Context) error { return errors.New("target unavailable") }
	if _, err := m.StartAccountsManagerSwitch(t.Context(), rec.ID, cfg); err != nil {
		t.Fatal(err)
	}
	op := waitAccountSwitch(t, m, st, cfg.OperationID)
	if op.Phase != domain.AccountsManagerSwitchFailed || op.ErrorCode != "TARGET_UNAVAILABLE" {
		t.Fatalf("ordinary validation failure changed semantics: phase=%s code=%s", op.Phase, op.ErrorCode)
	}
	if release, ok := m.AcquireSessionInput(rec.ID); !ok {
		t.Fatal("ordinary validation failure retained the operation fence")
	} else {
		release()
	}
}
