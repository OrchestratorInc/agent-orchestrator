package store_test

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestAccountsManagerBindingCASAndSnapshot(t *testing.T) {
	store := newTestStore(t)
	ctx := t.Context()
	seedProject(t, store, "bindings")
	record := sampleRecord("bindings")
	record.Metadata = domain.SessionMetadata{}
	first, err := store.CreateSession(ctx, record)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.CreateSession(ctx, record)
	if err != nil {
		t.Fatal(err)
	}
	before, err := store.AccountsManagerBindings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	a, _, err := store.GetOrCreateAccountsManagerSessionRoute(ctx, domain.AccountsManagerSessionRoute{SessionID: first.ID, Provider: domain.AccountsManagerProviderCodex, Mode: domain.AccountsManagerManaged, AccountID: "account-a"})
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := store.GetOrCreateAccountsManagerSessionRoute(ctx, domain.AccountsManagerSessionRoute{SessionID: second.ID, Provider: domain.AccountsManagerProviderCodex, Mode: domain.AccountsManagerNative})
	if err != nil {
		t.Fatal(err)
	}
	if a.Revision != before.Revision+1 || b.Revision != before.Revision+2 || b.AccountID != "" {
		t.Fatal("incorrect initial binding")
	}
	var won atomic.Int32
	var workers sync.WaitGroup
	for range 12 {
		workers.Go(func() {
			wanted := a
			wanted.AccountID = "account-b"
			updated, err := store.CompareAndSwapAccountsManagerSessionRoute(ctx, wanted, a.Revision)
			if err == nil {
				won.Add(1)
				if updated.Revision != before.Revision+3 || updated.AccountID != "account-b" {
					t.Error("incorrect committed binding")
				}
			} else if !errors.Is(err, domain.ErrAccountsManagerBindingConflict) {
				t.Errorf("unexpected CAS error: %v", err)
			}
		})
	}
	workers.Wait()
	if won.Load() != 1 {
		t.Fatal("concurrent switches did not have exactly one winner")
	}
	snapshot, err := store.AccountsManagerBindings(ctx)
	if err != nil || snapshot.Revision != before.Revision+3 || len(snapshot.Bindings) != 2 {
		t.Fatalf("snapshot=%+v err=%v", snapshot, err)
	}
	unchanged, found, err := store.GetAccountsManagerSessionRoute(ctx, second.ID, b.Provider)
	if err != nil || !found || unchanged.Mode != domain.AccountsManagerNative || unchanged.Revision != b.Revision {
		t.Fatal("switch affected another session")
	}
	if deleted, err := store.DeleteSession(ctx, first.ID); err != nil || !deleted {
		t.Fatalf("delete seed session: deleted=%v err=%v", deleted, err)
	}
	after, err := store.AccountsManagerBindings(ctx)
	if err != nil || after.Revision != snapshot.Revision+1 || len(after.Bindings) != 1 {
		t.Fatalf("cascade snapshot=%+v previous=%+v err=%v", after, snapshot, err)
	}
}

func TestAccountsManagerBindingRejectsInvalidModes(t *testing.T) {
	store := newTestStore(t)
	for _, route := range []domain.AccountsManagerSessionRoute{
		{SessionID: "session", Provider: domain.AccountsManagerProviderCodex, Mode: domain.AccountsManagerNative, AccountID: "account"},
		{SessionID: "session", Provider: domain.AccountsManagerProviderCodex, Mode: domain.AccountsManagerManaged},
		{SessionID: "session", Provider: domain.AccountsManagerProviderCodex, Mode: "invalid", AccountID: "account"},
	} {
		if _, _, err := store.GetOrCreateAccountsManagerSessionRoute(t.Context(), route); err == nil {
			t.Fatal("invalid binding accepted")
		}
	}
}
