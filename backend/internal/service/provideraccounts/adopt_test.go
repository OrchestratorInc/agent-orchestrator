package provideraccounts

import (
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestAdoptSessionPutsALegacySessionOnTheProvidersDefaultAccount(t *testing.T) {
	h := setupAccounts(t)
	alice := h.login(t, "codex", "alice@example.test")
	h.login(t, "codex", "bob@example.test")
	adopted, err := h.svc.AdoptSession(h.ctx, "legacy", domain.HarnessCodex)
	if err != nil || !adopted {
		t.Fatalf("adopted=%v err=%v", adopted, err)
	}
	route, managed, err := h.svc.SessionAccount(h.ctx, "legacy")
	if err != nil || !managed || route.AccountID != alice || route.Provider != "codex" {
		t.Fatalf("route=%+v managed=%v err=%v", route, managed, err)
	}
	// The helper was told, so the session's ticket reaches the account.
	if routes := h.proxy.snapshot.Routes; len(routes) != 1 || routes[0].SessionID != "legacy" || routes[0].AuthID != "alice@example.test-auth" {
		t.Fatalf("helper routes=%+v", routes)
	}
	// From here it launches like any managed session.
	if env, err := h.svc.LaunchAccountEnv(h.ctx, "legacy"); err != nil || env["AO_PROXY_TICKET"] == "" {
		t.Fatalf("env=%v err=%v", env, err)
	}
}

func TestAdoptSessionLeavesAManagedSessionAsItIs(t *testing.T) {
	h := setupAccounts(t)
	h.login(t, "codex", "alice@example.test")
	bob := h.login(t, "codex", "bob@example.test")
	h.assign(t, "worker", domain.HarnessCodex, bob)
	revision := h.proxy.snapshot.Revision
	adopted, err := h.svc.AdoptSession(h.ctx, "worker", domain.HarnessCodex)
	if err != nil || adopted {
		t.Fatalf("adopted=%v err=%v", adopted, err)
	}
	if route, _, _ := h.svc.SessionAccount(h.ctx, "worker"); route.AccountID != bob {
		t.Fatalf("a chosen account was replaced by the default: %+v", route)
	}
	if h.proxy.snapshot.Revision != revision {
		t.Fatal("leaving a session alone still rewrote the routes")
	}
}

func TestAdoptSessionWaitsForAnAccountWhenTheProviderHasNone(t *testing.T) {
	h := setupAccounts(t)
	// Another provider's account is no help to a Claude session.
	h.login(t, "codex", "alice@example.test")
	adopted, err := h.svc.AdoptSession(h.ctx, "legacy", domain.HarnessClaudeCode)
	if err != nil || !adopted {
		t.Fatalf("adopted=%v err=%v", adopted, err)
	}
	route, managed, _ := h.svc.SessionAccount(h.ctx, "legacy")
	if !managed || route.AccountID != "" || route.Provider != "claude" {
		t.Fatalf("route=%+v managed=%v", route, managed)
	}
	// It is pointed at the helper already, so it cannot use this computer's own sign-in.
	if env, err := h.svc.LaunchAccountEnv(h.ctx, "legacy"); err != nil || env["ANTHROPIC_BASE_URL"] == "" || env["ANTHROPIC_AUTH_TOKEN"] == "" {
		t.Fatalf("env=%v err=%v", env, err)
	}
	// The first Claude sign-in takes it in.
	carol := h.login(t, "claude", "carol@example.test")
	if route, _, _ := h.svc.SessionAccount(h.ctx, "legacy"); route.AccountID != carol {
		t.Fatalf("the waiting session was not given the first account: %+v", route)
	}
}

func TestAdoptSessionKeepsADefaultThatNeedsANewSignIn(t *testing.T) {
	svc, proxy, alice, _ := setupSignIn(t)
	ctx := t.Context()
	proxy.failures = map[string]string{"alice-auth": "unauthorized"}
	svc.WarmAccounts(ctx)
	// A new session would be refused; an existing one keeps its place on the
	// default and works again once that account is signed in to.
	if _, _, err := svc.ResolveAccount(ctx, domain.HarnessCodex, ""); !errors.Is(err, ports.ErrProviderLoginRequired) {
		t.Fatalf("err=%v", err)
	}
	adopted, err := svc.AdoptSession(ctx, "legacy", domain.HarnessCodex)
	if err != nil || !adopted {
		t.Fatalf("adopted=%v err=%v", adopted, err)
	}
	if route, _, _ := svc.SessionAccount(ctx, "legacy"); route.AccountID != alice {
		t.Fatalf("route=%+v, want the default account", route)
	}
}

func TestAdoptSessionImportsThisComputersSignInFirst(t *testing.T) {
	h := setupAccounts(t)
	h.svc.SetNativeAccountSource(&nativeSourceFake{
		inputs:   map[string]ports.NativeProviderCredential{"codex": {Fingerprint: "native-one"}},
		verified: map[string]ports.VerifiedProviderLogin{"codex": {Provider: "codex", Email: "me@example.test", CredentialRef: "native.json", AuthID: "native-auth"}},
	})
	// Nothing has asked for the accounts yet, so none exists.
	adopted, err := h.svc.AdoptSession(h.ctx, "legacy", domain.HarnessCodex)
	if err != nil || !adopted {
		t.Fatalf("adopted=%v err=%v", adopted, err)
	}
	state, _ := h.svc.State(h.ctx)
	route, _, _ := h.svc.SessionAccount(h.ctx, "legacy")
	if len(state.Accounts) != 1 || !state.Accounts[0].Global || route.AccountID != state.Accounts[0].ID {
		t.Fatalf("accounts=%+v route=%+v, want the session on the imported sign-in", state.Accounts, route)
	}
}

func TestAdoptSessionChangesNothingWhenItCannotTellTheHelper(t *testing.T) {
	h := setupAccounts(t)
	h.login(t, "codex", "alice@example.test")
	h.proxy.fail = "apply"
	if adopted, err := h.svc.AdoptSession(h.ctx, "legacy", domain.HarnessCodex); err == nil || adopted {
		t.Fatalf("adopted=%v err=%v", adopted, err)
	}
	h.proxy.fail = ""
	// Other agents are not Account Manager's to adopt.
	if adopted, err := h.svc.AdoptSession(h.ctx, "other", domain.HarnessCursor); err != nil || adopted {
		t.Fatalf("adopted=%v err=%v", adopted, err)
	}
}
