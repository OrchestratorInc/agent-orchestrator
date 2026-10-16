package provideraccounts

import (
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// AO never copies the computer's own login; the account under the same email
// carries the Device mark, and keys never do.
func TestTheDeviceMarkFollowsTheComputersLoginByEmail(t *testing.T) {
	h := setup(t)
	h.helper.devices["codex"] = "bob@example.com"
	alice := h.signIn("codex", "alice@example.com")
	bob := h.signIn("codex", "Bob@Example.com")
	key, err := h.svc.record(h.ctx, "codex", ports.VerifiedProviderLogin{Provider: "codex", Email: "Work key", Kind: "api_key", CredentialRef: "config-index:codex:0", AuthID: "key-auth"}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	views, err := h.svc.Accounts(h.ctx, false, true)
	if err != nil {
		t.Fatal(err)
	}
	marked := map[string]bool{}
	for _, view := range views {
		marked[view.ID] = view.Global
	}
	if marked[alice] || !marked[bob] || marked[key] {
		t.Fatalf("device marks=%v", marked)
	}
	if len(h.helper.started) != 0 {
		t.Fatalf("listing the accounts started a sign-in: %+v", h.helper.started)
	}
	// The computer logs in as alice: the mark moves once the read is due again.
	h.helper.devices["codex"] = "alice@example.com"
	views, _ = h.svc.Accounts(h.ctx, false, true)
	for _, view := range views {
		if view.Global != (view.ID == alice) {
			t.Fatalf("after a new login on the computer: %s global=%t", view.ID, view.Global)
		}
	}
	reads := h.helper.reads
	if _, _ = h.svc.Accounts(h.ctx, false, false); h.helper.reads != reads {
		t.Fatal("the computer's login was read again before it was due")
	}
}

// A provider with no account in AO is left on the computer's own setup, as
// before Account Manager: Bedrock, Vertex and apiKeyHelper users keep working.
func TestAProviderWithNoDefaultKeepsTheComputersOwnSetup(t *testing.T) {
	h := setup(t)
	if id, managed, err := h.svc.ResolveAccount(h.ctx, domain.HarnessClaudeCode, ""); id != "" || managed || err != nil {
		t.Fatalf("no account: id=%q managed=%t err=%v", id, managed, err)
	}
	alice := h.signIn("claude", "alice@example.com")
	if id, managed, err := h.svc.ResolveAccount(h.ctx, domain.HarnessClaudeCode, ""); id != alice || !managed || err != nil {
		t.Fatalf("with a default: id=%q managed=%t err=%v", id, managed, err)
	}
	if _, managed, err := h.svc.ResolveAccount(h.ctx, domain.HarnessCodex, ""); managed || err != nil {
		t.Fatalf("codex has no account: managed=%t err=%v", managed, err)
	}
	if _, _, err := h.svc.ResolveAccount(h.ctx, domain.HarnessCodex, alice); !errors.Is(err, ports.ErrProviderAccountIncompatible) {
		t.Fatalf("an account of another provider named outright: err=%v", err)
	}
}
