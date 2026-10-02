package sessionmanager

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/lifecycle"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

type accountRemovalRetryStore struct {
	*sqlite.Store
	read *accountRetryBarrier
}

type accountRemovalCancelReadErrorStore struct {
	*sqlite.Store
	cancelled atomic.Bool
	read      *accountRetryBarrier
}

func (s *accountRemovalCancelReadErrorStore) CancelAccountsManagerRemoval(ctx context.Context, id string) error {
	if err := s.Store.CancelAccountsManagerRemoval(ctx, id); err != nil {
		return err
	}
	s.cancelled.Store(true)
	return nil
}

func (s *accountRemovalCancelReadErrorStore) GetAccountsManagerRemoval(ctx context.Context, id string) (domain.AccountsManagerRemoval, bool, error) {
	if ctx.Value(accountRetryReadKey{}) != nil {
		op, found, err := s.Store.GetAccountsManagerRemoval(ctx, id)
		s.read.pause()
		return op, found, err
	}
	if s.cancelled.Load() {
		return domain.AccountsManagerRemoval{}, false, errors.New("read response unavailable after committed cancellation")
	}
	return s.Store.GetAccountsManagerRemoval(ctx, id)
}

func TestAccountsManagerRemovalCommittedCancellationReleasesDespiteReadFailure(t *testing.T) {
	for _, prefix := range []string{"ptyhost-v1:", ""} {
		t.Run(prefix, func(t *testing.T) {
			m, st, fake, _, rec, _ := accountSwitchFixture(t)
			rec.Metadata.RuntimeHandleID = prefix + string(rec.ID)
			if err := st.UpdateSession(t.Context(), rec); err != nil {
				t.Fatal(err)
			}
			other := rec
			other.ID = ""
			other.Metadata.RuntimeHandleID = "unrelated"
			other, err := st.CreateSession(t.Context(), other)
			if err != nil {
				t.Fatal(err)
			}
			provider, _ := accountsManagerProvider(rec.Harness)
			if _, _, err := st.GetOrCreateAccountsManagerSessionRoute(t.Context(), domain.AccountsManagerSessionRoute{SessionID: other.ID, Provider: provider, Mode: domain.AccountsManagerManaged, AccountID: "account-b"}); err != nil {
				t.Fatal(err)
			}
			for id, account := range map[string]string{"committed-cancel": "account-a", "keep-fenced": "account-b"} {
				impact, err := st.AccountsManagerRemovalImpact(t.Context(), account)
				if err != nil {
					t.Fatal(err)
				}
				if _, _, err := st.CreateAccountsManagerRemoval(t.Context(), id, account, impact.Revision, true); err != nil {
					t.Fatal(err)
				}
			}
			if err := st.Close(); err != nil {
				t.Fatal(err)
			}
			st, err = sqlite.OpenPreMigrated(rec.Metadata.WorkspacePath)
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			wrapped := &accountRemovalCancelReadErrorStore{Store: st, read: newAccountRetryBarrier(t)}
			router := &accountRemovalRouter{accountSwitchRouter: &accountSwitchRouter{store: st}}
			m = New(Deps{Store: wrapped, Runtime: m.runtime, Lifecycle: lifecycle.New(st, nil), AccountsManager: router, BackgroundContext: t.Context()})
			gate := &transitionInputGate{acquired: make(chan string, 8), released: make(chan string, 8)}
			m.SetTerminalInputGate(gate)
			if err := m.ReconcileStartupSafety(t.Context()); err != nil {
				t.Fatal(err)
			}
			retried := make(chan error, 1)
			go func() {
				_, err := m.RetryAccountsManagerRemoval(context.WithValue(t.Context(), accountRetryReadKey{}, true), "committed-cancel")
				retried <- err
			}()
			wrapped.read.wait(t)
			if _, err := m.CancelAccountsManagerRemoval(t.Context(), "committed-cancel"); err == nil {
				t.Error("response-read failure was not injected")
			}
			wrapped.cancelled.Store(false)
			wrapped.read.release()
			if err := <-retried; !errors.Is(err, domain.ErrAccountsManagerRemovalConflict) {
				t.Error("stale retry crossed committed cancellation after response recovery", err)
			}
			m.agentSwitchWorkers.Wait()
			op, _, err := st.GetAccountsManagerRemoval(t.Context(), "committed-cancel")
			if err != nil || op.Phase != domain.AccountsManagerRemovalCancelled || op.StopStarted {
				t.Fatal("cancellation was not preserved", err)
			}
			if release, ok := m.AcquireSessionInput(rec.ID); !ok {
				t.Error("committed cancellation retained session reservation after response read failed")
			} else {
				release()
			}
			if release, ok := m.AcquireSessionInput(other.ID); ok {
				release()
				t.Error("cancellation released an unrelated deletion reservation")
			}
			if len(gate.acquired) != 2 || len(gate.released) != 1 || fake.created != 0 || fake.destroyed != 0 || len(fake.interrupts) != 0 || router.finalized != 0 {
				t.Error("committed cancellation failed to release only its own fences without irreversible work")
			}
		})
	}
}

func (s *accountRemovalRetryStore) GetAccountsManagerRemoval(ctx context.Context, id string) (domain.AccountsManagerRemoval, bool, error) {
	op, found, err := s.Store.GetAccountsManagerRemoval(ctx, id)
	if ctx.Value(accountRetryReadKey{}) != nil {
		s.read.pause()
	}
	return op, found, err
}

func TestAccountsManagerRemovalCancellationRejectsCachedRetry(t *testing.T) {
	for _, prefix := range []string{"ptyhost-v1:", ""} {
		t.Run(prefix, func(t *testing.T) {
			m, st, fake, _, rec, _ := accountSwitchFixture(t)
			rec.Metadata.RuntimeHandleID = prefix + string(rec.ID)
			if err := st.UpdateSession(t.Context(), rec); err != nil {
				t.Fatal(err)
			}
			impact, err := st.AccountsManagerRemovalImpact(t.Context(), "account-a")
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := st.CreateAccountsManagerRemoval(t.Context(), "cancel-delete", "account-a", impact.Revision, true); err != nil {
				t.Fatal(err)
			}
			if err := st.Close(); err != nil {
				t.Fatal(err)
			}
			st, err = sqlite.OpenPreMigrated(rec.Metadata.WorkspacePath)
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			wrapped := &accountRemovalRetryStore{Store: st, read: newAccountRetryBarrier(t)}
			router := &accountRemovalRouter{accountSwitchRouter: &accountSwitchRouter{store: st}}
			m = New(Deps{Store: wrapped, Runtime: m.runtime, Lifecycle: lifecycle.New(st, nil), AccountsManager: router, BackgroundContext: t.Context()})
			gate := &transitionInputGate{acquired: make(chan string, 8), released: make(chan string, 8)}
			m.SetTerminalInputGate(gate)
			if err := m.ReconcileStartupSafety(t.Context()); err != nil {
				t.Fatal(err)
			}
			retried := make(chan error, 1)
			go func() {
				_, err := m.RetryAccountsManagerRemoval(context.WithValue(t.Context(), accountRetryReadKey{}, true), "cancel-delete")
				retried <- err
			}()
			wrapped.read.wait(t)
			if _, err := m.CancelAccountsManagerRemoval(t.Context(), "cancel-delete"); err != nil {
				t.Fatal(err)
			}
			wrapped.read.release()
			if err := <-retried; !errors.Is(err, domain.ErrAccountsManagerRemovalConflict) {
				t.Error("stale retry admitted a durably cancelled deletion", err)
			}
			m.agentSwitchWorkers.Wait()
			if fake.destroyed != 0 || fake.created != 0 || len(fake.interrupts) != 0 || router.finalized != 0 || len(gate.acquired) != 1 || len(gate.released) != 1 {
				t.Fatal("cancellation failed to release exactly its existing fences")
			}
			if release, ok := m.AcquireSessionInput(rec.ID); !ok {
				t.Fatal("cancelled deletion retained intake")
			} else {
				release()
			}
		})
	}
}

func TestAccountsManagerRemovalTakeoverAfterStopError(t *testing.T) {
	for _, prefix := range []string{"ptyhost-v1:", ""} {
		for _, replacement := range []string{"replacement-generation", "unknown"} {
			t.Run(prefix+"/"+replacement, func(t *testing.T) {
				m, st, fake, _, rec, _ := accountSwitchFixture(t)
				handle := ports.RuntimeHandle{ID: prefix + string(rec.ID)}
				rec.Metadata.RuntimeHandleID = handle.ID
				if err := st.UpdateSession(t.Context(), rec); err != nil {
					t.Fatal(err)
				}
				rt := &accountTeardownSlots{accountSlotsRuntime: accountSlotsRuntime{
					accountSwitchRuntime: accountSwitchRuntime{fake}, generations: map[string]string{handle.ID: rec.Metadata.RuntimeLaunchID}},
					handles: []ports.RuntimeHandle{{ID: "ptyhost-v1:" + string(rec.ID)}, {ID: string(rec.ID)}}, active: handle}
				fake.aliveByHandle = map[string]bool{handle.ID: true}
				fake.destroyErrSequence = []error{errors.New("destroy response lost")}
				fake.onDestroy = func(call int, got ports.RuntimeHandle) {
					if call != 0 || got != handle {
						t.Error("destructive retry targeted an unproven owner", got.ID)
					}
					rt.generations[handle.ID] = replacement
				}
				router := &accountRemovalRouter{accountSwitchRouter: &accountSwitchRouter{store: st}, finalErr: errors.New("vault unavailable")}
				m.runtime, m.accountsManager = rt, router
				impact, err := st.AccountsManagerRemovalImpact(t.Context(), "account-a")
				if err != nil {
					t.Fatal(err)
				}
				if _, err := m.StartAccountsManagerRemoval(t.Context(), "takeover-delete", "account-a", impact.Revision, true); err != nil {
					t.Fatal(err)
				}
				assertHeld := func(manager *Manager, db *sqlite.Store) {
					t.Helper()
					op := waitAccountRemoval(t, manager, db, "takeover-delete")
					if op.Phase != domain.AccountsManagerRemovalRecovery || !op.BindingsRevoked || router.finalized != 0 ||
						fake.destroyed != 1 || fake.created != 0 || len(fake.interrupts) != 0 || rt.generations[handle.ID] != replacement || !fake.aliveByHandle[handle.ID] {
						t.Fatal("takeover recovery mutated replacement or lost revocation", op.Phase, fake.destroyed)
					}
					if op.Impact.Sessions[0].Stopped != (replacement != "unknown") {
						t.Error("recorded-owner acknowledgement does not match ownership evidence")
					}
					if release, ok := manager.AcquireSessionInput(rec.ID); ok {
						release()
						t.Error("pending deletion reopened intake")
					}
				}
				assertHeld(m, st)
				for range 2 {
					if _, err := m.RetryAccountsManagerRemoval(t.Context(), "takeover-delete"); err != nil {
						t.Fatal(err)
					}
					assertHeld(m, st)
				}
				if err := st.Close(); err != nil {
					t.Fatal(err)
				}
				reopened, err := sqlite.OpenPreMigrated(rec.Metadata.WorkspacePath)
				if err != nil {
					t.Fatal(err)
				}
				defer reopened.Close()
				router.store = reopened
				restarted := New(Deps{Store: reopened, Runtime: rt, Lifecycle: lifecycle.New(reopened, nil), AccountsManager: router, BackgroundContext: t.Context()})
				if err := restarted.ReconcileStartupSafety(t.Context()); err != nil {
					t.Fatal(err)
				}
				for range 2 {
					if _, err := restarted.RetryAccountsManagerRemoval(t.Context(), "takeover-delete"); err != nil {
						t.Fatal(err)
					}
					assertHeld(restarted, reopened)
				}
				router.finalErr = nil
				if _, err := restarted.RetryAccountsManagerRemoval(t.Context(), "takeover-delete"); err != nil {
					t.Fatal(err)
				}
				op := waitAccountRemoval(t, restarted, reopened, "takeover-delete")
				want := domain.AccountsManagerRemovalComplete
				if replacement == "unknown" {
					want = domain.AccountsManagerRemovalRecovery
				}
				if op.Phase != want || fake.destroyed != 1 || !fake.aliveByHandle[handle.ID] {
					t.Fatal("final retry did not preserve replacement", op.Phase)
				}
				route, found, err := reopened.GetAccountsManagerSessionRoute(t.Context(), rec.ID, op.Impact.Sessions[0].Provider)
				if err != nil || !found || !route.Blocked || route.AccountID != "account-a" {
					t.Fatal("deletion dropped the old-account selection fence", err)
				}
			})
		}
	}
}

func TestAccountsManagerRemovalSwitchDeleteAdmission(t *testing.T) {
	for _, winner := range []string{"deletion", "switch"} {
		t.Run(winner, func(t *testing.T) {
			m, st, fake, _, rec, cfg := accountSwitchFixture(t)
			router := &accountRemovalRouter{accountSwitchRouter: &accountSwitchRouter{store: st}}
			m.accountsManager = router
			impact, err := st.AccountsManagerRemovalImpact(t.Context(), "account-a")
			if err != nil {
				t.Fatal(err)
			}
			if winner == "deletion" {
				if _, _, err := st.CreateAccountsManagerRemoval(t.Context(), "admit-delete", "account-a", impact.Revision, true); err != nil {
					t.Fatal(err)
				}
				if _, err := m.StartAccountsManagerSwitch(t.Context(), rec.ID, cfg); err == nil {
					t.Fatal("switch crossed durable deletion fence")
				}
			} else {
				accountHandoffJournal(t, st, rec, cfg, domain.AccountsManagerSwitchWaiting)
				impact, err = st.AccountsManagerRemovalImpact(t.Context(), "account-a")
				if err != nil {
					t.Fatal(err)
				}
				if _, err := m.StartAccountsManagerRemoval(t.Context(), "admit-delete", "account-a", impact.Revision, true); !errors.Is(err, domain.ErrAccountsManagerSwitchConflict) {
					t.Fatal("deletion crossed admitted account switch", err)
				}
			}
			m.agentSwitchWorkers.Wait()
			if fake.created != 0 || fake.destroyed != 0 || len(fake.interrupts) != 0 || router.finalized != 0 {
				t.Fatal("losing operation performed irreversible work")
			}
		})
	}
}

func TestAccountsManagerRemovalCrashCuts(t *testing.T) {
	for _, cut := range []string{"requested", "stopping", "bindings-revoked", "runtime-stopped", "stop-acknowledged", "credential-revoked"} {
		t.Run(cut, func(t *testing.T) {
			_, st, fake, _, rec, _ := accountSwitchFixture(t)
			rt := &accountSlotsRuntime{accountSwitchRuntime: accountSwitchRuntime{fake}, generations: map[string]string{"source": rec.Metadata.RuntimeLaunchID}}
			impact, err := st.AccountsManagerRemovalImpact(t.Context(), "account-a")
			if err != nil {
				t.Fatal(err)
			}
			op, _, err := st.CreateAccountsManagerRemoval(t.Context(), "crash-delete", "account-a", impact.Revision, true)
			if err != nil {
				t.Fatal(err)
			}
			if cut != "requested" {
				if err := st.BeginAccountsManagerRemovalStop(t.Context(), op.ID); err != nil {
					t.Fatal(err)
				}
			}
			if cut != "requested" && cut != "stopping" {
				if err := st.RecordAccountsManagerRemovalBindingsRevoked(t.Context(), op.ID); err != nil {
					t.Fatal(err)
				}
			}
			if cut == "runtime-stopped" || cut == "stop-acknowledged" || cut == "credential-revoked" {
				delete(rt.generations, "source")
				fake.aliveByHandle["source"] = false
			}
			if cut == "stop-acknowledged" || cut == "credential-revoked" {
				if err := st.RecordAccountsManagerRemovalStopped(t.Context(), op.ID, rec.ID); err != nil {
					t.Fatal(err)
				}
			}
			if cut == "credential-revoked" {
				if err := st.RecordAccountsManagerRemovalRevoked(t.Context(), op.ID); err != nil {
					t.Fatal(err)
				}
			}
			if err := st.Close(); err != nil {
				t.Fatal(err)
			}
			st, err = sqlite.OpenPreMigrated(rec.Metadata.WorkspacePath)
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			router := &accountRemovalRouter{accountSwitchRouter: &accountSwitchRouter{store: st}}
			m := New(Deps{Store: st, Runtime: rt, Lifecycle: lifecycle.New(st, nil), AccountsManager: router, BackgroundContext: t.Context()})
			if err := m.ReconcileStartupSafety(t.Context()); err != nil {
				t.Fatal(err)
			}
			if release, ok := m.AcquireSessionInput(rec.ID); ok {
				release()
				t.Fatal("startup did not restore deletion fence")
			}
			if cut != "requested" {
				if _, err := m.CancelAccountsManagerRemoval(t.Context(), op.ID); !errors.Is(err, domain.ErrAccountsManagerRemovalConflict) {
					t.Fatal("post-stop cancellation was admitted", err)
				}
			}
			if _, err := m.RetryAccountsManagerRemoval(t.Context(), op.ID); err != nil {
				t.Fatal(err)
			}
			final := waitAccountRemoval(t, m, st, op.ID)
			if final.Phase != domain.AccountsManagerRemovalComplete || router.finalized != 1 || fake.created != 0 || fake.aliveByHandle["source"] {
				t.Fatal("crash recovery did not finish exact-owner deletion", final.Phase, final.ErrorCode)
			}
		})
	}
}
