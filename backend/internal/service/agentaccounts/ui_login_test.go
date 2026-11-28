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

// A metered folder now signed in as another email, or to another organization,
// is not the account: it cannot be picked, and is shown not logged in.
func TestMeteredAccountRefusesAnotherLoginOrOrganization(t *testing.T) {
	ctx := context.Background()
	claude := &fakeClaude{signedInAs: map[string]string{"": "a@example.com"}, apiKey: map[string]bool{}, orgOf: map[string]string{}}
	svc, _, ui := newUILoginService(t, claude)
	start, err := svc.StartLogin(ctx, domain.AgentAccountProviderClaude, LoginRequest{Method: "console"})
	if err != nil {
		t.Fatal(err)
	}
	home := ui.started[0].env["CLAUDE_CONFIG_DIR"]
	claude.apiKey[home], claude.signedInAs[home], claude.orgOf[home] = true, "console@example.com", "org-1"
	_, view, err := svc.VerifyLogin(ctx, start.OperationID)
	if err != nil || view == nil {
		t.Fatalf("verify = %+v, %v", view, err)
	}
	id := view.Account.ID
	if _, err := svc.List(ctx, true); err != nil { // records the account's organization
		t.Fatal(err)
	}
	if err := svc.ValidateAccount(ctx, domain.HarnessClaudeCode, id); err != nil {
		t.Fatalf("the account's own login: %v", err)
	}

	claude.orgOf[home] = "org-2"
	if _, err := svc.List(ctx, true); err != nil {
		t.Fatal(err)
	}
	if err := svc.ValidateAccount(ctx, domain.HarnessClaudeCode, id); !errors.Is(err, ports.ErrAgentAccountSignedOut) {
		t.Fatalf("another organization: err = %v, want ErrAgentAccountSignedOut", err)
	}

	claude.orgOf[home], claude.signedInAs[home] = "org-1", "someone@example.com"
	views, err := svc.List(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range views {
		if v.Account.ID == id && (v.LoggedIn || v.IdentityProblem != domain.AgentAccountProblemOtherLogin) {
			t.Fatalf("another login: view = %+v", v)
		}
	}
	if err := svc.ValidateAccount(ctx, domain.HarnessClaudeCode, id); !errors.Is(err, ports.ErrAgentAccountSignedOut) {
		t.Fatalf("another login: err = %v, want ErrAgentAccountSignedOut", err)
	}
}

// An accepted setup token replacement keeps the new token and its hint.
func TestSetupTokenReplacement(t *testing.T) {
	ctx := context.Background()
	claude := &fakeClaude{signedInAs: map[string]string{"": "a@example.com"}}
	svc, store, _ := newUILoginService(t, claude)
	start, err := svc.StartLogin(ctx, domain.AgentAccountProviderClaude, LoginRequest{Method: "setup_token", Secret: "sk-ant-oat01-good-token-1111"})
	if err != nil {
		t.Fatal(err)
	}
	account := start.Account.Account
	replaced, err := svc.StartSignInAgain(ctx, account.ID, LoginRequest{Method: "setup_token", Secret: "sk-ant-oat01-good-token-3333"})
	if err != nil {
		t.Fatal(err)
	}
	if replaced.Account == nil || replaced.Account.Account.KeyHint != "3333" || store.accounts[0].KeyHint != "3333" {
		t.Fatalf("replaced = %+v, stored hint %q", replaced.Account, store.accounts[0].KeyHint)
	}
	if token, _ := claude.SetupTokenOf(account.Home); token != "sk-ant-oat01-good-token-3333" {
		t.Fatalf("token after replace = %q", token)
	}
}

// Cancelling a saved account's sign-in again stops its background sign-in
// even though its folder stays, so a late approval cannot write into it. A
// metered account cannot sign in again in a terminal: that sign-in can pick a
// subscription, and the account would keep its metered kind.
func TestCancelSignInAgainStopsTheBackgroundSignIn(t *testing.T) {
	ctx := context.Background()
	claude := &fakeClaude{signedInAs: map[string]string{"": "a@example.com"}, apiKey: map[string]bool{}}
	svc, _, ui := newUILoginService(t, claude)
	start, err := svc.StartLogin(ctx, domain.AgentAccountProviderClaude, LoginRequest{Method: "console"})
	if err != nil {
		t.Fatal(err)
	}
	home := ui.started[0].env["CLAUDE_CONFIG_DIR"]
	claude.apiKey[home], claude.signedInAs[home] = true, "console@example.com"
	_, view, err := svc.VerifyLogin(ctx, start.OperationID)
	if err != nil || view == nil {
		t.Fatalf("verify = %+v, %v", view, err)
	}

	if _, err := svc.StartSignInAgain(ctx, view.Account.ID, LoginRequest{}); !errors.Is(err, ports.ErrAgentAccountKindMismatch) {
		t.Fatalf("terminal sign-in again of a metered account: err = %v", err)
	}
	again, err := svc.StartSignInAgain(ctx, view.Account.ID, LoginRequest{Method: "console"})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.CancelLogin(ctx, again.OperationID); err != nil {
		t.Fatal(err)
	}
	if len(ui.cancelled) != 1 || ui.cancelled[0] != again.AuthOperationID {
		t.Fatalf("cancelled = %v, want [%s]", ui.cancelled, again.AuthOperationID)
	}
	if _, err := os.Stat(home); err != nil {
		t.Fatalf("cancelling a sign-in again removed the saved folder: %v", err)
	}
}

// A Codex API key is checked with OpenAI before its sign-in starts.
func TestCodexAPIKeyIsCheckedBeforeSignIn(t *testing.T) {
	ctx := context.Background()
	codex := &fakeCodex{fakeClaude: fakeClaude{signedInAs: map[string]string{}}}
	store := &memStore{defaults: map[domain.AgentAccountProvider]string{}, bindings: map[domain.SessionID]string{}}
	svc := New(store, []ports.AgentAccountProviderCLI{codex}, t.TempDir(), fakeTerminals{}, nil)
	ui := &fakeUILogin{}
	svc.SetUILogin(ui)
	var checked []string
	svc.SetKeyCheck(func(_ context.Context, p domain.AgentAccountProvider, key string) error {
		checked = append(checked, string(p)+":"+key)
		if key == "sk-bad" {
			return ports.ErrAgentAccountKeyRejected
		}
		return nil
	})

	if _, err := svc.StartLogin(ctx, domain.AgentAccountProviderCodex, LoginRequest{Method: "api_key", Secret: " sk-bad "}); !errors.Is(err, ports.ErrAgentAccountKeyRejected) {
		t.Fatalf("a rejected key: err = %v", err)
	}
	if len(ui.started) != 0 {
		t.Fatalf("a rejected key started a sign-in: %+v", ui.started)
	}
	if _, err := svc.StartLogin(ctx, domain.AgentAccountProviderCodex, LoginRequest{Method: "api_key", Secret: "sk-good"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.StartLogin(ctx, domain.AgentAccountProviderCodex, LoginRequest{Method: "browser"}); err != nil {
		t.Fatal(err)
	}
	if len(checked) != 2 || checked[0] != "codex:sk-bad" || checked[1] != "codex:sk-good" || len(ui.started) != 2 {
		t.Fatalf("checked = %v, started = %d", checked, len(ui.started))
	}
}
