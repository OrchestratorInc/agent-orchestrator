package acp

import (
	"context"
	"errors"
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

func testSteerPrompt(wait time.Duration) *SteerPrompt {
	return &SteerPrompt{
		Supported: func(init acpsdk.InitializeResponse) bool {
			return init.AgentInfo != nil && init.AgentInfo.Name == "prompt-steerer"
		},
		Wait: wait,
	}
}

// handoffAgent steers the way Antigravity's `agy acp` does: a second
// session/prompt while one is running makes the agent answer the earlier one
// with end_turn and carry on under the new one. refuse makes it reject the
// second prompt instead, as an agent that does not steer would.
type handoffAgent struct {
	*fakeAgent
	release chan struct{}
	// stop ends every prompt at test cleanup. The SDK's agent side cancels a
	// session's earlier prompt context when a new prompt arrives, which
	// Antigravity does not do, so the fake ignores its request context.
	stop chan struct{}

	mu      sync.Mutex
	running chan struct{} // closed when a newer prompt takes over the work
	prompts []string
	refuse  bool
	started chan struct{}
	steered chan string
}

func newHandoffAgent() *handoffAgent {
	agent := &handoffAgent{
		fakeAgent: &fakeAgent{},
		release:   make(chan struct{}),
		stop:      make(chan struct{}),
		started:   make(chan struct{}, 4),
		steered:   make(chan string, 4),
	}
	agent.customPrompt = agent.prompt
	return agent
}

func (a *handoffAgent) prompt(_ context.Context, params acpsdk.PromptRequest) (acpsdk.PromptResponse, error) {
	text := ""
	for _, block := range params.Prompt {
		if block.Text != nil {
			text += block.Text.Text
		}
	}
	a.mu.Lock()
	if a.running != nil && a.refuse {
		a.mu.Unlock()
		return acpsdk.PromptResponse{}, acpsdk.NewInvalidRequest(map[string]any{"error": "a prompt is already running"})
	}
	handedOff := make(chan struct{})
	if a.running != nil {
		close(a.running) // hand the earlier prompt off
		a.steered <- text
	}
	a.running = handedOff
	a.prompts = append(a.prompts, text)
	a.mu.Unlock()
	a.started <- struct{}{}

	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-handedOff:
			return acpsdk.PromptResponse{StopReason: acpsdk.StopReasonEndTurn}, nil
		case <-a.release:
			a.mu.Lock()
			if a.running == handedOff {
				a.running = nil
			}
			a.mu.Unlock()
			return acpsdk.PromptResponse{StopReason: acpsdk.StopReasonEndTurn}, nil
		case <-a.stop:
			return acpsdk.PromptResponse{StopReason: acpsdk.StopReasonCancelled}, nil
		case <-ticker.C:
			a.fakeAgent.mu.Lock()
			cancelled := a.cancelCalls > 0
			a.fakeAgent.mu.Unlock()
			if cancelled {
				return acpsdk.PromptResponse{StopReason: acpsdk.StopReasonCancelled}, nil
			}
		}
	}
}

func startPromptSteerConversation(t *testing.T, agent *handoffAgent, steer *SteerPrompt) (*conversation, string) {
	t.Helper()
	agent.agentInfo = &acpsdk.Implementation{Name: "prompt-steerer", Version: "1.0.0"}
	driver := New(Config{
		Harness: domain.HarnessAgy,
		Capabilities: ports.ChatCapabilities{
			ports.ChatCapabilityStreaming: true, ports.ChatCapabilityInterrupt: true,
		},
		Probe:       func(context.Context) error { return nil },
		Launch:      func(context.Context, LaunchConfig) (Launch, error) { return Launch{Command: "fake"}, nil },
		SteerPrompt: steer,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	driver.useTestProcess(fakeSpawn(agent.fakeAgent))
	opened, err := driver.Start(context.Background(), ports.ChatStartConfig{WorkspacePath: t.TempDir()})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		close(agent.stop)
		_ = opened.Close()
	})
	ref, err := opened.SendTurn(context.Background(), ports.ChatUserMessage{Text: "edit prod.yaml"})
	if err != nil {
		t.Fatalf("SendTurn: %v", err)
	}
	if err := opened.(ports.ChatDeferredTurnStarter).StartDeferredTurn(ref.ProviderTurnID); err != nil {
		t.Fatalf("StartDeferredTurn: %v", err)
	}
	select {
	case <-agent.started:
	case <-time.After(2 * time.Second):
		t.Fatal("first prompt never reached the agent")
	}
	return opened.(*conversation), ref.ProviderTurnID
}

// turnCompletions collects TurnCompleted events for turn until quiet passes
// with no event.
func turnCompletions(t *testing.T, events <-chan ports.ChatEvent, turn string, quiet time.Duration) []ports.ChatEvent {
	t.Helper()
	var completed []ports.ChatEvent
	for {
		select {
		case event, ok := <-events:
			if !ok {
				return completed
			}
			if event.Kind == ports.ChatEventTurnCompleted && event.ProviderTurnID == turn {
				completed = append(completed, event)
			}
		case <-time.After(quiet):
			return completed
		}
	}
}

func TestACPSteerPromptKeepsOneTurnAcrossTheHandoff(t *testing.T) {
	agent := newHandoffAgent()
	conv, turn := startPromptSteerConversation(t, agent, testSteerPrompt(2*time.Second))
	if !conv.Capabilities()[ports.ChatCapabilitySteer] {
		t.Fatal("Steer capability not offered for an agent the binding supports")
	}

	ref, err := conv.Steer(context.Background(), turn, ports.ChatUserMessage{Text: "edit staging.yaml instead"})
	if err != nil {
		t.Fatalf("Steer: %v", err)
	}
	if ref.ProviderTurnID != turn {
		t.Fatalf("steered turn = %q, want %q", ref.ProviderTurnID, turn)
	}
	if got := <-agent.steered; got != "edit staging.yaml instead" {
		t.Fatalf("agent got steer %q", got)
	}
	// The first prompt has returned end_turn, but the turn is still running.
	if done := turnCompletions(t, conv.Events(), turn, 100*time.Millisecond); len(done) != 0 {
		t.Fatalf("turn completed at the handoff: %+v", done)
	}
	conv.mu.Lock()
	active, segment := conv.activeTurn, conv.steerSegment
	conv.mu.Unlock()
	if active != turn || segment != 1 {
		t.Fatalf("after handoff active=%q segment=%d, want %q and 1", active, segment, turn)
	}

	close(agent.release)
	done := turnCompletions(t, conv.Events(), turn, 500*time.Millisecond)
	if len(done) != 1 || done[0].TurnState != domain.TurnStateCompleted {
		t.Fatalf("turn completions = %+v, want one completed", done)
	}
	conv.mu.Lock()
	active = conv.activeTurn
	conv.mu.Unlock()
	if active != "" {
		t.Fatalf("turn still active after the steer prompt returned: %q", active)
	}
}

func TestACPSteerPromptTakesTwoSteersInOrder(t *testing.T) {
	agent := newHandoffAgent()
	conv, turn := startPromptSteerConversation(t, agent, testSteerPrompt(2*time.Second))
	for _, text := range []string{"use staging", "and keep the timeout at 30"} {
		if _, err := conv.Steer(context.Background(), turn, ports.ChatUserMessage{Text: text}); err != nil {
			t.Fatalf("Steer %q: %v", text, err)
		}
		if got := <-agent.steered; got != text {
			t.Fatalf("agent got %q, want %q", got, text)
		}
	}
	close(agent.release)
	if done := turnCompletions(t, conv.Events(), turn, 500*time.Millisecond); len(done) != 1 {
		t.Fatalf("turn completions = %d, want 1", len(done))
	}
}

func TestACPSteerPromptRefusalLeavesTheEarlierPromptInCharge(t *testing.T) {
	agent := newHandoffAgent()
	agent.refuse = true
	conv, turn := startPromptSteerConversation(t, agent, testSteerPrompt(2*time.Second))

	_, err := conv.Steer(context.Background(), turn, ports.ChatUserMessage{Text: "edit staging.yaml instead"})
	var refusal interface{ ChatRefusal() bool }
	if err == nil || !errors.As(err, &refusal) || !refusal.ChatRefusal() {
		t.Fatalf("Steer error = %v, want a provider refusal", err)
	}
	conv.mu.Lock()
	active, outstanding, segment := conv.activeTurn, len(conv.outstanding), conv.steerSegment
	firstRunning := conv.outstanding[0]
	conv.mu.Unlock()
	if active != turn || outstanding != 1 || !firstRunning || segment != 0 {
		t.Fatalf("after refusal active=%q outstanding=%d first running=%v segment=%d, want %q, only the first prompt, 0",
			active, outstanding, firstRunning, segment, turn)
	}

	close(agent.release)
	done := turnCompletions(t, conv.Events(), turn, 500*time.Millisecond)
	if len(done) != 1 || done[0].TurnState != domain.TurnStateCompleted {
		t.Fatalf("turn completions = %+v, want the first prompt to settle it once", done)
	}
}

func TestACPSteerPromptStopEndsTheWholeTurn(t *testing.T) {
	agent := newHandoffAgent()
	conv, turn := startPromptSteerConversation(t, agent, testSteerPrompt(2*time.Second))
	if _, err := conv.Steer(context.Background(), turn, ports.ChatUserMessage{Text: "use staging"}); err != nil {
		t.Fatalf("Steer: %v", err)
	}
	<-agent.steered
	if err := conv.Interrupt(context.Background(), turn); err != nil {
		t.Fatalf("Interrupt: %v", err)
	}
	done := turnCompletions(t, conv.Events(), turn, 500*time.Millisecond)
	if len(done) != 1 || done[0].TurnState != domain.TurnStateInterrupted {
		t.Fatalf("turn completions = %+v, want one interrupted", done)
	}
}

func TestACPSteerPromptNeedsARunningTurn(t *testing.T) {
	agent := newHandoffAgent()
	conv, turn := startPromptSteerConversation(t, agent, testSteerPrompt(2*time.Second))
	close(agent.release)
	if done := turnCompletions(t, conv.Events(), turn, 500*time.Millisecond); len(done) != 1 {
		t.Fatalf("turn completions = %d, want 1", len(done))
	}
	if _, err := conv.Steer(context.Background(), turn, ports.ChatUserMessage{Text: "too late"}); !errors.Is(err, ports.ErrChatNoSteerableTurn) {
		t.Fatalf("Steer after the turn = %v, want ErrChatNoSteerableTurn", err)
	}
	agent.mu.Lock()
	prompts := len(agent.prompts)
	agent.mu.Unlock()
	if prompts != 1 {
		t.Fatalf("agent received %d prompts, want only the turn's own", prompts)
	}
}

func TestACPSteerPromptSupportComesFromTheBinding(t *testing.T) {
	init := acpsdk.InitializeResponse{AgentInfo: &acpsdk.Implementation{Name: "someone-else"}}
	if steeringFor(Config{SteerPrompt: testSteerPrompt(0)}).supported(init) {
		t.Fatal("prompt steering offered for an agent the binding does not support")
	}
	init.AgentInfo.Name = "prompt-steerer"
	if !steeringFor(Config{SteerPrompt: testSteerPrompt(0)}).supported(init) {
		t.Fatal("prompt steering not offered for an agent the binding supports")
	}
	if steeringFor(Config{SteerPrompt: testSteerPrompt(0), SteerDrain: testDrain(0)}).prompt != nil {
		t.Fatal("a drain must take precedence over prompt steering")
	}
}

// ackAgent steers the way Hermes does: a `/steer <text>` prompt while a turn
// runs is answered at once, after an acknowledgement message, and the running
// prompt keeps the turn. decline makes it acknowledge without taking it.
type ackAgent struct {
	*fakeAgent
	release chan struct{}
	stop    chan struct{}
	started chan struct{}
	decline bool
	// silent answers a /steer without an acknowledgement, as an agent that
	// does not know the command would.
	silent bool

	mu      sync.Mutex
	running bool
	steers  []string
}

func newAckAgent() *ackAgent {
	agent := &ackAgent{fakeAgent: &fakeAgent{}, release: make(chan struct{}), stop: make(chan struct{}), started: make(chan struct{}, 4)}
	agent.customPrompt = agent.prompt
	return agent
}

func (a *ackAgent) prompt(ctx context.Context, params acpsdk.PromptRequest) (acpsdk.PromptResponse, error) {
	text := ""
	for _, block := range params.Prompt {
		if block.Text != nil {
			text += block.Text.Text
		}
	}
	a.mu.Lock()
	if a.running && strings.HasPrefix(text, "/steer ") {
		a.steers = append(a.steers, strings.TrimPrefix(text, "/steer "))
		decline := a.decline
		a.mu.Unlock()
		ack := "ACK: " + strings.TrimPrefix(text, "/steer ")
		if decline {
			ack = "DECLINED: no"
		}
		if a.silent {
			ack = "Unknown command"
		}
		_ = a.conn.SessionUpdate(ctx, acpsdk.SessionNotification{SessionId: params.SessionId, Update: acpsdk.UpdateAgentMessageText(ack)})
		return acpsdk.PromptResponse{StopReason: acpsdk.StopReasonEndTurn}, nil
	}
	a.running = true
	a.mu.Unlock()
	a.started <- struct{}{}
	_ = a.conn.SessionUpdate(ctx, acpsdk.SessionNotification{SessionId: params.SessionId, Update: acpsdk.UpdateAgentMessageText("working on it")})
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-a.release:
			a.mu.Lock()
			a.running = false
			a.mu.Unlock()
			return acpsdk.PromptResponse{StopReason: acpsdk.StopReasonEndTurn}, nil
		case <-a.stop:
			return acpsdk.PromptResponse{StopReason: acpsdk.StopReasonCancelled}, nil
		case <-ticker.C:
			a.fakeAgent.mu.Lock()
			cancelled := a.cancelCalls > 0
			a.fakeAgent.mu.Unlock()
			if cancelled {
				return acpsdk.PromptResponse{StopReason: acpsdk.StopReasonCancelled}, nil
			}
		}
	}
}

func testAckSteerPrompt() *SteerPrompt {
	return &SteerPrompt{
		Supported: func(init acpsdk.InitializeResponse) bool { return true },
		Prompt: func(prompt []acpsdk.ContentBlock) ([]acpsdk.ContentBlock, error) {
			text, err := drainText(prompt)
			if err != nil {
				return nil, err
			}
			return []acpsdk.ContentBlock{acpsdk.TextBlock("/steer " + text)}, nil
		},
		Ack: func(text string) (bool, error) {
			switch {
			case strings.HasPrefix(text, "ACK: "):
				return true, nil
			case strings.HasPrefix(text, "DECLINED: "):
				return true, errors.New(strings.TrimPrefix(text, "DECLINED: "))
			}
			return false, nil
		},
		Wait: 2 * time.Second,
	}
}

func startAckConversation(t *testing.T, agent *ackAgent) (*conversation, string) {
	t.Helper()
	driver := New(Config{
		Harness: domain.HarnessHermes,
		Capabilities: ports.ChatCapabilities{
			ports.ChatCapabilityStreaming: true, ports.ChatCapabilityInterrupt: true,
		},
		Probe:       func(context.Context) error { return nil },
		Launch:      func(context.Context, LaunchConfig) (Launch, error) { return Launch{Command: "fake"}, nil },
		SteerPrompt: testAckSteerPrompt(),
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	driver.useTestProcess(fakeSpawn(agent.fakeAgent))
	opened, err := driver.Start(context.Background(), ports.ChatStartConfig{WorkspacePath: t.TempDir()})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		close(agent.stop)
		_ = opened.Close()
	})
	ref, err := opened.SendTurn(context.Background(), ports.ChatUserMessage{Text: "edit prod.yaml"})
	if err != nil {
		t.Fatalf("SendTurn: %v", err)
	}
	if err := opened.(ports.ChatDeferredTurnStarter).StartDeferredTurn(ref.ProviderTurnID); err != nil {
		t.Fatalf("StartDeferredTurn: %v", err)
	}
	select {
	case <-agent.started:
	case <-time.After(2 * time.Second):
		t.Fatal("first prompt never reached the agent")
	}
	return opened.(*conversation), ref.ProviderTurnID
}

// collectTurn gathers message text and completions for turn until quiet.
func collectTurn(t *testing.T, events <-chan ports.ChatEvent, turn string, quiet time.Duration) (string, []ports.ChatEvent) {
	t.Helper()
	var text strings.Builder
	var completed []ports.ChatEvent
	for {
		select {
		case event, ok := <-events:
			if !ok {
				return text.String(), completed
			}
			if event.ProviderTurnID != turn {
				continue
			}
			switch event.Kind {
			case ports.ChatEventMessageDelta:
				text.WriteString(event.Delta)
			case ports.ChatEventTurnCompleted:
				completed = append(completed, event)
			}
		case <-time.After(quiet):
			return text.String(), completed
		}
	}
}

func TestACPSteerPromptAcknowledgedFirstKeepsTheRunningPrompt(t *testing.T) {
	agent := newAckAgent()
	conv, turn := startAckConversation(t, agent)
	if _, err := conv.Steer(context.Background(), turn, ports.ChatUserMessage{Text: "use staging"}); err != nil {
		t.Fatalf("Steer: %v", err)
	}
	agent.mu.Lock()
	steers := append([]string(nil), agent.steers...)
	agent.mu.Unlock()
	if len(steers) != 1 || steers[0] != "use staging" {
		t.Fatalf("agent got steers %q, want the /steer text", steers)
	}
	text, done := collectTurn(t, conv.Events(), turn, 200*time.Millisecond)
	if len(done) != 0 {
		t.Fatalf("the acknowledged steer prompt completed the turn: %+v", done)
	}
	if strings.Contains(text, "ACK:") {
		t.Fatalf("the acknowledgement reached the reply: %q", text)
	}
	conv.mu.Lock()
	segment := conv.steerSegment
	conv.mu.Unlock()
	if segment != 1 {
		t.Fatalf("steer segment = %d, want 1", segment)
	}

	close(agent.release)
	_, done = collectTurn(t, conv.Events(), turn, 500*time.Millisecond)
	if len(done) != 1 || done[0].TurnState != domain.TurnStateCompleted {
		t.Fatalf("turn completions = %+v, want the running prompt to settle it once", done)
	}
}

func TestACPSteerPromptDeclinedAcknowledgementIsARefusal(t *testing.T) {
	agent := newAckAgent()
	agent.decline = true
	conv, turn := startAckConversation(t, agent)
	_, err := conv.Steer(context.Background(), turn, ports.ChatUserMessage{Text: "use staging"})
	var refusal interface{ ChatRefusal() bool }
	if err == nil || !errors.As(err, &refusal) || !refusal.ChatRefusal() || !strings.Contains(err.Error(), "no") {
		t.Fatalf("Steer error = %v, want a refusal carrying the agent's reason", err)
	}
	close(agent.release)
	text, done := collectTurn(t, conv.Events(), turn, 500*time.Millisecond)
	if len(done) != 1 || strings.Contains(text, "DECLINED") {
		t.Fatalf("completions=%d text=%q, want one completion and no acknowledgement text", len(done), text)
	}
}

func TestACPSteerPromptAcknowledgementOutsideASteerIsOrdinaryText(t *testing.T) {
	agent := newAckAgent()
	conv, turn := startAckConversation(t, agent)
	// No steer was sent, so a reply that happens to look like an
	// acknowledgement is the agent's answer and must be shown.
	if conv.steerAcknowledgement(turn, "ACK: something") {
		t.Fatal("text was hidden as an acknowledgement in a turn that sent no steer")
	}
}

func TestACPSteerPromptWithoutAnAcknowledgementIsARefusal(t *testing.T) {
	agent := newAckAgent()
	agent.silent = true
	conv, turn := startAckConversation(t, agent)
	_, err := conv.Steer(context.Background(), turn, ports.ChatUserMessage{Text: "use staging"})
	var refusal interface{ ChatRefusal() bool }
	if err == nil || !errors.As(err, &refusal) || !refusal.ChatRefusal() {
		t.Fatalf("Steer error = %v, want a refusal when the agent does not acknowledge", err)
	}
	close(agent.release)
	if _, done := collectTurn(t, conv.Events(), turn, 500*time.Millisecond); len(done) != 1 {
		t.Fatalf("turn completions = %d, want 1", len(done))
	}
}
