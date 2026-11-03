package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
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
	resumeFound  bool
	preparedIDs  []string
	restoreCalls int
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
