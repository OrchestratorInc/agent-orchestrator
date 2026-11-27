package lifecycle

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// turnRecordingStore captures what lifecycle writes to the terminal turn table.
type turnRecordingStore struct {
	*fakeStore
	calls []string
}

func (s *turnRecordingStore) RecordTerminalTurnRequest(_ context.Context, turn domain.TerminalTurn) error {
	s.calls = append(s.calls, "request:"+string(turn.Harness)+":"+turn.NativeSessionID+":"+turn.UserRequest)
	return nil
}

func (s *turnRecordingStore) RecordTerminalTurnResponse(_ context.Context, _ domain.SessionID, response string, _ time.Time) error {
	s.calls = append(s.calls, "response:"+response)
	return nil
}

func TestTerminalTurnsAreRecordedFromHooksInTerminalModeOnly(t *testing.T) {
	signals := []ports.ActivitySignal{
		// Claude and Codex report the request as a replay checkpoint too.
		{Event: "user-prompt-submit", LatestUserPrompt: "fix the importer", TurnUserRequest: "fix the importer"},
		// Codex reports its final message without making it a replay checkpoint.
		{Event: "stop", FinalAssistantMessage: "importer fixed"},
		// AO's own message opens a turn the user did not ask for.
		{Event: "user-prompt-submit", ConversationCheckpointOrigin: domain.ConversationCheckpointOriginCoordination},
		{Event: "pre-tool-use", LatestUserPrompt: "not a main-turn boundary", TurnUserRequest: "nor is this"},
		// OpenCode and Grok report the request outside the replay-checkpoint field.
		{Event: "user-prompt-submit", TurnUserRequest: "add a retry"},
		// A helper agent finishing is not the end of the turn.
		{Event: "stop", SubagentID: "helper-1", FinalAssistantMessage: "helper done"},
		// Some agents report the end of a turn without its text. The turn
		// still closes, so the next request starts one of its own.
		{Event: "stop"},
		{Event: "user-prompt-submit", TurnUserRequest: "now add a test"},
		// A prompt hook with no text is not a request; it closes the open turn.
		{Event: "user-prompt-submit"},
	}
	for _, tc := range []struct {
		name string
		rec  domain.SessionRecord
		want []string
	}{
		{
			name: "terminal",
			rec: domain.SessionRecord{ID: "s-1", Harness: domain.HarnessCodex, Mode: domain.SessionModeTUI,
				Metadata: domain.SessionMetadata{RuntimeLaunchID: "launch", AgentSessionID: "thread-1", AgentSessionIDLaunchID: "launch"}},
			want: []string{
				"request:codex:thread-1:fix the importer", "response:importer fixed", "response:",
				"request:codex:thread-1:add a retry", "response:",
				"request:codex:thread-1:now add a test", "response:",
			},
		},
		{
			name: "chat",
			rec: domain.SessionRecord{ID: "s-1", Harness: domain.HarnessCodex, Mode: domain.SessionModeChat,
				Metadata: domain.SessionMetadata{ControllerGeneration: "generation"}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &turnRecordingStore{fakeStore: newFakeStore()}
			store.sessions["s-1"] = tc.rec
			manager := New(store, nil)
			for _, signal := range signals {
				signal.Valid, signal.State = true, domain.ActivityActive
				if tc.rec.Mode == domain.SessionModeChat {
					signal.ControllerGeneration = "generation"
				} else {
					signal.LaunchID = "launch"
				}
				if err := manager.ApplyActivitySignal(context.Background(), "s-1", signal); err != nil {
					t.Fatal(err)
				}
			}
			if !reflect.DeepEqual(store.calls, tc.want) {
				t.Fatalf("recorded %q, want %q", store.calls, tc.want)
			}
		})
	}
}

// Kimi reports a turn that a provider error ended through StopFailure, never
// Stop (#3387). The failed turn ends: a pending permission prompt clears, the
// session reads idle, and the user's retry starts a turn of its own instead of
// joining the unanswered one.
func TestKimiStopFailureEndsTheTurnUnanswered(t *testing.T) {
	store := &turnRecordingStore{fakeStore: newFakeStore()}
	store.sessions["s-1"] = domain.SessionRecord{ID: "s-1", Harness: domain.HarnessKimi, Mode: domain.SessionModeTUI,
		Metadata: domain.SessionMetadata{RuntimeLaunchID: "launch", AgentSessionID: "kimi-1", AgentSessionIDLaunchID: "launch"}}
	manager := New(store, nil)
	apply := func(signal ports.ActivitySignal) {
		t.Helper()
		signal.Valid, signal.LaunchID = true, "launch"
		if err := manager.ApplyActivitySignal(context.Background(), "s-1", signal); err != nil {
			t.Fatal(err)
		}
	}
	apply(ports.ActivitySignal{Event: "user-prompt-submit", State: domain.ActivityActive, TurnUserRequest: "fix the importer"})
	apply(ports.ActivitySignal{Event: "permission-request", State: domain.ActivityWaitingInput})
	apply(ports.ActivitySignal{Event: "stop-failure", State: domain.ActivityIdle})
	if got := store.sessions["s-1"].Activity.State; got != domain.ActivityIdle {
		t.Fatalf("activity after stop-failure = %q, want idle", got)
	}
	apply(ports.ActivitySignal{Event: "user-prompt-submit", State: domain.ActivityActive, TurnUserRequest: "fix the importer"})
	apply(ports.ActivitySignal{Event: "stop", State: domain.ActivityIdle, FinalAssistantMessage: "importer fixed"})

	want := []string{
		"request:kimi:kimi-1:fix the importer", "response:",
		"request:kimi:kimi-1:fix the importer", "response:importer fixed",
	}
	if !reflect.DeepEqual(store.calls, want) {
		t.Fatalf("recorded %q, want %q", store.calls, want)
	}
}

// Kimi fires StopFailure per agent with no agent id. A child's failure must not
// end the parent's turn: the parent keeps working and its own Stop answers the
// turn. Once the child is closed, the parent's own failure still ends a turn.
func TestKimiChildStopFailureKeepsTheParentWorking(t *testing.T) {
	store := &turnRecordingStore{fakeStore: newFakeStore()}
	store.sessions["s-1"] = domain.SessionRecord{ID: "s-1", Harness: domain.HarnessKimi, Mode: domain.SessionModeTUI,
		Metadata: domain.SessionMetadata{RuntimeLaunchID: "launch", AgentSessionID: "kimi-1", AgentSessionIDLaunchID: "launch"}}
	manager := New(store, nil)
	apply := func(signal ports.ActivitySignal) {
		t.Helper()
		signal.Valid, signal.LaunchID, signal.ExpectedHarness = signal.State != "", "launch", domain.HarnessKimi
		signal.AgentSessionID = "kimi-1"
		if err := manager.ApplyActivitySignal(context.Background(), "s-1", signal); err != nil {
			t.Fatal(err)
		}
	}
	apply(ports.ActivitySignal{Event: "user-prompt-submit", State: domain.ActivityActive, TurnUserRequest: "map the importer"})
	apply(ports.ActivitySignal{Event: "subagent-start"})
	// The Explore child's provider request fails; the parent continues.
	apply(ports.ActivitySignal{Event: "stop-failure", State: domain.ActivityIdle})
	if got := store.sessions["s-1"].Activity.State; got != domain.ActivityActive {
		t.Fatalf("activity after the child's stop-failure = %q, want active", got)
	}
	apply(ports.ActivitySignal{Event: "stop", State: domain.ActivityIdle, FinalAssistantMessage: "importer mapped"})
	if got := store.sessions["s-1"].Activity.State; got != domain.ActivityIdle {
		t.Fatalf("activity after the parent's stop = %q, want idle", got)
	}

	apply(ports.ActivitySignal{Event: "user-prompt-submit", State: domain.ActivityActive, TurnUserRequest: "map the exporter"})
	apply(ports.ActivitySignal{Event: "subagent-start"})
	apply(ports.ActivitySignal{Event: "subagent-stop"})
	apply(ports.ActivitySignal{Event: "stop-failure", State: domain.ActivityIdle})
	if got := store.sessions["s-1"].Activity.State; got != domain.ActivityIdle {
		t.Fatalf("activity after the parent's stop-failure = %q, want idle", got)
	}

	want := []string{
		"request:kimi:kimi-1:map the importer", "response:importer mapped",
		"request:kimi:kimi-1:map the exporter", "response:",
	}
	if !reflect.DeepEqual(store.calls, want) {
		t.Fatalf("recorded %q, want %q", store.calls, want)
	}
}

// A superseded runtime's late subagent-start must not count a child, or the
// current runtime's own stop-failure would be taken for that child's.
func TestKimiStaleLaunchSubagentStartDoesNotSwallowStopFailure(t *testing.T) {
	store := &turnRecordingStore{fakeStore: newFakeStore()}
	store.sessions["s-1"] = domain.SessionRecord{ID: "s-1", Harness: domain.HarnessKimi, Mode: domain.SessionModeTUI,
		Metadata: domain.SessionMetadata{RuntimeLaunchID: "launch-2", AgentSessionID: "kimi-1", AgentSessionIDLaunchID: "launch-2"}}
	manager := New(store, nil)
	apply := func(launch string, signal ports.ActivitySignal) {
		t.Helper()
		signal.Valid, signal.LaunchID, signal.ExpectedHarness = signal.State != "", launch, domain.HarnessKimi
		signal.AgentSessionID = "kimi-1"
		if err := manager.ApplyActivitySignal(context.Background(), "s-1", signal); err != nil {
			t.Fatal(err)
		}
	}
	apply("launch-2", ports.ActivitySignal{Event: "user-prompt-submit", State: domain.ActivityActive, TurnUserRequest: "map the importer"})
	apply("launch-1", ports.ActivitySignal{Event: "subagent-start"})
	apply("launch-2", ports.ActivitySignal{Event: "stop-failure", State: domain.ActivityIdle})
	if got := store.sessions["s-1"].Activity.State; got != domain.ActivityIdle {
		t.Fatalf("activity after the current launch's stop-failure = %q, want idle", got)
	}
}

// A child counted while an earlier launch was current must not swallow the
// replacement launch's own StopFailure, even when the new launch's opening
// hooks never reached AO.
func TestKimiEarlierLaunchChildCountDoesNotSwallowStopFailure(t *testing.T) {
	store := &turnRecordingStore{fakeStore: newFakeStore()}
	store.sessions["s-1"] = domain.SessionRecord{ID: "s-1", Harness: domain.HarnessKimi, Mode: domain.SessionModeTUI,
		Metadata: domain.SessionMetadata{RuntimeLaunchID: "launch-1", AgentSessionID: "kimi-1", AgentSessionIDLaunchID: "launch-1"}}
	manager := New(store, nil)
	apply := func(launch string, signal ports.ActivitySignal) {
		t.Helper()
		signal.Valid, signal.LaunchID, signal.ExpectedHarness = signal.State != "", launch, domain.HarnessKimi
		signal.AgentSessionID = "kimi-1"
		if err := manager.ApplyActivitySignal(context.Background(), "s-1", signal); err != nil {
			t.Fatal(err)
		}
	}
	apply("launch-1", ports.ActivitySignal{Event: "user-prompt-submit", State: domain.ActivityActive, TurnUserRequest: "map the importer"})
	apply("launch-1", ports.ActivitySignal{Event: "subagent-start"})

	// The runtime is replaced while the child is open.
	rec := store.sessions["s-1"]
	rec.Metadata.RuntimeLaunchID, rec.Metadata.AgentSessionIDLaunchID = "launch-2", "launch-2"
	store.sessions["s-1"] = rec

	apply("launch-2", ports.ActivitySignal{Event: "stop-failure", State: domain.ActivityIdle})
	if got := store.sessions["s-1"].Activity.State; got != domain.ActivityIdle {
		t.Fatalf("activity after the new launch's stop-failure = %q, want idle", got)
	}
}

// A StopFailure from an earlier turn that reaches AO late must not close one
// of this turn's children, or the child's own failure would idle the parent.
func TestKimiEarlierTurnStopFailureDoesNotCloseACurrentChild(t *testing.T) {
	start := time.Unix(1_000, 0).UTC()
	store := &turnRecordingStore{fakeStore: newFakeStore()}
	store.sessions["s-1"] = domain.SessionRecord{ID: "s-1", Harness: domain.HarnessKimi, Mode: domain.SessionModeTUI,
		Metadata: domain.SessionMetadata{RuntimeLaunchID: "launch", AgentSessionID: "kimi-1", AgentSessionIDLaunchID: "launch"}}
	manager := New(store, nil)
	manager.clock = func() time.Time { return start.Add(time.Minute) }
	apply := func(at time.Duration, signal ports.ActivitySignal) {
		t.Helper()
		signal.Valid, signal.LaunchID, signal.ExpectedHarness = signal.State != "", "launch", domain.HarnessKimi
		signal.AgentSessionID, signal.Timestamp = "kimi-1", start.Add(at)
		if err := manager.ApplyActivitySignal(context.Background(), "s-1", signal); err != nil {
			t.Fatal(err)
		}
	}
	apply(0, ports.ActivitySignal{Event: "user-prompt-submit", State: domain.ActivityActive, TurnUserRequest: "map the importer"})
	apply(20*time.Second, ports.ActivitySignal{Event: "user-prompt-submit", State: domain.ActivityActive, TurnUserRequest: "map the exporter"})
	apply(21*time.Second, ports.ActivitySignal{Event: "subagent-start"})
	// The first turn's failure, stamped before the second prompt, lands now.
	apply(10*time.Second, ports.ActivitySignal{Event: "stop-failure", State: domain.ActivityIdle})
	if got := store.sessions["s-1"].Activity.State; got != domain.ActivityActive {
		t.Fatalf("activity after the earlier turn's stop-failure = %q, want active", got)
	}
	// The current child fails; the parent keeps working.
	apply(30*time.Second, ports.ActivitySignal{Event: "stop-failure", State: domain.ActivityIdle})
	if got := store.sessions["s-1"].Activity.State; got != domain.ActivityActive {
		t.Fatalf("activity after the current child's stop-failure = %q, want active", got)
	}
}

// A child that is cancelled or times out reports neither SubagentStop nor
// StopFailure, so its count stays open and the parent's own StopFailure is
// taken for the child's. The observer is then asked to read the screen early;
// once it proves the parent back at its prompt, the failed turn closes
// unanswered and the retry is a turn of its own.
func TestKimiUnreportedChildEndRechecksTheScreen(t *testing.T) {
	start := time.Unix(1_000, 0).UTC()
	now := start
	store := &turnRecordingStore{fakeStore: newFakeStore()}
	store.sessions["s-1"] = domain.SessionRecord{ID: "s-1", Harness: domain.HarnessKimi, Mode: domain.SessionModeTUI,
		Metadata: domain.SessionMetadata{RuntimeLaunchID: "launch", AgentSessionID: "kimi-1", AgentSessionIDLaunchID: "launch"}}
	manager := New(store, nil)
	manager.clock = func() time.Time { return now }
	hook := func(signal ports.ActivitySignal) {
		t.Helper()
		signal.Valid, signal.LaunchID, signal.ExpectedHarness = signal.State != "", "launch", domain.HarnessKimi
		signal.AgentSessionID, signal.Timestamp = "kimi-1", now
		if err := manager.ApplyActivitySignal(context.Background(), "s-1", signal); err != nil {
			t.Fatal(err)
		}
	}
	hook(ports.ActivitySignal{Event: "user-prompt-submit", State: domain.ActivityActive, TurnUserRequest: "map the importer"})
	// The child times out without a hook; the parent's next request fails.
	hook(ports.ActivitySignal{Event: "subagent-start"})
	hook(ports.ActivitySignal{Event: "stop-failure", State: domain.ActivityIdle})
	if got := store.sessions["s-1"].Activity.State; got != domain.ActivityActive {
		t.Fatalf("activity after the attributed stop-failure = %q, want active", got)
	}
	if manager.TerminalActivityRecheckDue("s-1", start.Add(time.Second)) {
		t.Fatal("recheck due before Kimi could redraw")
	}
	if !manager.TerminalActivityRecheckDue("s-1", start.Add(kimiChildFailureRecheckSettle)) {
		t.Fatal("recheck not due after the settle delay")
	}

	// The observer reads Kimi's composer with no spinner.
	now = start.Add(30 * time.Second)
	if err := manager.ApplyActivitySignal(context.Background(), "s-1", ports.ActivitySignal{
		Event: "terminal-idle", State: domain.ActivityIdle, Valid: true, LaunchID: "launch", Timestamp: now,
	}); err != nil {
		t.Fatal(err)
	}
	if got := store.sessions["s-1"].Activity.State; got != domain.ActivityIdle {
		t.Fatalf("activity after terminal-idle = %q, want idle", got)
	}
	if manager.TerminalActivityRecheckDue("s-1", now) {
		t.Fatal("recheck still due after the screen settled the failure")
	}

	now = start.Add(time.Minute)
	hook(ports.ActivitySignal{Event: "user-prompt-submit", State: domain.ActivityActive, TurnUserRequest: "map the importer"})
	hook(ports.ActivitySignal{Event: "stop", State: domain.ActivityIdle, FinalAssistantMessage: "importer mapped"})
	want := []string{
		"request:kimi:kimi-1:map the importer", "response:",
		"request:kimi:kimi-1:map the importer", "response:importer mapped",
	}
	if !reflect.DeepEqual(store.calls, want) {
		t.Fatalf("recorded %q, want %q", store.calls, want)
	}
}

// The early read is bounded: after the window the observer's ordinary
// stale-activity read takes over. The failure outlives the window, so that
// later read still closes the failed turn.
func TestKimiChildFailureRecheckExpires(t *testing.T) {
	start := time.Unix(1_000, 0).UTC()
	now := start
	store := &turnRecordingStore{fakeStore: newFakeStore()}
	store.sessions["s-1"] = domain.SessionRecord{ID: "s-1", Harness: domain.HarnessKimi, Mode: domain.SessionModeTUI,
		Metadata: domain.SessionMetadata{RuntimeLaunchID: "launch", AgentSessionID: "kimi-1", AgentSessionIDLaunchID: "launch"}}
	manager := New(store, nil)
	manager.clock = func() time.Time { return now }
	for _, signal := range []ports.ActivitySignal{
		{Event: "user-prompt-submit", State: domain.ActivityActive, TurnUserRequest: "map the importer"},
		{Event: "subagent-start"},
		{Event: "stop-failure", State: domain.ActivityIdle},
	} {
		signal.Valid, signal.LaunchID, signal.ExpectedHarness = signal.State != "", "launch", domain.HarnessKimi
		signal.AgentSessionID = "kimi-1"
		if err := manager.ApplyActivitySignal(context.Background(), "s-1", signal); err != nil {
			t.Fatal(err)
		}
	}
	if manager.TerminalActivityRecheckDue("s-1", start.Add(kimiChildFailureRecheckWindow+time.Second)) {
		t.Fatal("recheck due after its window")
	}

	// The ordinary stale read finds Kimi idle after the window.
	now = start.Add(3 * time.Minute)
	if err := manager.ApplyActivitySignal(context.Background(), "s-1", ports.ActivitySignal{
		Event: "terminal-idle", State: domain.ActivityIdle, Valid: true, LaunchID: "launch", Timestamp: now,
	}); err != nil {
		t.Fatal(err)
	}
	want := []string{"request:kimi:kimi-1:map the importer", "response:"}
	if !reflect.DeepEqual(store.calls, want) {
		t.Fatalf("recorded %q, want %q", store.calls, want)
	}
}

// After a provider switch the previous agent's hook config can still be in the
// workspace. A signal that names an agent the session no longer runs is dropped
// before it can record a turn or change activity.
func TestHookSignalFromAPreviousAgentIsIgnored(t *testing.T) {
	store := &turnRecordingStore{fakeStore: newFakeStore()}
	store.sessions["s-1"] = domain.SessionRecord{ID: "s-1", Harness: domain.HarnessCodex, Mode: domain.SessionModeTUI,
		Metadata: domain.SessionMetadata{RuntimeLaunchID: "launch", AgentSessionID: "thread-1", AgentSessionIDLaunchID: "launch"}}
	manager := New(store, nil)
	for _, harness := range []domain.AgentHarness{domain.HarnessClaudeCode, domain.HarnessCodex} {
		if err := manager.ApplyActivitySignal(context.Background(), "s-1", ports.ActivitySignal{
			Event: "user-prompt-submit", TurnUserRequest: "ask from " + string(harness), ExpectedHarness: harness,
			LaunchID: "launch", Valid: true, State: domain.ActivityActive,
		}); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{"request:codex:thread-1:ask from codex"}
	if !reflect.DeepEqual(store.calls, want) {
		t.Fatalf("recorded %q, want only the running agent's turn %q", store.calls, want)
	}
}
