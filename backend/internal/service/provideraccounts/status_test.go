package provideraccounts

import (
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func (h *harness) usageOf(accountID string, refresh bool) *domain.ProviderAccountUsage {
	h.t.Helper()
	views, err := h.svc.Accounts(h.ctx, true, refresh)
	if err != nil {
		h.t.Fatal(err)
	}
	for _, v := range views {
		if v.ID == accountID {
			return v.Usage
		}
	}
	h.t.Fatalf("account %s is not listed", accountID)
	return nil
}

func (h *harness) fail(authID string) {
	h.helper.mu.Lock()
	h.helper.held = []ports.ProviderCredential{{AuthID: authID, Name: "dead.json", Provider: "codex", ModifiedAt: h.svc.now(), Failed: "unauthorized"}}
	h.helper.mu.Unlock()
	h.advance(30 * time.Second)
}

func TestUsageIsCachedAndAProviderThatCannotReportDoesNotHideTheAccount(t *testing.T) {
	h := setup(t)
	alice := h.signIn("codex", "alice@example.com")
	out := h.signIn("codex", "bob@example.com")
	if err := h.act(out, "sign-out"); err != nil {
		t.Fatal(err)
	}
	if h.view(alice).Usage != nil || len(h.helper.usageFor) != 0 {
		t.Fatal("usage was read although the list did not ask for it")
	}
	for range 3 {
		if usage := h.usageOf(alice, false); usage == nil || usage.Status != "available" || usage.Plan != "Pro" {
			t.Fatalf("usage=%+v", usage)
		}
	}
	if !reflect.DeepEqual(h.helper.usageFor, []string{"alice@example.com-auth"}) || h.usageOf(out, false) != nil {
		t.Fatalf("usage reads=%v", h.helper.usageFor)
	}
	h.advance(2 * time.Minute)
	h.helper.usageErr, h.helper.usage = errInjected, domain.ProviderAccountUsage{PausedReason: "rate limited"}
	for range 3 {
		if usage := h.usageOf(alice, false); usage == nil || usage.Status != "unavailable" || usage.PausedReason != "rate limited" || !h.view(alice).SignedIn {
			t.Fatalf("usage=%+v", usage)
		}
	}
	if len(h.helper.usageFor) != 2 {
		t.Fatalf("a failed reading was not kept: %d reads", len(h.helper.usageFor))
	}
	h.advance(15 * time.Second)
	h.helper.usageErr, h.helper.usage = nil, domain.ProviderAccountUsage{Status: "available", Plan: "Max"}
	if usage := h.usageOf(alice, false); usage.Plan != "Max" || len(h.helper.usageFor) != 3 {
		t.Fatalf("a failed reading was kept too long: usage=%+v reads=%d", usage, len(h.helper.usageFor))
	}
}

func TestADeadSignInIsShownBlocksNewUseAndKeepsItsSessions(t *testing.T) {
	h := setup(t)
	alice := h.signIn("codex", "alice@example.com")
	bob := h.signIn("codex", "bob@example.com")
	h.assign("s1", domain.HarnessCodex, alice)
	h.assign("s2", domain.HarnessCodex, bob)
	h.fail("alice@example.com-auth")
	if h.view(alice).SignedIn || !h.view(bob).SignedIn || !h.view(alice).Primary {
		t.Fatalf("alice=%+v bob=%+v", h.view(alice), h.view(bob))
	}
	h.route("s1", alice)
	for name, use := range map[string]func() error{
		"new session":  func() error { _, _, err := h.svc.ResolveAccount(h.ctx, domain.HarnessCodex, ""); return err },
		"explicit":     func() error { _, _, err := h.svc.ResolveAccount(h.ctx, domain.HarnessCodex, alice); return err },
		"assign":       func() error { return h.svc.AssignAccount(h.ctx, "s3", domain.HarnessCodex, alice) },
		"move session": func() error { return h.act(alice, "assign-session", "s2") },
		"models":       func() error { _, _, err := h.svc.DiscoverModels(h.ctx, domain.HarnessCodex, ""); return err },
	} {
		if err := use(); !errors.Is(err, ports.ErrProviderLoginRequired) {
			t.Fatalf("%s on a dead sign-in: %v", name, err)
		}
	}
	if err := h.act(bob, "primary"); err != nil {
		t.Fatal(err)
	}
	if err := h.act(bob, "remove", alice); !errors.Is(err, ports.ErrProviderLoginRequired) {
		t.Fatalf("a dead sign-in as the replacement default: %v", err)
	}
	h.route("s1", alice)
	h.route("s2", bob)
	h.helper.mu.Lock()
	h.helper.held = nil
	h.helper.mu.Unlock()
	h.advance(30 * time.Second)
	if !h.view(alice).SignedIn {
		t.Fatal("a sign-in the provider accepts again is still shown as dead")
	}
}

func TestAHelperOutageNeverMarksAHealthyAccountSignedOut(t *testing.T) {
	h := setup(t)
	alice := h.signIn("codex", "alice@example.com")
	bob := h.signIn("codex", "bob@example.com")
	h.helper.heldErr = errInjected
	h.advance(time.Minute)
	if views, err := h.svc.Accounts(h.ctx, true, true); err != nil || !views[0].SignedIn || !views[1].SignedIn {
		t.Fatalf("views=%+v err=%v", views, err)
	}
	if id, _, err := h.svc.ResolveAccount(h.ctx, domain.HarnessCodex, ""); err != nil || id != alice {
		t.Fatalf("id=%q err=%v", id, err)
	}
	// The last verdict stands through an outage, for a dead sign-in too.
	h.helper.heldErr = nil
	h.fail("bob@example.com-auth")
	_ = h.view(bob)
	h.helper.heldErr = errInjected
	h.advance(time.Minute)
	if h.view(bob).SignedIn || !h.view(alice).SignedIn {
		t.Fatal("an outage changed the verdict")
	}
	listings := h.helper.heldCalls
	for range 5 {
		_ = h.view(alice)
	}
	if h.helper.heldCalls != listings {
		t.Fatalf("the verdict was re-read %d times within its window", h.helper.heldCalls-listings)
	}
}

func TestReadinessFollowsTheAccountsAndIsInvalidatedWhenTheyChange(t *testing.T) {
	h := setup(t)
	var mu sync.Mutex
	var changes []string
	h.svc.OnChange(func(provider string) { mu.Lock(); defer mu.Unlock(); changes = append(changes, provider) })
	changed := func(wait ...time.Duration) int {
		t.Helper()
		deadline := time.Now().Add(append(wait, 2*time.Second)[0])
		for {
			mu.Lock()
			n := len(changes)
			changes = nil
			mu.Unlock()
			if n > 0 || time.Now().After(deadline) {
				return n
			}
			time.Sleep(time.Millisecond)
		}
	}
	ready := func(agent domain.AgentHarness) (domain.AgentAuthenticationState, string) {
		t.Helper()
		observation, handled := h.svc.AuthenticationReadiness(h.ctx, agent, "")
		if !handled || observation.AttemptedAt == nil {
			t.Fatalf("observation=%+v handled=%t", observation, handled)
		}
		return observation.State, observation.Reason
	}
	if _, handled := h.svc.AuthenticationReadiness(h.ctx, domain.AgentHarness("opencode"), ""); handled {
		t.Fatal("an agent AO does not manage was answered")
	}
	if state, reason := ready(domain.HarnessCodex); state != domain.AgentAuthenticationUnauthorized || reason != "Sign in through Account Manager to use this provider." {
		t.Fatalf("state=%s reason=%q", state, reason)
	}
	alice := h.signIn("codex", "alice@example.com")
	if n := changed(); n != 2 {
		t.Fatalf("a sign-in invalidated %d providers", n)
	}
	if state, _ := ready(domain.HarnessCodex); state != domain.AgentAuthenticationAuthorized {
		t.Fatalf("state=%s", state)
	}
	if state, _ := ready(domain.HarnessClaudeCode); state != domain.AgentAuthenticationUnauthorized {
		t.Fatalf("claude state=%s", state)
	}
	h.assign("s1", domain.HarnessCodex, alice)
	if err := h.act(alice, "primary"); err != nil || changed(20*time.Millisecond) != 0 {
		t.Fatalf("a change that adds or removes no sign-in invalidated readiness (err=%v)", err)
	}
	h.fail("alice@example.com-auth")
	if state, reason := ready(domain.HarnessCodex); state != domain.AgentAuthenticationUnauthorized || reason != "Sign in again through Account Manager to use this provider." {
		t.Fatalf("only a dead sign-in: state=%s reason=%q", state, reason)
	}
	if changed() == 0 {
		t.Fatal("a sign-in dying did not invalidate readiness")
	}
	h.signIn("codex", "bob@example.com")
	if state, _ := ready(domain.HarnessCodex); state != domain.AgentAuthenticationAuthorized || changed() == 0 {
		t.Fatalf("one live account beside a dead one: state=%s", state)
	}
	if err := h.act(alice, "sign-out", "id-2"); err != nil || changed() == 0 {
		t.Fatalf("a sign-out did not invalidate readiness (err=%v)", err)
	}
	h.helper.native["claude"] = nativeSource{fingerprint: "f1", login: signIn("claude", "me@example.com")}
	h.svc.importNative(h.ctx, true)
	if state, _ := ready(domain.HarnessClaudeCode); state != domain.AgentAuthenticationAuthorized || changed() == 0 {
		t.Fatalf("after importing this computer's login: state=%s", state)
	}
	h.store.loadErr = errInjected
	if observation, handled := h.svc.AuthenticationReadiness(h.ctx, domain.HarnessCodex, ""); !handled || observation.State != domain.AgentAuthenticationUnknown || observation.Freshness != domain.AgentReadinessStale || observation.CheckedAt != nil {
		t.Fatalf("unreadable storage: %+v", observation)
	}
}

func TestModelsComeFromTheDefaultOrTheScopedAccount(t *testing.T) {
	h := setup(t)
	h.helper.models = []ports.AgentModelInfo{{ID: "gpt-5", Label: "GPT-5", Efforts: []string{"low", "high"}}}
	if _, handled, err := h.svc.DiscoverModels(h.ctx, domain.HarnessCodex, ""); !handled || !errors.Is(err, ports.ErrProviderLoginRequired) {
		t.Fatalf("no account: handled=%t err=%v", handled, err)
	}
	alice := h.signIn("codex", "alice@example.com")
	bob := h.signIn("codex", "bob@example.com")
	claude := h.signIn("claude", "carol@example.com")
	catalog, handled, err := h.svc.DiscoverModels(h.ctx, domain.HarnessCodex, "project-1")
	if err != nil || !handled || catalog.AgentID != string(domain.HarnessCodex) || catalog.Source != ports.ModelCatalogSourceManagedAccount || !reflect.DeepEqual(catalog.Models, h.helper.models) {
		t.Fatalf("catalog=%+v handled=%t err=%v", catalog, handled, err)
	}
	if catalog.SelectionMode != ports.ModelSelectionCatalog || catalog.CustomModelEntry != ports.CustomModelEntryDirect || !catalog.AllowCustom || catalog.FetchedAt.IsZero() {
		t.Fatalf("catalog=%+v", catalog)
	}
	if _, _, err = h.svc.DiscoverModels(h.ctx, domain.HarnessCodex, ports.ModelCatalogAccountScope(bob)); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(h.helper.modelsFor, []string{"alice@example.com-auth", "bob@example.com-auth"}) {
		t.Fatalf("models were read for %v", h.helper.modelsFor)
	}
	for scope, want := range map[string]error{ports.ModelCatalogAccountScope("gone"): ports.ErrProviderAccountUnknown, ports.ModelCatalogAccountScope(claude): ports.ErrProviderAccountIncompatible} {
		if _, handled, err := h.svc.DiscoverModels(h.ctx, domain.HarnessCodex, scope); !handled || !errors.Is(err, want) {
			t.Fatalf("scope %s: handled=%t err=%v", scope, handled, err)
		}
	}
	for _, unhandled := range []func() (bool, error){
		func() (bool, error) {
			_, handled, err := h.svc.DiscoverModels(h.ctx, domain.HarnessCodex, "@cred:cloud")
			return handled, err
		},
		func() (bool, error) {
			_, handled, err := h.svc.DiscoverModels(h.ctx, domain.AgentHarness("opencode"), "")
			return handled, err
		},
		func() (bool, error) {
			value, handled, err := h.svc.ModelsFingerprint(h.ctx, domain.HarnessCodex, " @cred:cloud")
			return handled || value != "", err
		},
	} {
		if handled, err := unhandled(); handled || err != nil {
			t.Fatalf("a scope Account Manager does not own was handled: err=%v", err)
		}
	}
	// The fingerprint names the scope's account and sign-in, and needs no helper.
	reads := len(h.helper.modelsFor)
	fingerprint := func(scope string) string {
		t.Helper()
		value, handled, err := h.svc.ModelsFingerprint(h.ctx, domain.HarnessCodex, scope)
		if err != nil || !handled || value == "" {
			t.Fatalf("fingerprint=%q handled=%t err=%v", value, handled, err)
		}
		return value
	}
	first := fingerprint("")
	if fingerprint("project-2") != first || fingerprint(ports.ModelCatalogAccountScope(alice)) != first || fingerprint(ports.ModelCatalogAccountScope(bob)) == first {
		t.Fatal("the fingerprint does not follow the scope's account")
	}
	if err = h.act(bob, "primary"); err != nil || fingerprint("") == first {
		t.Fatalf("another default kept the fingerprint (err=%v)", err)
	}
	if err = h.act(alice, "sign-out"); err != nil {
		t.Fatal(err)
	}
	again := signIn("codex", "alice@example.com")
	again.CredentialRef, again.AuthID = "again.json", "again-auth"
	if _, err = h.svc.record(h.ctx, "codex", again, alice); err != nil || fingerprint(ports.ModelCatalogAccountScope(alice)) == first {
		t.Fatalf("another sign-in kept the fingerprint (err=%v)", err)
	}
	if len(h.helper.modelsFor) != reads {
		t.Fatal("a fingerprint asked the helper for models")
	}
}

func TestAccountActionsReachTheHelperAndDropTheCachedUsage(t *testing.T) {
	h := setup(t)
	alice := h.signIn("codex", "alice@example.com")
	out := h.signIn("codex", "bob@example.com")
	if err := h.act(out, "sign-out"); err != nil {
		t.Fatal(err)
	}
	h.helper.outcome = domain.ProviderResetDone
	for i, action := range []string{ports.AccountActionReset, ports.AccountActionResume, ports.AccountActionRefresh} {
		_ = h.usageOf(alice, false)
		reads, listings := len(h.helper.usageFor), h.helper.heldCalls
		outcome, err := h.svc.Act(h.ctx, alice, ports.ProviderAccountAction{Action: action})
		if err != nil || outcome != domain.ProviderResetDone || h.helper.actions[i] != action+" alice@example.com-auth" {
			t.Fatalf("%s: outcome=%q err=%v actions=%v", action, outcome, err, h.helper.actions)
		}
		if _ = h.usageOf(alice, false); len(h.helper.usageFor) != reads+1 {
			t.Fatalf("%s kept the cached usage", action)
		}
		if refreshed := h.helper.heldCalls > listings; refreshed != (action == ports.AccountActionRefresh) {
			t.Fatalf("%s: sign-in verdict re-read=%t", action, refreshed)
		}
		for id, want := range map[string]error{out: ports.ErrProviderLoginRequired, "gone": ports.ErrProviderAccountUnknown} {
			if _, err := h.svc.Act(h.ctx, id, ports.ProviderAccountAction{Action: action}); !errors.Is(err, want) {
				t.Fatalf("%s on %s: %v", action, id, err)
			}
		}
	}
	h.helper.actionErr = errInjected
	if _, err := h.svc.Act(h.ctx, alice, ports.ProviderAccountAction{Action: ports.AccountActionReset}); !errors.Is(err, errInjected) || len(h.helper.actions) != 4 {
		t.Fatalf("err=%v actions=%v", err, h.helper.actions)
	}
}
