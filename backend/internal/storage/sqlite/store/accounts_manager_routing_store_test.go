package store_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestAccountsManagerRoutingPolicyPreservesOrderAndReplacement(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	want := domain.AccountsManagerRoutingPolicy{
		Provider:   domain.AccountsManagerProviderCodex,
		Enabled:    true,
		AccountIDs: []string{"account-primary", "account-fallback"},
	}
	if err := store.PutAccountsManagerRoutingPolicy(ctx, want); err != nil {
		t.Fatalf("put routing policy: %v", err)
	}
	got, err := store.GetAccountsManagerRoutingPolicy(ctx, want.Provider)
	if err != nil {
		t.Fatalf("get routing policy: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("routing policy = %#v, want %#v", got, want)
	}

	want.Enabled = false
	want.AccountIDs = []string{"account-fallback"}
	if err := store.PutAccountsManagerRoutingPolicy(ctx, want); err != nil {
		t.Fatalf("replace routing policy: %v", err)
	}
	got, err = store.GetAccountsManagerRoutingPolicy(ctx, want.Provider)
	if err != nil {
		t.Fatalf("get replaced routing policy: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("replaced routing policy = %#v, want %#v", got, want)
	}
}

func TestAccountsManagerSessionRouteKeepsFirstPinAndCascadesWithSession(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	seedProject(t, store, "routing")
	record := sampleRecord("routing")
	record.Metadata = domain.SessionMetadata{}
	session, err := store.CreateSession(ctx, record)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	first, created, err := store.GetOrCreateAccountsManagerSessionRoute(ctx, domain.AccountsManagerSessionRoute{
		SessionID: session.ID,
		Provider:  domain.AccountsManagerProviderCodex,
		AccountID: "account-first",
		CreatedAt: time.Now().UTC().Truncate(time.Second),
	})
	if err != nil || !created {
		t.Fatalf("create route: created=%v err=%v", created, err)
	}
	if first.AccountID != "account-first" {
		t.Fatalf("first route account = %q", first.AccountID)
	}

	again, created, err := store.GetOrCreateAccountsManagerSessionRoute(ctx, domain.AccountsManagerSessionRoute{
		SessionID: session.ID,
		Provider:  domain.AccountsManagerProviderCodex,
		AccountID: "account-second",
		CreatedAt: first.CreatedAt.Add(time.Minute),
	})
	if err != nil || created {
		t.Fatalf("reuse route: created=%v err=%v", created, err)
	}
	if again.AccountID != "account-first" {
		t.Fatalf("reused route account = %q, want first pin", again.AccountID)
	}

	if _, err := store.DeleteSession(ctx, session.ID); err != nil {
		t.Fatalf("delete session: %v", err)
	}
	if _, found, err := store.GetAccountsManagerSessionRoute(ctx, session.ID, domain.AccountsManagerProviderCodex); err != nil || found {
		t.Fatalf("route after session delete: found=%v err=%v", found, err)
	}
}
