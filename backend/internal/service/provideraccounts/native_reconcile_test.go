package provideraccounts

import (
	"context"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type nativeSourceFake struct {
	inputs   map[string]ports.NativeProviderCredential
	verified map[string]ports.VerifiedProviderLogin
	calls    int
}

func (f *nativeSourceFake) ReadNativeAccount(_ context.Context, provider string) (ports.NativeProviderCredential, error) {
	f.calls++
	return f.inputs[provider], nil
}
func (f *nativeSourceFake) ImportNativeAccount(_ context.Context, provider string, _ ports.NativeProviderCredential) (ports.VerifiedProviderLogin, error) {
	return f.verified[provider], nil
}

func TestRefreshNativeAccountsImportsOnceAndPreservesExplicitRemoval(t *testing.T) {
	h := setupAccounts(t)
	source := &nativeSourceFake{inputs: map[string]ports.NativeProviderCredential{"codex": {Fingerprint: "native-one"}}, verified: map[string]ports.VerifiedProviderLogin{"codex": {Provider: "codex", Email: "native@example.test", Kind: "oauth", CredentialRef: "native.json", AuthID: "native-auth"}}}
	h.svc.SetNativeAccountSource(source)
	if err := h.svc.RefreshNativeAccounts(h.ctx); err != nil {
		t.Fatal(err)
	}
	state, err := h.svc.State(h.ctx)
	if err != nil || len(state.Accounts) != 1 || !state.Accounts[0].Global {
		t.Fatalf("state=%+v err=%v", state, err)
	}
	id := state.Accounts[0].ID
	if err := h.svc.Remove(h.ctx, id, "", true); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.RefreshNativeAccounts(h.ctx); err != nil {
		t.Fatal(err)
	}
	state, err = h.svc.State(h.ctx)
	if err != nil || len(state.Accounts) != 1 || state.Accounts[0].CredentialRef != "" {
		t.Fatalf("native refresh undid sign-out: state=%+v err=%v", state, err)
	}
	if source.calls != 4 {
		t.Fatalf("expected both providers on each refresh, calls=%d", source.calls)
	}
}

func TestRefreshNativeAccountsIfDueReadsNativeLoginsOncePerWindow(t *testing.T) {
	h := setupAccounts(t)
	source := &nativeSourceFake{inputs: map[string]ports.NativeProviderCredential{}}
	h.svc.SetNativeAccountSource(source)
	for attempt := 0; attempt < 3; attempt++ {
		if err := h.svc.RefreshNativeAccountsIfDue(h.ctx); err != nil {
			t.Fatal(err)
		}
	}
	if source.calls != 2 {
		t.Fatalf("routine account reads re-read native logins: calls=%d", source.calls)
	}
	if err := h.svc.RefreshNativeAccounts(h.ctx); err != nil || source.calls != 4 {
		t.Fatalf("explicit refresh was throttled: calls=%d err=%v", source.calls, err)
	}
}

func globalAccounts(t *testing.T, h accountHarness) map[string]bool {
	t.Helper()
	state, err := h.svc.State(h.ctx)
	if err != nil {
		t.Fatal(err)
	}
	marks := make(map[string]bool)
	for _, a := range state.Accounts {
		marks[a.Email] = a.Global
	}
	return marks
}

// The Global mark belongs to whoever this computer is signed in as, not to the
// one account record that happened to be created by the import.
func TestGlobalMarkFollowsTheComputersLoginHoweverTheAccountWasAdded(t *testing.T) {
	h := setupAccounts(t)
	source := &nativeSourceFake{inputs: map[string]ports.NativeProviderCredential{"codex": {Fingerprint: "native-one"}}, verified: map[string]ports.VerifiedProviderLogin{"codex": {Provider: "codex", Email: "alice@example.test", CredentialRef: "native.json", AuthID: "native-auth"}}}
	h.svc.SetNativeAccountSource(source)
	if err := h.svc.RefreshNativeAccounts(h.ctx); err != nil {
		t.Fatal(err)
	}
	state, _ := h.svc.State(h.ctx)
	imported := state.Accounts[0].ID
	other := h.login(t, "codex", "bob@example.test")
	if marks := globalAccounts(t, h); !marks["alice@example.test"] || marks["bob@example.test"] {
		t.Fatalf("after import: %v", marks)
	}

	// Removed, then signed in to again through the browser: a new record, same person.
	if err := h.svc.Remove(h.ctx, imported, other, false); err != nil {
		t.Fatal(err)
	}
	again, err := recordLogin(h.ctx, h.svc, "codex", "alice@example.test", "browser.json", "browser-auth", "")
	if err != nil {
		t.Fatal(err)
	}
	if marks := globalAccounts(t, h); !marks["alice@example.test"] || marks["bob@example.test"] {
		t.Fatalf("after removing and signing in again: %v", marks)
	}

	// Signed out and in again on the same record.
	if err = h.svc.Remove(h.ctx, again, "", true); err != nil {
		t.Fatal(err)
	}
	if _, err = recordLogin(h.ctx, h.svc, "codex", "alice@example.test", "relogin.json", "relogin-auth", again); err != nil {
		t.Fatal(err)
	}
	if marks := globalAccounts(t, h); !marks["alice@example.test"] {
		t.Fatalf("after signing in again on the same account: %v", marks)
	}
}

// An import remembered before the email was recorded is repaired from the
// computer's login itself, without importing or re-adding anything.
func TestGlobalMarkIsRepairedForAnImportRememberedWithoutItsEmail(t *testing.T) {
	h := setupAccounts(t)
	source := &nativeSourceFake{inputs: map[string]ports.NativeProviderCredential{"codex": {Fingerprint: "native-one"}}, verified: map[string]ports.VerifiedProviderLogin{"codex": {Provider: "codex", Email: "alice@example.test", CredentialRef: "native.json", AuthID: "native-auth"}}}
	h.svc.SetNativeAccountSource(source)
	if err := h.svc.RefreshNativeAccounts(h.ctx); err != nil {
		t.Fatal(err)
	}
	state, _ := h.svc.State(h.ctx)
	other := h.login(t, "codex", "bob@example.test")
	if err := h.svc.Remove(h.ctx, state.Accounts[0].ID, other, false); err != nil {
		t.Fatal(err)
	}
	if _, err := recordLogin(h.ctx, h.svc, "codex", "alice@example.test", "browser.json", "browser-auth", ""); err != nil {
		t.Fatal(err)
	}
	// Put the state back to how an older build left it: no email, no mark.
	h.store.state.NativeImports["codex"] = domain.NativeProviderImport{Fingerprint: "native-one", AccountID: "gone"}
	for i := range h.store.state.Accounts {
		h.store.state.Accounts[i].Global = false
	}
	source.inputs["codex"] = ports.NativeProviderCredential{Fingerprint: "native-one", Email: "Alice@Example.test"}
	if err := h.svc.RefreshNativeAccounts(h.ctx); err != nil {
		t.Fatal(err)
	}
	marks := globalAccounts(t, h)
	if !marks["alice@example.test"] || marks["bob@example.test"] || len(marks) != 2 {
		t.Fatalf("after repair: %v", marks)
	}
}
