package acp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	// gate, when set before the turn starts, holds the agent's collection
	// requests until it is closed.
	gate chan struct{}
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
			if agent.gate != nil {
				select {
				case <-agent.gate:
				case <-release:
					return acpsdk.PromptResponse{StopReason: acpsdk.StopReasonEndTurn}, nil
				}
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
	if agent.agentInfo == nil {
		// testDrain offers Steer only to this agent, as a real binding would.
		agent.agentInfo = &acpsdk.Implementation{Name: "drainer", Version: "1.0.0"}
	}
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
	if !opened.Capabilities().Has(ports.ChatCapabilitySteer) {
		t.Fatal("Steer not offered to the agent the drain binding supports")
	}
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
	agent.gate = make(chan struct{})
	acceptImages(agent.fakeAgent)
	conv, turn := startDrainConversation(t, agent.fakeAgent, testDrain(2*time.Second))

	type steered struct {
		ref ports.ChatTurnRef
		err error
	}
	outcome := make(chan steered, 1)
	go func() {
		ref, err := conv.Steer(context.Background(), turn, ports.ChatUserMessage{Text: "use staging instead"})
		outcome <- steered{ref, err}
	}()
	select {
	case got := <-outcome:
		t.Fatalf("Steer returned %+v before the agent collected the guidance", got)
	case <-time.After(150 * time.Millisecond):
	}
	close(agent.gate)
	var got steered
	select {
	case got = <-outcome:
	case <-time.After(2 * time.Second):
		t.Fatal("Steer never returned after the agent collected")
	}
	if got.err != nil {
		t.Fatalf("Steer: %v", got.err)
	}
	if got.ref.ProviderTurnID != turn {
		t.Fatalf("steered turn = %q, want %q", got.ref.ProviderTurnID, turn)
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

// Stop withdraws a steer that is still waiting for the agent, so it does not
// sit behind the steer's wait. The caller is told to retry, not to send the
// guidance as a new turn, because the turn is being stopped.
func TestACPSteerDrainStopWithdrawsAWaitingSteer(t *testing.T) {
	agent := &fakeAgent{promptBlock: true}
	acceptImages(agent)
	conv, turn := startDrainConversation(t, agent, testDrain(time.Minute))

	done := make(chan error, 1)
	go func() {
		_, err := conv.Steer(context.Background(), turn, ports.ChatUserMessage{Text: "use staging instead"})
		done <- err
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
	var withdrawer ports.ChatSteerWithdrawer = conv
	withdrawer.WithdrawPendingSteers()
	select {
	case err := <-done:
		if !errors.Is(err, ports.ErrChatTurnNotSteerable) {
			t.Fatalf("Steer error = %v, want ErrChatTurnNotSteerable", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Steer kept waiting after Stop withdrew it")
	}
	result, err := conv.HandleExtensionMethod(context.Background(), testDrainMethod, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("drain after withdrawal: %v", err)
	}
	if encoded, _ := json.Marshal(result); string(encoded) != `{"messages":[]}` {
		t.Fatalf("drain after withdrawal = %s, want empty messages", encoded)
	}
}

// A steer goes to the parent turn. A subagent's reply that streams across it
// stays one message instead of starting a second subagent activity.
func TestACPSteerKeepsASubagentReplyInOneMessage(t *testing.T) {
	collected := make(chan struct{})
	agent := &fakeAgent{}
	agent.customPrompt = func(ctx context.Context, params acpsdk.PromptRequest) (acpsdk.PromptResponse, error) {
		say := func(text string) {
			_ = agent.conn.SessionUpdate(ctx, acpsdk.SessionNotification{
				SessionId: params.SessionId,
				Update: acpsdk.SessionUpdate{AgentMessageChunk: &acpsdk.SessionUpdateAgentMessageChunk{
					Content: acpsdk.TextBlock(text),
					Meta:    map[string]any{"claudeCode": map[string]any{"parentToolUseId": "task-1"}},
				}},
			})
		}
		say("reading the config. ")
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
		say("found the timeout.")
		return acpsdk.PromptResponse{StopReason: acpsdk.StopReasonEndTurn}, nil
	}
	conv, turn := startDrainConversation(t, agent, testDrain(2*time.Second))
	if _, err := conv.Steer(context.Background(), turn, ports.ChatUserMessage{Text: "use staging"}); err != nil {
		t.Fatalf("Steer: %v", err)
	}
	<-collected

	texts := map[string]string{}
	started := 0
	for {
		event := nextEvent(t, conv.Events())
		switch event.Kind {
		case ports.ChatEventActivityStarted:
			if event.Summary == "Subagent response" {
				started++
			}
		case ports.ChatEventActivityText:
			texts[event.ProviderItemID] += event.Delta
		}
		if event.Kind == ports.ChatEventTurnCompleted {
			break
		}
	}
	if started != 1 || len(texts) != 1 {
		t.Fatalf("subagent activities = %d, texts = %#v, want one", started, texts)
	}
	for _, text := range texts {
		if text != "reading the config. found the timeout." {
			t.Fatalf("subagent text = %q", text)
		}
	}
}

// Stop withdraws a turn's guidance before it takes the dispatch lock. A steer
// that reaches the conversation after that, such as one that already held the
// lock, is refused at once instead of joining, or holding up, the turn being
// stopped. A later turn steers as usual.
func TestACPSteerAfterStopBeganIsRefused(t *testing.T) {
	agent := &fakeAgent{promptBlock: true}
	conv, turn := startDrainConversation(t, agent, testDrain(time.Minute))
	var withdrawer ports.ChatSteerWithdrawer = conv
	withdrawer.WithdrawPendingSteers()

	started := time.Now()
	_, err := conv.Steer(context.Background(), turn, ports.ChatUserMessage{Text: "use staging instead"})
	if !errors.Is(err, ports.ErrChatTurnNotSteerable) || time.Since(started) > time.Second {
		t.Fatalf("Steer after Stop began = %v after %s, want ErrChatTurnNotSteerable at once", err, time.Since(started))
	}
	conv.mu.Lock()
	parked := len(conv.pendingSteers)
	conv.activeTurn = "next-turn" // the stopped turn is over; another runs
	conv.steering.drain = testDrain(50 * time.Millisecond)
	conv.mu.Unlock()
	if parked != 0 {
		t.Fatalf("pending steers = %d, want none", parked)
	}
	_, err = conv.Steer(context.Background(), "next-turn", ports.ChatUserMessage{Text: "and keep the timeout"})
	if err == nil || !strings.Contains(err.Error(), "did not collect") {
		t.Fatalf("Steer on the next turn = %v, want it parked for the agent like any steer", err)
	}
}

// The nudge asking a drain agent to collect now is best effort. A write that
// stalls must not hold Steer past its wait.
func TestACPSteerDrainStalledNudgeCannotHoldSteer(t *testing.T) {
	agent := &fakeAgent{promptBlock: true}
	conv, turn := startDrainConversation(t, agent, testDrain(100*time.Millisecond))
	writer := conv.legacyWire.writer
	writer.mu.Lock() // every write to the agent now stalls
	unlock := sync.OnceFunc(writer.mu.Unlock)
	t.Cleanup(unlock)

	done := make(chan error, 1)
	go func() {
		_, err := conv.Steer(context.Background(), turn, ports.ChatUserMessage{Text: "use staging instead"})
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, ports.ErrChatTurnNotSteerable) {
			t.Fatalf("Steer = %v, want the guidance withdrawn as not collected", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Steer waited on the stalled nudge")
	}
	unlock()
}

// An agent version that would drop images sent with its steering request gets
// a clear refusal instead, and nothing is sent.
func TestACPSteerRequestRefusesImagesTheAgentWouldDrop(t *testing.T) {
	for _, takesImages := range []bool{false, true} {
		t.Run(fmt.Sprintf("takes images %v", takesImages), func(t *testing.T) {
			var calls []map[string]any
			var mu sync.Mutex
			agent := &fakeAgent{
				capabilities: &acpsdk.AgentCapabilities{
					PromptCapabilities:  acpsdk.PromptCapabilities{Image: true},
					SessionCapabilities: acpsdk.SessionCapabilities{Resume: &acpsdk.SessionResumeCapabilities{}},
				},
				agentInfo: &acpsdk.Implementation{Name: "vendor", Version: "1.1.0"},
				extension: func(method string, raw json.RawMessage) (any, error) {
					var params map[string]any
					_ = json.Unmarshal(raw, &params)
					mu.Lock()
					calls = append(calls, params)
					mu.Unlock()
					return map[string]any{"status": "injected"}, nil
				},
			}
			request := &SteerRequest{
				Supported: func(acpsdk.InitializeResponse) bool { return true },
				Method:    "_vendor/steering",
				Params: func(sessionID string, prompt []acpsdk.ContentBlock) (any, error) {
					return map[string]any{"sessionId": sessionID, "blocks": prompt}, nil
				},
				TakesImages: func(init acpsdk.InitializeResponse) bool {
					return init.AgentInfo != nil && init.AgentInfo.Version == "1.1.0" && takesImages
				},
				Outcome: func(json.RawMessage) (SteerOutcome, error) { return SteerInjected, nil },
			}
			driver := New(Config{
				Harness:      domain.HarnessKimchi,
				Capabilities: ports.ChatCapabilities{ports.ChatCapabilityStreaming: true},
				Probe:        func(context.Context) error { return nil },
				Launch:       func(context.Context, LaunchConfig) (Launch, error) { return Launch{Command: "fake"}, nil },
				SteerRequest: request,
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

			_, err = conv.Steer(context.Background(), "turn-1", ports.ChatUserMessage{
				Text:    "look at this",
				Content: []ports.ChatContent{{Type: "image", Data: "aGVsbG8=", MIMEType: "image/png"}},
			})
			mu.Lock()
			sent := len(calls)
			mu.Unlock()
			if !takesImages {
				if !errors.Is(err, ports.ErrChatSteerContentUnsupported) || sent != 0 {
					t.Fatalf("image steer = %v with %d requests sent, want refused before sending", err, sent)
				}
				if _, err := conv.Steer(context.Background(), "turn-1", ports.ChatUserMessage{Text: "text still steers"}); err != nil {
					t.Fatalf("text steer: %v", err)
				}
				return
			}
			if err != nil || sent != 1 {
				t.Fatalf("image steer = %v with %d requests sent, want it sent", err, sent)
			}
			mu.Lock()
			blocks, _ := calls[0]["blocks"].([]any)
			mu.Unlock()
			images := 0
			for _, block := range blocks {
				if fields, ok := block.(map[string]any); ok && fields["type"] == "image" {
					images++
				}
			}
			if images != 1 {
				t.Fatalf("steer request blocks = %v, want the one image", blocks)
			}
		})
	}
}

// Kimchi and OMP tag every streamed message with an id. A request-mode steer,
// counted when the agent queues the guidance, then cannot split a reply that
// is still streaming: text keeps its own message until the agent starts a new
// one.
func TestACPSteerKeepsAnIdentifiedReplyInOneMessage(t *testing.T) {
	agent := &fakeAgent{
		capabilities: &acpsdk.AgentCapabilities{
			Meta:                map[string]any{"steering": map[string]any{"supported": true}},
			SessionCapabilities: acpsdk.SessionCapabilities{Resume: &acpsdk.SessionResumeCapabilities{}},
		},
		initMeta: map[string]any{"steering": map[string]any{"supported": true}},
		extension: func(string, json.RawMessage) (any, error) {
			return map[string]any{"outcome": "injected"}, nil
		},
	}
	driver := New(Config{
		Harness:      domain.HarnessOMP,
		Capabilities: ports.ChatCapabilities{ports.ChatCapabilityStreaming: true},
		Probe:        func(context.Context) error { return nil },
		Launch:       func(context.Context, LaunchConfig) (Launch, error) { return Launch{Command: "fake"}, nil },
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	driver.useTestProcess(fakeSpawn(agent))
	opened, err := driver.Start(context.Background(), ports.ChatStartConfig{WorkspacePath: t.TempDir()})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer opened.Close()
	if !opened.Capabilities().Has(ports.ChatCapabilitySteer) {
		t.Fatal("Steer not offered to an agent that advertises _session/steering")
	}
	conv := opened.(*conversation)
	conv.mu.Lock()
	conv.activeTurn = "turn-1"
	conv.mu.Unlock()
	say := func(messageID, text string) {
		t.Helper()
		if err := conv.SessionUpdate(context.Background(), acpsdk.SessionNotification{
			SessionId: acpsdk.SessionId(opened.ProviderConversationID()),
			Update: acpsdk.SessionUpdate{AgentMessageChunk: &acpsdk.SessionUpdateAgentMessageChunk{
				MessageId: &messageID, Content: acpsdk.TextBlock(text),
			}},
		}); err != nil {
			t.Fatalf("SessionUpdate: %v", err)
		}
	}

	say("reply-1", "editing prod.yaml, ")
	if _, err := conv.Steer(context.Background(), "turn-1", ports.ChatUserMessage{Text: "use staging"}); err != nil {
		t.Fatalf("Steer: %v", err)
	}
	say("reply-1", "then the timeout.")
	say("reply-2", "Switched to staging.")

	var items []string
	deltas := map[string]string{}
	for len(items) < 2 || deltas[items[1]] == "" {
		event := nextEvent(t, conv.Events())
		if event.Kind != ports.ChatEventMessageDelta {
			continue
		}
		if _, seen := deltas[event.ProviderItemID]; !seen {
			items = append(items, event.ProviderItemID)
		}
		deltas[event.ProviderItemID] += event.Delta
	}
	if deltas[items[0]] != "editing prod.yaml, then the timeout." || deltas[items[1]] != "Switched to staging." {
		t.Fatalf("messages = %#v in order %v, want the first reply whole and the steered one after it", deltas, items)
	}
}

// A Stop that fails leaves the turn running, and the turn can be steered again.
func TestACPSteerAfterAFailedStopIsAccepted(t *testing.T) {
	agent := &fakeAgent{promptBlock: true}
	conv, turn := startDrainConversation(t, agent, testDrain(50*time.Millisecond))
	var withdrawer ports.ChatSteerWithdrawer = conv
	restore := withdrawer.WithdrawPendingSteers()
	if _, err := conv.Steer(context.Background(), turn, ports.ChatUserMessage{Text: "refused"}); !errors.Is(err, errSteerTurnStopping) {
		t.Fatalf("Steer while stopping = %v, want it refused", err)
	}
	restore()
	_, err := conv.Steer(context.Background(), turn, ports.ChatUserMessage{Text: "parked again"})
	if err == nil || !strings.Contains(err.Error(), "did not collect") {
		t.Fatalf("Steer after the failed Stop = %v, want it parked for the agent like any steer", err)
	}
}

// Only one nudge is in flight. Steers that time out behind a stalled write do
// not each leave another blocked request behind.
func TestACPSteerDrainNudgesOneAtATime(t *testing.T) {
	agent := &fakeAgent{promptBlock: true}
	conv, turn := startDrainConversation(t, agent, testDrain(60*time.Millisecond))
	writer := conv.legacyWire.writer
	writer.mu.Lock()
	unlock := sync.OnceFunc(writer.mu.Unlock)
	t.Cleanup(unlock)
	for range 3 {
		if _, err := conv.Steer(context.Background(), turn, ports.ChatUserMessage{Text: "use staging"}); !errors.Is(err, ports.ErrChatTurnNotSteerable) {
			t.Fatalf("Steer = %v, want the guidance withdrawn as not collected", err)
		}
	}
	conv.legacyWire.mu.Lock()
	inFlight := len(conv.legacyWire.pending)
	conv.legacyWire.mu.Unlock()
	if inFlight != 1 {
		t.Fatalf("nudges in flight = %d, want 1", inFlight)
	}
	unlock()
}

// Stops can overlap. One that fails gives steering back only once no other
// Stop of the turn is still under way.
func TestACPSteerOverlappingStopsKeepSteeringStopped(t *testing.T) {
	agent := &fakeAgent{promptBlock: true}
	conv, turn := startDrainConversation(t, agent, testDrain(50*time.Millisecond))
	restoreA := conv.WithdrawPendingSteers() // Stop A
	restoreB := conv.WithdrawPendingSteers() // Stop B
	restoreA()                               // Stop A fails; B is still stopping the turn
	restoreA()                               // a second call gives nothing more back
	if _, err := conv.Steer(context.Background(), turn, ports.ChatUserMessage{Text: "refused"}); !errors.Is(err, errSteerTurnStopping) {
		t.Fatalf("Steer while another Stop is under way = %v, want it refused", err)
	}
	restoreB() // Stop B fails too
	_, err := conv.Steer(context.Background(), turn, ports.ChatUserMessage{Text: "parked again"})
	if err == nil || !strings.Contains(err.Error(), "did not collect") {
		t.Fatalf("Steer after both Stops failed = %v, want it parked for the agent like any steer", err)
	}
	// A later turn starts with no Stop of its own under way.
	conv.WithdrawPendingSteers()
	conv.mu.Lock()
	conv.activeTurn = "next-turn"
	conv.mu.Unlock()
	restore := conv.WithdrawPendingSteers()
	restore()
	if _, err := conv.Steer(context.Background(), "next-turn", ports.ChatUserMessage{Text: "steers"}); err == nil || errors.Is(err, errSteerTurnStopping) {
		t.Fatalf("Steer on the next turn after its one Stop failed = %v, want it parked, not refused", err)
	}
}

// A Stop of an earlier turn that fails late gives back only its own turn's
// steering. It cannot lift a Stop the next turn has under way.
func TestACPSteerLateRestoreFromAnEarlierTurnKeepsTheNextTurnStopped(t *testing.T) {
	agent := &fakeAgent{promptBlock: true}
	conv, _ := startDrainConversation(t, agent, testDrain(50*time.Millisecond))
	restoreA := conv.WithdrawPendingSteers() // Stop A, for the first turn, stalls before it fails
	conv.mu.Lock()
	conv.activeTurn = "next-turn" // the first turn ends and the next starts
	conv.mu.Unlock()
	conv.WithdrawPendingSteers() // Stop B, for the next turn
	restoreA()                   // Stop A fails at last
	if _, err := conv.Steer(context.Background(), "next-turn", ports.ChatUserMessage{Text: "refused"}); !errors.Is(err, errSteerTurnStopping) {
		t.Fatalf("Steer while the next turn's Stop is under way = %v, want it refused", err)
	}
}
