package agentaccounts

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
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
	onIdentity func(home string) // runs as a folder's identity is read
	apiKey     map[string]bool   // folders logged in with an API key
	failing    map[string]bool   // folders whose check fails (a timeout)
	windows    []domain.AgentAccountLimitWindow
	limitsErr  error
	orgOf      map[string]string
	orgNameOf  map[string]string
}

func (*fakeClaude) Provider() domain.AgentAccountProvider { return domain.AgentAccountProviderClaude }
func (f *fakeClaude) Identity(_ context.Context, home string) (domain.AgentAccountIdentityCheck, error) {
	if hook := f.onIdentity; hook != nil {
		f.onIdentity = nil
		hook(home)
	}
	if f.failing[home] {
		return domain.AgentAccountIdentityCheck{}, errors.New("account/read: context deadline exceeded")
	}
	email, ok := f.signedInAs[home]
	if f.apiKey[home] {
		return domain.AgentAccountIdentityCheck{LoggedIn: true}, nil
	}
	return domain.AgentAccountIdentityCheck{SignedIn: ok, LoggedIn: ok, Email: email, OrgID: f.orgOf[home], OrgName: f.orgNameOf[home]}, nil
}
func (f *fakeClaude) Limits(context.Context, string) (domain.AgentAccountLimits, error) {
	if f.limitsErr != nil {
		return domain.AgentAccountLimits{}, f.limitsErr
	}
	return domain.AgentAccountLimits{Windows: f.windows}, nil
}
func (*fakeClaude) LoginArgv(context.Context) ([]string, error) {
	return []string{"claude", "auth", "login"}, nil
}
func (f *fakeClaude) Logout(_ context.Context, home string) error {
	delete(f.signedInAs, home)
	return nil
}
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

// A signed-out account signs in again in its own folder, so its sessions keep
// it; only the login it was saved with is kept there.
func TestSignInAgainKeepsTheAccountAndItsLogin(t *testing.T) {
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
	accountID := start.OperationID
	delete(claude.signedInAs, b)
	if _, err := svc.List(ctx, true); err != nil {
		t.Fatal(err)
	}

	if _, err := svc.StartSignInAgain(ctx, "device-claude"); !errors.Is(err, ports.ErrAgentAccountDeviceFixed) {
		t.Fatalf("sign in again to this computer: %v", err)
	}
	// Cancelled: the saved folder stays.
	again, err := svc.StartSignInAgain(ctx, accountID)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.CancelLogin(ctx, again.OperationID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(b); err != nil {
		t.Fatalf("cancel removed the saved folder: %v", err)
	}
	// Another login in the folder is refused and signed out again.
	again, _ = svc.StartSignInAgain(ctx, accountID)
	claude.signedInAs[b] = "c@example.com"
	if status, _, err := svc.VerifyLogin(ctx, again.OperationID); err != nil || status != LoginMismatch {
		t.Fatalf("other login = %q, %v", status, err)
	}
	if _, signedIn := claude.signedInAs[b]; signedIn {
		t.Fatal("the other login stayed in the account's folder")
	}
	// The same login comes back on the same account.
	again, _ = svc.StartSignInAgain(ctx, accountID)
	claude.signedInAs[b] = "B@example.com"
	status, view, err := svc.VerifyLogin(ctx, again.OperationID)
	if err != nil || status != LoginComplete || view == nil || view.Account.ID != accountID || !view.SignedIn {
		t.Fatalf("same login = %q view=%+v err=%v", status, view, err)
	}
	if len(store.accounts) != 1 {
		t.Fatalf("accounts = %+v, want the one saved account", store.accounts)
	}
	if _, err := os.Stat(b); err != nil {
		t.Fatalf("the saved folder is gone: %v", err)
	}
}

// Removing an account stops its agents first; one that cannot be stopped keeps
// the account, signed in.
func TestRemoveStopsTheAccountsAgentsFirst(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	claude := &fakeClaude{signedInAs: map[string]string{"": "a@example.com"}}
	store := &memStore{defaults: map[domain.AgentAccountProvider]string{}, bindings: map[domain.SessionID]string{}}
	svc := New(store, []ports.AgentAccountProviderCLI{claude}, root, fakeTerminals{}, nil)
	start, _ := svc.StartLogin(ctx, domain.AgentAccountProviderClaude)
	b := filepath.Join(root, "claude", start.OperationID)
	claude.signedInAs[b] = "b@example.com"
	if status, _, err := svc.VerifyLogin(ctx, start.OperationID); err != nil || status != LoginComplete {
		t.Fatalf("sign-in = %q, %v", status, err)
	}
	var stopped []string
	stopErr := errors.New("exit agent s1: switch in progress")
	svc.SetAgentStopper(func(_ context.Context, accountID string) error {
		stopped = append(stopped, accountID)
		return stopErr
	})

	if err := svc.Remove(ctx, "device-claude"); !errors.Is(err, ports.ErrAgentAccountDeviceFixed) || len(stopped) != 0 {
		t.Fatalf("remove this computer: %v, stopped %v", err, stopped)
	}
	if err := svc.Remove(ctx, start.OperationID); !errors.Is(err, ports.ErrAgentAccountInUse) {
		t.Fatalf("remove with an agent that cannot stop: %v", err)
	}
	if _, signedIn := claude.signedInAs[b]; !signedIn {
		t.Fatal("a refused removal signed the account out")
	}
	stopErr = nil
	if err := svc.Remove(ctx, start.OperationID); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(stopped) != fmt.Sprintf("[%s %s]", start.OperationID, start.OperationID) {
		t.Fatalf("stopped = %v", stopped)
	}
	if _, signedIn := claude.signedInAs[b]; signedIn {
		t.Fatal("the removed account stayed signed in")
	}
}

// A sign-in cancelled while it is being checked is not saved: the cancel has
// already signed the folder out and removed it.
func TestCancelDuringVerifyDoesNotSaveTheAccount(t *testing.T) {
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
	claude.onIdentity = func(string) {
		if err := svc.CancelLogin(ctx, start.OperationID); err != nil {
			t.Errorf("cancel: %v", err)
		}
		claude.signedInAs[b] = "b@example.com" // the CLI's answer was already on its way
	}

	if _, _, err := svc.VerifyLogin(ctx, start.OperationID); !errors.Is(err, ports.ErrAgentAccountLoginUnknown) {
		t.Fatalf("verify of a cancelled sign-in: %v, want ErrAgentAccountLoginUnknown", err)
	}
	if len(store.accounts) != 0 {
		t.Fatalf("cancelled sign-in saved: %+v", store.accounts)
	}
}

// While an account is being removed no launch resolves to it: a session that
// starts in that window runs on the default, and a pick is refused. The
// sessions still on it stay visible, so its agents can be found and stopped.
func TestRemovingAnAccountKeepsNewLaunchesOffIt(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	claude := &fakeClaude{signedInAs: map[string]string{"": "a@example.com"}}
	store := &memStore{defaults: map[domain.AgentAccountProvider]string{}, bindings: map[domain.SessionID]string{}}
	svc := New(store, []ports.AgentAccountProviderCLI{claude}, root, fakeTerminals{}, nil)
	start, _ := svc.StartLogin(ctx, domain.AgentAccountProviderClaude)
	b := filepath.Join(root, "claude", start.OperationID)
	claude.signedInAs[b] = "b@example.com"
	if status, _, err := svc.VerifyLogin(ctx, start.OperationID); err != nil || status != LoginComplete {
		t.Fatalf("sign-in = %q, %v", status, err)
	}
	accountID := start.OperationID
	store.bindings["running"] = accountID
	store.bindings["launching"] = accountID
	if err := svc.SetDefault(ctx, accountID); err != nil {
		t.Fatal(err)
	}

	refuse := errors.New("an agent would not stop")
	svc.SetAgentStopper(func(ctx context.Context, id string) error {
		if got, _ := svc.SessionAccountID(ctx, "running", domain.HarnessClaudeCode); got != accountID {
			t.Errorf("session on the account reads as %q while it is removed", got)
		}
		env, _ := svc.SessionLaunchEnv(ctx, "launching")
		if env["CLAUDE_CONFIG_DIR"] != "" || store.bindings["launching"] != "device-claude" {
			t.Errorf("a launch during removal got %v bound to %q, want this computer", env, store.bindings["launching"])
		}
		if err := svc.BindSession(ctx, "new", domain.HarnessClaudeCode, ""); err != nil || store.bindings["new"] != "device-claude" {
			t.Errorf("a new session during removal bound to %q (%v), want this computer", store.bindings["new"], err)
		}
		if err := svc.ValidateAccount(ctx, domain.HarnessClaudeCode, accountID); !errors.Is(err, ports.ErrAgentAccountUnknown) {
			t.Errorf("picking the account during removal: %v", err)
		}
		return refuse
	})
	if err := svc.Remove(ctx, accountID); !errors.Is(err, ports.ErrAgentAccountInUse) {
		t.Fatalf("remove: %v", err)
	}
	// The refused removal leaves the account usable.
	if err := svc.ValidateAccount(ctx, domain.HarnessClaudeCode, accountID); err != nil {
		t.Fatalf("account after a refused removal: %v", err)
	}
}

// A new task on a default account that is known to be signed out, or now holds
// another login, is refused rather than started there or quietly elsewhere.
// Not knowing (no reading yet) is not a reason to refuse.
// A new task on a default that is known to be signed out, or to hold another
// login, starts on another account that is signed in, and is refused only when
// there is none.
func TestNewTaskOnAKnownBadDefaultStartsOnAnotherAccount(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	claude := &fakeClaude{signedInAs: map[string]string{"": "a@example.com"}}
	store := &memStore{defaults: map[domain.AgentAccountProvider]string{}, bindings: map[domain.SessionID]string{}}
	svc := New(store, []ports.AgentAccountProviderCLI{claude}, root, fakeTerminals{}, nil)
	start, _ := svc.StartLogin(ctx, domain.AgentAccountProviderClaude)
	b := filepath.Join(root, "claude", start.OperationID)
	claude.signedInAs[b] = "b@example.com"
	if status, _, err := svc.VerifyLogin(ctx, start.OperationID); err != nil || status != LoginComplete {
		t.Fatalf("sign-in = %q, %v", status, err)
	}
	if err := svc.SetDefault(ctx, start.OperationID); err != nil {
		t.Fatal(err)
	}
	if err := svc.BindSession(ctx, "ok", domain.HarnessClaudeCode, ""); err != nil || store.bindings["ok"] != start.OperationID {
		t.Fatalf("bind on a good default: %q %v", store.bindings["ok"], err)
	}

	for _, tc := range []struct {
		name  string
		login string // "" signs the folder out
	}{{"signed out", ""}, {"another login", "c@example.com"}} {
		t.Run(tc.name, func(t *testing.T) {
			delete(claude.signedInAs, b)
			if tc.login != "" {
				claude.signedInAs[b] = tc.login
			}
			if _, err := svc.List(ctx, true); err != nil {
				t.Fatal(err)
			}
			id := domain.SessionID("bad-" + tc.name)
			if err := svc.BindSession(ctx, id, domain.HarnessClaudeCode, ""); err != nil || store.bindings[id] != "device-claude" {
				t.Fatalf("bind on a bad default: %q %v", store.bindings[id], err)
			}
			if !svc.RunsOnComputerLogin(ctx, domain.HarnessClaudeCode, "") {
				t.Fatal("a new task on a bad default does not run on the computer's login it starts on")
			}
		})
	}
	// The computer's own login as the default follows the same rule.
	if err := svc.SetDefault(ctx, "device-claude"); err != nil {
		t.Fatal(err)
	}
	delete(claude.signedInAs, "")
	claude.signedInAs[b] = "b@example.com"
	if _, err := svc.List(ctx, true); err != nil {
		t.Fatal(err)
	}
	if err := svc.BindSession(ctx, "device-out", domain.HarnessClaudeCode, ""); err != nil || store.bindings["device-out"] != start.OperationID {
		t.Fatalf("bind on a signed-out CLI login default: %q %v", store.bindings["device-out"], err)
	}
	// With no account signed in, the task is refused rather than started signed out.
	delete(claude.signedInAs, b)
	if _, err := svc.List(ctx, true); err != nil {
		t.Fatal(err)
	}
	if err := svc.BindSession(ctx, "none", domain.HarnessClaudeCode, ""); !errors.Is(err, ports.ErrAgentAccountDefaultUnusable) {
		t.Fatalf("bind with every account signed out: %v", err)
	}
}

// A signed-out reading of the default that is older than the cache window is
// read again first: the user may have signed it in outside AO since.
func TestAnOldSignedOutDefaultIsReadAgainBeforeMovingOn(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	claude := &fakeClaude{signedInAs: map[string]string{"": "a@example.com"}}
	store := &memStore{defaults: map[domain.AgentAccountProvider]string{}, bindings: map[domain.SessionID]string{}}
	svc := New(store, []ports.AgentAccountProviderCLI{claude}, root, fakeTerminals{}, nil)
	now := time.Now()
	svc.now = func() time.Time { return now }
	start, _ := svc.StartLogin(ctx, domain.AgentAccountProviderClaude)
	b := filepath.Join(root, "claude", start.OperationID)
	claude.signedInAs[b] = "b@example.com"
	if _, _, err := svc.VerifyLogin(ctx, start.OperationID); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetDefault(ctx, start.OperationID); err != nil {
		t.Fatal(err)
	}
	delete(claude.signedInAs, b)
	if _, err := svc.List(ctx, true); err != nil {
		t.Fatal(err)
	}
	claude.signedInAs[b] = "b@example.com"
	now = now.Add(freshFor + time.Minute)
	if err := svc.BindSession(ctx, "s1", domain.HarnessClaudeCode, ""); err != nil || store.bindings["s1"] != start.OperationID {
		t.Fatalf("bind after the default was signed in again: %q %v", store.bindings["s1"], err)
	}
}

// When a session's agent is refused for its credentials, its account is read
// again at once: it shows as signed out, and new tasks stop starting on it.
func TestRecheckSessionAccountSeesALoginThatDied(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	claude := &fakeClaude{signedInAs: map[string]string{"": "a@example.com"}}
	store := &memStore{defaults: map[domain.AgentAccountProvider]string{}, bindings: map[domain.SessionID]string{}}
	svc := New(store, []ports.AgentAccountProviderCLI{claude}, root, fakeTerminals{}, nil)
	start, _ := svc.StartLogin(ctx, domain.AgentAccountProviderClaude)
	b := filepath.Join(root, "claude", start.OperationID)
	claude.signedInAs[b] = "b@example.com"
	if _, _, err := svc.VerifyLogin(ctx, start.OperationID); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetDefault(ctx, start.OperationID); err != nil {
		t.Fatal(err)
	}
	if err := svc.BindSession(ctx, "s1", domain.HarnessClaudeCode, ""); err != nil {
		t.Fatal(err)
	}
	// Claude clears a folder's login when its refresh token is refused.
	delete(claude.signedInAs, b)
	svc.RecheckSessionAccount(ctx, "s1", domain.HarnessClaudeCode)
	views, err := svc.List(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, view := range views {
		if view.Account.ID == start.OperationID && view.SignedIn {
			t.Fatal("the account still reads as signed in after its session was refused")
		}
	}
	if err := svc.BindSession(ctx, "s2", domain.HarnessClaudeCode, ""); err != nil || store.bindings["s2"] != "device-claude" {
		t.Fatalf("new task after the default's login died: %q %v", store.bindings["s2"], err)
	}
	// A harness without accounts has nothing to read.
	svc.RecheckSessionAccount(ctx, "s1", domain.HarnessGemini)
}

func TestRunsOnComputerLoginFollowsTheDefaultAndThePick(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	claude := &fakeClaude{signedInAs: map[string]string{"": "a@example.com"}}
	store := &memStore{defaults: map[domain.AgentAccountProvider]string{}, bindings: map[domain.SessionID]string{}}
	svc := New(store, []ports.AgentAccountProviderCLI{claude}, root, fakeTerminals{}, nil)
	start, _ := svc.StartLogin(ctx, domain.AgentAccountProviderClaude)
	claude.signedInAs[filepath.Join(root, "claude", start.OperationID)] = "b@example.com"
	if _, _, err := svc.VerifyLogin(ctx, start.OperationID); err != nil {
		t.Fatal(err)
	}
	b := start.OperationID
	check := func(accountID string, want bool) {
		t.Helper()
		if got := svc.RunsOnComputerLogin(ctx, domain.HarnessClaudeCode, accountID); got != want {
			t.Fatalf("RunsOnComputerLogin(%q) = %v, want %v", accountID, got, want)
		}
	}
	check("", true)
	check(b, false)
	if err := svc.SetDefault(ctx, b); err != nil {
		t.Fatal(err)
	}
	check("", false)
	check("device-claude", true)
	if !svc.RunsOnComputerLogin(ctx, domain.HarnessGemini, "") {
		t.Fatal("a harness without accounts runs on the computer's login")
	}
}

// A CLI login that uses an API key is not a subscription, so it cannot be an
// account to switch to, but the CLI launches with it: as the default it must
// not refuse new tasks.
func TestAnAPIKeyCLILoginStillLaunchesAsTheDefault(t *testing.T) {
	ctx := context.Background()
	claude := &fakeClaude{signedInAs: map[string]string{}, apiKey: map[string]bool{"": true}}
	store := &memStore{defaults: map[domain.AgentAccountProvider]string{}, bindings: map[domain.SessionID]string{}}
	svc := New(store, []ports.AgentAccountProviderCLI{claude}, t.TempDir(), fakeTerminals{}, nil)
	views, err := svc.List(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	// The app shows it as a login, not as signed out.
	if len(views) != 1 || views[0].SignedIn || !views[0].LoggedIn {
		t.Fatalf("API-key CLI login view = %+v, want logged in without a subscription", views)
	}
	if err := svc.BindSession(ctx, "s1", domain.HarnessClaudeCode, ""); err != nil || store.bindings["s1"] != "device-claude" {
		t.Fatalf("new task on an API-key CLI login: %q %v", store.bindings["s1"], err)
	}
}

// Agent readiness asks whether the saved account new sessions start on is
// signed in. It has no say while they start on the computer's own login, and
// the default changing tells readiness to look again.
func TestDefaultSignInAnswersOnlyForASavedDefault(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	claude := &fakeClaude{signedInAs: map[string]string{"": "a@example.com"}}
	store := &memStore{defaults: map[domain.AgentAccountProvider]string{}, bindings: map[domain.SessionID]string{}}
	svc := New(store, []ports.AgentAccountProviderCLI{claude}, root, fakeTerminals{}, nil)
	var changed []domain.AgentAccountProvider
	svc.SetDefaultChanged(func(p domain.AgentAccountProvider) { changed = append(changed, p) })
	if _, handled := svc.DefaultSignIn(ctx, domain.HarnessClaudeCode); handled {
		t.Fatal("answered for the computer's own login")
	}
	start, _ := svc.StartLogin(ctx, domain.AgentAccountProviderClaude)
	b := filepath.Join(root, "claude", start.OperationID)
	claude.signedInAs[b] = "b@example.com"
	if _, _, err := svc.VerifyLogin(ctx, start.OperationID); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetDefault(ctx, start.OperationID); err != nil {
		t.Fatal(err)
	}
	if identity, handled := svc.DefaultSignIn(ctx, domain.HarnessClaudeCode); !handled || !identity.SignedIn || identity.Email != "b@example.com" {
		t.Fatalf("saved default = %+v handled %v, want b@example.com signed in", identity, handled)
	}
	// A signed-out default sends new sessions to the computer's own login, whose
	// readiness the CLI reports itself.
	delete(claude.signedInAs, b)
	if _, err := svc.List(ctx, true); err != nil {
		t.Fatal(err)
	}
	if _, handled := svc.DefaultSignIn(ctx, domain.HarnessClaudeCode); handled {
		t.Fatal("answered for a signed-out default whose sessions start on the computer's login")
	}
	delete(claude.signedInAs, "")
	if _, err := svc.List(ctx, true); err != nil {
		t.Fatal(err)
	}
	if identity, handled := svc.DefaultSignIn(ctx, domain.HarnessClaudeCode); !handled || identity.SignedIn || identity.Email != "b@example.com" {
		t.Fatalf("every account signed out = %+v handled %v", identity, handled)
	}
	if len(changed) < 2 {
		t.Fatalf("readiness was told %v, want at least the sign-in and the new default", changed)
	}
}

// With the default signed out and another saved account signed in, readiness
// describes that account, which new sessions start on.
func TestDefaultSignInFollowsTheAccountNewSessionsStartOn(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	claude := &fakeClaude{signedInAs: map[string]string{}}
	store := &memStore{defaults: map[domain.AgentAccountProvider]string{}, bindings: map[domain.SessionID]string{}}
	svc := New(store, []ports.AgentAccountProviderCLI{claude}, root, fakeTerminals{}, nil)
	add := func(email string) string {
		t.Helper()
		start, _ := svc.StartLogin(ctx, domain.AgentAccountProviderClaude)
		claude.signedInAs[filepath.Join(root, "claude", start.OperationID)] = email
		if _, _, err := svc.VerifyLogin(ctx, start.OperationID); err != nil {
			t.Fatal(err)
		}
		return start.OperationID
	}
	b, c := add("b@example.com"), add("c@example.com")
	if err := svc.SetDefault(ctx, b); err != nil {
		t.Fatal(err)
	}
	delete(claude.signedInAs, filepath.Join(root, "claude", b))
	if _, err := svc.List(ctx, true); err != nil {
		t.Fatal(err)
	}
	if identity, handled := svc.DefaultSignIn(ctx, domain.HarnessClaudeCode); !handled || !identity.SignedIn || identity.Email != "c@example.com" {
		t.Fatalf("readiness on a signed-out default = %+v handled %v, want c@example.com signed in", identity, handled)
	}
	if err := svc.BindSession(ctx, "s1", domain.HarnessClaudeCode, ""); err != nil || store.bindings["s1"] != c {
		t.Fatalf("new task = %q %v, want %q", store.bindings["s1"], err, c)
	}
}

// The data directory may be reached through a symlink now, though the account
// was made under its real path. Removal still deletes the folder, and still
// refuses anything outside the account root.
func TestRemoveDeletesTheFolderThroughASymlinkedRoot(t *testing.T) {
	physical := t.TempDir()
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(physical, alias); err != nil {
		t.Skip("no symlinks here")
	}
	folder := filepath.Join(physical, "claude", "acct-b")
	if err := os.MkdirAll(folder, 0o700); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	svc := New(&memStore{defaults: map[domain.AgentAccountProvider]string{}, bindings: map[domain.SessionID]string{}}, nil, alias, fakeTerminals{}, nil)
	svc.removeFolder(outside)
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("removed a folder outside the root: %v", err)
	}
	svc.removeFolder(folder)
	if _, err := os.Stat(folder); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("account folder kept after removal through the alias: %v", err)
	}
}

// A check that fails (a timeout under load) says nothing about the login: the
// account keeps reading as signed in, with the failure shown as its problem.
func TestAFailedCheckDoesNotReadAsSignedOut(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	claude := &fakeClaude{signedInAs: map[string]string{"": "a@example.com"}, failing: map[string]bool{}}
	store := &memStore{defaults: map[domain.AgentAccountProvider]string{}, bindings: map[domain.SessionID]string{}}
	svc := New(store, []ports.AgentAccountProviderCLI{claude}, root, fakeTerminals{}, nil)
	start, _ := svc.StartLogin(ctx, domain.AgentAccountProviderClaude)
	b := filepath.Join(root, "claude", start.OperationID)
	claude.signedInAs[b] = "b@example.com"
	if _, _, err := svc.VerifyLogin(ctx, start.OperationID); err != nil {
		t.Fatal(err)
	}
	claude.failing[b] = true
	views, err := svc.List(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, view := range views {
		if view.Account.ID == start.OperationID && (!view.SignedIn || view.IdentityErr == "") {
			t.Fatalf("after a failed check: signed in %v, problem %q", view.SignedIn, view.IdentityErr)
		}
	}
}

func TestLimitsWithNoWindowsAreReadAgainSoon(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	claude := &fakeClaude{signedInAs: map[string]string{"": "a@example.com"}}
	store := &memStore{defaults: map[domain.AgentAccountProvider]string{}, bindings: map[domain.SessionID]string{}}
	svc := New(store, []ports.AgentAccountProviderCLI{claude}, root, fakeTerminals{}, nil)
	now := time.Now()
	svc.now = func() time.Time { return now }
	start, _ := svc.StartLogin(ctx, domain.AgentAccountProviderClaude)
	claude.signedInAs[filepath.Join(root, "claude", start.OperationID)] = "b@example.com"
	// The read right after sign-in answers before the limits are known.
	if _, _, err := svc.VerifyLogin(ctx, start.OperationID); err != nil {
		t.Fatal(err)
	}
	claude.windows = []domain.AgentAccountLimitWindow{{ID: "five_hour", UsedPercent: 1}}
	now = now.Add(time.Minute) // the next poll, well inside the cache window
	views, err := svc.List(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, view := range views {
		if view.Account.ID == start.OperationID && (view.Limits == nil || len(view.Limits.Windows) != 1) {
			t.Fatalf("limits a minute after an empty answer: %+v", view.Limits)
		}
	}
	// An answer with windows is kept for the whole cache window.
	claude.windows = nil
	now = now.Add(time.Minute)
	views, _ = svc.List(ctx, false)
	for _, view := range views {
		if view.Account.ID == start.OperationID && (view.Limits == nil || len(view.Limits.Windows) != 1) {
			t.Fatalf("limits with windows were not kept: %+v", view.Limits)
		}
	}
}

func TestAReadingThatReachesTheProviderConfirmsTheLogin(t *testing.T) {
	ctx := context.Background()
	claude := &fakeClaude{signedInAs: map[string]string{}, failing: map[string]bool{}}
	svc := New(&memStore{defaults: map[domain.AgentAccountProvider]string{}, bindings: map[domain.SessionID]string{}},
		[]ports.AgentAccountProviderCLI{claude}, t.TempDir(), fakeTerminals{}, nil)
	at := time.Date(2026, 11, 1, 5, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return at }
	var confirmed []string
	svc.SetLoginConfirmed(func(account domain.AgentAccount, checkedAt time.Time) {
		if !checkedAt.Equal(at) {
			t.Errorf("confirmed at %v, want the reading's start %v", checkedAt, at)
		}
		confirmed = append(confirmed, account.ID)
	})
	read := func() {
		if _, err := svc.List(ctx, true); err != nil {
			t.Fatal(err)
		}
	}
	read() // signed out
	claude.failing[""] = true
	read() // the check fails
	if len(confirmed) != 0 {
		t.Fatalf("confirmed without a working login: %v", confirmed)
	}
	// Signed in with the CLI's own login, outside AO.
	claude.failing[""] = false
	claude.signedInAs[""] = "a@example.com"
	read()
	if len(confirmed) != 1 || confirmed[0] != domain.DeviceAgentAccountID(domain.AgentAccountProviderClaude) {
		t.Fatalf("confirmed = %v", confirmed)
	}
}

func TestFailedLimitsRefreshKeepsTheLastReading(t *testing.T) {
	ctx := context.Background()
	claude := &fakeClaude{
		signedInAs: map[string]string{"": "a@example.com"},
		windows:    []domain.AgentAccountLimitWindow{{ID: "five_hour", UsedPercent: 40}},
	}
	store := &memStore{defaults: map[domain.AgentAccountProvider]string{}, bindings: map[domain.SessionID]string{}}
	svc := New(store, []ports.AgentAccountProviderCLI{claude}, t.TempDir(), fakeTerminals{}, nil)
	device := func() AccountView {
		t.Helper()
		views, err := svc.List(ctx, true)
		if err != nil || len(views) == 0 {
			t.Fatalf("List = %v, %v", views, err)
		}
		return views[0]
	}
	device()

	claude.limitsErr = errors.New("claude could not fetch its usage limits")
	if view := device(); view.LimitsErr == "" || view.Limits == nil || len(view.Limits.Windows) != 1 || view.Limits.Windows[0].UsedPercent != 40 {
		t.Fatalf("failed refresh = %+v, want the last windows and the error", view)
	}

	claude.signedInAs[""] = "b@example.com"
	if view := device(); view.Limits != nil {
		t.Fatalf("another login kept the last login's limits: %+v", view.Limits)
	}
}

func TestProblemKindSortsFailuresByWhatFailed(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want domain.AgentAccountProblemKind
	}{
		{fmt.Errorf("claude get_usage: %w", context.DeadlineExceeded), domain.AgentAccountProblemSlow},
		{errors.New("account/read: context deadline exceeded"), domain.AgentAccountProblemSlow},
		{fmt.Errorf("resolve claude: %w", ports.ErrAgentBinaryNotFound), domain.AgentAccountProblemCLIMissing},
		{&exec.Error{Name: "claude", Err: exec.ErrNotFound}, domain.AgentAccountProblemCLIMissing},
		{errors.New("claude get_usage: 429 Too Many Requests"), domain.AgentAccountProblemRateLimited},
		{errors.New("claude get_usage: rate_limit_error"), domain.AgentAccountProblemRateLimited},
		{errors.New("claude ended without answering get_usage"), domain.AgentAccountProblemUnavailable},
		{errors.New("claude get_usage: API error: status 429"), domain.AgentAccountProblemRateLimited},
		{errors.New("claude get_usage: HTTP 429"), domain.AgentAccountProblemRateLimited},
		// Digits in a port are not a status.
		{errors.New("dial tcp 127.0.0.1:42901: connect: connection refused"), domain.AgentAccountProblemUnavailable},
		{errors.New("dial tcp 127.0.0.1:429: connect: connection refused"), domain.AgentAccountProblemUnavailable},
	} {
		if got := problemKind(tc.err); got != tc.want {
			t.Errorf("problemKind(%q) = %q, want %q", tc.err, got, tc.want)
		}
	}
}

func TestASlowCheckIsTriedAgainSoonAndARateLimitIsNot(t *testing.T) {
	ctx := context.Background()
	claude := &fakeClaude{
		signedInAs: map[string]string{"": "a@example.com"},
		windows:    []domain.AgentAccountLimitWindow{{ID: "five_hour", UsedPercent: 40}},
	}
	svc := New(&memStore{defaults: map[domain.AgentAccountProvider]string{}, bindings: map[domain.SessionID]string{}},
		[]ports.AgentAccountProviderCLI{claude}, t.TempDir(), fakeTerminals{}, nil)
	now := time.Now()
	svc.now = func() time.Time { return now }
	device := func(refresh bool) AccountView {
		t.Helper()
		views, err := svc.List(ctx, refresh)
		if err != nil || len(views) == 0 {
			t.Fatalf("List = %v, %v", views, err)
		}
		return views[0]
	}
	device(true)

	claude.limitsErr = fmt.Errorf("claude get_usage: %w", context.DeadlineExceeded)
	if view := device(true); view.LimitsProblem != domain.AgentAccountProblemSlow || view.Limits == nil {
		t.Fatalf("slow check = %+v, want kind slow and the last limits", view)
	}
	claude.limitsErr = nil
	now = now.Add(retryFailedAfter + time.Second) // well inside the five-minute window
	if view := device(false); view.LimitsProblem != "" || view.LimitsErr != "" {
		t.Fatalf("a minute after a slow check: %+v, want it read again", view)
	}

	claude.limitsErr = errors.New("claude get_usage: 429 Too Many Requests")
	if view := device(true); view.LimitsProblem != domain.AgentAccountProblemRateLimited {
		t.Fatalf("rate limited check = %+v", view)
	}
	claude.limitsErr = nil
	now = now.Add(retryFailedAfter + time.Second)
	if view := device(false); view.LimitsProblem != domain.AgentAccountProblemRateLimited {
		t.Fatalf("a refused check was asked again within a minute: %+v", view)
	}
}

func TestACLIThatStaysSlowIsAskedLessOften(t *testing.T) {
	ctx := context.Background()
	claude := &fakeClaude{
		signedInAs: map[string]string{"": "a@example.com"},
		limitsErr:  fmt.Errorf("claude get_usage: %w", context.DeadlineExceeded),
	}
	svc := New(&memStore{defaults: map[domain.AgentAccountProvider]string{}, bindings: map[domain.SessionID]string{}},
		[]ports.AgentAccountProviderCLI{claude}, t.TempDir(), fakeTerminals{}, nil)
	now := time.Now()
	svc.now = func() time.Time { return now }
	id := domain.DeviceAgentAccountID(domain.AgentAccountProviderClaude)
	if _, err := svc.List(ctx, true); err != nil {
		t.Fatal(err)
	}
	// Each slow check in a row doubles the wait, up to the usual five minutes.
	for _, wait := range []time.Duration{time.Minute, 2 * time.Minute, 4 * time.Minute, freshFor, freshFor} {
		now = now.Add(wait - time.Second)
		if svc.needsReading(id, false) {
			t.Fatalf("read again before %v", wait)
		}
		now = now.Add(2 * time.Second)
		if !svc.needsReading(id, false) {
			t.Fatalf("not read again after %v", wait)
		}
		if _, err := svc.List(ctx, false); err != nil {
			t.Fatal(err)
		}
	}
}

// A failed read can keep earlier limits with no windows; the failure backoff
// still applies instead of the 30-second empty-windows retry.
func TestASlowCheckOnAPlanWithoutWindowsStillBacksOff(t *testing.T) {
	ctx := context.Background()
	claude := &fakeClaude{signedInAs: map[string]string{"": "a@example.com"}}
	svc := New(&memStore{defaults: map[domain.AgentAccountProvider]string{}, bindings: map[domain.SessionID]string{}},
		[]ports.AgentAccountProviderCLI{claude}, t.TempDir(), fakeTerminals{}, nil)
	now := time.Now()
	svc.now = func() time.Time { return now }
	id := domain.DeviceAgentAccountID(domain.AgentAccountProviderClaude)
	if _, err := svc.List(ctx, true); err != nil { // limits with no windows
		t.Fatal(err)
	}
	claude.limitsErr = fmt.Errorf("claude get_usage: %w", context.DeadlineExceeded)
	if view, err := svc.List(ctx, true); err != nil || view[0].Limits == nil || len(view[0].Limits.Windows) != 0 {
		t.Fatalf("slow check = %+v, %v; want the earlier empty limits kept", view, err)
	}
	now = now.Add(retryEmptyAfter + time.Second)
	if svc.needsReading(id, false) {
		t.Fatal("a slow check was read again after 30 seconds")
	}
	now = now.Add(-retryEmptyAfter - time.Second)
	// Each slow check in a row doubles the wait: one, two, then four minutes.
	for _, wait := range []time.Duration{time.Minute, 2 * time.Minute, 4 * time.Minute} {
		now = now.Add(wait - time.Second)
		if svc.needsReading(id, false) {
			t.Fatalf("read again before %v", wait)
		}
		now = now.Add(2 * time.Second)
		if !svc.needsReading(id, false) {
			t.Fatalf("not read again after %v", wait)
		}
		if _, err := svc.List(ctx, false); err != nil {
			t.Fatal(err)
		}
	}
}
