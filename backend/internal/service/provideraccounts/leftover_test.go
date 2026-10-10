package provideraccounts

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// inventoryProxy is a helper that can say which sign-ins it holds.
type inventoryProxy struct {
	*fakeProxy
	held    []ports.ProviderCredential
	listErr error
	lists   int
}

func (p *inventoryProxy) ListCredentials(context.Context) ([]ports.ProviderCredential, error) {
	p.lists++
	return p.held, p.listErr
}

func leftoverHarness(t *testing.T) (accountHarness, *inventoryProxy) {
	t.Helper()
	h := setupAccounts(t)
	proxy := &inventoryProxy{fakeProxy: h.proxy}
	h.svc.proxy = proxy
	return h, proxy
}

func TestLeftoverCredentialsAreRemovedAndAccountsKeepTheirs(t *testing.T) {
	h, proxy := leftoverHarness(t)
	h.login(t, "codex", "alice@example.test")
	h.login(t, "claude", "bob@example.test")
	old := time.Now().Add(-time.Hour)
	proxy.held = []ports.ProviderCredential{
		{Name: "alice@example.test.json", Provider: "codex", ModifiedAt: old},
		{Name: "bob@example.test.json", Provider: "claude", ModifiedAt: old},
		// Saved by a sign-in nobody recorded.
		{Name: "ao-abandoned.json", Provider: "codex", ModifiedAt: old},
		// Still inside a sign-in attempt; it may be recorded yet.
		{Name: "ao-in-progress.json", Provider: "claude", ModifiedAt: time.Now().Add(-time.Minute)},
		// AO cannot tell how old this one is.
		{Name: "ao-undated.json", Provider: "codex"},
		// Not a provider AO manages.
		{Name: "other.json", Provider: "gemini", ModifiedAt: old},
	}
	removed, err := h.svc.RemoveLeftoverCredentials(h.ctx, true)
	if err != nil || removed != 1 {
		t.Fatalf("removed=%d err=%v", removed, err)
	}
	if !reflect.DeepEqual(h.proxy.deleted, []string{"ao-abandoned.json"}) {
		t.Fatalf("deleted=%v", h.proxy.deleted)
	}
}

func TestLeftoverCredentialsWaitForAnUnfinishedAccountChange(t *testing.T) {
	h, proxy := leftoverHarness(t)
	h.login(t, "codex", "alice@example.test")
	proxy.held = []ports.ProviderCredential{{Name: "ao-abandoned.json", Provider: "codex", ModifiedAt: time.Now().Add(-time.Hour)}}
	// A change that did not finish may be about to name this credential.
	h.store.mu.Lock()
	h.store.pending = &domain.ProviderAccountIntent{Next: h.store.state}
	h.store.mu.Unlock()
	if removed, err := h.svc.RemoveLeftoverCredentials(h.ctx, true); err != nil || removed != 0 || len(h.proxy.deleted) != 0 {
		t.Fatalf("removed=%d deleted=%v err=%v", removed, h.proxy.deleted, err)
	}
}

func TestLeftoverCredentialsAreNotJudgedWhenTheHelperCannotListThem(t *testing.T) {
	h, proxy := leftoverHarness(t)
	proxy.listErr = errTestFailure
	if removed, err := h.svc.RemoveLeftoverCredentials(h.ctx, true); err == nil || removed != 0 || len(h.proxy.deleted) != 0 {
		t.Fatalf("removed=%d deleted=%v err=%v", removed, h.proxy.deleted, err)
	}
}

func TestLeftoverCredentialCheckPacesItself(t *testing.T) {
	h, proxy := leftoverHarness(t)
	for i := 0; i < 3; i++ {
		if _, err := h.svc.RemoveLeftoverCredentials(h.ctx, false); err != nil {
			t.Fatal(err)
		}
	}
	if proxy.lists != 1 {
		t.Fatalf("the helper was asked %d times in a row", proxy.lists)
	}
}
