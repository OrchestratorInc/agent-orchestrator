package acp

import (
	"context"
	"encoding/json"
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

const testDrainMethod = "_test/drainMidTurnQueue"

func testDrain(wait time.Duration) *SteerDrain {
	return &SteerDrain{
		Supported: func(init acpsdk.InitializeResponse) bool {
			return init.AgentInfo != nil && init.AgentInfo.Name == "drainer"
		},
		Method: testDrainMethod,
		Response: func(texts []string) any {
			return map[string]any{"messages": texts}
		},
		// No agent here answers it; a failed nudge must not affect delivery.
		Nudge: "test/sendNow",
		Wait:  wait,
	}
}

type drainAgent struct {
	*fakeAgent
	collected chan []string
}

// newDrainAgent runs prompts that ask AO for mid-turn guidance at a steady
// cadence, the way Qwen Code does at its step boundaries, until release closes.
func newDrainAgent(release <-chan struct{}) *drainAgent {
	agent := &drainAgent{fakeAgent: &fakeAgent{}, collected: make(chan []string, 8)}
	agent.customPrompt = func(ctx context.Context, params acpsdk.PromptRequest) (acpsdk.PromptResponse, error) {
		ticker := time.NewTicker(5 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-release:
				return acpsdk.PromptResponse{StopReason: acpsdk.StopReasonEndTurn}, nil
			case <-ctx.Done():
				return acpsdk.PromptResponse{StopReason: acpsdk.StopReasonCancelled}, nil
			case <-ticker.C:
			}
			raw, err := agent.conn.CallExtension(ctx, testDrainMethod, map[string]any{
				"sessionId": string(params.SessionId),
			})
			if err != nil {
				continue
			}
			var response struct {
				Messages []string `json:"messages"`
			}
			_ = json.Unmarshal(raw, &response)
			if len(response.Messages) > 0 {
				agent.collected <- response.Messages
			}
		}
	}
	return agent
}

// startDrainConversation opens a conversation on agent and starts one turn.
func startDrainConversation(
	t *testing.T,
	agent *fakeAgent,
	drain *SteerDrain,
) (*conversation, string) {
	t.Helper()
	driver := New(Config{
		Harness:      domain.HarnessQwen,
		Capabilities: ports.ChatCapabilities{ports.ChatCapabilityStreaming: true},
		Probe:        func(context.Context) error { return nil },
		Launch: func(context.Context, LaunchConfig) (Launch, error) {
			return Launch{Command: "fake"}, nil
		},
		SteerDrain: drain,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	driver.useTestProcess(fakeSpawn(agent))
	opened, err := driver.Start(context.Background(), ports.ChatStartConfig{WorkspacePath: t.TempDir()})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = opened.Close() })
	ref, err := opened.SendTurn(context.Background(), ports.ChatUserMessage{Text: "raise the timeout"})
	if err != nil {
		t.Fatalf("SendTurn: %v", err)
	}
	if err := opened.(ports.ChatDeferredTurnStarter).StartDeferredTurn(ref.ProviderTurnID); err != nil {
		t.Fatalf("StartDeferredTurn: %v", err)
	}
	return opened.(*conversation), ref.ProviderTurnID
}

// acceptImages lets prompts carry images, so a drain's own text-only rule is
// what refuses them.
func acceptImages(agent *fakeAgent) {
	agent.capabilities = &acpsdk.AgentCapabilities{
		PromptCapabilities:  acpsdk.PromptCapabilities{Image: true},
		SessionCapabilities: acpsdk.SessionCapabilities{Resume: &acpsdk.SessionResumeCapabilities{}},
	}
}

func TestACPSteerDrainReportsInjectedOnlyAfterTheAgentCollects(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	agent := newDrainAgent(release)
	acceptImages(agent.fakeAgent)
	conv, turn := startDrainConversation(t, agent.fakeAgent, testDrain(2*time.Second))

	ref, err := conv.Steer(context.Background(), turn, ports.ChatUserMessage{Text: "use staging instead"})
	if err != nil {
		t.Fatalf("Steer: %v", err)
	}
	if ref.ProviderTurnID != turn {
		t.Fatalf("steered turn = %q, want %q", ref.ProviderTurnID, turn)
	}
	select {
	case texts := <-agent.collected:
		if len(texts) != 1 || texts[0] != "use staging instead" {
			t.Fatalf("collected = %#v", texts)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("agent never collected the guidance")
	}
	conv.mu.Lock()
	left := len(conv.pendingSteers)
	conv.mu.Unlock()
	if left != 0 {
		t.Fatalf("pending steers after collection = %d", left)
	}
}

func TestACPSteerDrainWithdrawsGuidanceTheAgentDoesNotCollect(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	agent := &fakeAgent{promptBlock: true}
	acceptImages(agent)
	conv, turn := startDrainConversation(t, agent, testDrain(30*time.Millisecond))

	_, err := conv.Steer(context.Background(), turn, ports.ChatUserMessage{Text: "use staging instead"})
	if !errors.Is(err, ports.ErrChatTurnNotSteerable) {
		t.Fatalf("Steer error = %v, want ErrChatTurnNotSteerable", err)
	}
	// The agent collects only after AO gave up. It must get nothing, or the
	// guidance would reach it after the caller was told it did not.
	result, err := conv.HandleExtensionMethod(context.Background(), testDrainMethod, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("drain after withdrawal: %v", err)
	}
	encoded, _ := json.Marshal(result)
	if string(encoded) != `{"messages":[]}` {
		t.Fatalf("drain after withdrawal = %s, want empty messages", encoded)
	}
}

func TestACPSteerDrainTurnEndingFirstHandsTheGuidanceBack(t *testing.T) {
	end := make(chan struct{})
	agent := &fakeAgent{}
	agent.customPrompt = func(ctx context.Context, _ acpsdk.PromptRequest) (acpsdk.PromptResponse, error) {
		<-end
		return acpsdk.PromptResponse{StopReason: acpsdk.StopReasonEndTurn}, nil
	}
	acceptImages(agent)
	conv, turn := startDrainConversation(t, agent, testDrain(5*time.Second))

	var (
		wg       sync.WaitGroup
		steerErr error
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, steerErr = conv.Steer(context.Background(), turn, ports.ChatUserMessage{Text: "too late"})
	}()
	deadline := time.Now().Add(2 * time.Second)
	for {
		conv.mu.Lock()
		parked := len(conv.pendingSteers)
		conv.mu.Unlock()
		if parked == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("steer was never parked")
		}
		time.Sleep(time.Millisecond)
	}
	close(end)
	wg.Wait()
	if !errors.Is(steerErr, ports.ErrChatNoSteerableTurn) {
		t.Fatalf("Steer error = %v, want ErrChatNoSteerableTurn", steerErr)
	}
}

func TestACPSteerDrainRefusesBlocksItCannotCarry(t *testing.T) {
	agent := &fakeAgent{promptBlock: true}
	acceptImages(agent)
	conv, turn := startDrainConversation(t, agent, testDrain(time.Second))

	_, err := conv.Steer(context.Background(), turn, ports.ChatUserMessage{
		Text:    "look at this",
		Content: []ports.ChatContent{{Type: "image", Data: "aGVsbG8=", MIMEType: "image/png"}},
	})
	if !errors.Is(err, ports.ErrChatSteerContentUnsupported) {
		t.Fatalf("Steer error = %v, want ErrChatSteerContentUnsupported", err)
	}
	conv.mu.Lock()
	parked := len(conv.pendingSteers)
	conv.mu.Unlock()
	if parked != 0 {
		t.Fatalf("pending steers = %d, want none", parked)
	}
}

func TestACPSteerDrainSupportComesFromTheBinding(t *testing.T) {
	init := acpsdk.InitializeResponse{AgentInfo: &acpsdk.Implementation{Name: "drainer"}}
	caps := conversationCapabilities(nil, init, steeringFor(Config{SteerDrain: testDrain(0)}))
	if !caps.Has(ports.ChatCapabilitySteer) {
		t.Fatal("drain binding that reports support did not enable steering")
	}
	other := acpsdk.InitializeResponse{
		AgentInfo: &acpsdk.Implementation{Name: "other"},
		// A drain binding decides by itself; the ACP extension flag is not its signal.
		Meta: map[string]any{"steering": map[string]any{"supported": true}},
	}
	if conversationCapabilities(nil, other, steeringFor(Config{SteerDrain: testDrain(0)})).Has(ports.ChatCapabilitySteer) {
		t.Fatal("drain binding enabled steering for an agent it does not support")
	}
}

func TestACPSteerDrainLegacyMethodIsAliased(t *testing.T) {
	driver := New(Config{
		ClientExtensionAliases: map[string]string{"cursor/ask_question": "_cursor/ask_question"},
		SteerDrain:             &SteerDrain{Method: "craft/drainMidTurnQueue"},
	}, nil)
	aliases := driver.clientExtensionAliases()
	if aliases["craft/drainMidTurnQueue"] != "_craft/drainMidTurnQueue" {
		t.Fatalf("aliases = %#v, want the drain method aliased", aliases)
	}
	if aliases["cursor/ask_question"] != "_cursor/ask_question" {
		t.Fatalf("aliases = %#v, binding alias lost", aliases)
	}
	if driver.cfg.ClientExtensionAliases["craft/drainMidTurnQueue"] != "" {
		t.Fatal("aliasing mutated the binding's own alias map")
	}
}

func TestACPSteerRequestBindingReplacesTheExtension(t *testing.T) {
	var got map[string]any
	agent := &fakeAgent{
		capabilities: &acpsdk.AgentCapabilities{
			Meta:                map[string]any{"vendor": map[string]any{"steering": true}},
			SessionCapabilities: acpsdk.SessionCapabilities{Resume: &acpsdk.SessionResumeCapabilities{}},
		},
		extension: func(method string, raw json.RawMessage) (any, error) {
			if method != "_vendor/steering" {
				return nil, acpsdk.NewMethodNotFound(method)
			}
			_ = json.Unmarshal(raw, &got)
			return map[string]any{"status": "injected"}, nil
		},
	}
	request := &SteerRequest{
		Supported: func(init acpsdk.InitializeResponse) bool {
			vendor, _ := init.AgentCapabilities.Meta["vendor"].(map[string]any)
			return vendor["steering"] == true
		},
		Method: "_vendor/steering",
		Params: func(sessionID string, prompt []acpsdk.ContentBlock) (any, error) {
			return map[string]any{"sessionId": sessionID, "prompt": prompt[0].Text.Text}, nil
		},
		Outcome: func(raw json.RawMessage) (SteerOutcome, error) {
			var response struct {
				Status string `json:"status"`
			}
			_ = json.Unmarshal(raw, &response)
			if response.Status == "injected" {
				return SteerInjected, nil
			}
			return SteerNoRunningTurn, nil
		},
	}
	driver := New(Config{
		Harness:      domain.HarnessKimchi,
		Capabilities: ports.ChatCapabilities{ports.ChatCapabilityStreaming: true},
		Probe:        func(context.Context) error { return nil },
		Launch: func(context.Context, LaunchConfig) (Launch, error) {
			return Launch{Command: "fake"}, nil
		},
		SteerRequest: request,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	driver.useTestProcess(fakeSpawn(agent))
	opened, err := driver.Start(context.Background(), ports.ChatStartConfig{WorkspacePath: t.TempDir()})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer opened.Close()
	if !opened.Capabilities().Has(ports.ChatCapabilitySteer) {
		t.Fatal("vendor advertisement did not enable steering")
	}
	conv := opened.(*conversation)
	conv.mu.Lock()
	conv.activeTurn = "turn-1"
	conv.mu.Unlock()
	ref, err := conv.Steer(context.Background(), "turn-1", ports.ChatUserMessage{Text: "use staging"})
	if err != nil {
		t.Fatalf("Steer: %v", err)
	}
	if ref.ProviderTurnID != "turn-1" || got["prompt"] != "use staging" || got["sessionId"] != opened.ProviderConversationID() {
		t.Fatalf("ref = %#v, request = %#v", ref, got)
	}
}

func TestACPSteeringFailedOutcomeAndInvalidParamsAreRefusals(t *testing.T) {
	for name, answer := range map[string]func() (any, error){
		"failed outcome": func() (any, error) {
			return steeringResponse{Outcome: "failed", Reason: "image preprocessing failed"}, nil
		},
		"invalid params": func() (any, error) {
			return nil, acpsdk.NewInvalidParams(map[string]any{"error": "prompt is empty"})
		},
	} {
		t.Run(name, func(t *testing.T) {
			agent := &fakeAgent{
				steering: true,
				extension: func(string, json.RawMessage) (any, error) {
					return answer()
				},
			}
			driver := New(Config{
				Harness:      domain.HarnessOMP,
				Capabilities: ports.ChatCapabilities{ports.ChatCapabilityStreaming: true},
				Probe:        func(context.Context) error { return nil },
				Launch: func(context.Context, LaunchConfig) (Launch, error) {
					return Launch{Command: "fake"}, nil
				},
			}, slog.New(slog.NewTextHandler(io.Discard, nil)))
			driver.useTestProcess(fakeSpawn(agent))
			opened, err := driver.Start(context.Background(), ports.ChatStartConfig{WorkspacePath: t.TempDir()})
			if err != nil {
				t.Fatalf("Start: %v", err)
			}
			defer opened.Close()
			conv := opened.(*conversation)
			conv.mu.Lock()
			conv.activeTurn = "turn-1"
			conv.mu.Unlock()

			_, err = conv.Steer(context.Background(), "turn-1", ports.ChatUserMessage{Text: "use staging"})
			var refusal interface{ ChatRefusal() bool }
			if !errors.As(err, &refusal) || !refusal.ChatRefusal() {
				t.Fatalf("Steer error = %v, want a definite refusal", err)
			}
		})
	}
}

func TestACPSteerStartsANewMessageForIdlessText(t *testing.T) {
	collected := make(chan struct{})
	agent := &fakeAgent{}
	agent.customPrompt = func(ctx context.Context, params acpsdk.PromptRequest) (acpsdk.PromptResponse, error) {
		say := func(text string) {
			_ = agent.conn.SessionUpdate(ctx, acpsdk.SessionNotification{
				SessionId: params.SessionId,
				Update: acpsdk.SessionUpdate{AgentMessageChunk: &acpsdk.SessionUpdateAgentMessageChunk{
					Content: acpsdk.TextBlock(text),
				}},
			})
		}
		say("editing prod. ")
		for {
			raw, err := agent.conn.CallExtension(ctx, testDrainMethod, map[string]any{"sessionId": string(params.SessionId)})
			if err != nil {
				return acpsdk.PromptResponse{}, err
			}
			var response struct {
				Messages []string `json:"messages"`
			}
			_ = json.Unmarshal(raw, &response)
			if len(response.Messages) > 0 {
				close(collected)
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
		say("switched to staging.")
		return acpsdk.PromptResponse{StopReason: acpsdk.StopReasonEndTurn}, nil
	}
	conv, turn := startDrainConversation(t, agent, testDrain(2*time.Second))
	if _, err := conv.Steer(context.Background(), turn, ports.ChatUserMessage{Text: "use staging"}); err != nil {
		t.Fatalf("Steer: %v", err)
	}
	<-collected

	deltas := map[string]string{}
	var order []string
	for {
		event := nextEvent(t, conv.Events())
		if event.Kind == ports.ChatEventMessageDelta {
			if _, seen := deltas[event.ProviderItemID]; !seen {
				order = append(order, event.ProviderItemID)
			}
			deltas[event.ProviderItemID] += event.Delta
		}
		if event.Kind == ports.ChatEventTurnCompleted {
			break
		}
	}
	if len(order) != 2 {
		t.Fatalf("messages = %#v, want the reply split at the steer", deltas)
	}
	if deltas[order[0]] != "editing prod. " || deltas[order[1]] != "switched to staging." {
		t.Fatalf("messages in order = %q then %q", deltas[order[0]], deltas[order[1]])
	}
}
