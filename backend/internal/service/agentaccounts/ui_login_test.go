package agentaccounts

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// fakeUILogin records the in-app sign-ins an account starts and cancels.
type fakeUILogin struct {
	mu        sync.Mutex
	started   []fakeUIStart
	cancelled []string
}

type fakeUIStart struct {
	harness domain.AgentHarness
	method  string
	secret  string
	env     map[string]string
}

func (f *fakeUILogin) StartAccountLogin(_ context.Context, harness domain.AgentHarness, method, secret string, env map[string]string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.started = append(f.started, fakeUIStart{harness, method, secret, env})
	return "auth-" + method, nil
}

func (f *fakeUILogin) CancelAccountLogin(_ context.Context, _ domain.AgentHarness, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cancelled = append(f.cancelled, id)
	return nil
}

func newUILoginService(t *testing.T, claude *fakeClaude) (*Service, *memStore, *fakeUILogin) {
	t.Helper()
	store := &memStore{defaults: map[domain.AgentAccountProvider]string{}, bindings: map[domain.SessionID]string{}}
	svc := New(store, []ports.AgentAccountProviderCLI{claude}, t.TempDir(), fakeTerminals{}, nil)
	ui := &fakeUILogin{}
	svc.SetUILogin(ui)
	return svc, store, ui
}

// A method sign-in runs the CLI's own sign-in in the new account's folder in
// the background, and a Console sign-in, which bills per token, is saved as a
// metered account instead of refused as "not a subscription".
func TestUILoginSavesAMeteredConsoleAccount(t *testing.T) {
	ctx := context.Background()
	claude := &fakeClaude{signedInAs: map[string]string{"": "a@example.com"}, apiKey: map[string]bool{}}
	svc, _, ui := newUILoginService(t, claude)

	start, err := svc.StartLogin(ctx, domain.AgentAccountProviderClaude, LoginRequest{Method: "console", Name: "Console"})
	if err != nil {
		t.Fatal(err)
	}
	if start.AuthOperationID != "auth-console" || start.Terminal.HandleID != "" || len(ui.started) != 1 {
		t.Fatalf("start = %+v, ui = %+v", start, ui.started)
	}
	home := ui.started[0].env["CLAUDE_CONFIG_DIR"]
	if ui.started[0].harness != domain.HarnessClaudeCode || home == "" {
		t.Fatalf("sign-in env = %+v", ui.started[0])
	}

	if status, _, err := svc.VerifyLogin(ctx, start.OperationID); err != nil || status != LoginWaiting {
		t.Fatalf("before sign-in: %q, %v", status, err)
	}
	claude.apiKey[home] = true
	claude.signedInAs[home] = "console@example.com"
	status, view, err := svc.VerifyLogin(ctx, start.OperationID)
	if err != nil || status != LoginComplete || view == nil {
		t.Fatalf("verify = %q, %+v, %v", status, view, err)
	}
	// The fake answers every limits read with limits, so none means none was read.
	if view.Account.Kind != domain.AgentAccountKindMetered || !view.Account.PerToken() || view.Account.Name != "Console" || !view.LoggedIn || view.Limits != nil || view.LimitsErr != "" {
		t.Fatalf("view = %+v", view)
	}
}

// A subscription method that signs the folder in to API billing is still
// refused; cancelling stops the background sign-in and removes the folder.
func TestUILoginRefusesASubscriptionMethodSignedInWithAKey(t *testing.T) {
	ctx := context.Background()
	claude := &fakeClaude{signedInAs: map[string]string{}, apiKey: map[string]bool{}}
	svc, store, ui := newUILoginService(t, claude)

	start, err := svc.StartLogin(ctx, domain.AgentAccountProviderClaude, LoginRequest{Method: "browser"})
	if err != nil {
		t.Fatal(err)
	}
	home := ui.started[0].env["CLAUDE_CONFIG_DIR"]
	claude.apiKey[home] = true
	if status, _, err := svc.VerifyLogin(ctx, start.OperationID); err != nil || status != LoginNotSubscription {
		t.Fatalf("verify = %q, %v; want not_subscription", status, err)
	}
	if err := svc.CancelLogin(ctx, start.OperationID); err != nil {
		t.Fatal(err)
	}
	if len(ui.cancelled) != 1 || ui.cancelled[0] != "auth-browser" {
		t.Fatalf("cancelled = %v", ui.cancelled)
	}
	if _, err := os.Stat(home); !errors.Is(err, os.ErrNotExist) || len(store.accounts) != 0 {
		t.Fatalf("a cancelled sign-in left its folder (%v) or an account (%d)", err, len(store.accounts))
	}
	if _, err := svc.StartLogin(ctx, domain.AgentAccountProviderClaude, LoginRequest{Method: "device_code"}); !errors.Is(err, ports.ErrAgentAccountMethodInvalid) {
		t.Fatalf("a Codex method for Claude: err = %v", err)
	}
}

// A setup token is saved as a subscription account at once, but only when
// Claude Code signs in with it; a refused replacement keeps the earlier token.
func TestSetupTokenAccount(t *testing.T) {
	ctx := context.Background()
	claude := &fakeClaude{signedInAs: map[string]string{"": "a@example.com"}}
	svc, store, ui := newUILoginService(t, claude)

	if _, err := svc.StartLogin(ctx, domain.AgentAccountProviderClaude, LoginRequest{Method: "setup_token", Secret: "sk-ant-oat01-bad-token-0000"}); !errors.Is(err, ports.ErrAgentAccountKeyRejected) {
		t.Fatalf("refused token: err = %v", err)
	}
	if len(store.accounts) != 0 {
		t.Fatal("a refused token was saved")
	}
	start, err := svc.StartLogin(ctx, domain.AgentAccountProviderClaude, LoginRequest{Method: "setup_token", Secret: "sk-ant-oat01-good-token-1111", Name: "CI"})
	if err != nil {
		t.Fatal(err)
	}
	if start.Account == nil || start.OperationID != "" || len(ui.started) != 0 {
		t.Fatalf("start = %+v, ui = %v", start, ui.started)
	}
	account := start.Account.Account
	if !account.SetupToken() || account.PerToken() || account.Name != "CI" || account.KeyHint != "1111" || !start.Account.SignedIn {
		t.Fatalf("setup token account = %+v (signed in %v)", account, start.Account.SignedIn)
	}

	if _, err := svc.StartSignInAgain(ctx, account.ID, LoginRequest{Method: "setup_token", Secret: "sk-ant-oat01-bad-token-2222"}); !errors.Is(err, ports.ErrAgentAccountKeyRejected) {
		t.Fatalf("refused replacement: err = %v", err)
	}
	if token, _ := claude.SetupTokenOf(account.Home); token != "sk-ant-oat01-good-token-1111" {
		t.Fatalf("after a refused replacement the token is %q", token)
	}
	if _, err := svc.StartSignInAgain(ctx, account.ID, LoginRequest{Method: "browser"}); !errors.Is(err, ports.ErrAgentAccountKindMismatch) {
		t.Fatalf("browser sign-in of a setup token account: err = %v", err)
	}
	if _, err := svc.StartSignInAgain(ctx, account.ID, LoginRequest{}); !errors.Is(err, ports.ErrAgentAccountKindMismatch) {
		t.Fatalf("terminal sign-in of a setup token account: err = %v", err)
	}
}
