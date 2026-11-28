package agentaccounts

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/domain"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/ports"
)

const (
	goodKey  = "sk-ant-api03-good-0123456789abcdef"
	otherKey = "sk-ant-api03-other-0123456789wxyz"
)

func newAPIKeyService(t *testing.T, claude *fakeClaude) (*Service, *memStore, string) {
	t.Helper()
	root := t.TempDir()
	store := &memStore{defaults: map[domain.AgentAccountProvider]string{}, bindings: map[domain.SessionID]string{}}
	svc := New(store, []ports.AgentAccountProviderCLI{claude}, root, fakeTerminals{}, nil)
	svc.SetKeyCheck(func(_ context.Context, _ domain.AgentAccountProvider, key string) error {
		switch key {
		case goodKey, otherKey:
			return nil
		case "sk-ant-unreachable-0000":
			return errors.New("dial tcp: no route to host")
		}
		return ports.ErrAgentAccountKeyRejected
	})
	return svc, store, root
}

// A key the provider refuses, or could not check, leaves nothing behind; an
// accepted one is saved in its own folder, accepted for Claude Code, and never
// read through the CLI, which has no sign-in for it.
func TestAddAPIKeyAccountChecksTheKeyAndSavesItInItsFolder(t *testing.T) {
	ctx := context.Background()
	claude := &fakeClaude{signedInAs: map[string]string{"": "a@example.com"}}
	svc, store, root := newAPIKeyService(t, claude)

	if _, err := svc.AddAPIKeyAccount(ctx, domain.AgentAccountProviderClaude, "Work", "sk-ant-bad-0000000000"); !errors.Is(err, ports.ErrAgentAccountKeyRejected) {
		t.Fatalf("rejected key: err = %v, want ErrAgentAccountKeyRejected", err)
	}
	if _, err := svc.AddAPIKeyAccount(ctx, domain.AgentAccountProviderClaude, "Work", "sk-ant-unreachable-0000"); !errors.Is(err, ports.ErrAgentAccountKeyUnchecked) {
		t.Fatalf("unchecked key: err = %v, want ErrAgentAccountKeyUnchecked", err)
	}
	if _, err := svc.AddAPIKeyAccount(ctx, domain.AgentAccountProviderCodex, "Work", goodKey); !errors.Is(err, ports.ErrAgentAccountProviderMismatch) {
		t.Fatalf("codex key: err = %v, want ErrAgentAccountProviderMismatch", err)
	}
	if entries, _ := os.ReadDir(filepath.Join(root, "claude")); len(store.accounts) != 0 || len(entries) != 0 {
		t.Fatalf("a refused key left %d accounts and %d folders", len(store.accounts), len(entries))
	}

	view, err := svc.AddAPIKeyAccount(ctx, domain.AgentAccountProviderClaude, "  Work \n key ", "  "+goodKey+"\n")
	if err != nil {
		t.Fatal(err)
	}
	account := view.Account
	if !account.APIKey() || account.Name != "Work key" || account.KeyHint != "cdef" || account.Email != "" {
		t.Fatalf("account = %+v", account)
	}
	if !view.LoggedIn || view.SignedIn || view.Limits != nil {
		t.Fatalf("view = %+v, want logged in, not a subscription, no limits", view)
	}
	if data, err := os.ReadFile(filepath.Join(account.Home, apiKeyFile)); err != nil || string(data) != goodKey {
		t.Fatalf("saved key = %q, %v", data, err)
	}
	if claude.approved[account.Home] != goodKey {
		t.Fatalf("approved key = %q, want the saved key", claude.approved[account.Home])
	}
	if _, err := svc.List(ctx, true); err != nil {
		t.Fatal(err)
	}
	if claude.wasChecked(account.Home) {
		t.Fatal("an API key account's folder was read through the CLI")
	}
}

// A session on an API key account launches with the key, reads no limits, and
// is never moved at a usage limit; a new key replaces the old one.
func TestAPIKeyAccountLaunchesWithItsKeyAndHasNoLimits(t *testing.T) {
	ctx := context.Background()
	claude := &fakeClaude{signedInAs: map[string]string{"": "a@example.com"}, windows: []domain.AgentAccountLimitWindow{{ID: "five_hour", UsedPercent: 10, DurationMinutes: 300}}}
	svc, _, _ := newAPIKeyService(t, claude)
	view, err := svc.AddAPIKeyAccount(ctx, domain.AgentAccountProviderClaude, "Work", goodKey)
	if err != nil {
		t.Fatal(err)
	}
	account := view.Account

	if err := svc.BindSession(ctx, "s1", domain.HarnessClaudeCode, account.ID); err != nil {
		t.Fatalf("bind to the API key account: %v", err)
	}
	env, err := svc.SessionLaunchEnv(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if env["ANTHROPIC_API_KEY"] != goodKey || env["CLAUDE_CONFIG_DIR"] != account.Home {
		t.Fatalf("launch env = %v", env)
	}
	if _, _, err := svc.SessionLimits(ctx, "s1", domain.HarnessClaudeCode, true); !errors.Is(err, ports.ErrAgentAccountNoLimits) {
		t.Fatalf("SessionLimits err = %v, want ErrAgentAccountNoLimits", err)
	}
	// The computer's own login has room, but a session never moves off a key.
	if target, err := svc.HandoffTarget(ctx, "s1", domain.HarnessClaudeCode, ""); err != nil || target != "" {
		t.Fatalf("HandoffTarget = %q, %v; want none", target, err)
	}

	if _, err := svc.StartSignInAgain(ctx, account.ID, LoginRequest{}); !errors.Is(err, ports.ErrAgentAccountKindMismatch) {
		t.Fatalf("sign in again: err = %v, want ErrAgentAccountKindMismatch", err)
	}
	if _, err := svc.ReplaceAPIKey(ctx, domain.DeviceAgentAccountID(domain.AgentAccountProviderClaude), otherKey); !errors.Is(err, ports.ErrAgentAccountKindMismatch) {
		t.Fatalf("replace on a subscription: err = %v, want ErrAgentAccountKindMismatch", err)
	}
	if _, err := svc.ReplaceAPIKey(ctx, account.ID, "sk-ant-bad-0000000000"); !errors.Is(err, ports.ErrAgentAccountKeyRejected) {
		t.Fatalf("replace with a rejected key: err = %v", err)
	}
	replaced, err := svc.ReplaceAPIKey(ctx, account.ID, otherKey)
	if err != nil {
		t.Fatal(err)
	}
	if replaced.Account.KeyHint != "wxyz" || claude.approved[account.Home] != otherKey {
		t.Fatalf("replaced = %+v, approved %q", replaced.Account, claude.approved[account.Home])
	}
	if env, _ := svc.SessionLaunchEnv(ctx, "s1"); env["ANTHROPIC_API_KEY"] != otherKey {
		t.Fatalf("launch env after replace = %v", env)
	}
}

// A signed-out default never falls back to an API key account, which bills per
// token, and a usage-limited session never moves to one.
func TestAPIKeyAccountIsOnlyUsedWhenChosen(t *testing.T) {
	ctx := context.Background()
	claude := &fakeClaude{signedInAs: map[string]string{}}
	svc, _, _ := newAPIKeyService(t, claude)
	view, err := svc.AddAPIKeyAccount(ctx, domain.AgentAccountProviderClaude, "Work", goodKey)
	if err != nil {
		t.Fatal(err)
	}

	// The default is known to be signed out once it has been read.
	if _, err := svc.List(ctx, true); err != nil {
		t.Fatal(err)
	}
	if err := svc.BindSession(ctx, "s1", domain.HarnessClaudeCode, ""); !errors.Is(err, ports.ErrAgentAccountDefaultUnusable) {
		t.Fatalf("bind on a signed-out default: err = %v, want ErrAgentAccountDefaultUnusable", err)
	}

	claude.signedInAs[""] = "a@example.com"
	if _, err := svc.List(ctx, true); err != nil {
		t.Fatal(err)
	}
	if err := svc.BindSession(ctx, "s2", domain.HarnessClaudeCode, ""); err != nil {
		t.Fatal(err)
	}
	if target, err := svc.HandoffTarget(ctx, "s2", domain.HarnessClaudeCode, ""); err != nil || target == view.Account.ID {
		t.Fatalf("HandoffTarget = %q, %v; an API key account is never a target", target, err)
	}

	// Chosen as the default, it is what new sessions start on.
	if err := svc.SetDefault(ctx, view.Account.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.BindSession(ctx, "s3", domain.HarnessClaudeCode, ""); err != nil {
		t.Fatal(err)
	}
	if id, err := svc.SessionAccountID(ctx, "s3", domain.HarnessClaudeCode); err != nil || id != view.Account.ID {
		t.Fatalf("new session account = %q, %v; want the API key default", id, err)
	}
	if identity, handled := svc.DefaultSignIn(ctx, domain.HarnessClaudeCode); !handled || !identity.SignedIn {
		t.Fatalf("DefaultSignIn = %+v, %v; an API key default with its key is ready", identity, handled)
	}
}

// A replacement that fails keeps the account's current key and key hint, so
// launches never use a key the account does not show.
func TestReplaceAPIKeyKeepsTheCurrentKeyWhenItFails(t *testing.T) {
	ctx := context.Background()
	claude := &fakeClaude{signedInAs: map[string]string{"": "a@example.com"}}
	svc, store, _ := newAPIKeyService(t, claude)
	view, err := svc.AddAPIKeyAccount(ctx, domain.AgentAccountProviderClaude, "Work", goodKey)
	if err != nil {
		t.Fatal(err)
	}
	home := view.Account.Home
	savedKey := func() string {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(home, apiKeyFile))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}

	claude.approveErr = errors.New("read .claude.json: invalid character")
	if _, err := svc.ReplaceAPIKey(ctx, view.Account.ID, otherKey); err == nil {
		t.Fatal("replace with a failing approval succeeded")
	}
	if got := savedKey(); got != goodKey {
		t.Fatalf("after a failed approval the saved key is %q, want the current key", got)
	}
	claude.approveErr = nil

	store.hintErr = errors.New("database is locked")
	if _, err := svc.ReplaceAPIKey(ctx, view.Account.ID, otherKey); err == nil {
		t.Fatal("replace with a failing hint update succeeded")
	}
	if got := savedKey(); got != goodKey {
		t.Fatalf("after a failed hint update the saved key is %q, want the current key restored", got)
	}
	if hint := store.accounts[0].KeyHint; hint != "cdef" {
		t.Fatalf("key hint = %q, want the current key's", hint)
	}
}
