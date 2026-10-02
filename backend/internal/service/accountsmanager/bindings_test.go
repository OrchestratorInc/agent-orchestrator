package accountsmanager

import (
	"context"
	"errors"
	"testing"
	"time"

	core "github.com/aoagents/agent-orchestrator/backend/internal/accountsmanager"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type bindingClient struct {
	*fakeClient
	syncErr   error
	snapshots chan core.BindingSnapshot
}

func TestRecordNativeModeDoesNotFollowLaterDefaultOrReplaceManagedBinding(t *testing.T) {
	store := newFakeRoutingStore()
	client := &fakeClient{credentials: []core.CredentialSummary{{Ref: "a", Provider: core.ProviderCodex, Status: core.CredentialActive}}}
	svc := New(client, store)
	if err := svc.RecordNativeAgentSessionRoute(t.Context(), "native", domain.AccountsManagerProviderCodex); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetRoutingPolicy(t.Context(), core.ProviderCodex, true, []string{"safe-a"}); err != nil {
		t.Fatal(err)
	}
	if route, err := svc.PrepareLaunchRoute(t.Context(), "native", core.ProviderCodex, ""); err != nil || route != nil {
		t.Fatal("native mode followed new default")
	}
	if route, err := svc.PrepareLaunchRoute(t.Context(), "managed", core.ProviderCodex, ""); err != nil || route == nil {
		t.Fatal("explicit managed default was not used")
	}
	if err := svc.RecordNativeAgentSessionRoute(t.Context(), "managed", domain.AccountsManagerProviderCodex); !errors.Is(err, ports.ErrChatUnsupported) {
		t.Fatal("managed binding was converted to native")
	}
}

func (c *bindingClient) SynchronizeBindings(ctx context.Context, snapshot core.BindingSnapshot) error {
	if c.snapshots != nil {
		select {
		case c.snapshots <- snapshot:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if c.syncErr != nil {
		return c.syncErr
	}
	return c.fakeClient.SynchronizeBindings(ctx, snapshot)
}

func TestMintBoundRouteRejectsStaleChoiceAndFailedReconciliation(t *testing.T) {
	client := &bindingClient{fakeClient: &fakeClient{}}
	store := newFakeRoutingStore()
	svc := New(client, store)
	a, _, err := store.GetOrCreateAccountsManagerSessionRoute(t.Context(), domain.AccountsManagerSessionRoute{SessionID: "session-a", Provider: domain.AccountsManagerProviderCodex, AccountID: "safe-a"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.mintBoundRoute(t.Context(), client, a, "a"); err != nil {
		t.Fatal(err)
	}
	client.minted = nil
	client.syncErr = core.ErrUnavailable
	if _, err := svc.mintBoundRoute(t.Context(), client, a, "a"); !errors.Is(err, core.ErrUnavailable) || len(client.minted) != 0 {
		t.Fatal("failed reconciliation minted a token")
	}
	client.syncErr = nil
	store.mu.Lock()
	store.revision++
	current := a
	current.Revision, current.AccountID = store.revision, "safe-b"
	store.routes["session-a:codex"] = current
	store.mu.Unlock()
	if _, err := svc.mintBoundRoute(t.Context(), client, a, "a"); !errors.Is(err, domain.ErrAccountsManagerBindingConflict) || len(client.minted) != 0 {
		t.Fatal("stale preparation minted a token")
	}
	if _, err := svc.mintBoundRoute(t.Context(), client, current, "b"); err != nil {
		t.Fatal(err)
	}
}

func TestBindingWatcherReconcilesWithoutNewLaunchAndStops(t *testing.T) {
	client := &bindingClient{fakeClient: &fakeClient{}, snapshots: make(chan core.BindingSnapshot, 4)}
	store := newFakeRoutingStore()
	svc := New(client, store)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan struct{})
	go func() { svc.watchBindings(ctx); close(done) }()
	select {
	case snapshot := <-client.snapshots:
		if snapshot.Revision != 1 || len(snapshot.Bindings) != 0 {
			t.Fatal("incorrect initial reconciliation")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("initial reconciliation missing")
	}
	_, _, err := store.GetOrCreateAccountsManagerSessionRoute(t.Context(), domain.AccountsManagerSessionRoute{SessionID: "session-a", Provider: domain.AccountsManagerProviderCodex, AccountID: "safe-a"})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case snapshot := <-client.snapshots:
		if snapshot.Revision != 2 || len(snapshot.Bindings) != 1 || snapshot.Bindings[0].AccountID != "safe-a" {
			t.Fatal("new durable choice was not reconciled")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("watcher missed new choice")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("watcher ignored cancellation")
	}
}
