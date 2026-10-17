package agentaccounts

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/shellterm"
)

type memStore struct {
	accounts []domain.AgentAccount
	defaults map[domain.AgentAccountProvider]string
	bindings map[domain.SessionID]string
}

func (m *memStore) ListAgentAccounts(context.Context) ([]domain.AgentAccount, error) {
	return m.accounts, nil
}
func (m *memStore) CreateAgentAccount(_ context.Context, a domain.AgentAccount) error {
	m.accounts = append(m.accounts, a)
	return nil
}
func (m *memStore) DeleteAgentAccount(context.Context, string) error { return nil }
func (m *memStore) GetAgentAccountDefault(_ context.Context, p domain.AgentAccountProvider) (string, bool, error) {
	id, ok := m.defaults[p]
	return id, ok, nil
}
func (m *memStore) SetAgentAccountDefault(_ context.Context, p domain.AgentAccountProvider, id string) error {
	m.defaults[p] = id
	return nil
}
func (m *memStore) BindSessionAgentAccount(_ context.Context, id domain.SessionID, _ domain.AgentAccountProvider, accountID string, _ time.Time) error {
	m.bindings[id] = accountID
	return nil
}
func (m *memStore) GetSessionAgentAccount(_ context.Context, id domain.SessionID, _ domain.AgentAccountProvider) (string, bool, error) {
	accountID, ok := m.bindings[id]
	return accountID, ok, nil
}
func (m *memStore) AgentAccountUsageSince(context.Context, time.Time) ([]domain.AgentAccountUsage, error) {
	return nil, nil
}

// fakeClaude reports who each folder is signed in as; "" is the device.
// unshared makes every account folder fail to link.
type fakeClaude struct {
	signedInAs map[string]string
	unshared   bool
}

func (*fakeClaude) Provider() domain.AgentAccountProvider { return domain.AgentAccountProviderClaude }
func (f *fakeClaude) Identity(_ context.Context, home string) (domain.AgentAccountIdentityCheck, error) {
	email, ok := f.signedInAs[home]
	return domain.AgentAccountIdentityCheck{SignedIn: ok, Email: email}, nil
}
func (*fakeClaude) Limits(context.Context, string) (domain.AgentAccountLimits, error) {
	return domain.AgentAccountLimits{}, nil
}
func (*fakeClaude) LoginArgv(context.Context) ([]string, error) {
	return []string{"claude", "auth", "login"}, nil
}
func (*fakeClaude) Logout(context.Context, string) error { return nil }
func (f *fakeClaude) Prepare(string) error {
	if f.unshared {
		return fmt.Errorf("%w: link projects: permission denied", ports.ErrAgentAccountUnshared)
	}
	return nil
}
func (*fakeClaude) LaunchEnv(home string) map[string]string {
	if home == "" {
		return nil
	}
	return map[string]string{"CLAUDE_CONFIG_DIR": home}
}

type fakeTerminals struct{}

func (fakeTerminals) OpenCommandTerminal(context.Context, shellterm.OpenCommandTerminalInput) (shellterm.ShellTerminal, error) {
	return shellterm.ShellTerminal{HandleID: "term-1"}, nil
}
func (fakeTerminals) CloseShellTerminal(context.Context, string) error { return nil }

func TestAccountsRefuseTheWrongLogin(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	claude := &fakeClaude{signedInAs: map[string]string{"": "a@example.com"}}
	store := &memStore{defaults: map[domain.AgentAccountProvider]string{}, bindings: map[domain.SessionID]string{}}
	svc := New(store, []ports.AgentAccountProviderCLI{claude}, root, fakeTerminals{}, nil)

	// Signing in to the device's own login again is refused, not saved twice.
	start, err := svc.StartLogin(ctx, domain.AgentAccountProviderClaude)
	if err != nil {
		t.Fatal(err)
	}
	pending := filepath.Join(root, "claude", start.OperationID)
	if status, _, _ := svc.VerifyLogin(ctx, start.OperationID); status != LoginWaiting {
		t.Fatalf("status before sign-in = %q", status)
	}
	claude.signedInAs[pending] = "A@example.com"
	if status, view, _ := svc.VerifyLogin(ctx, start.OperationID); status != LoginDuplicate || view == nil || !view.Account.Device() {
		t.Fatalf("duplicate status = %q view=%+v", status, view)
	}
	if len(store.accounts) != 0 {
		t.Fatalf("duplicate saved: %+v", store.accounts)
	}

	// A new login is saved, and new sessions use the default until one is picked.
	start, _ = svc.StartLogin(ctx, domain.AgentAccountProviderClaude)
	b := filepath.Join(root, "claude", start.OperationID)
	claude.signedInAs[b] = "b@example.com"
	if status, view, err := svc.VerifyLogin(ctx, start.OperationID); err != nil || status != LoginComplete || view == nil || view.Account.Email != "b@example.com" {
		t.Fatalf("complete status = %q view=%+v err=%v", status, view, err)
	}
	if err := svc.BindSession(ctx, "s1", domain.HarnessClaudeCode, ""); err != nil || store.bindings["s1"] != "device-claude" {
		t.Fatalf("default binding = %q err=%v", store.bindings["s1"], err)
	}
	env, _ := svc.SessionLaunchEnv(ctx, "s2")
	if len(env) != 0 || store.bindings["s2"] != "device-claude" {
		t.Fatalf("unbound session env = %v binding=%q", env, store.bindings["s2"])
	}
	if err := svc.BindSession(ctx, "s2", domain.HarnessClaudeCode, start.OperationID); err != nil {
		t.Fatal(err)
	}
	if env, _ := svc.SessionLaunchEnv(ctx, "s2"); env["CLAUDE_CONFIG_DIR"] != b {
		t.Fatalf("moved session env = %v", env)
	}

	// The folder now holds someone else's login: picking it is refused.
	claude.signedInAs[b] = "c@example.com"
	if _, err := svc.List(ctx, true); err != nil {
		t.Fatal(err)
	}
	if err := svc.BindSession(ctx, "s3", domain.HarnessClaudeCode, start.OperationID); !errors.Is(err, ports.ErrAgentAccountSignedOut) {
		t.Fatalf("bind to a folder signed in as someone else: %v", err)
	}
	if err := svc.BindSession(ctx, "s3", domain.HarnessCodex, start.OperationID); !errors.Is(err, ports.ErrAgentAccountProviderMismatch) {
		t.Fatalf("bind a Codex session to a Claude account: %v", err)
	}
}

// TestAccountsRefuseAFolderThatCannotLink pins that an account whose folder
// cannot link to the computer's history is never signed in, bound or moved to:
// a session there would keep history of its own, and moves could not resume it.
func TestAccountsRefuseAFolderThatCannotLink(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	claude := &fakeClaude{signedInAs: map[string]string{"": "a@example.com"}}
	store := &memStore{defaults: map[domain.AgentAccountProvider]string{}, bindings: map[domain.SessionID]string{}}
	svc := New(store, []ports.AgentAccountProviderCLI{claude}, root, fakeTerminals{}, nil)

	start, err := svc.StartLogin(ctx, domain.AgentAccountProviderClaude)
	if err != nil {
		t.Fatal(err)
	}
	b := filepath.Join(root, "claude", start.OperationID)
	claude.signedInAs[b] = "b@example.com"
	if status, _, err := svc.VerifyLogin(ctx, start.OperationID); err != nil || status != LoginComplete {
		t.Fatalf("sign-in = %q, %v", status, err)
	}
	if err := svc.SetDefault(ctx, start.OperationID); err != nil {
		t.Fatal(err)
	}

	claude.unshared = true
	if _, err := svc.StartLogin(ctx, domain.AgentAccountProviderClaude); !errors.Is(err, ports.ErrAgentAccountUnshared) {
		t.Fatalf("sign-in into a folder that cannot link: %v", err)
	}
	if entries, _ := os.ReadDir(filepath.Join(root, "claude")); len(entries) != 1 {
		t.Fatalf("refused sign-in left its folder: %v", entries)
	}
	if err := svc.BindSession(ctx, "s1", domain.HarnessClaudeCode, ""); !errors.Is(err, ports.ErrAgentAccountUnshared) {
		t.Fatalf("bind to the default account: %v", err)
	}
	if err := svc.ValidateAccount(ctx, domain.HarnessClaudeCode, start.OperationID); !errors.Is(err, ports.ErrAgentAccountUnshared) {
		t.Fatalf("move to the account: %v", err)
	}
	// This computer's own login has no folder to link.
	if err := svc.BindSession(ctx, "s2", domain.HarnessClaudeCode, "device-claude"); err != nil {
		t.Fatalf("bind to this computer: %v", err)
	}
}
