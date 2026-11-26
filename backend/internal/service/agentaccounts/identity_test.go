package agentaccounts

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func strPtr(s string) *string { return &s }

// A nil field is left as it is; an empty name falls back to the email, and an
// empty color clears the color.
func TestUpdateIdentityIsAPartialUpdate(t *testing.T) {
	ctx := context.Background()
	claude := &fakeClaude{signedInAs: map[string]string{}}
	svc, store, root := newAPIKeyService(t, claude)
	store.accounts = []domain.AgentAccount{{
		ID: "acct-a", Provider: domain.AgentAccountProviderClaude, Home: root + "/claude/acct-a", Email: "a@example.com",
		Name: "Old", Color: "red",
	}}

	view, err := svc.UpdateIdentity(ctx, "acct-a", strPtr("  Work \n laptop "), nil)
	if err != nil {
		t.Fatal(err)
	}
	if view.Account.Name != "Work laptop" || view.Account.Color != "red" {
		t.Fatalf("name only: %+v", view.Account)
	}
	if view, err = svc.UpdateIdentity(ctx, "acct-a", nil, strPtr("blue")); err != nil || view.Account.Name != "Work laptop" || view.Account.Color != "blue" {
		t.Fatalf("color only: %+v, %v", view.Account, err)
	}
	if view, err = svc.UpdateIdentity(ctx, "acct-a", nil, strPtr("")); err != nil || view.Account.Color != "" || view.Account.Name != "Work laptop" {
		t.Fatalf("clear color: %+v, %v", view.Account, err)
	}
	if view, err = svc.UpdateIdentity(ctx, "acct-a", strPtr(""), nil); err != nil || view.Account.Name != "" {
		t.Fatalf("clear name: %+v, %v", view.Account, err)
	}
	long := strings.Repeat("é", maxAPIKeyName+20)
	if view, err = svc.UpdateIdentity(ctx, "acct-a", &long, nil); err != nil || len([]rune(view.Account.Name)) != maxAPIKeyName {
		t.Fatalf("long name: %d runes, %v", len([]rune(view.Account.Name)), err)
	}
}

func TestUpdateIdentityRejectsAnUnknownColorAndAccount(t *testing.T) {
	ctx := context.Background()
	svc, store, root := newAPIKeyService(t, &fakeClaude{signedInAs: map[string]string{}})
	store.accounts = []domain.AgentAccount{{
		ID: "acct-a", Provider: domain.AgentAccountProviderClaude, Home: root + "/claude/acct-a", Color: "red",
	}}
	if _, err := svc.UpdateIdentity(ctx, "acct-a", strPtr("New"), strPtr("chartreuse")); !errors.Is(err, ports.ErrAgentAccountColorInvalid) {
		t.Fatalf("invalid color: err = %v", err)
	}
	if got := store.accounts[0]; got.Name != "" || got.Color != "red" {
		t.Fatalf("a rejected update changed the account: %+v", got)
	}
	if _, err := svc.UpdateIdentity(ctx, "missing", strPtr("x"), nil); !errors.Is(err, ports.ErrAgentAccountUnknown) {
		t.Fatalf("unknown id: err = %v", err)
	}
}

func TestAddAPIKeyAccountSavesAValidColor(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := newAPIKeyService(t, &fakeClaude{signedInAs: map[string]string{}})
	if _, err := svc.AddAPIKeyAccount(ctx, domain.AgentAccountProviderClaude, "Work", goodKey, "mauve"); !errors.Is(err, ports.ErrAgentAccountColorInvalid) {
		t.Fatalf("invalid color: err = %v", err)
	}
	view, err := svc.AddAPIKeyAccount(ctx, domain.AgentAccountProviderClaude, "Work", goodKey, "pink")
	if err != nil || view.Account.Color != "pink" {
		t.Fatalf("view = %+v, err = %v", view.Account, err)
	}
}

// holdingStore holds the first identity write until release is closed, so a
// second update can run while the first is between its read and its write. It
// guards memStore, which is not safe for concurrent use, the way SQLite is.
type holdingStore struct {
	*memStore
	mu      sync.Mutex
	held    chan struct{}
	release chan struct{}
	first   bool
}

func (h *holdingStore) ListAgentAccounts(ctx context.Context) ([]domain.AgentAccount, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	accounts, err := h.memStore.ListAgentAccounts(ctx)
	return append([]domain.AgentAccount(nil), accounts...), err
}

func (h *holdingStore) UpdateAgentAccountIdentity(ctx context.Context, id, name, color string) error {
	h.mu.Lock()
	first := !h.first
	h.first = true
	h.mu.Unlock()
	if first {
		close(h.held)
		<-h.release
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.memStore.UpdateAgentAccountIdentity(ctx, id, name, color)
}

// Two partial updates that overlap keep both changes: one that sends only the
// name does not write back the color it read before the other set it.
func TestUpdateIdentityOverlappingPartialUpdatesKeepBothFields(t *testing.T) {
	ctx := context.Background()
	claude := &fakeClaude{signedInAs: map[string]string{}}
	svc, store, root := newAPIKeyService(t, claude)
	store.accounts = []domain.AgentAccount{{
		ID: "acct-a", Provider: domain.AgentAccountProviderClaude, Home: root + "/claude/acct-a", Email: "a@example.com",
		Name: "Old", Color: "red",
	}}
	holding := &holdingStore{memStore: store, held: make(chan struct{}), release: make(chan struct{})}
	svc.store = holding

	nameDone := make(chan error, 1)
	go func() {
		_, err := svc.UpdateIdentity(ctx, "acct-a", strPtr("Work"), nil)
		nameDone <- err
	}()
	<-holding.held

	colorDone := make(chan error, 1)
	go func() {
		_, err := svc.UpdateIdentity(ctx, "acct-a", nil, strPtr("blue"))
		colorDone <- err
	}()
	// Without serialization the color update finishes while the name update is
	// held; with it, the color update waits. Either way, release the name update.
	select {
	case err := <-colorDone:
		colorDone <- err
	case <-time.After(100 * time.Millisecond):
	}
	close(holding.release)
	if err := <-nameDone; err != nil {
		t.Fatal(err)
	}
	if err := <-colorDone; err != nil {
		t.Fatal(err)
	}
	holding.mu.Lock()
	got := store.accounts[0]
	holding.mu.Unlock()
	if got.Name != "Work" || got.Color != "blue" {
		t.Fatalf("overlapping updates lost a field: name %q color %q", got.Name, got.Color)
	}
}
