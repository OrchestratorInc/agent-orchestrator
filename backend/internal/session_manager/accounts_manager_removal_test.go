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

type accountRemovalCoordinator interface {
	StartAccountsManagerRemoval(context.Context, string, string, int64, bool) (domain.AccountsManagerRemoval, error)
	RetryAccountsManagerRemoval(context.Context, string) (domain.AccountsManagerRemoval, error)
	CancelAccountsManagerRemoval(context.Context, string) (domain.AccountsManagerRemoval, error)
}

type accountRemovalRouter struct {
	*accountSwitchRouter
	finalized int
	finalErr  error
}

func (r *accountRemovalRouter) PrepareAccountRemoval(ctx context.Context, id, account string, revision int64, confirmed bool) (domain.AccountsManagerRemoval, bool, error) {
	return r.store.CreateAccountsManagerRemoval(ctx, id, account, revision, confirmed)
}

func (r *accountRemovalRouter) FinalizeAccountRemoval(ctx context.Context, id string) error {
	if r.finalErr != nil {
		return r.finalErr
	}
	op, _, err := r.store.GetAccountsManagerRemoval(ctx, id)
	if err != nil {
		return err
	}
	if !op.BindingsRevoked {
		return errors.New("credential deletion preceded revocation")
	}
	for _, entry := range op.Impact.Sessions {
		if !entry.Stopped {
			return errors.New("credential deletion preceded controller stop")
		}
	}
	if err := r.store.RecordAccountsManagerRemovalRevoked(ctx, id); err != nil {
		return err
	}
	if err := r.store.CompleteAccountsManagerRemoval(ctx, id); err != nil {
		return err
	}
	r.finalized++
	return nil
}

func waitAccountRemoval(t *testing.T, m *Manager, st *sqlite.Store, id string) domain.AccountsManagerRemoval {
	t.Helper()
	m.accountRemovalMu.Lock()
	run := m.accountRemovals[id]
	var done <-chan struct{}
	if run != nil {
		done = run.done
	}
	m.accountRemovalMu.Unlock()
	if done != nil {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("account removal did not settle")
		}
	}
	op, found, err := st.GetAccountsManagerRemoval(t.Context(), id)
	if err != nil || !found {
		t.Fatal("missing account removal", err)
	}
	return op
}

func TestAccountsManagerRemovalCoordinatesEveryBinding(t *testing.T) {
	for _, state := range []string{"active", "inactive", "missing-runtime", "credential-retry", "revocation-retry", "generation-changed"} {
		t.Run(state, func(t *testing.T) {
			m, st, fake, _, rec, _ := accountSwitchFixture(t)
			second := rec
			second.ID = ""
			second.Metadata.RuntimeHandleID, second.Metadata.RuntimeLaunchID = "second", "second-generation"
			if state == "inactive" {
				second.IsTerminated = true
			}
			second, err := st.CreateSession(t.Context(), second)
			if err != nil {
				t.Fatal(err)
			}
			provider, _ := accountsManagerProvider(rec.Harness)
			if _, _, err := st.GetOrCreateAccountsManagerSessionRoute(t.Context(), domain.AccountsManagerSessionRoute{SessionID: second.ID, Provider: provider, Mode: domain.AccountsManagerManaged, AccountID: "account-a"}); err != nil {
				t.Fatal(err)
			}
			native := rec
			native.ID, native.Metadata.RuntimeHandleID, native.Metadata.RuntimeLaunchID = "", "native", "native-generation"
			native, err = st.CreateSession(t.Context(), native)
			if err != nil {
				t.Fatal(err)
			}
			nativeBinding, _, err := st.GetOrCreateAccountsManagerSessionRoute(t.Context(), domain.AccountsManagerSessionRoute{SessionID: native.ID, Provider: provider, Mode: domain.AccountsManagerNative})
			if err != nil {
				t.Fatal(err)
			}
			rt := &accountSlotsRuntime{accountSwitchRuntime: accountSwitchRuntime{fake}, generations: map[string]string{"source": rec.Metadata.RuntimeLaunchID, "second": second.Metadata.RuntimeLaunchID, "native": native.Metadata.RuntimeLaunchID}}
			fake.aliveByHandle = map[string]bool{"source": true, "second": true, "native": true}
			if state == "missing-runtime" {
				delete(rt.generations, "second")
				fake.aliveByHandle["second"] = false
			}
			router := &accountRemovalRouter{accountSwitchRouter: &accountSwitchRouter{store: st}}
			if state == "credential-retry" {
				router.finalErr = errors.New("vault unavailable")
			}
			if state == "revocation-retry" {
				router.syncErr = errors.New("runner unavailable")
			}
			if state == "generation-changed" {
				rt.generations["second"] = "foreign-generation"
			}
			m.accountsManager, m.runtime = router, rt
			fake.onDestroy = func(_ int, handle ports.RuntimeHandle) {
				if handle.ID == "native" {
					t.Error("unrelated native runtime targeted")
				}
				for _, id := range []domain.SessionID{rec.ID, second.ID} {
					if release, ok := m.AcquireSessionInput(id); ok {
						release()
						t.Error("stop began before every session was reserved")
					}
				}
				if release, ok := m.AcquireSessionInput(native.ID); !ok {
					t.Error("native intake blocked")
				} else {
					release()
				}
			}
			impact, err := st.AccountsManagerRemovalImpact(t.Context(), "account-a")
			if err != nil || len(impact.Sessions) != 2 {
				t.Fatal("incomplete binding inventory", err)
			}
			if _, err := m.StartAccountsManagerRemoval(t.Context(), "delete-a", "account-a", impact.Revision, true); err != nil {
				t.Fatal(err)
			}
			op := waitAccountRemoval(t, m, st, "delete-a")
			if state == "credential-retry" || state == "revocation-retry" {
				if op.Phase != domain.AccountsManagerRemovalRecovery || router.finalized != 0 {
					t.Fatalf("uncertain deletion completed: %+v", op)
				}
				if state == "revocation-retry" && fake.destroyed != 0 {
					t.Fatal("stop preceded revocation acknowledgement")
				}
				router.finalErr, router.syncErr = nil, nil
				if _, err := m.RetryAccountsManagerRemoval(t.Context(), op.ID); err != nil {
					t.Fatal(err)
				}
				op = waitAccountRemoval(t, m, st, op.ID)
			}
			if op.Phase != domain.AccountsManagerRemovalComplete || router.finalized != 1 || fake.created != 0 || !fake.aliveByHandle["native"] {
				t.Fatalf("incomplete removal: phase=%s code=%s removed=%d", op.Phase, op.ErrorCode, router.finalized)
			}
			if state == "generation-changed" && (rt.generations["second"] != "foreign-generation" || !fake.aliveByHandle["second"] || fake.destroyed != 1) {
				t.Fatal("replacement generation was targeted during deletion")
			}
			if _, err := m.StartAccountsManagerRemoval(t.Context(), op.ID, "account-a", impact.Revision, true); err != nil || router.finalized != 1 {
				t.Fatal("completed request repeated removal", err)
			}
			current, _, err := st.GetAccountsManagerSessionRoute(t.Context(), native.ID, provider)
			if err != nil || current != nativeBinding {
				t.Fatal("unrelated native binding changed", err)
			}
		})
	}
}

func TestAccountsManagerRemovalCoordinatorAvailable(t *testing.T) {
	m, _, _, _, _, _ := accountSwitchFixture(t)
	if _, ok := any(m).(accountRemovalCoordinator); !ok {
		t.Fatal("account deletion has no controller coordinator, retry, or cancellation boundary")
	}
}

func TestAccountsManagerRemovalDormantBindingPreservesNativeIntake(t *testing.T) {
	m, st, fake, _, rec, _ := accountSwitchFixture(t)
	rec.Harness = domain.HarnessCodex
	if err := st.UpdateSession(t.Context(), rec); err != nil {
		t.Fatal(err)
	}
	native, _, err := st.GetOrCreateAccountsManagerSessionRoute(t.Context(), domain.AccountsManagerSessionRoute{SessionID: rec.ID, Provider: domain.AccountsManagerProviderCodex, Mode: domain.AccountsManagerNative})
	if err != nil {
		t.Fatal(err)
	}
	router := &accountRemovalRouter{accountSwitchRouter: &accountSwitchRouter{store: st}, finalErr: errors.New("vault unavailable")}
	m.accountsManager = router
	impact, err := st.AccountsManagerRemovalImpact(t.Context(), "account-a")
	if err != nil || len(impact.Sessions) != 1 || impact.Sessions[0].OwnsController() {
		t.Fatal("fixture did not retain a dormant managed binding", err)
	}
	if _, err := m.StartAccountsManagerRemoval(t.Context(), "dormant-delete", "account-a", impact.Revision, true); err != nil {
		t.Fatal(err)
	}
	waitAccountRemoval(t, m, st, "dormant-delete")
	if release, ok := m.AcquireSessionInput(rec.ID); !ok {
		t.Error("dormant account removal fenced unrelated native controller intake")
	} else {
		release()
	}
	if fake.destroyed != 0 || fake.created != 0 || len(fake.interrupts) != 0 {
		t.Error("dormant removal mutated unrelated controller")
	}
	current, _, err := st.GetAccountsManagerSessionRoute(t.Context(), rec.ID, native.Provider)
	if err != nil || current != native {
		t.Fatal("dormant removal mutated native binding", err)
	}
}

func TestAccountsManagerRemovalStartupRestoresFence(t *testing.T) {
	m, st, rt, _, rec, _ := accountSwitchFixture(t)
	impact, err := st.AccountsManagerRemovalImpact(t.Context(), "account-a")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.CreateAccountsManagerRemoval(t.Context(), "remove-restart", "account-a", impact.Revision, true); err != nil {
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
	restarted := New(Deps{Store: st, Runtime: m.runtime, Lifecycle: lifecycle.New(st, nil), AccountsManager: &accountSwitchRouter{store: st}, BackgroundContext: t.Context()})
	if err := restarted.ReconcileStartupSafety(t.Context()); err != nil {
		t.Fatal(err)
	}
	if release, ok := restarted.AcquireSessionInput(rec.ID); ok {
		release()
		t.Error("restart reopened input during pending account deletion")
	}
	if release, ok := restarted.AcquireSessionInput("unrelated"); !ok {
		t.Error("restart fenced an unrelated session")
	} else {
		release()
	}
	if rt.created != 0 || rt.destroyed != 0 {
		t.Error("startup performed an unacknowledged runtime mutation")
	}
}
