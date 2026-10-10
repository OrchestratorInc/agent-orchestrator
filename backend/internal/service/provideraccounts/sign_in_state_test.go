package provideraccounts

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// signInProxy reports CLIProxy's per-account sign-in verdict.
type signInProxy struct {
	*fakeProxy
	failures map[string]string
	err      error
}

func (p *signInProxy) AccountSignInFailures(context.Context) (map[string]string, error) {
	return p.failures, p.err
}

func setupSignIn(t *testing.T) (*Service, *signInProxy, string, string) {
	t.Helper()
	proxy := &signInProxy{fakeProxy: &fakeProxy{}}
	n := 0
	svc := New(&memoryStore{}, proxy, &fakeGuard{busy: make(map[domain.SessionID]bool)}, []byte(strings.Repeat("k", 32)), "http://127.0.0.1:1234", func() string { n++; return "account-" + string(rune('0'+n)) })
	ctx := context.Background()
	alice, err := recordLogin(ctx, svc, "codex", "alice@example.test", "alice.json", "alice-auth", "")
	if err != nil {
		t.Fatal(err)
	}
	bob, err := recordLogin(ctx, svc, "codex", "bob@example.test", "bob.json", "bob-auth", "")
	if err != nil {
		t.Fatal(err)
	}
	return svc, proxy, alice, bob
}

func TestDeadSignInIsShownAndBlocksNewUseButKeepsExistingSessions(t *testing.T) {
	svc, proxy, alice, bob := setupSignIn(t)
	ctx := context.Background()
	if err := svc.AssignAccount(ctx, "existing", domain.HarnessCodex, alice); err != nil {
		t.Fatal(err)
	}
	if err := svc.AssignAccount(ctx, "other", domain.HarnessCodex, bob); err != nil {
		t.Fatal(err)
	}
	proxy.failures = map[string]string{"alice-auth": "disabled (invalid grant)"}
	state, err := svc.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	failed := svc.AccountSignInFailures(ctx, state.Accounts, true)
	if !failed[alice] || failed[bob] {
		t.Fatalf("sign-in verdict by account = %v", failed)
	}
	// alice is the default: new sessions must ask for sign-in, never fall back.
	if _, managed, err := svc.ResolveAccount(ctx, domain.HarnessCodex, ""); !managed || !errors.Is(err, ports.ErrProviderLoginRequired) {
		t.Fatalf("new session on a dead default: managed=%t err=%v", managed, err)
	}
	if _, _, err := svc.ResolveAccount(ctx, domain.HarnessCodex, bob); err != nil {
		t.Fatalf("healthy explicit account was blocked: %v", err)
	}
	if err := svc.Switch(ctx, "other", alice); !errors.Is(err, ports.ErrProviderLoginRequired) {
		t.Fatalf("session moved onto a dead account: %v", err)
	}
	if err := svc.AssignAccount(ctx, "new", domain.HarnessCodex, alice); !errors.Is(err, ports.ErrProviderLoginRequired) {
		t.Fatalf("new session assigned to a dead account: %v", err)
	}
	if route, managed, err := svc.SessionAccount(ctx, "existing"); err != nil || !managed || route.AccountID != alice {
		t.Fatalf("existing session was moved off its account: %+v err=%v", route, err)
	}
	// Moving the default to a healthy account restores new sessions.
	if err := svc.SetPrimary(ctx, bob); err != nil {
		t.Fatal(err)
	}
	if id, _, err := svc.ResolveAccount(ctx, domain.HarnessCodex, ""); err != nil || id != bob {
		t.Fatalf("resolved=%s err=%v", id, err)
	}
	if err := svc.SetPrimary(ctx, alice); !errors.Is(err, ports.ErrProviderLoginRequired) {
		t.Fatalf("dead account became the default: %v", err)
	}
}

func TestReadinessCountsOnlyAccountsCLIProxyStillAccepts(t *testing.T) {
	svc, proxy, _, _ := setupSignIn(t)
	ctx := context.Background()
	state, err := svc.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	observe := func() domain.AgentAuthenticationState {
		observation, handled := svc.AuthenticationReadiness(ctx, domain.HarnessCodex, domain.AgentReadinessPurposeDisplay)
		if !handled {
			t.Fatal("managed provider readiness was not handled")
		}
		return observation.State
	}
	if got := observe(); got != domain.AgentAuthenticationAuthorized {
		t.Fatalf("healthy accounts: %v", got)
	}
	proxy.failures = map[string]string{"alice-auth": "unauthorized"}
	svc.AccountSignInFailures(ctx, state.Accounts, true)
	if got := observe(); got != domain.AgentAuthenticationAuthorized {
		t.Fatalf("one healthy account left: %v", got)
	}
	proxy.failures = map[string]string{"alice-auth": "unauthorized", "bob-auth": "invalid grant (retrying)"}
	svc.AccountSignInFailures(ctx, state.Accounts, true)
	if got := observe(); got != domain.AgentAuthenticationUnauthorized {
		t.Fatalf("every account dead: %v", got)
	}
}

func TestHelperOutageNeverBlocksAHealthyAccount(t *testing.T) {
	svc, proxy, alice, bob := setupSignIn(t)
	ctx := context.Background()
	state, err := svc.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	proxy.err = errTestFailure
	if failed := svc.AccountSignInFailures(ctx, state.Accounts, true); len(failed) != 0 {
		t.Fatalf("an unreachable helper marked accounts dead: %v", failed)
	}
	if _, _, err := svc.ResolveAccount(ctx, domain.HarnessCodex, ""); err != nil {
		t.Fatalf("an unreachable helper blocked a healthy account: %v", err)
	}
	// The last verdict stands while the helper cannot answer.
	proxy.err, proxy.failures = nil, map[string]string{"bob-auth": "unauthorized"}
	svc.AccountSignInFailures(ctx, state.Accounts, true)
	proxy.err, proxy.failures = errTestFailure, nil
	if failed := svc.AccountSignInFailures(ctx, state.Accounts, true); !failed[bob] || failed[alice] {
		t.Fatalf("verdict was dropped during an outage: %v", failed)
	}
}

func TestSignInAgainReplacesADeadCredentialAndKeepsItsSessions(t *testing.T) {
	svc, proxy, alice, bob := setupSignIn(t)
	ctx := context.Background()
	if err := svc.AssignAccount(ctx, "existing", domain.HarnessCodex, alice); err != nil {
		t.Fatal(err)
	}
	// A working sign-in is never replaced by another login.
	if _, err := recordLogin(ctx, svc, "codex", "bob@example.test", "bob-new.json", "bob-auth-2", bob); !errors.Is(err, ports.ErrProviderAccountConflict) {
		t.Fatalf("a healthy account was replaced: %v", err)
	}
	proxy.failures = map[string]string{"alice-auth": "unauthorized"}
	svc.WarmAccounts(ctx)
	guard := svc.guard.(*fakeGuard)
	guard.busy["existing"] = true
	if id, err := recordLogin(ctx, svc, "codex", "alice@example.test", "alice-new.json", "alice-auth-2", alice); err != nil || id != alice {
		t.Fatalf("sign in again on a dead account: id=%s err=%v", id, err)
	}
	state, err := svc.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	entry, _ := account(state, alice)
	if entry.CredentialRef != "alice-new.json" || entry.AuthID != "alice-auth-2" || len(state.Accounts) != 2 {
		t.Fatalf("account after sign-in again = %+v accounts=%d", entry, len(state.Accounts))
	}
	if len(proxy.deleted) != 1 || proxy.deleted[0] != "alice.json" {
		t.Fatalf("dead credential cleanup = %v", proxy.deleted)
	}
	if len(guard.acquired) != 0 {
		t.Fatal("sign in again waited for the account's sessions to be idle")
	}
	if route, managed, err := svc.SessionAccount(ctx, "existing"); err != nil || !managed || route.AccountID != alice {
		t.Fatalf("session lost its account: %+v err=%v", route, err)
	}
	if got := proxy.snapshot.Routes[0].AuthID; got != "alice-auth-2" {
		t.Fatalf("session still routed to the dead sign-in: %s", got)
	}
	if id, _, err := svc.ResolveAccount(ctx, domain.HarnessCodex, ""); err != nil || id != alice {
		t.Fatalf("new sessions after sign-in again: id=%s err=%v", id, err)
	}
}

func TestSignInCanBeStartedAgainOnlyForADeadAccount(t *testing.T) {
	svc, proxy, alice, _ := setupSignIn(t)
	ctx := context.Background()
	logins := NewLoginCoordinator(svc, &loginProxyFake{status: "waiting"}, func() string { return "login-1" })
	if _, err := logins.Start(ctx, "codex", alice); !errors.Is(err, ports.ErrProviderAccountConflict) {
		t.Fatalf("sign-in started for a healthy signed-in account: %v", err)
	}
	proxy.failures = map[string]string{"alice-auth": "disabled (invalid grant)"}
	svc.WarmAccounts(ctx)
	login, err := logins.Start(ctx, "codex", alice)
	if err != nil || login.AccountID != alice || login.Status != "waiting" {
		t.Fatalf("sign in again on a dead account: %+v err=%v", login, err)
	}
}

// endingProxy is a helper whose usage reading says one account's sign-in has
// stopped renewing.
type endingProxy struct {
	*signInProxy
	ending string
}

func (p *endingProxy) FetchAccountUsage(_ context.Context, _, authID, _ string) (domain.ProviderAccountUsage, error) {
	return domain.ProviderAccountUsage{Status: "available", SignInEnding: authID == p.ending}, nil
}

func TestASignInThatHasStoppedRenewingCanBeReplacedWhileItStillWorks(t *testing.T) {
	svc, signIn, alice, bob := setupSignIn(t)
	ctx := context.Background()
	proxy := &endingProxy{signInProxy: signIn, ending: "alice-auth"}
	svc.proxy = proxy
	if err := svc.AssignAccount(ctx, "existing", domain.HarnessCodex, alice); err != nil {
		t.Fatal(err)
	}
	logins := NewLoginCoordinator(svc, &loginProxyFake{status: "waiting"}, func() string { return "login-1" })
	// Nothing is known about the account yet, so it counts as healthy.
	if _, err := logins.Start(ctx, "codex", alice); !errors.Is(err, ports.ErrProviderAccountConflict) {
		t.Fatalf("sign-in started before the account was read: %v", err)
	}
	state, err := svc.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	svc.AccountUsages(ctx, state.Accounts)

	// It still works: new sessions use it and nothing reports it signed out.
	if id, _, err := svc.ResolveAccount(ctx, domain.HarnessCodex, ""); err != nil || id != alice {
		t.Fatalf("a still-working account was refused: id=%s err=%v", id, err)
	}
	if failed := svc.AccountSignInFailures(ctx, state.Accounts, true); len(failed) != 0 {
		t.Fatalf("a still-working account was reported signed out: %v", failed)
	}
	if login, err := logins.Start(ctx, "codex", alice); err != nil || login.AccountID != alice {
		t.Fatalf("sign in again on an ending account: %+v err=%v", login, err)
	}
	if id, err := recordLogin(ctx, svc, "codex", "alice@example.test", "alice-new.json", "alice-auth-2", alice); err != nil || id != alice {
		t.Fatalf("replace an ending sign-in: id=%s err=%v", id, err)
	}
	if len(signIn.deleted) != 1 || signIn.deleted[0] != "alice.json" {
		t.Fatalf("the ending credential was not removed: %v", signIn.deleted)
	}
	if got := signIn.snapshot.Routes[0].AuthID; got != "alice-auth-2" {
		t.Fatalf("the session still uses the ending sign-in: %s", got)
	}
	// The account next to it is healthy and stays protected.
	if _, err := recordLogin(ctx, svc, "codex", "bob@example.test", "bob-new.json", "bob-auth-2", bob); !errors.Is(err, ports.ErrProviderAccountConflict) {
		t.Fatalf("a healthy account was replaced: %v", err)
	}
}
