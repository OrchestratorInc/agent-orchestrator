package store_test

import (
	"errors"
	"sync"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestAccountsManagerRemovalIncludesInactiveBindings(t *testing.T) {
	for _, state := range []string{"terminated", "dormant-provider"} {
		t.Run(state, func(t *testing.T) {
			st, rec, _ := switchFixture(t)
			if state == "terminated" {
				rec.IsTerminated = true
			} else {
				rec.Harness = domain.HarnessOpenCode
			}
			if err := st.UpdateSession(t.Context(), rec); err != nil {
				t.Fatal(err)
			}
			impact, err := st.AccountsManagerRemovalImpact(t.Context(), "account-a")
			if err != nil || len(impact.Sessions) != 1 || impact.Sessions[0].SessionID != rec.ID {
				t.Fatalf("inactive account binding omitted: impact=%+v err=%v", impact, err)
			}
		})
	}
}

func TestAccountsManagerRemovalConcurrentSwitchHasOneWinner(t *testing.T) {
	for range 12 {
		st, _, request := switchFixture(t)
		impact, err := st.AccountsManagerRemovalImpact(t.Context(), request.SourceAccountID)
		if err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		var group sync.WaitGroup
		var switchErr, removalErr error
		group.Go(func() { <-start; _, _, switchErr = st.CreateAccountsManagerSwitch(t.Context(), request) })
		group.Go(func() {
			<-start
			_, _, removalErr = st.CreateAccountsManagerRemoval(t.Context(), "parallel-delete", request.SourceAccountID, impact.Revision, true)
		})
		close(start)
		group.Wait()
		if (switchErr == nil) == (removalErr == nil) {
			t.Fatal("switch/delete did not have exactly one admitted operation", switchErr, removalErr)
		}
	}
}

func TestAccountsManagerRemovalCancellationFencesEveryOldStep(t *testing.T) {
	st, rec, _ := switchFixture(t)
	impact, err := st.AccountsManagerRemovalImpact(t.Context(), "account-a")
	if err != nil {
		t.Fatal(err)
	}
	op, _, err := st.CreateAccountsManagerRemoval(t.Context(), "old-delete", "account-a", impact.Revision, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CancelAccountsManagerRemoval(t.Context(), op.ID); err != nil {
		t.Fatal(err)
	}
	impact, err = st.AccountsManagerRemovalImpact(t.Context(), "account-a")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.CreateAccountsManagerRemoval(t.Context(), "new-delete", "account-a", impact.Revision, true); err != nil {
		t.Fatal(err)
	}
	for _, step := range []func() error{
		func() error { return st.BeginAccountsManagerRemovalStop(t.Context(), op.ID) },
		func() error { return st.RecordAccountsManagerRemovalBindingsRevoked(t.Context(), op.ID) },
		func() error { return st.RecordAccountsManagerRemovalStopped(t.Context(), op.ID, rec.ID) },
		func() error { return st.RecordAccountsManagerRemovalRevoked(t.Context(), op.ID) },
		func() error { return st.CompleteAccountsManagerRemoval(t.Context(), op.ID) },
	} {
		if err := step(); !errors.Is(err, domain.ErrAccountsManagerRemovalConflict) {
			t.Fatal("superseded deletion step was admitted", err)
		}
	}
	after, _, err := st.GetAccountsManagerRemoval(t.Context(), "new-delete")
	if err != nil || after.Phase != domain.AccountsManagerRemovalRequested || after.StopStarted || after.BindingsRevoked || after.Impact.Sessions[0].Stopped {
		t.Fatal("old operation mutated replacement deletion", err)
	}
}

func TestAccountsManagerRemovalStartsCancellable(t *testing.T) {
	st, _, _ := switchFixture(t)
	impact, err := st.AccountsManagerRemovalImpact(t.Context(), "account-a")
	if err != nil {
		t.Fatal(err)
	}
	op, _, err := st.CreateAccountsManagerRemoval(t.Context(), "remove-cancellable", "account-a", impact.Revision, true)
	if err != nil || string(op.Phase) != "requested" {
		t.Fatalf("removal crossed stop boundary before coordinator admission: phase=%s err=%v", op.Phase, err)
	}
}

func TestAccountsManagerRemovalRejectsStopBeforeBoundary(t *testing.T) {
	st, rec, _ := switchFixture(t)
	impact, _ := st.AccountsManagerRemovalImpact(t.Context(), "account-a")
	op, _, err := st.CreateAccountsManagerRemoval(t.Context(), "remove-boundary", "account-a", impact.Revision, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.RecordAccountsManagerRemovalStopped(t.Context(), op.ID, rec.ID); !errors.Is(err, domain.ErrAccountsManagerRemovalConflict) {
		t.Fatal("stop acknowledgement accepted before irreversible boundary", err)
	}
}

func TestAccountsManagerRemovalFinalCleanupNeverFallsBack(t *testing.T) {
	st, rec, request := switchFixture(t)
	impact, _ := st.AccountsManagerRemovalImpact(t.Context(), "account-a")
	op, _, err := st.CreateAccountsManagerRemoval(t.Context(), "remove-final", "account-a", impact.Revision, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.BeginAccountsManagerRemovalStop(t.Context(), op.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordAccountsManagerRemovalBindingsRevoked(t.Context(), op.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordAccountsManagerRemovalStopped(t.Context(), op.ID, rec.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordAccountsManagerRemovalRevoked(t.Context(), op.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.CompleteAccountsManagerRemoval(t.Context(), op.ID); err != nil {
		t.Fatal(err)
	}
	snapshot, err := st.AccountsManagerBindings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, binding := range snapshot.Bindings {
		if binding.AccountID == "account-a" {
			t.Error("completed removal retained an active account binding")
		}
	}
	choice := domain.AccountsManagerSessionRoute{SessionID: rec.ID, Provider: request.Provider, Mode: domain.AccountsManagerManaged, AccountID: "account-b"}
	got, inserted, err := st.GetOrCreateAccountsManagerSessionRoute(t.Context(), choice)
	if err == nil && (inserted || got.Mode == domain.AccountsManagerNative || got.AccountID != "account-a" || !got.Blocked) {
		t.Fatalf("implicit default bypassed the deleted-choice fence: route=%+v inserted=%v", got, inserted)
	}
}
