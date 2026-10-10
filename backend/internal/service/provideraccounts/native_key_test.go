package provideraccounts

import (
	"context"
	"reflect"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// nativeKeySource is this computer: a sign-in, an API key, both or neither.
type nativeKeySource struct {
	nativeSourceFake
	keys     map[string]ports.NativeProviderAPIKey
	imported map[string]ports.VerifiedProviderLogin
	held     map[string]bool // fingerprints the helper already holds, added by hand
	imports  int
}

func (f *nativeKeySource) ReadNativeAPIKey(_ context.Context, provider string) (ports.NativeProviderAPIKey, error) {
	return f.keys[provider], nil
}

func (f *nativeKeySource) ImportNativeAPIKey(_ context.Context, provider string, key ports.NativeProviderAPIKey) (ports.VerifiedProviderLogin, error) {
	f.imports++
	if f.held[key.Fingerprint] {
		return ports.VerifiedProviderLogin{}, ports.ErrProviderAccountConflict
	}
	return f.imported[key.Fingerprint], nil
}

func keyLogin(provider, ref string) ports.VerifiedProviderLogin {
	return ports.VerifiedProviderLogin{Provider: provider, Email: "Global API key", Kind: "api_key", CredentialRef: "config-index:" + provider + ":" + ref, AuthID: "key-auth-" + ref}
}

func accountsOf(state domain.ProviderAccountState, provider string) (keys, signIns []domain.ProviderAccount) {
	for _, a := range state.Accounts {
		if a.Provider != provider {
			continue
		}
		if a.APIKey() {
			keys = append(keys, a)
		} else {
			signIns = append(signIns, a)
		}
	}
	return keys, signIns
}

func TestNativeAPIKeyBecomesAGlobalAccountAndTheDefault(t *testing.T) {
	h := setupAccounts(t)
	source := &nativeKeySource{
		keys:     map[string]ports.NativeProviderAPIKey{"claude": {Fingerprint: "key-one", APIKey: "secret", BaseURL: "https://api.anthropic.com"}},
		imported: map[string]ports.VerifiedProviderLogin{"key-one": keyLogin("claude", "1")},
	}
	h.svc.SetNativeAccountSource(source)
	if err := h.svc.RefreshNativeAccounts(h.ctx); err != nil {
		t.Fatal(err)
	}
	state, _ := h.svc.State(h.ctx)
	keys, signIns := accountsOf(state, "claude")
	if len(keys) != 1 || len(signIns) != 0 || !keys[0].Global || keys[0].Kind != "api_key" || keys[0].CredentialRef != "config-index:claude:1" {
		t.Fatalf("accounts=%+v", state.Accounts)
	}
	if id, _ := primary(state, "claude"); id != keys[0].ID {
		t.Fatalf("default=%q, want the key", id)
	}
	// Codex has no key and gets no account.
	if codexKeys, codexSignIns := accountsOf(state, "codex"); len(codexKeys)+len(codexSignIns) != 0 {
		t.Fatalf("codex accounts=%+v", state.Accounts)
	}
	// New sessions use it like any account.
	if id, managed, err := h.svc.ResolveAccount(h.ctx, domain.HarnessClaudeCode, ""); err != nil || !managed || id != keys[0].ID {
		t.Fatalf("resolve id=%q managed=%v err=%v", id, managed, err)
	}
}

func TestNativeAPIKeyBesideASignInIsTheDefaultAndBothAreGlobal(t *testing.T) {
	h := setupAccounts(t)
	source := &nativeKeySource{
		nativeSourceFake: nativeSourceFake{
			inputs:   map[string]ports.NativeProviderCredential{"claude": {Fingerprint: "sign-in-one"}},
			verified: map[string]ports.VerifiedProviderLogin{"claude": {Provider: "claude", Email: "me@example.test", CredentialRef: "native.json", AuthID: "native-auth"}},
		},
		keys:     map[string]ports.NativeProviderAPIKey{"claude": {Fingerprint: "key-one"}},
		imported: map[string]ports.VerifiedProviderLogin{"key-one": keyLogin("claude", "1")},
	}
	h.svc.SetNativeAccountSource(source)
	// A session adopted before anything was imported waits for an account.
	if _, err := h.svc.AdoptSession(h.ctx, "legacy", domain.HarnessClaudeCode); err != nil {
		t.Fatal(err)
	}
	state, _ := h.svc.State(h.ctx)
	keys, signIns := accountsOf(state, "claude")
	if len(keys) != 1 || len(signIns) != 1 || !keys[0].Global || !signIns[0].Global {
		t.Fatalf("accounts=%+v, want a key and a sign-in, both this computer's", state.Accounts)
	}
	// The agent uses a key ahead of a sign-in, so the key is the default.
	if id, _ := primary(state, "claude"); id != keys[0].ID {
		t.Fatalf("default=%q, want the key %q", id, keys[0].ID)
	}
	if route, _, _ := h.svc.SessionAccount(h.ctx, "legacy"); route.AccountID == "" {
		t.Fatalf("the adopted session was left waiting: %+v", route)
	}
}

func TestNativeAPIKeyIsImportedOnceAndLaterChoicesStand(t *testing.T) {
	h := setupAccounts(t)
	source := &nativeKeySource{
		keys:     map[string]ports.NativeProviderAPIKey{"codex": {Fingerprint: "key-one"}},
		imported: map[string]ports.VerifiedProviderLogin{"key-one": keyLogin("codex", "1")},
	}
	h.svc.SetNativeAccountSource(source)
	if err := h.svc.RefreshNativeAccounts(h.ctx); err != nil {
		t.Fatal(err)
	}
	// The user signs in and makes that the default.
	alice := h.login(t, "codex", "alice@example.test")
	if err := h.svc.SetPrimary(h.ctx, alice); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := h.svc.RefreshNativeAccounts(h.ctx); err != nil {
			t.Fatal(err)
		}
	}
	state, _ := h.svc.State(h.ctx)
	if id, _ := primary(state, "codex"); id != alice {
		t.Fatalf("default=%q, want the user's choice", id)
	}
	if source.imports != 1 {
		t.Fatalf("the same key was imported %d times", source.imports)
	}
	// Removing the imported account is not undone either.
	keys, _ := accountsOf(state, "codex")
	if err := h.svc.Remove(h.ctx, keys[0].ID, "", false); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.RefreshNativeAccounts(h.ctx); err != nil {
		t.Fatal(err)
	}
	state, _ = h.svc.State(h.ctx)
	if keys, _ := accountsOf(state, "codex"); len(keys) != 0 || source.imports != 1 {
		t.Fatalf("a removed key came back: accounts=%+v imports=%d", state.Accounts, source.imports)
	}
}

func TestAChangedNativeAPIKeyTakesThePlaceOfTheOneBefore(t *testing.T) {
	h := setupAccounts(t)
	source := &nativeKeySource{
		keys:     map[string]ports.NativeProviderAPIKey{"codex": {Fingerprint: "key-one"}},
		imported: map[string]ports.VerifiedProviderLogin{"key-one": keyLogin("codex", "1"), "key-two": keyLogin("codex", "2")},
	}
	h.svc.SetNativeAccountSource(source)
	if err := h.svc.RefreshNativeAccounts(h.ctx); err != nil {
		t.Fatal(err)
	}
	state, _ := h.svc.State(h.ctx)
	keys, _ := accountsOf(state, "codex")
	h.assign(t, "worker", domain.HarnessCodex, keys[0].ID)

	source.keys["codex"] = ports.NativeProviderAPIKey{Fingerprint: "key-two"}
	if err := h.svc.RefreshNativeAccounts(h.ctx); err != nil {
		t.Fatal(err)
	}
	state, _ = h.svc.State(h.ctx)
	after, _ := accountsOf(state, "codex")
	if len(after) != 1 || after[0].ID != keys[0].ID || after[0].CredentialRef != "config-index:codex:2" || !after[0].Global {
		t.Fatalf("accounts=%+v, want the same account holding the new key", state.Accounts)
	}
	if !reflect.DeepEqual(h.proxy.deleted, []string{"config-index:codex:1"}) {
		t.Fatalf("deleted=%v, want the old key removed from the helper", h.proxy.deleted)
	}
	// Its sessions stay on it and now use the new key.
	if route, _, _ := h.svc.SessionAccount(h.ctx, "worker"); route.AccountID != keys[0].ID {
		t.Fatalf("route=%+v", route)
	}
	if routes := h.proxy.snapshot.Routes; len(routes) != 1 || routes[0].AuthID != "key-auth-2" {
		t.Fatalf("helper routes=%+v", routes)
	}
}

func TestANativeAPIKeyAlreadyAddedByHandIsLeftAsItIs(t *testing.T) {
	h := setupAccounts(t)
	// The user typed this key in themselves, and it is not the default.
	byHand, err := h.svc.RecordCredential(h.ctx, ports.VerifiedProviderLogin{Provider: "codex", Email: "Work key", Kind: "api_key", CredentialRef: "config-index:codex:9", AuthID: "hand-auth"}, "")
	if err != nil {
		t.Fatal(err)
	}
	alice := h.login(t, "codex", "alice@example.test")
	if err := h.svc.SetPrimary(h.ctx, alice); err != nil {
		t.Fatal(err)
	}
	source := &nativeKeySource{
		keys: map[string]ports.NativeProviderAPIKey{"codex": {Fingerprint: "key-one"}},
		held: map[string]bool{"key-one": true},
	}
	h.svc.SetNativeAccountSource(source)
	for i := 0; i < 3; i++ {
		if err := h.svc.RefreshNativeAccounts(h.ctx); err != nil {
			t.Fatal(err)
		}
	}
	state, _ := h.svc.State(h.ctx)
	keys, _ := accountsOf(state, "codex")
	if len(keys) != 1 || keys[0].ID != byHand || keys[0].Global {
		t.Fatalf("accounts=%+v, want only the key added by hand, unmarked", state.Accounts)
	}
	if id, _ := primary(state, "codex"); id != alice {
		t.Fatalf("default=%q, want it unchanged", id)
	}
	if source.imports != 1 {
		t.Fatalf("the helper was asked %d times about a key it already holds", source.imports)
	}
}
