package provideraccounts

import (
	"context"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// actionProxy records the account actions and the attempt identity of each reset.
type actionProxy struct {
	*usageProxy
	calls    []string
	attempts []string
	outcome  string
}

func (p *actionProxy) UseAccountReset(_ context.Context, provider, authID, requestID string) (string, error) {
	p.calls = append(p.calls, "reset "+provider+" "+authID)
	p.attempts = append(p.attempts, requestID)
	return p.outcome, nil
}
func (p *actionProxy) ResumeAccount(_ context.Context, provider, authID string) error {
	p.calls = append(p.calls, "resume "+provider+" "+authID)
	return nil
}
func (p *actionProxy) RefreshAccountSignIn(_ context.Context, provider, authID string) error {
	p.calls = append(p.calls, "refresh "+provider+" "+authID)
	return nil
}

func TestAccountActionsReachTheHelperAndRereadUsage(t *testing.T) {
	ctx := context.Background()
	proxy := &actionProxy{usageProxy: &usageProxy{fakeProxy: &fakeProxy{}, usage: domain.ProviderAccountUsage{Status: "available"}}, outcome: domain.ProviderResetDone}
	svc := New(&memoryStore{}, proxy, &fakeGuard{busy: make(map[domain.SessionID]bool)}, []byte("key"), "", func() string { return "account-1" })
	id, err := recordLogin(ctx, svc, "codex", "alice@example.test", "alice.json", "auth-1", "")
	if err != nil {
		t.Fatal(err)
	}
	state, err := svc.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	svc.AccountUsages(ctx, state.Accounts)

	outcome, err := svc.UseAccountReset(ctx, id)
	if err != nil || outcome != domain.ProviderResetDone {
		t.Fatalf("outcome %q, err %v", outcome, err)
	}
	// The limits just changed at the provider, so the cached reading is dropped.
	svc.AccountUsages(ctx, state.Accounts)
	if proxy.usageCalls != 2 {
		t.Fatalf("usage was read %d times, want a fresh read after the reset", proxy.usageCalls)
	}
	if _, err = svc.UseAccountReset(ctx, id); err != nil {
		t.Fatal(err)
	}
	// Each attempt has its own identity: AO never replays one on its own.
	if len(proxy.attempts) != 2 || proxy.attempts[0] == "" || proxy.attempts[0] == proxy.attempts[1] {
		t.Fatalf("attempts = %v", proxy.attempts)
	}
	if err = svc.ResumeAccount(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err = svc.RefreshAccountSignIn(ctx, id); err != nil {
		t.Fatal(err)
	}
	want := []string{"reset codex auth-1", "reset codex auth-1", "resume codex auth-1", "refresh codex auth-1"}
	if len(proxy.calls) != len(want) {
		t.Fatalf("calls = %v", proxy.calls)
	}
	for i := range want {
		if proxy.calls[i] != want[i] {
			t.Fatalf("calls = %v, want %v", proxy.calls, want)
		}
	}
}

func TestAccountActionsRefuseAccountsTheHelperCannotActOn(t *testing.T) {
	ctx := context.Background()
	h := setupAccounts(t)
	id := h.login(t, "codex", "alice@example.test")
	if _, err := h.svc.UseAccountReset(ctx, "missing"); !errors.Is(err, ports.ErrProviderAccountUnknown) {
		t.Fatalf("unknown account: %v", err)
	}
	// This helper predates the actions.
	if err := h.svc.ResumeAccount(ctx, id); !errors.Is(err, ports.ErrProviderAccountActionUnavailable) {
		t.Fatalf("helper without actions: %v", err)
	}
	if err := h.svc.Remove(ctx, id, "", true); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.UseAccountReset(ctx, id); !errors.Is(err, ports.ErrProviderLoginRequired) {
		t.Fatalf("signed-out account: %v", err)
	}
}
