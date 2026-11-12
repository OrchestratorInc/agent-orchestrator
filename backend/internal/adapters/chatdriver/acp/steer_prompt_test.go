package acp

import (
	"context"
	"errors"
	"io"
	"log/slog"
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
	active, running, segment := conv.activeTurn, len(conv.outstanding), conv.steerSegment
	conv.mu.Unlock()
	if active != turn || running != 1 || segment != 0 {
		t.Fatalf("after refusal active=%q running prompts=%d segment=%d, want %q, 1, 0", active, running, segment, turn)
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
