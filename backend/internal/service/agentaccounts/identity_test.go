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
// empty color takes the first color no other account uses.
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
	if view, err = svc.UpdateIdentity(ctx, "acct-a", nil, strPtr("green")); err != nil || view.Account.Name != "Work laptop" || view.Account.Color != "green" {
		t.Fatalf("color only: %+v, %v", view.Account, err)
	}
	// The computer's own account took blue when the accounts were first listed.
	if view, err = svc.UpdateIdentity(ctx, "acct-a", nil, strPtr("")); err != nil || view.Account.Color != "violet" || view.Account.Name != "Work laptop" {
		t.Fatalf("empty color: %+v, %v", view.Account, err)
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
		_, err := svc.UpdateIdentity(ctx, "acct-a", nil, strPtr("green"))
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
	if got.Name != "Work" || got.Color != "green" {
		t.Fatalf("overlapping updates lost a field: name %q color %q", got.Name, got.Color)
	}
}

// Listing gives every account without a color, the computer's own included,
// the first free one in auto order, and a second account that shares a color
// a new one. Colors already set and unique stay.
func TestListGivesEveryAccountItsOwnColor(t *testing.T) {
	ctx := context.Background()
	svc, store, root := newAPIKeyService(t, &fakeClaude{signedInAs: map[string]string{}})
	t0 := time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)
	store.accounts = []domain.AgentAccount{
		{ID: "acct-a", Provider: domain.AgentAccountProviderClaude, Home: root + "/claude/acct-a", Email: "ronish.rohan@gmail.com", CreatedAt: t0},
		{ID: "acct-b", Provider: domain.AgentAccountProviderClaude, Home: root + "/claude/acct-b", Email: "ronish@untrivial.ai", Color: "blue", CreatedAt: t0.Add(time.Minute)},
		{ID: "acct-c", Provider: domain.AgentAccountProviderClaude, Home: root + "/claude/acct-c", Email: "c@example.com", Color: "blue", CreatedAt: t0.Add(2 * time.Minute)},
	}
	views, err := svc.List(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, view := range views {
		got[view.Account.ID] = view.Account.Color
	}
	// In list order: the device account skips the blue two accounts hold and
	// takes violet, acct-a takes teal, acct-b keeps its blue, and acct-c, the
	// second blue, moves to green.
	want := map[string]string{"device-claude": "violet", "acct-a": "teal", "acct-b": "blue", "acct-c": "green"}
	for id, color := range want {
		if got[id] != color {
			t.Fatalf("colors = %v, want %v", got, want)
		}
	}
	// The colors are saved, so a second list shows the same ones.
	if label := store.devices["device-claude"]; label.Color != "violet" {
		t.Fatalf("device label = %+v", label)
	}
	again, err := svc.List(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, view := range again {
		if got[view.Account.ID] != view.Account.Color {
			t.Fatalf("second list moved %s from %s to %s", view.Account.ID, got[view.Account.ID], view.Account.Color)
		}
	}
}

// A color another account of the provider uses is refused, on an update and
// on a new API key account; the same color on the account itself is fine.
func TestAColorTakenByAnotherAccountIsRefused(t *testing.T) {
	ctx := context.Background()
	svc, store, root := newAPIKeyService(t, &fakeClaude{signedInAs: map[string]string{}})
	store.accounts = []domain.AgentAccount{
		{ID: "acct-a", Provider: domain.AgentAccountProviderClaude, Home: root + "/claude/acct-a", Color: "red"},
		{ID: "acct-b", Provider: domain.AgentAccountProviderClaude, Home: root + "/claude/acct-b", Color: "pink"},
	}
	if _, err := svc.UpdateIdentity(ctx, "acct-a", nil, strPtr("pink")); !errors.Is(err, ports.ErrAgentAccountColorTaken) {
		t.Fatalf("taken color: err = %v", err)
	}
	if got := store.accounts[0]; got.Color != "red" {
		t.Fatalf("a refused update changed the color: %+v", got)
	}
	if view, err := svc.UpdateIdentity(ctx, "acct-a", strPtr("Work"), strPtr("red")); err != nil || view.Account.Color != "red" {
		t.Fatalf("own color: %+v, %v", view.Account, err)
	}
	if _, err := svc.AddAPIKeyAccount(ctx, domain.AgentAccountProviderClaude, "Key", goodKey, "pink"); !errors.Is(err, ports.ErrAgentAccountColorTaken) {
		t.Fatalf("new account with a taken color: err = %v", err)
	}
	view, err := svc.AddAPIKeyAccount(ctx, domain.AgentAccountProviderClaude, "Key", goodKey, "")
	if err != nil || view.Account.Color == "" || view.Account.Color == "red" || view.Account.Color == "pink" {
		t.Fatalf("new account without a color: %+v, %v", view.Account, err)
	}
}

// The computer's own account has no saved row, but its name and color are
// saved apart and listed with it.
func TestUpdateIdentityRenamesTheComputersOwnAccount(t *testing.T) {
	ctx := context.Background()
	svc, store, _ := newAPIKeyService(t, &fakeClaude{signedInAs: map[string]string{}})
	view, err := svc.UpdateIdentity(ctx, "device-claude", strPtr("Personal"), strPtr("amber"))
	if err != nil || view.Account.Name != "Personal" || view.Account.Color != "amber" || !view.Account.Device() {
		t.Fatalf("view = %+v, err = %v", view.Account, err)
	}
	if label := store.devices["device-claude"]; label != (domain.AgentAccountLabel{Name: "Personal", Color: "amber"}) {
		t.Fatalf("saved label = %+v", label)
	}
	views, err := svc.List(ctx, false)
	if err != nil || len(views) == 0 || views[0].Account.Name != "Personal" || views[0].Account.Color != "amber" {
		t.Fatalf("listed = %+v, err = %v", views, err)
	}
}

// countingStore counts reads of the "This computer" accounts' labels.
type countingStore struct {
	*memStore
	deviceReads int
}

func (c *countingStore) ListDeviceAgentAccountIdentities(ctx context.Context) (map[string]domain.AgentAccountLabel, error) {
	c.deviceReads++
	return c.memStore.ListDeviceAgentAccountIdentities(ctx)
}

// The computer's own account's name and color are read from the store once;
// later lookups, and a rename, use and update the kept copy.
func TestDeviceAccountLabelsAreReadOnce(t *testing.T) {
	ctx := context.Background()
	svc, store, _ := newAPIKeyService(t, &fakeClaude{signedInAs: map[string]string{}})
	counting := &countingStore{memStore: store}
	svc.store = counting
	if _, err := svc.List(ctx, false); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpdateIdentity(ctx, "device-claude", strPtr("Personal"), nil); err != nil {
		t.Fatal(err)
	}
	views, err := svc.List(ctx, false)
	if err != nil || views[0].Account.Name != "Personal" {
		t.Fatalf("listed = %+v, err = %v", views, err)
	}
	if counting.deviceReads != 1 {
		t.Fatalf("device labels read %d times, want 1", counting.deviceReads)
	}
}
