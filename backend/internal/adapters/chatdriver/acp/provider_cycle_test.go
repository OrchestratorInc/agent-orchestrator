package acp

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// The wire shape below is an anonymized replay of what `agy acp` 1.5.14 sends:
// a turn ends with end_turn while a command it ran with WaitMsBeforeAsync is
// still in progress; when the command finishes, agy runs a model cycle on its
// own, with no prompt, no messageId, and no start or end marker, and closes
// each model response with a usage_update.
func TestACPProviderCycleAfterTurnSettlesAndStaysOutOfTheNextTurn(t *testing.T) {
	restore := setProviderCycleWaits(50*time.Millisecond, time.Minute)
	defer restore()

	agent := &fakeAgent{}
	var prompts int
	var promptMu sync.Mutex
	agent.customPrompt = func(ctx context.Context, params acpsdk.PromptRequest) (acpsdk.PromptResponse, error) {
		promptMu.Lock()
		prompts++
		n := prompts
		promptMu.Unlock()
		send := func(update acpsdk.SessionUpdate) {
			_ = agent.conn.SessionUpdate(ctx, acpsdk.SessionNotification{SessionId: params.SessionId, Update: update})
		}
		if n == 1 {
			send(acpsdk.UpdateAgentThoughtText("Checking the change."))
			send(acpsdk.UpdateAgentMessageText("Started the checks; waiting for them to finish."))
			send(acpsdk.StartToolCall("call-bg", "run checks",
				acpsdk.WithStartKind(acpsdk.ToolKindExecute),
				acpsdk.WithStartStatus(acpsdk.ToolCallStatusInProgress),
				acpsdk.WithStartRawInput(map[string]any{"CommandLine": "run checks", "WaitMsBeforeAsync": 500})))
			send(usageUpdate())
			return acpsdk.PromptResponse{StopReason: acpsdk.StopReasonEndTurn}, nil
		}
		send(acpsdk.UpdateAgentMessageText("Reply to the user."))
		send(usageUpdate())
		return acpsdk.PromptResponse{StopReason: acpsdk.StopReasonEndTurn}, nil
	}
	driver := New(Config{
		Harness:          domain.HarnessAgy,
		Capabilities:     ports.ChatCapabilities{ports.ChatCapabilityStreaming: true, ports.ChatCapabilityTools: true},
		Probe:            func(context.Context) error { return nil },
		Launch:           func(context.Context, LaunchConfig) (Launch, error) { return Launch{Command: "fake"}, nil },
		ToolsOutliveTurn: true,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	driver.useTestProcess(fakeSpawn(agent))
	opened, err := driver.Start(context.Background(), ports.ChatStartConfig{WorkspacePath: t.TempDir()})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer opened.Close()
	conv := opened.(*conversation)
	events := opened.Events()
	push := func(update acpsdk.SessionUpdate) {
		t.Helper()
		if err := agent.conn.SessionUpdate(context.Background(), acpsdk.SessionNotification{
			SessionId: acpsdk.SessionId(opened.ProviderConversationID()), Update: update,
		}); err != nil {
			t.Fatalf("session/update: %v", err)
		}
	}

	// Turn 1 ends with the command still running in the background.
	turn1 := startTurn(t, opened, "run the checks")
	seen := collectUntil(t, events, func(e ports.ChatEvent) bool {
		return e.Kind == ports.ChatEventTurnCompleted && e.ProviderTurnID == turn1
	})
	seen = append(seen, collectUntil(t, events, func(e ports.ChatEvent) bool {
		return e.Kind == ports.ChatEventControllerState && e.ControllerState == ports.ChatControllerReady
	})...)
	for _, e := range seen {
		if e.ProviderItemID == conv.providerItemID("call-bg") && e.ActivityStatus == domain.ActivityStatusFailed {
			t.Fatalf("a command still running in the background was failed at turn end: %+v", e)
		}
	}
	if tasks, _ := conv.BackgroundTasks(context.Background()); len(tasks) != 1 || tasks[0].ID != "call-bg" || tasks[0].State != "running" ||
		tasks[0].Kind != "shell" || tasks[0].Command != "run checks" {
		t.Fatalf("background tasks after turn end = %+v, want call-bg running", tasks)
	}
	if ok, _ := conv.CanHibernate(context.Background()); ok {
		t.Fatal("session could hibernate while its background command runs")
	}

	// The command finishes, and agy answers it on its own.
	push(acpsdk.UpdateToolCall("call-bg", acpsdk.WithUpdateStatus(acpsdk.ToolCallStatusCompleted),
		acpsdk.WithUpdateRawOutput(map[string]any{"output": "Command exited with code 0.", "exit_code": 0})))
	push(acpsdk.UpdateAgentThoughtText("The checks finished."))
	push(acpsdk.UpdateAgentMessageText("All checks passed. "))
	push(acpsdk.StartToolCall("call-2", "git status",
		acpsdk.WithStartKind(acpsdk.ToolKindExecute), acpsdk.WithStartStatus(acpsdk.ToolCallStatusInProgress)))
	push(acpsdk.UpdateToolCall("call-2", acpsdk.WithUpdateStatus(acpsdk.ToolCallStatusCompleted)))
	push(acpsdk.UpdateAgentMessageText("Nothing else to do."))
	push(usageUpdate())

	cycle1 := collectUntil(t, events, isProviderState(domain.ThreadStatusIdle))
	if tasks, _ := conv.BackgroundTasks(context.Background()); len(tasks) != 1 || tasks[0].State != "completed" {
		t.Fatalf("background tasks after the command finished = %+v, want completed", tasks)
	}
	if !hasEvent(cycle1, isProviderState(domain.ThreadStatusActive)) {
		t.Fatal("the agent's own cycle never reported the agent as working")
	}
	message1 := settledMessage(t, cycle1)
	if message1.Text != "All checks passed. Nothing else to do." || message1.ProviderTurnID != "" {
		t.Fatalf("cycle message = %+v", message1)
	}
	if strings.HasSuffix(message1.ProviderItemID, "assistant-") {
		t.Fatalf("cycle message uses the shared turn-less id %q", message1.ProviderItemID)
	}
	if !hasEvent(cycle1, func(e ports.ChatEvent) bool {
		return e.Kind == ports.ChatEventActivityCompleted && e.ActivityKind == domain.ActivityKindReasoning &&
			e.ActivityStatus == domain.ActivityStatusCompleted && e.Text == "The checks finished."
	}) {
		t.Fatal("the cycle's reasoning was left running")
	}
	for _, e := range cycle1 {
		if e.ProviderTurnID == turn1 {
			t.Fatalf("the agent's own cycle was attributed to the finished turn: %+v", e)
		}
	}

	// A later cycle is its own message, not more text on the first one.
	push(acpsdk.UpdateAgentMessageText("Second note."))
	push(usageUpdate())
	message2 := settledMessage(t, collectUntil(t, events, isProviderState(domain.ThreadStatusIdle)))
	if message2.ProviderItemID == message1.ProviderItemID || message2.Text != "Second note." {
		t.Fatalf("second cycle message = %+v, first = %+v", message2, message1)
	}

	// A cycle still streaming when the user sends is settled before the turn
	// starts, and the turn's own reply does not join it.
	push(acpsdk.UpdateAgentMessageText("Still looking"))
	open := collectUntil(t, events, func(e ports.ChatEvent) bool { return e.Kind == ports.ChatEventMessageDelta })
	openID := open[len(open)-1].ProviderItemID
	turn2 := startTurn(t, opened, "what next?")
	next := collectUntil(t, events, func(e ports.ChatEvent) bool {
		return e.Kind == ports.ChatEventTurnCompleted && e.ProviderTurnID == turn2
	})
	settledAt, startedAt := -1, -1
	for i, e := range next {
		switch {
		case e.Kind == ports.ChatEventMessageCompleted && e.ProviderItemID == openID:
			settledAt = i
			if e.ProviderTurnID != "" || e.Text != "Still looking" {
				t.Fatalf("open cycle settled as %+v", e)
			}
		case e.Kind == ports.ChatEventTurnStarted && e.ProviderTurnID == turn2:
			startedAt = i
		case e.Kind == ports.ChatEventMessageDelta:
			if e.ProviderTurnID != turn2 || e.ProviderItemID == openID {
				t.Fatalf("turn reply leaked into the agent's cycle: %+v", e)
			}
		}
	}
	if settledAt < 0 || startedAt < 0 || settledAt > startedAt {
		t.Fatalf("open cycle settled at %d, turn started at %d: %+v", settledAt, startedAt, next)
	}
}

// A usage_update before a tool call closed the response that asked for the
// tool. The agent answers the tool's result in a new response, so the tool's
// completion must not end the cycle at the short grace; the new response's
// own usage_update does.
func TestACPProviderCycleWaitsForTheResponseAfterAToolCompletes(t *testing.T) {
	restore := setProviderCycleWaits(20*time.Millisecond, time.Hour)
	defer restore()
	conv := &conversation{
		log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		sessionID: "s", events: make(chan ports.ChatEvent, 32),
		messages: map[string]string{}, thoughts: map[string]string{},
		nestedMessages: map[string]nestedMessageState{}, tools: map[string]*toolState{},
	}
	update := func(u acpsdk.SessionUpdate) {
		t.Helper()
		if err := conv.processUpdate(acpsdk.SessionNotification{SessionId: "s", Update: u}); err != nil {
			t.Fatal(err)
		}
	}
	update(acpsdk.UpdateAgentMessageText("Checking."))
	update(usageUpdate())
	update(acpsdk.StartToolCall("call-1", "git status",
		acpsdk.WithStartKind(acpsdk.ToolKindExecute), acpsdk.WithStartStatus(acpsdk.ToolCallStatusInProgress)))
	update(acpsdk.UpdateToolCall("call-1", acpsdk.WithUpdateStatus(acpsdk.ToolCallStatusCompleted)))
	time.Sleep(100 * time.Millisecond)
	for len(conv.events) > 0 {
		if e := <-conv.events; e.Kind == ports.ChatEventMessageCompleted || isProviderState(domain.ThreadStatusIdle)(e) {
			t.Fatalf("cycle ended before the agent answered its tool: %+v", e)
		}
	}
	update(acpsdk.UpdateAgentMessageText(" Clean."))
	update(usageUpdate())
	message := settledMessage(t, collectUntil(t, conv.events, isProviderState(domain.ThreadStatusIdle)))
	if message.Text != "Checking. Clean." {
		t.Fatalf("settled message = %+v", message)
	}
}

// Claude Code reports its own running and idle states; its idle ends the
// cycle, and AO adds no state of its own.
func TestACPProviderCycleEndsOnTheProvidersOwnIdle(t *testing.T) {
	restore := setProviderCycleWaits(time.Hour, time.Hour)
	defer restore()
	conv := &conversation{
		log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		sessionID: "s", events: make(chan ports.ChatEvent, 32),
		messages: map[string]string{}, thoughts: map[string]string{},
		nestedMessages: map[string]nestedMessageState{}, tools: map[string]*toolState{},
	}
	notify := func(state string) {
		conv.handleNotification(claudeSDKMessageMethod, []byte(`{"sessionId":"s","message":{"type":"system","subtype":"session_state_changed","state":"`+state+`"}}`))
	}
	notify("running")
	if err := conv.processUpdate(acpsdk.SessionNotification{SessionId: "s", Update: acpsdk.UpdateAgentMessageText("hi")}); err != nil {
		t.Fatal(err)
	}
	notify("idle")
	var got []ports.ChatEvent
	for len(conv.events) > 0 {
		got = append(got, <-conv.events)
	}
	kinds := make([]string, 0, len(got))
	for _, e := range got {
		kind := string(e.Kind)
		if e.ThreadState != nil {
			kind += ":" + string(e.ThreadState.Status)
		}
		kinds = append(kinds, kind)
	}
	want := []string{"provider.state:active", string(ports.ChatEventMessageDelta), string(ports.ChatEventMessageCompleted), "provider.state:idle"}
	if strings.Join(kinds, ",") != strings.Join(want, ",") {
		t.Fatalf("events = %v, want %v", kinds, want)
	}
}

func setProviderCycleWaits(grace, quiet time.Duration) func() {
	oldGrace, oldQuiet := providerCycleSettleGrace, providerCycleQuietWait
	providerCycleSettleGrace, providerCycleQuietWait = grace, quiet
	return func() { providerCycleSettleGrace, providerCycleQuietWait = oldGrace, oldQuiet }
}

func usageUpdate() acpsdk.SessionUpdate {
	return acpsdk.SessionUpdate{UsageUpdate: &acpsdk.SessionUsageUpdate{SessionUpdate: "usage_update", Used: 10, Size: 100}}
}

func startTurn(t *testing.T, conv ports.ChatConversation, text string) string {
	t.Helper()
	ref, err := conv.SendTurn(context.Background(), ports.ChatUserMessage{Text: text})
	if err != nil {
		t.Fatalf("SendTurn: %v", err)
	}
	if err := conv.(ports.ChatDeferredTurnStarter).StartDeferredTurn(ref.ProviderTurnID); err != nil {
		t.Fatalf("StartDeferredTurn: %v", err)
	}
	return ref.ProviderTurnID
}

func collectUntil(t *testing.T, events <-chan ports.ChatEvent, done func(ports.ChatEvent) bool) []ports.ChatEvent {
	t.Helper()
	var seen []ports.ChatEvent
	for {
		event := nextEvent(t, events)
		seen = append(seen, event)
		if done(event) {
			return seen
		}
	}
}

func isProviderState(status domain.ThreadStatus) func(ports.ChatEvent) bool {
	return func(e ports.ChatEvent) bool {
		return e.Kind == ports.ChatEventProviderState && e.ThreadState != nil && e.ThreadState.Status == status
	}
}

func hasEvent(events []ports.ChatEvent, match func(ports.ChatEvent) bool) bool {
	for _, e := range events {
		if match(e) {
			return true
		}
	}
	return false
}

func settledMessage(t *testing.T, events []ports.ChatEvent) ports.ChatEvent {
	t.Helper()
	for _, e := range events {
		if e.Kind == ports.ChatEventMessageCompleted {
			return e
		}
	}
	t.Fatalf("no settled message in %+v", events)
	return ports.ChatEvent{}
}

// A turn can start, and detach the cycle, after an update entered the cycle
// but before it wrote its text. The text must not land in the turn's maps: it
// goes on the cycle's settled copy and comes back to be settled again whole.
func TestACPProviderCycleTextAfterDetachSettlesWithTheCycle(t *testing.T) {
	c := &conversation{messages: map[string]string{}, thoughts: map[string]string{},
		nestedMessages: map[string]nestedMessageState{}}
	cycle := &providerCycle{id: "cycle-1", items: map[string]bool{}, tools: map[string]bool{}}
	c.cycle = cycle
	if _, late := c.appendItemText(cycle, itemMessage, "assistant-cycle-1", "", "All checks"); late != nil {
		t.Fatalf("open cycle returned a late settle: %+v", late)
	}

	c.mu.Lock()
	settled := c.detachProviderCycleLocked()
	c.messages = map[string]string{} // the turn's fresh maps
	c.thoughts = map[string]string{}
	c.mu.Unlock()
	if got := settled.messages["assistant-cycle-1"]; got != "All checks" {
		t.Fatalf("settled at detach = %q", got)
	}

	existed, late := c.appendItemText(cycle, itemMessage, "assistant-cycle-1", "", " passed.")
	if !existed || late == nil || late.messages["assistant-cycle-1"] != "All checks passed." {
		t.Fatalf("late message = %v, %+v; want the whole text to settle again", existed, late)
	}
	existed, late = c.appendItemText(cycle, itemThought, "thought-cycle-1", "", "thinking")
	if existed || late == nil || late.thoughts["thought-cycle-1"] != "thinking" {
		t.Fatalf("late thought = %v, %+v; want a new item to settle", existed, late)
	}
	if len(c.messages) != 0 || len(c.thoughts) != 0 {
		t.Fatalf("cycle text landed in the turn's maps: %v %v", c.messages, c.thoughts)
	}
}
