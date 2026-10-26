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
	onLaunchWith    func() // runs as SessionLaunchEnvWith resolves, outside the lock
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
	hook := a.onLaunchWith
	a.launchOverrides = append(a.launchOverrides, accountID)
	env := a.env(accountID)
	a.mu.Unlock()
	if hook != nil {
		hook()
	}
	return env, nil
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

func (a *transitionAccounts) CommitSession(ctx context.Context, id domain.SessionID, harness domain.AgentHarness, accountID string) error {
	return a.BindSession(ctx, id, harness, accountID)
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

// A daemon exit after the binding committed but before the transition row
// moved on leaves the target as the session's agent: recovery keeps it.
func TestAccountTransitionRecoveryKeepsACommittedTarget(t *testing.T) {
	manager, store, _, _, accounts, log := newAccountTransitionManager(t, domain.SessionModeTUI)
	accounts.bound["session-1"] = "acct-b"
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
	if len(recovered) != 1 || recovered[0].Phase != domain.SessionInterfaceTransitionRecovery ||
		!strings.Contains(recovered[0].ErrorDetail, "stays on its new account") {
		t.Fatalf("recovered = %+v", recovered)
	}
	if len(*log) != 0 {
		t.Fatalf("recovery touched the committed agent: %v", *log)
	}
	if accounts.boundTo("session-1") != "acct-b" {
		t.Fatal("recovery moved the binding")
	}
}

// The preflight resolves the target account's env while the source agent still
// runs. A restart of that agent meanwhile must keep the session's own account.
func TestAccountPreflightEnvLeavesOtherLaunchesOnTheBoundAccount(t *testing.T) {
	manager, store, _, _, accounts, _ := newAccountTransitionManager(t, domain.SessionModeTUI)
	rec, _, err := store.GetSession(context.Background(), "session-1")
	if err != nil {
		t.Fatal(err)
	}
	var concurrent map[string]string
	accounts.onLaunchWith = func() {
		concurrent = manager.runtimeEnv(rec.ID, rec.ProjectID, rec.IssueID, nil)
	}

	env, err := manager.runtimeEnvOnAccount(rec, nil, "acct-b")
	if err != nil {
		t.Fatal(err)
	}
	if env["CLAUDE_CONFIG_DIR"] != "/accounts/b" {
		t.Fatalf("preflight env = %v, want the target account's folder", env)
	}
	if _, set := concurrent["CLAUDE_CONFIG_DIR"]; set {
		t.Fatalf("a launch during the preflight got %v, want the bound account's environment", concurrent)
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

// Removing an account first stops the agents that run on it, and only those.
// Their sessions stay, with the agent exited, so each can continue elsewhere.
func TestExitAgentsOnAccountStopsOnlyThatAccountsRunningAgents(t *testing.T) {
	m, st, runtime, _ := newManager()
	session := func(id string, state domain.ActivityState) domain.SessionRecord {
		return domain.SessionRecord{
			ID: domain.SessionID(id), ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode,
			Activity: domain.Activity{State: state},
			Metadata: domain.SessionMetadata{
				WorkspacePath: "/ws/" + id, RuntimeHandleID: "tmux-" + id, RuntimeLaunchID: "launch-" + id, AgentSessionID: "native-" + id,
			},
		}
	}
	st.sessions["mer-1"] = session("mer-1", domain.ActivityIdle)
	st.sessions["mer-2"] = session("mer-2", domain.ActivityIdle)
	st.sessions["mer-3"] = session("mer-3", domain.ActivityExited)
	runtime.aliveByHandle = map[string]bool{"tmux-mer-1": true, "tmux-mer-2": true}
	var log []string
	accounts := newTransitionAccounts(&log)
	accounts.bound = map[domain.SessionID]string{"mer-1": "acct-b", "mer-2": "device-claude", "mer-3": "acct-b"}
	m.SetAgentAccounts(accounts)

	if err := m.ExitAgentsOnAccount(ctx, "acct-b"); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(runtime.destroyedIDs) != "[tmux-mer-1]" {
		t.Fatalf("stopped = %v, want only the running agent on the account", runtime.destroyedIDs)
	}
	if got := st.sessions["mer-1"]; got.IsTerminated || got.Activity.State != domain.ActivityExited {
		t.Fatalf("mer-1 = %+v, want its agent exited and the session kept", got)
	}
	if got := st.sessions["mer-2"]; got.Activity.State != domain.ActivityIdle {
		t.Fatalf("mer-2 on another account changed: %+v", got.Activity)
	}
}

// An agent stopped by an account's removal continues on the account picked in
// its session: the move finds nothing to drain and starts it there.
func TestAccountTransitionContinuesAnExitedAgentOnTheNewAccount(t *testing.T) {
	for _, mode := range []domain.SessionMode{domain.SessionModeTUI, domain.SessionModeChat} {
		t.Run(string(mode), func(t *testing.T) {
			manager, store, runtime, chat, accounts, _ := newAccountTransitionManager(t, mode)
			rec := store.sessions["session-1"]
			rec.Activity = domain.Activity{State: domain.ActivityExited, LastActivityAt: time.Now()}
			store.sessions["session-1"] = rec

			transition, err := manager.StartAccountTransition(context.Background(), "session-1", "acct-b", domain.SessionInterfaceTransitionDrain)
			if err != nil {
				t.Fatal(err)
			}
			settled := awaitTransition(t, store, transition.ID)
			if settled.Phase != domain.SessionInterfaceTransitionCompleted {
				t.Fatalf("phase = %s, error = %s %s", settled.Phase, settled.ErrorCode, settled.ErrorDetail)
			}
			env := runtime.lastCfg.Env
			if mode == domain.SessionModeChat {
				env = chat.start.Env
			}
			if env["CLAUDE_CONFIG_DIR"] != "/accounts/b" {
				t.Fatalf("agent started with %v, want the new account", env)
			}
			if accounts.boundTo("session-1") != "acct-b" {
				t.Fatal("binding did not move")
			}
		})
	}
}

// A move that stops the turn tells the chat that the switch ended it, so the
// conversation shows the switch rather than a failed turn. A drain lets the
// turn finish, so there is nothing to explain.
func TestAccountTransitionThatStopsTheTurnMarksTheSwitchInChat(t *testing.T) {
	for _, tc := range []struct {
		policy domain.SessionInterfaceTransitionPolicy
		want   string
	}{
		{domain.SessionInterfaceTransitionInterrupt, "[acct-b]"},
		{domain.SessionInterfaceTransitionDrain, "[]"},
	} {
		t.Run(string(tc.policy), func(t *testing.T) {
			manager, store, _, chat, _, _ := newAccountTransitionManager(t, domain.SessionModeChat)
			transition, err := manager.StartAccountTransition(context.Background(), "session-1", "acct-b", tc.policy)
			if err != nil {
				t.Fatal(err)
			}
			if settled := awaitTransition(t, store, transition.ID); settled.Phase != domain.SessionInterfaceTransitionCompleted {
				t.Fatalf("phase = %s %s", settled.Phase, settled.ErrorDetail)
			}
			if got := fmt.Sprint(chat.switchMarks); got != tc.want {
				t.Fatalf("switch marks = %s, want %s", got, tc.want)
			}
		})
	}
}

// A terminal move keeps its mode, so the reaper probes the session all through
// it and finds the old terminal dead while the new one starts. The move must
// fence it, or the session is ended (and its containers reaped) mid-move.
func TestAccountTransitionFencesTheReaperWhileTheTargetStarts(t *testing.T) {
	manager, store, runtime, _, _, _ := newAccountTransitionManager(t, domain.SessionModeTUI)
	fenced := false
	runtime.onCreate = func() { fenced = manager.SessionMutationInProgress("session-1") }

	transition, err := manager.StartAccountTransition(context.Background(), "session-1", "acct-b", domain.SessionInterfaceTransitionDrain)
	if err != nil {
		t.Fatal(err)
	}
	if settled := awaitTransition(t, store, transition.ID); settled.Phase != domain.SessionInterfaceTransitionCompleted {
		t.Fatalf("phase = %s %s", settled.Phase, settled.ErrorDetail)
	}
	if !fenced {
		t.Fatal("the reaper was not fenced while the target terminal started")
	}
	deadline := time.Now().Add(2 * time.Second)
	for manager.SessionMutationInProgress("session-1") {
		if time.Now().After(deadline) {
			t.Fatal("the fence outlived the move")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A resume already underway read the account's folder while the session still
// reads as exited. Removing the account then refuses instead of deleting the
// folder under it.
func TestExitAgentsOnAccountRefusesWhileAResumeIsStarting(t *testing.T) {
	m, st, _, _ := newManager()
	st.sessions["mer-1"] = domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode,
		Activity: domain.Activity{State: domain.ActivityExited},
		Metadata: domain.SessionMetadata{RuntimeHandleID: "tmux-mer-1", RuntimeLaunchID: "launch-1"},
	}
	var log []string
	accounts := newTransitionAccounts(&log)
	accounts.bound = map[domain.SessionID]string{"mer-1": "acct-b"}
	m.SetAgentAccounts(accounts)
	if err := m.beginAgentResume(ctx, "mer-1"); err != nil {
		t.Fatal(err)
	}

	if err := m.ExitAgentsOnAccount(ctx, "acct-b"); err == nil {
		t.Fatal("removal went ahead while a resume on the account was starting")
	}
	m.endAgentResume("mer-1")
	if err := m.ExitAgentsOnAccount(ctx, "acct-b"); err != nil {
		t.Fatalf("an exited session with nothing starting: %v", err)
	}
}
