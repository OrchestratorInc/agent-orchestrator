package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// transitionAccounts is a fake account catalog: each account is a config
// folder, and the device account launches with no folder variable.
type transitionAccounts struct {
	mu              sync.Mutex
	log             *[]string
	bound           map[domain.SessionID]string
	homes           map[string]string
	validateErrs    []error // consumed one per ValidateAccount call
	bindErr         error
	launchOverrides []string
}

func newTransitionAccounts(log *[]string) *transitionAccounts {
	return &transitionAccounts{
		log:   log,
		bound: map[domain.SessionID]string{"session-1": "device-claude"},
		homes: map[string]string{"device-claude": "", "acct-b": "/accounts/b"},
	}
}

func (a *transitionAccounts) env(accountID string) map[string]string {
	if home := a.homes[accountID]; home != "" {
		return map[string]string{"CLAUDE_CONFIG_DIR": home}
	}
	return map[string]string{}
}

func (a *transitionAccounts) SessionLaunchEnv(_ context.Context, id domain.SessionID) (map[string]string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.env(a.bound[id]), nil
}

func (a *transitionAccounts) SessionLaunchEnvWith(_ context.Context, _ domain.SessionID, accountID string) (map[string]string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.launchOverrides = append(a.launchOverrides, accountID)
	return a.env(accountID), nil
}

func (a *transitionAccounts) BindSession(_ context.Context, id domain.SessionID, _ domain.AgentHarness, accountID string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.bindErr != nil {
		return a.bindErr
	}
	if accountID == "" {
		accountID = "device-claude"
	}
	*a.log = append(*a.log, "bind:"+accountID)
	a.bound[id] = accountID
	return nil
}

func (a *transitionAccounts) SessionAccountID(_ context.Context, id domain.SessionID, _ domain.AgentHarness) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.bound[id], nil
}

func (a *transitionAccounts) ValidateAccount(context.Context, domain.AgentHarness, string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.validateErrs) == 0 {
		return nil
	}
	err := a.validateErrs[0]
	a.validateErrs = a.validateErrs[1:]
	return err
}

func (a *transitionAccounts) boundTo(id domain.SessionID) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.bound[id]
}

func newAccountTransitionManager(t *testing.T, mode domain.SessionMode) (*Manager, *transitionStore, *transitionRuntime, *transitionChat, *transitionAccounts, *[]string) {
	t.Helper()
	manager, store, runtime, chat, log := newTransitionManager(t, mode)
	useFastInterfaceTransitionTimings(manager)
	accounts := newTransitionAccounts(log)
	manager.SetAgentAccounts(accounts)
	return manager, store, runtime, chat, accounts, log
}

// The move is a transaction: the agent restarts on the new account first, and
// the session's binding changes only once that agent is live.
func TestAccountTransitionMovesATerminalSessionAndCommitsAfterTheTargetStarts(t *testing.T) {
	manager, store, runtime, _, accounts, log := newAccountTransitionManager(t, domain.SessionModeTUI)

	transition, err := manager.StartAccountTransition(context.Background(), "session-1", "acct-b", domain.SessionInterfaceTransitionDrain)
	if err != nil {
		t.Fatal(err)
	}
	if transition.SourceMode != domain.SessionModeTUI || transition.TargetMode != domain.SessionModeTUI ||
		transition.SourceAccountID != "device-claude" || transition.TargetAccountID != "acct-b" {
		t.Fatalf("transition = %+v, want a same-mode move from device-claude to acct-b", transition)
	}
	settled := awaitTransition(t, store, transition.ID)
	if settled.Phase != domain.SessionInterfaceTransitionCompleted {
		t.Fatalf("phase = %s, error = %s %s", settled.Phase, settled.ErrorCode, settled.ErrorDetail)
	}
	if got := fmt.Sprint(*log); got != "[stop:tui:runtime-1 start:tui bind:acct-b]" {
		t.Fatalf("order = %s, want stop, start on the new account, then bind", got)
	}
	if got := runtime.lastCfg.Env["CLAUDE_CONFIG_DIR"]; got != "/accounts/b" {
		t.Fatalf("target launched with CLAUDE_CONFIG_DIR=%q, want /accounts/b", got)
	}
	if got := accounts.boundTo("session-1"); got != "acct-b" {
		t.Fatalf("binding = %q, want acct-b", got)
	}
	if store.sessions["session-1"].Mode != domain.SessionModeTUI {
		t.Fatal("an account move changed the session mode")
	}
	if manager.accountOverride("session-1") != "" {
		t.Fatal("account override outlived the transition")
	}
}

func TestAccountTransitionResumesTheSameChatConversationOnTheNewAccount(t *testing.T) {
	manager, store, _, chat, accounts, log := newAccountTransitionManager(t, domain.SessionModeChat)

	transition, err := manager.StartAccountTransition(context.Background(), "session-1", "acct-b", domain.SessionInterfaceTransitionDrain)
	if err != nil {
		t.Fatal(err)
	}
	settled := awaitTransition(t, store, transition.ID)
	if settled.Phase != domain.SessionInterfaceTransitionCompleted {
		t.Fatalf("phase = %s, error = %s %s", settled.Phase, settled.ErrorCode, settled.ErrorDetail)
	}
	if got := fmt.Sprint(*log); got != "[prepare:chat:drain stop:chat start:chat bind:acct-b]" {
		t.Fatalf("order = %s", got)
	}
	if chat.start.ProviderConversationID != "native-1" {
		t.Fatalf("provider conversation = %q, want the same native-1", chat.start.ProviderConversationID)
	}
	if got := chat.start.Env["CLAUDE_CONFIG_DIR"]; got != "/accounts/b" {
		t.Fatalf("chat started with CLAUDE_CONFIG_DIR=%q, want /accounts/b", got)
	}
	if accounts.boundTo("session-1") != "acct-b" {
		t.Fatal("binding did not move to acct-b")
	}
}

// If the new account's agent fails to start, neither half happens: the old
// agent restarts on the old account and the binding never changed.
func TestAccountTransitionRollsBackToTheOldAccountWhenTheTargetFails(t *testing.T) {
	manager, store, runtime, _, accounts, log := newAccountTransitionManager(t, domain.SessionModeTUI)
	runtime.createErrSequence = []error{errors.New("claude exited: not logged in"), nil}

	transition, err := manager.StartAccountTransition(context.Background(), "session-1", "acct-b", domain.SessionInterfaceTransitionDrain)
	if err != nil {
		t.Fatal(err)
	}
	settled := awaitTransition(t, store, transition.ID)
	if settled.Phase != domain.SessionInterfaceTransitionFailed || settled.ErrorCode != "TARGET_RESUME_FAILED" {
		t.Fatalf("transition = %s %s %s, want failed after rollback", settled.Phase, settled.ErrorCode, settled.ErrorDetail)
	}
	if got := accounts.boundTo("session-1"); got != "device-claude" {
		t.Fatalf("binding = %q, want unchanged device-claude", got)
	}
	if _, set := runtime.lastCfg.Env["CLAUDE_CONFIG_DIR"]; set {
		t.Fatalf("restored agent launched with %v, want the old account's environment", runtime.lastCfg.Env)
	}
	if runtime.created != 1 {
		t.Fatalf("created = %d, want only the restored source", runtime.created)
	}
	for _, entry := range *log {
		if entry == "bind:acct-b" {
			t.Fatalf("binding moved on a failed start: %v", *log)
		}
	}
}

// Everything that can fail without touching the conversation is checked while
// the old agent still runs, so a bad target never stops it.
func TestAccountTransitionPreflightFailureLeavesTheAgentRunning(t *testing.T) {
	manager, store, _, _, accounts, log := newAccountTransitionManager(t, domain.SessionModeTUI)
	accounts.validateErrs = []error{nil, ports.ErrAgentAccountSignedOut}

	transition, err := manager.StartAccountTransition(context.Background(), "session-1", "acct-b", domain.SessionInterfaceTransitionDrain)
	if err != nil {
		t.Fatal(err)
	}
	settled := awaitTransition(t, store, transition.ID)
	if settled.Phase != domain.SessionInterfaceTransitionFailed {
		t.Fatalf("phase = %s, want failed", settled.Phase)
	}
	if len(*log) != 0 {
		t.Fatalf("controllers touched on a failed preflight: %v", *log)
	}
	if accounts.boundTo("session-1") != "device-claude" {
		t.Fatal("binding changed on a failed preflight")
	}
}

func TestAccountTransitionRefusesAnUnusableOrUnchangedAccountBeforeClaiming(t *testing.T) {
	manager, store, _, _, accounts, log := newAccountTransitionManager(t, domain.SessionModeTUI)

	if _, err := manager.StartAccountTransition(context.Background(), "session-1", "device-claude", domain.SessionInterfaceTransitionDrain); !errors.Is(err, ports.ErrAgentAccountUnchanged) {
		t.Fatalf("same account: err = %v, want ErrAgentAccountUnchanged", err)
	}
	accounts.validateErrs = []error{ports.ErrAgentAccountSignedOut}
	if _, err := manager.StartAccountTransition(context.Background(), "session-1", "acct-b", domain.SessionInterfaceTransitionDrain); !errors.Is(err, ports.ErrAgentAccountSignedOut) {
		t.Fatalf("signed-out account: err = %v, want ErrAgentAccountSignedOut", err)
	}
	if _, active, _ := store.GetActiveSessionInterfaceTransition(context.Background(), "session-1"); active {
		t.Fatal("a refused move claimed the session")
	}
	if len(*log) != 0 {
		t.Fatalf("controllers touched: %v", *log)
	}
}

// A bind failure after the target is live is still before the commit point:
// the target stops and the old account's agent comes back.
func TestAccountTransitionRollsBackWhenTheCommitFails(t *testing.T) {
	manager, store, runtime, _, accounts, log := newAccountTransitionManager(t, domain.SessionModeTUI)
	accounts.bindErr = errors.New("database is locked")

	transition, err := manager.StartAccountTransition(context.Background(), "session-1", "acct-b", domain.SessionInterfaceTransitionDrain)
	if err != nil {
		t.Fatal(err)
	}
	settled := awaitTransition(t, store, transition.ID)
	if settled.Phase != domain.SessionInterfaceTransitionFailed || settled.ErrorCode != "ACCOUNT_BIND_FAILED" {
		t.Fatalf("transition = %s %s, want failed bind", settled.Phase, settled.ErrorCode)
	}
	if accounts.boundTo("session-1") != "device-claude" {
		t.Fatal("binding changed")
	}
	if _, set := runtime.lastCfg.Env["CLAUDE_CONFIG_DIR"]; set {
		t.Fatalf("restored agent launched with %v, want the old account's environment", runtime.lastCfg.Env)
	}
	want := "[stop:tui:runtime-1 start:tui stop:tui:h1 stop:tui:runtime-1 start:tui]"
	if got := fmt.Sprint(*log); got != want {
		t.Fatalf("order = %s, want %s", got, want)
	}
}

// A daemon exit after the target may have started but before the commit leaves
// nothing running on an account the session does not name.
func TestAccountTransitionRecoveryStopsAnUncommittedTarget(t *testing.T) {
	manager, store, _, _, accounts, log := newAccountTransitionManager(t, domain.SessionModeTUI)
	now := time.Now()
	if _, _, err := store.CreateSessionInterfaceTransition(context.Background(), domain.SessionInterfaceTransition{
		ID: "move-1", SessionID: "session-1", SourceMode: domain.SessionModeTUI, TargetMode: domain.SessionModeTUI,
		Policy: domain.SessionInterfaceTransitionDrain, HistoryPolicy: domain.SessionInterfaceTransitionHistoryStrict,
		Phase: domain.SessionInterfaceTransitionTargetStarting, NativeConversationID: "native-1",
		SourceAccountID: "device-claude", TargetAccountID: "acct-b", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	recovered, err := manager.recoverInterruptedInterfaceTransitions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(recovered) != 1 || recovered[0].Phase != domain.SessionInterfaceTransitionRecovery || recovered[0].ErrorCode != "DAEMON_RESTARTED" {
		t.Fatalf("recovered = %+v", recovered)
	}
	if got := fmt.Sprint(*log); got != "[stop:tui:runtime-1]" {
		t.Fatalf("recovery controllers = %s, want the possibly-started target stopped", got)
	}
	if accounts.boundTo("session-1") != "device-claude" {
		t.Fatal("recovery moved the binding")
	}
}

// TestAccountMoveFailuresSpeakOfTheAccount pins the wording an account move
// shows: never "interface", and never an action the account switch hides.
func TestAccountMoveFailuresSpeakOfTheAccount(t *testing.T) {
	move := domain.SessionInterfaceTransition{SourceMode: domain.SessionModeTUI, TargetMode: domain.SessionModeTUI, TargetAccountID: "acct-b"}
	for _, code := range []string{"DRAIN_QUIESCENCE_UNVERIFIED", "DRAIN_DECISION_PENDING", "TRANSITION_CANCELLED", "SESSION_CHANGED"} {
		got := transitionDetail(move, code, "The source interface was left untouched; choose Cancel request and switch")
		if strings.Contains(got, "interface") || strings.Contains(got, "Cancel request") || !strings.Contains(got, "account") {
			t.Fatalf("%s: %q", code, got)
		}
	}
	modeSwitch := domain.SessionInterfaceTransition{SourceMode: domain.SessionModeTUI, TargetMode: domain.SessionModeChat}
	if got := transitionDetail(modeSwitch, "TRANSITION_CANCELLED", "The interface switch was cancelled."); got != "The interface switch was cancelled." {
		t.Fatalf("interface switch detail changed: %q", got)
	}
}
