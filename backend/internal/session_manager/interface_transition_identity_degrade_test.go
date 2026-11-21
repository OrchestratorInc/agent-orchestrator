package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	browsersvc "github.com/aoagents/agent-orchestrator/backend/internal/service/browser"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

// TestInterfaceTransitionHermesWithoutIdentityPairDegradesToFreshChat covers
// the upstream-hermes-main runtime: the installed adapter never declares a
// compatible TUI+Chat identity pair, so the strict gate used to refuse every
// TUI→Chat switch. The switch must instead degrade to a fresh Chat start
// through the same durable transition record, marking the row
// identity-reset and delivering an honest boundary turn into the new Chat.
func TestInterfaceTransitionHermesWithoutIdentityPairDegradesToFreshChat(t *testing.T) {
	manager, store, runtime, chat, _ := newTransitionManager(t, domain.SessionModeTUI)
	manager.agents = singleAgent{agent: fakeAgent{}}
	useFastInterfaceTransitionTimings(manager)
	rec := store.sessions["session-1"]
	rec.Harness = domain.HarnessHermes
	store.sessions["session-1"] = rec

	status, err := manager.InterfaceTransitionStatus(context.Background(), "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if !status.Supported {
		t.Fatalf("hermes without an identity pair must degrade, not refuse: %+v", status)
	}

	transition, err := manager.StartInterfaceTransition(context.Background(), "session-1",
		domain.SessionModeChat, domain.SessionInterfaceTransitionDrain, domain.SessionInterfaceTransitionHistoryStrict)
	if err != nil {
		t.Fatal(err)
	}
	settled := awaitTransition(t, store, transition.ID)
	if settled.Phase != domain.SessionInterfaceTransitionCompleted {
		t.Fatalf("phase = %s, code = %s, error = %s", settled.Phase, settled.ErrorCode, settled.ErrorDetail)
	}
	if settled.DegradedKind != domain.SessionInterfaceTransitionDegradedIdentityReset {
		t.Fatalf("degradedKind = %q, want identity-reset", settled.DegradedKind)
	}
	if settled.NativeConversationID != "" || chat.start.ProviderConversationID != "" {
		t.Fatalf("degraded handoff did not start Chat fresh: transition=%q target=%q",
			settled.NativeConversationID, chat.start.ProviderConversationID)
	}
	current := store.sessions["session-1"]
	if current.Mode != domain.SessionModeChat {
		t.Fatalf("mode = %s, want chat", current.Mode)
	}
	if runtime.created != 0 {
		t.Fatalf("terminal runtime created %d times while switching to Chat", runtime.created)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		pending, listErr := store.ListPendingSessionInterfaceTransitionMessages(context.Background(), transition.ID)
		if listErr != nil {
			t.Fatal(listErr)
		}
		if len(pending) == 0 {
			messages := store.messages[transition.ID]
			if len(messages) != 1 || messages[0].Message != degradedHandoffMarkerText {
				t.Fatalf("marker messages = %+v, want exactly the fresh-start boundary", messages)
			}
			if got := chat.relayMessages; len(got) != 1 || got[0] != degradedHandoffMarkerText {
				t.Fatalf("chat relay = %v, want the fresh-start boundary", got)
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("degraded marker was not delivered: pending=%+v", store.messages[transition.ID])
}

// TestInterfaceTransitionHermesWithDeclaredPairKeepsStrictHandoff pins the
// fork-patched hermes (identity chain present): the handoff resumes the native
// conversation as before and records no degradation.
func TestInterfaceTransitionHermesWithDeclaredPairKeepsStrictHandoff(t *testing.T) {
	manager, store, _, chat, _ := newTransitionManager(t, domain.SessionModeTUI)
	useFastInterfaceTransitionTimings(manager)
	rec := store.sessions["session-1"]
	rec.Harness = domain.HarnessHermes
	store.sessions["session-1"] = rec

	transition, err := manager.StartInterfaceTransition(context.Background(), "session-1",
		domain.SessionModeChat, domain.SessionInterfaceTransitionDrain, domain.SessionInterfaceTransitionHistoryStrict)
	if err != nil {
		t.Fatal(err)
	}
	settled := awaitTransition(t, store, transition.ID)
	if settled.Phase != domain.SessionInterfaceTransitionCompleted {
		t.Fatalf("phase = %s, code = %s, error = %s", settled.Phase, settled.ErrorCode, settled.ErrorDetail)
	}
	if settled.DegradedKind != "" {
		t.Fatalf("degradedKind = %q, want empty for a declared identity pair", settled.DegradedKind)
	}
	if settled.NativeConversationID != "native-1" || chat.start.ProviderConversationID != "native-1" {
		t.Fatalf("declared-pair handoff changed: transition=%q target=%q",
			settled.NativeConversationID, chat.start.ProviderConversationID)
	}
	if messages := store.messages[transition.ID]; len(messages) != 0 {
		t.Fatalf("strict handoff delivered %d marker messages, want none", len(messages))
	}
}

// hermesDegradedTransitionAgent models the upstream-hermes adapter: no declared
// identity pair, but it can resolve the recorded native conversation's terminal
// resume identity — found only when resumeFound is set.
type hermesDegradedTransitionAgent struct {
	fakeAgent
	resumeFound   bool
	preparedIDs   []string
	restoreCalls  int
	launchPrompts []string
}

func (a *hermesDegradedTransitionAgent) GetLaunchCommand(ctx context.Context, cfg ports.LaunchConfig) ([]string, error) {
	a.launchPrompts = append(a.launchPrompts, cfg.Prompt)
	return a.fakeAgent.GetLaunchCommand(ctx, cfg)
}

func (a *hermesDegradedTransitionAgent) PrepareNativeTerminalResume(_ context.Context, _, _, nativeConversationID, _ string) (bool, error) {
	a.preparedIDs = append(a.preparedIDs, nativeConversationID)
	return a.resumeFound, nil
}

func (a *hermesDegradedTransitionAgent) GetRestoreCommand(ctx context.Context, cfg ports.RestoreConfig) ([]string, bool, error) {
	a.restoreCalls++
	return a.fakeAgent.GetRestoreCommand(ctx, cfg)
}

// TestInterfaceTransitionHermesChatToTUIDegradesToNativeRestore covers the
// upstream-hermes-main runtime in the Chat→TUI direction: the installed adapter
// never declares a compatible TUI+Chat identity pair, but the recorded native
// conversation is still resumable, so the switch must restore THAT terminal,
// mark the row identity-reset, and deliver an honest boundary turn — exactly
// once — into the resumed terminal.
func TestInterfaceTransitionHermesChatToTUIDegradesToNativeRestore(t *testing.T) {
	manager, store, runtime, _, log := newTransitionManager(t, domain.SessionModeChat)
	useFastInterfaceTransitionTimings(manager)
	agent := &hermesDegradedTransitionAgent{resumeFound: true}
	manager.agents = singleAgent{agent: agent}
	rec := store.sessions["session-1"]
	rec.Harness = domain.HarnessHermes
	store.sessions["session-1"] = rec

	status, err := manager.InterfaceTransitionStatus(context.Background(), "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if !status.Supported {
		t.Fatalf("degraded hermes Chat→TUI must keep the switch enabled: %+v", status)
	}

	transition, err := manager.StartInterfaceTransition(context.Background(), "session-1",
		domain.SessionModeTUI, domain.SessionInterfaceTransitionDrain, domain.SessionInterfaceTransitionHistoryStrict)
	if err != nil {
		t.Fatal(err)
	}
	settled := awaitTransition(t, store, transition.ID)
	if settled.Phase != domain.SessionInterfaceTransitionCompleted {
		t.Fatalf("phase = %s, code = %s, error = %s", settled.Phase, settled.ErrorCode, settled.ErrorDetail)
	}
	if settled.DegradedKind != domain.SessionInterfaceTransitionDegradedIdentityReset {
		t.Fatalf("degradedKind = %q, want identity-reset", settled.DegradedKind)
	}
	if settled.NativeConversationID != "native-1" {
		t.Fatalf("native conversation = %q, want the recorded chat identity", settled.NativeConversationID)
	}
	if fmt.Sprint(agent.preparedIDs) != "[native-1]" {
		t.Fatalf("terminal resume lookups = %v, want exactly the recorded identity", agent.preparedIDs)
	}
	if agent.restoreCalls == 0 {
		t.Fatal("restore command never built; the switch did not resume the native terminal")
	}
	current := store.sessions["session-1"]
	if current.Mode != domain.SessionModeTUI {
		t.Fatalf("mode = %s, want tui", current.Mode)
	}
	if current.Metadata.AgentSessionIDLaunchID == "" ||
		current.Metadata.AgentSessionIDLaunchID != current.Metadata.RuntimeLaunchID {
		t.Fatalf("native identity launch proof = %q, runtime launch = %q; the restored terminal must be immediately switchable",
			current.Metadata.AgentSessionIDLaunchID, current.Metadata.RuntimeLaunchID)
	}
	if runtime.created != 1 {
		t.Fatalf("terminal runtime created %d times, want 1", runtime.created)
	}
	if got := fmt.Sprint(*log); got != "[prepare:chat:drain stop:chat start:tui]" {
		t.Fatalf("controller order = %s", got)
	}
	awaitDegradedMarker(t, store, transition.ID, degradedTerminalResumeMarkerText)
}

// TestInterfaceTransitionHermesChatToTUIDegradesToFreshTUI covers the missing
// native terminal: nothing resumable exists, so the switch starts an ordinary
// fresh TUI for the workspace, still marked identity-reset with its own honest
// boundary turn. The Chat conversation stays archived as-is.
func TestInterfaceTransitionHermesChatToTUIDegradesToFreshTUI(t *testing.T) {
	manager, store, runtime, _, log := newTransitionManager(t, domain.SessionModeChat)
	useFastInterfaceTransitionTimings(manager)
	agent := &hermesDegradedTransitionAgent{}
	manager.agents = singleAgent{agent: agent}
	rec := store.sessions["session-1"]
	rec.Harness = domain.HarnessHermes
	store.sessions["session-1"] = rec

	status, err := manager.InterfaceTransitionStatus(context.Background(), "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if !status.Supported {
		t.Fatalf("degraded hermes Chat→TUI must keep the switch enabled even without a native terminal: %+v", status)
	}

	transition, err := manager.StartInterfaceTransition(context.Background(), "session-1",
		domain.SessionModeTUI, domain.SessionInterfaceTransitionDrain, domain.SessionInterfaceTransitionHistoryStrict)
	if err != nil {
		t.Fatal(err)
	}
	settled := awaitTransition(t, store, transition.ID)
	if settled.Phase != domain.SessionInterfaceTransitionCompleted {
		t.Fatalf("phase = %s, code = %s, error = %s", settled.Phase, settled.ErrorCode, settled.ErrorDetail)
	}
	if settled.DegradedKind != domain.SessionInterfaceTransitionDegradedIdentityReset {
		t.Fatalf("degradedKind = %q, want identity-reset", settled.DegradedKind)
	}
	if settled.NativeConversationID != "" {
		t.Fatalf("native conversation = %q, want empty for a fresh TUI", settled.NativeConversationID)
	}
	if agent.restoreCalls != 0 {
		t.Fatalf("fresh fallback built a restore command %d times, want 0", agent.restoreCalls)
	}
	current := store.sessions["session-1"]
	if current.Mode != domain.SessionModeTUI {
		t.Fatalf("mode = %s, want tui", current.Mode)
	}
	if runtime.created != 1 {
		t.Fatalf("terminal runtime created %d times, want 1", runtime.created)
	}
	if got := fmt.Sprint(*log); got != "[prepare:chat:drain stop:chat start:tui]" {
		t.Fatalf("controller order = %s", got)
	}
	awaitDegradedMarker(t, store, transition.ID, degradedTerminalFreshMarkerText)
}

// A Chat that already ran the session's prompt must not hand it to the fresh
// terminal, or the agent starts the task again. A Chat that never accepted
// anything still owes that prompt to the terminal. Either way the session keeps
// its prompt. (Guard reused from OrchestratorInc/agent-orchestrator#7725 by
// Meraj-08.)
func TestInterfaceTransitionHermesFreshTUIReplaysPromptOnlyIfChatNeverRanIt(t *testing.T) {
	for _, tc := range []struct {
		name         string
		conversation *domain.ConversationRecord
		want         string
	}{
		{name: "chat ran the prompt", conversation: &domain.ConversationRecord{LatestSequence: 4}},
		{name: "chat accepted nothing", conversation: &domain.ConversationRecord{}, want: "original task"},
		{name: "chat never started", want: "original task"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manager, store, _, _, _ := newTransitionManager(t, domain.SessionModeChat)
			useFastInterfaceTransitionTimings(manager)
			agent := &hermesDegradedTransitionAgent{}
			manager.agents = singleAgent{agent: agent}
			rec := store.sessions["session-1"]
			rec.Harness = domain.HarnessHermes
			rec.Metadata.Prompt = "original task"
			store.sessions["session-1"] = rec
			if tc.conversation != nil {
				store.conversations = map[domain.SessionID]domain.ConversationRecord{"session-1": *tc.conversation}
			}

			transition, err := manager.StartInterfaceTransition(context.Background(), "session-1",
				domain.SessionModeTUI, domain.SessionInterfaceTransitionDrain, domain.SessionInterfaceTransitionHistoryStrict)
			if err != nil {
				t.Fatal(err)
			}
			settled := awaitTransition(t, store, transition.ID)
			if settled.Phase != domain.SessionInterfaceTransitionCompleted {
				t.Fatalf("phase = %s, code = %s, error = %s", settled.Phase, settled.ErrorCode, settled.ErrorDetail)
			}
			// The last build is the real launch; the first is the preflight's.
			if got := agent.launchPrompts[len(agent.launchPrompts)-1]; got != tc.want {
				t.Fatalf("fresh terminal prompt = %q, want %q (builds: %q)", got, tc.want, agent.launchPrompts)
			}
			if got := store.sessions["session-1"].Metadata.Prompt; got != "original task" {
				t.Fatalf("session prompt = %q, want it kept", got)
			}
		})
	}
}

// awaitDegradedMarker polls the transition outbox until the degraded boundary
// turn has been delivered, then asserts it was delivered exactly once with the
// expected text.
func awaitDegradedMarker(t *testing.T, store *transitionStore, transitionID, want string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		pending, listErr := store.ListPendingSessionInterfaceTransitionMessages(context.Background(), transitionID)
		if listErr != nil {
			t.Fatal(listErr)
		}
		if len(pending) == 0 {
			messages := store.messages[transitionID]
			if len(messages) != 1 || messages[0].Message != want {
				t.Fatalf("marker messages = %+v, want exactly %q", messages, want)
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("degraded marker was not delivered: pending=%+v", store.messages[transitionID])
}

// TestInterfaceTransitionNonHermesChatToTUIStillRequiresHandoffCapability keeps
// the degrade scoped to hermes: another harness without a declared identity
// pair still fails closed.
func TestInterfaceTransitionNonHermesChatToTUIStillRequiresHandoffCapability(t *testing.T) {
	manager, store, runtime, chat, _ := newTransitionManager(t, domain.SessionModeChat)
	manager.agents = singleAgent{agent: fakeAgent{}}

	_, err := manager.StartInterfaceTransition(context.Background(), "session-1",
		domain.SessionModeTUI, domain.SessionInterfaceTransitionDrain, domain.SessionInterfaceTransitionHistoryStrict)
	if !errors.Is(err, ErrInterfaceHandoffUnsupported) {
		t.Fatalf("error = %v, want ErrInterfaceHandoffUnsupported", err)
	}
	if len(store.transitions) != 0 || runtime.created != 0 || len(chat.relayMessages) != 0 {
		t.Fatal("unsupported Chat→TUI handoff mutated session or controllers")
	}
}

// hermesMessagedChatOrchestrator is a hermes Chat worker whose conversation
// already carries a real turn, backed by the real SQLite store: the Chat has
// messages, so its root branch is not untouched. The adapter (via
// hermesDegradedTransitionAgent with resumeFound=false) cannot resolve the
// native terminal, mirroring upstream issue #7724's unresumable conversation.
func hermesMessagedChatOrchestrator(t *testing.T) (*Manager, *sqlite.Store, *transitionRuntime, *transitionChat, *[]string, domain.SessionID) {
	t.Helper()
	ctx := context.Background()
	store := sqlitetest.MustOpenAt(t, t.TempDir())
	if err := store.UpsertProject(ctx, domain.ProjectRecord{ID: "proj", Path: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	created, err := store.CreateSession(ctx, domain.SessionRecord{
		ProjectID: "proj", Kind: domain.KindWorker, Harness: domain.HarnessHermes, Mode: domain.SessionModeChat,
		Metadata: domain.SessionMetadata{
			WorkspacePath:          t.TempDir(),
			Branch:                 "ao/session",
			ProviderConversationID: "reserved-chat-id",
			ControllerGeneration:   "chat-generation",
		},
		Activity:  domain.Activity{State: domain.ActivityIdle, LastActivityAt: now},
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := store.CreateConversation(ctx, "chat-conversation", domain.ConversationScopeSession,
		created.ProjectID, created.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	// One real message: the Chat is not untouched.
	if err := store.AdoptProviderTurn(ctx, conversation.ID, created.ID, "chat-generation", "turn-1", "provider-turn-1", now); err != nil {
		t.Fatal(err)
	}
	log := &[]string{}
	runtime := &transitionRuntime{fakeRuntime: &fakeRuntime{}, log: log}
	chat := &transitionChat{
		log: log, supportsChat: true,
		armed:   make(chan domain.SessionInterfaceTransitionPolicy, 1),
		aborted: make(chan struct{}, 1),
	}
	manager := New(Deps{
		Runtime: runtime, Agents: singleAgent{agent: &hermesDegradedTransitionAgent{}},
		Workspace: &fakeWorkspace{}, Store: store, Messenger: &fakeMessenger{}, Chat: chat,
		Lifecycle: &sqliteTransitionLifecycle{store: store},
		LookPath:  func(string) (string, error) { return "/bin/true", nil },
	})
	useFastInterfaceTransitionTimings(manager)
	manager.browserCapabilities = &recordingBrowserAuthority{authority: browsersvc.NewAuthority()}
	return manager, store, runtime, chat, log, created.ID
}

// TestInterfaceTransitionHermesChatWithMessagesToTUIDegradesToFreshTUI covers
// upstream issue #7724: a hermes Chat whose conversation carries real messages
// and whose native conversation cannot be resumed must still switch to the
// terminal through the degraded fresh-TUI path. It used to fail the controller
// epoch commit with SESSION_CHANGED ("release untouched Chat provider ...:
// empty root proof changed") because the commit required the untouched-Chat
// release proof, leaving the session stuck in Chat with every retry failing.
// The fresh TUI must start, the degraded boundary must be recorded, and the
// messaged Chat conversation must stay archived exactly as it was.
func TestInterfaceTransitionHermesChatWithMessagesToTUIDegradesToFreshTUI(t *testing.T) {
	manager, store, runtime, _, log, id := hermesMessagedChatOrchestrator(t)
	ctx := context.Background()

	status, err := manager.InterfaceTransitionStatus(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if !status.Supported {
		t.Fatalf("degraded hermes Chat→TUI must keep the switch enabled for a messaged Chat: %+v", status)
	}

	transition, err := manager.StartInterfaceTransition(ctx, id,
		domain.SessionModeTUI, domain.SessionInterfaceTransitionDrain, domain.SessionInterfaceTransitionHistoryStrict)
	if err != nil {
		t.Fatal(err)
	}
	settled := settleSQLiteTransition(t, store, transition.ID)
	if settled.Phase != domain.SessionInterfaceTransitionCompleted {
		t.Fatalf("phase = %s, code = %s, error = %s", settled.Phase, settled.ErrorCode, settled.ErrorDetail)
	}
	if settled.DegradedKind != domain.SessionInterfaceTransitionDegradedIdentityReset {
		t.Fatalf("degradedKind = %q, want identity-reset", settled.DegradedKind)
	}
	if settled.NativeConversationID != "" {
		t.Fatalf("native conversation = %q, want empty for a fresh TUI", settled.NativeConversationID)
	}
	current, _, err := store.GetSession(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if domain.NormalizeSessionMode(current.Mode) != domain.SessionModeTUI {
		t.Fatalf("mode = %s, want tui", current.Mode)
	}
	if runtime.created != 1 {
		t.Fatalf("terminal runtime created %d times, want 1", runtime.created)
	}
	if got := fmt.Sprint(*log); got != "[prepare:chat:drain stop:chat start:tui]" {
		t.Fatalf("controller order = %s", got)
	}
	// The messaged Chat conversation stays archived untouched: same root
	// branch, same provider binding, its turn history intact.
	conversation, err := store.ConversationForSession(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	after, err := store.ConversationBranch(ctx, conversation.ID, conversation.ActiveBranchID)
	if err != nil {
		t.Fatal(err)
	}
	if after.ProviderConversationID != "reserved-chat-id" || after.ProviderScopeID == "" {
		t.Fatalf("archived Chat root was disturbed: %+v", after)
	}
	if hasTurns, err := store.HasConversationTurns(ctx, conversation.ID); err != nil {
		t.Fatal(err)
	} else if !hasTurns {
		t.Fatal("archived Chat lost its turn history")
	}
}
