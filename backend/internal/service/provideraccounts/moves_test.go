package provideraccounts

import (
	"errors"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func (h *harness) addKey(provider, label, index string) string {
	h.t.Helper()
	id, err := h.svc.record(h.ctx, provider, ports.VerifiedProviderLogin{Provider: provider, Email: label, Kind: "api_key", CredentialRef: "config-index:" + provider + ":" + index, AuthID: provider + "-key-" + index}, "", "")
	if err != nil {
		h.t.Fatalf("add key %s: %v", label, err)
	}
	return id
}

// Codex replays its encrypted reasoning, which a ChatGPT plan cannot read
// after an API key wrote it, or the other way round: every later turn of a
// session moved across would fail with 400.
func TestACodexSessionMovesOnlyBetweenAccountsOfOneKind(t *testing.T) {
	h := setup(t)
	pro := h.signIn("codex", "pro@example.com")
	plus := h.signIn("codex", "plus@example.com")
	key := h.addKey("codex", "Work key", "0")
	limit := func(from, to string) error { return h.settings(from, ports.ProviderAccountAction{OnLimit: &to}) }
	if err := limit(pro, key); !errors.Is(err, ports.ErrProviderAccountKindMismatch) {
		t.Fatalf("a plan's sessions set to move to a key on its limit: err=%v", err)
	}
	if err := limit(key, pro); !errors.Is(err, ports.ErrProviderAccountKindMismatch) {
		t.Fatalf("a key's sessions set to move to a plan on its limit: err=%v", err)
	}
	if err := limit(pro, plus); err != nil {
		t.Fatalf("plan to plan: %v", err)
	}
	h.assign("s1", domain.HarnessCodex, pro)
	// The refusal names the kind, not the provider, which matches.
	if err := h.act(key, "assign-session", "s1"); !errors.Is(err, ports.ErrProviderAccountKindMismatch) || !strings.Contains(err.Error(), "API keys") {
		t.Fatalf("a plan session moved onto a key: err=%v", err)
	}
	// Another provider's account is refused as such.
	claude := h.signIn("claude", "claude@example.com")
	if err := h.act(claude, "assign-session", "s1"); !errors.Is(err, ports.ErrProviderAccountIncompatible) {
		t.Fatalf("a Codex session moved onto a Claude account: err=%v", err)
	}
	if err := limit(pro, claude); !errors.Is(err, ports.ErrProviderAccountIncompatible) {
		t.Fatalf("a plan's sessions set to move to a Claude account on its limit: err=%v", err)
	}
	h.route("s1", pro)
	if err := h.act(plus, "assign-session", "s1"); err != nil {
		t.Fatal(err)
	}
	h.route("s1", plus)
	// Removing the key leaves its sessions waiting for a sign-in rather than on the plan default.
	h.assign("s2", domain.HarnessCodex, key)
	if err := h.act(key, "remove"); err != nil {
		t.Fatal(err)
	}
	h.route("s2", "")
	// Nor does a later default of the other kind pick them up.
	if err := h.act(plus, "primary"); err != nil {
		t.Fatal(err)
	}
	h.route("s2", "")
	// The next key signed in takes them, default or not.
	key = h.addKey("codex", "Second key", "1")
	h.route("s2", key)
	if err := h.act(key, "primary"); err != nil {
		t.Fatal(err)
	}
	// A key signed out keeps its sessions until it signs in again; a plan does not take them meanwhile.
	h.assign("s3", domain.HarnessCodex, key)
	if err := h.act(key, "sign-out", plus); err != nil {
		t.Fatal(err)
	}
	h.route("s3", key)
	if err := h.act(pro, "primary"); err != nil {
		t.Fatal(err)
	}
	h.route("s3", key)
	if _, err := h.svc.record(h.ctx, "codex", ports.VerifiedProviderLogin{Provider: "codex", Email: "Second key", Kind: "api_key", CredentialRef: "config-index:codex:2", AuthID: "codex-key-2"}, key, ""); err != nil {
		t.Fatal(err)
	}
	h.route("s3", key)
	// Claude has no such binding.
	subscription := h.signIn("claude", "max@example.com")
	claudeKey := h.addKey("claude", "Console key", "0")
	if err := limit(subscription, claudeKey); err != nil {
		t.Fatalf("claude plan to key: %v", err)
	}
}

// A reset whose answer was lost is sent again under the same request ID, so
// the provider answers already_redeemed instead of spending a second reset.
func TestARetriedResetReusesItsRequestID(t *testing.T) {
	h := setup(t)
	alice := h.signIn("codex", "alice@example.com")
	reset := func() {
		t.Helper()
		if _, err := h.svc.Act(h.ctx, alice, ports.ProviderAccountAction{Action: ports.AccountActionReset}); err != nil {
			t.Fatal(err)
		}
	}
	h.helper.outcome = domain.ProviderResetUnknown
	reset()
	reset()
	h.helper.outcome = domain.ProviderResetDone
	reset()
	reset()
	ids := h.helper.requestIDs
	if len(ids) != 4 || ids[0] != ids[1] || ids[1] != ids[2] || ids[3] == ids[2] {
		t.Fatalf("request ids=%v", ids)
	}
	if _, err := h.svc.Act(h.ctx, alice, ports.ProviderAccountAction{Action: ports.AccountActionRefresh}); err != nil || h.helper.requestIDs[4] == ids[3] {
		t.Fatalf("another action reused the reset's id: err=%v ids=%v", err, h.helper.requestIDs)
	}
}
