package acp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/persistenthost"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// scriptedHost plays the persistent chat host on the other end of a pipe, so a
// test decides exactly which answers the host gives, and in which order.
type scriptedHost struct {
	t      *testing.T
	conn   net.Conn
	frames chan map[string]json.RawMessage
	// lastPrompt holds the params of the last session/prompt nextPrompt read.
	lastPrompt json.RawMessage
}

func (h *scriptedHost) send(frame string) {
	h.t.Helper()
	if _, err := fmt.Fprintln(h.conn, frame); err != nil {
		h.t.Fatalf("host write: %v", err)
	}
}

// nextPrompt returns the id of the next session/prompt the conversation sends.
func (h *scriptedHost) nextPrompt() string {
	h.t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case frame := <-h.frames:
			var method string
			_ = json.Unmarshal(frame["method"], &method)
			if method == "session/prompt" {
				h.lastPrompt = frame["params"]
				return string(frame["id"])
			}
		case <-deadline:
			h.t.Fatal("timed out waiting for a session/prompt")
			return ""
		}
	}
}

// startScriptedHostConversation reattaches to a scripted host. With adoptTurn
// set, the host is mid-turn and the conversation adopts it as that turn.
func startScriptedHostConversation(t *testing.T, adoptTurn string) (*conversation, *scriptedHost) {
	t.Helper()
	return startScriptedHostConversationWith(t, adoptTurn, nil)
}

// startScriptedHostConversationWith is startScriptedHostConversation with the
// host's reported state adjusted by edit.
func startScriptedHostConversationWith(
	t *testing.T,
	adoptTurn string,
	edit func(*persistenthost.ACPState),
) (*conversation, *scriptedHost) {
	t.Helper()
	initialize, err := json.Marshal(acpsdk.InitializeResponse{
		ProtocolVersion: acpsdk.ProtocolVersionNumber,
		AgentInfo:       &acpsdk.Implementation{Name: "prompt-steerer", Version: "1.0.0"},
		AgentCapabilities: acpsdk.AgentCapabilities{
			SessionCapabilities: acpsdk.SessionCapabilities{Resume: &acpsdk.SessionResumeCapabilities{}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	session, err := json.Marshal(acpsdk.NewSessionResponse{SessionId: "provider-session"})
	if err != nil {
		t.Fatal(err)
	}
	daemon, hostEnd := net.Pipe()
	host := &scriptedHost{t: t, conn: hostEnd, frames: make(chan map[string]json.RawMessage, 32)}
	go func() {
		reader := bufio.NewReader(hostEnd)
		for {
			line, err := reader.ReadBytes('\n')
			if err != nil {
				return
			}
			var frame map[string]json.RawMessage
			if json.Unmarshal(line, &frame) == nil {
				host.frames <- frame
			}
		}
	}()
	driver := New(Config{
		Harness: domain.HarnessAgy,
		Capabilities: ports.ChatCapabilities{
			ports.ChatCapabilityStreaming: true, ports.ChatCapabilityResume: true, ports.ChatCapabilityInterrupt: true,
		},
		Launch:      func(context.Context, LaunchConfig) (Launch, error) { return Launch{Command: "fake"}, nil },
		SteerPrompt: testSteerPrompt(2 * time.Second),
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	state := &persistenthost.ACPState{
		InitializeResult: initialize, SessionResult: session,
		SessionID: "provider-session", ActivePrompt: adoptTurn != "",
	}
	if edit != nil {
		edit(state)
	}
	driver.connectHost = func(context.Context, persistenthost.Config) (*persistenthost.Transport, error) {
		return &persistenthost.Transport{Stdin: daemon, Stdout: daemon, Reconnected: true, ACPState: state}, nil
	}
	opened, err := driver.Resume(context.Background(), ports.ChatResumeConfig{
		SessionID: "ao-session", DataDir: t.TempDir(), WorkspacePath: t.TempDir(),
		ProviderConversationID: "provider-session", ProviderScopeID: "scope",
	})
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	conv := opened.(*conversation)
	t.Cleanup(func() {
		_ = hostEnd.Close()
		_ = conv.Close()
	})
	if !conv.Capabilities()[ports.ChatCapabilitySteer] {
		t.Fatal("Steer not offered for an agent the binding supports")
	}
	if err := conv.ActivateLiveReconnect(context.Background(), adoptTurn); err != nil {
		t.Fatalf("ActivateLiveReconnect: %v", err)
	}
	return conv, host
}

// startScriptedTurn starts a turn and returns its id and its prompt's id on the
// wire.
func startScriptedTurn(t *testing.T, conv *conversation, host *scriptedHost) (string, string) {
	t.Helper()
	ref, err := conv.SendTurn(context.Background(), ports.ChatUserMessage{Text: "edit prod.yaml"})
	if err != nil {
		t.Fatalf("SendTurn: %v", err)
	}
	if err := conv.StartDeferredTurn(ref.ProviderTurnID); err != nil {
		t.Fatalf("StartDeferredTurn: %v", err)
	}
	return ref.ProviderTurnID, host.nextPrompt()
}

// steerAsync steers turn and returns the steer prompt's id on the wire and a
// channel with Steer's outcome.
func steerAsync(t *testing.T, conv *conversation, host *scriptedHost, turn string) (string, <-chan error) {
	t.Helper()
	outcome := make(chan error, 1)
	go func() {
		_, err := conv.Steer(context.Background(), turn, ports.ChatUserMessage{Text: "edit staging.yaml instead"})
		outcome <- err
	}()
	return host.nextPrompt(), outcome
}

func steerOutcome(t *testing.T, outcome <-chan error) error {
	t.Helper()
	select {
	case err := <-outcome:
		return err
	case <-time.After(3 * time.Second):
		t.Fatal("Steer never returned")
		return nil
	}
}

// answerOrder holds the routing of one prompt's answer until another's has
// been routed, so a test can pin the order the SDK happens to wake callers in.
func answerOrder(conv *conversation, first, second int) {
	firstDone := make(chan struct{})
	var once sync.Once
	conv.promptReturned = func(seq int) {
		switch seq {
		case second:
			select {
			case <-firstDone:
			case <-time.After(2 * time.Second):
			}
		case first:
			// finishTurnPrompt runs right after this returns; give it the lock.
			go func() {
				time.Sleep(20 * time.Millisecond)
				once.Do(func() { close(firstDone) })
			}()
		}
	}
}

// completionsOf collects the turn's terminal events until the conversation is
// ready again.
func completionsOf(t *testing.T, conv *conversation, turn string) []ports.ChatEvent {
	t.Helper()
	var done []ports.ChatEvent
	for {
		event := nextEvent(t, conv.Events())
		if event.Kind == ports.ChatEventTurnCompleted && event.ProviderTurnID == turn {
			done = append(done, event)
		}
		if len(done) > 0 && event.Kind == ports.ChatEventControllerState && event.ControllerState == ports.ChatControllerReady {
			// Anything after this would be a second completion.
			done = append(done, turnCompletions(t, conv.Events(), turn, 100*time.Millisecond)...)
			return done
		}
	}
}

func receiptResult(id, eventID string) string {
	return `{"jsonrpc":"2.0","id":` + id + `,"result":{"stopReason":"end_turn","_meta":{"` +
		persistenthost.ACPEventIDMetaKey + `":"` + eventID + `"}}}`
}

func plainResult(id string) string {
	return `{"jsonrpc":"2.0","id":` + id + `,"result":{"stopReason":"end_turn"}}`
}

// The turn's own prompt finishes before the steer prompt reaches the host. The
// host's result is the turn's, whichever answer the SDK hands over first, and
// the steer, which the agent never saw, can go as a new turn.
func TestACPSteerPromptTurnEndingFirstKeepsTheHostResult(t *testing.T) {
	for _, order := range []struct {
		name          string
		first, second int
	}{
		{"result routed first", 0, 1},
		{"refusal routed first", 1, 0},
	} {
		t.Run(order.name, func(t *testing.T) {
			conv, host := startScriptedHostConversation(t, "")
			answerOrder(conv, order.first, order.second)
			_ = nextEvent(t, conv.Events()) // controller.ready
			turn, root := startScriptedTurn(t, conv, host)
			steer, outcome := steerAsync(t, conv, host, turn)

			host.send(receiptResult(root, "acp-host:1"))
			host.send(`{"jsonrpc":"2.0","id":` + steer + `,"error":{"code":-32602,"message":"the turn ended before the steer prompt arrived","data":{"` +
				persistenthost.ACPSteerTurnEndedDataKey + `":true}}}`)

			if err := steerOutcome(t, outcome); !errors.Is(err, ports.ErrChatNoSteerableTurn) {
				t.Fatalf("Steer = %v, want ErrChatNoSteerableTurn so the guidance goes as a new turn", err)
			}
			done := completionsOf(t, conv, turn)
			if len(done) != 1 || done[0].TurnState != domain.TurnStateCompleted || done[0].ProviderEventID != "acp-host:1" {
				t.Fatalf("turn completions = %+v, want one completed with the host's receipt", done)
			}
		})
	}
}

// The host's result can be either prompt's answer, and the SDK can hand the
// answers over in either order. The receipt-bearing one settles the turn.
func TestACPSteerPromptSettlesWithTheHostResultInEitherOrder(t *testing.T) {
	for _, tc := range []struct {
		name string
		// rootFirst sends the turn's own prompt's answer first on the wire;
		// the last answer on the wire carries the receipt.
		rootFirst     bool
		first, second int
	}{
		{"handoff, then steer result", true, 0, 1},
		{"handoff, then steer result, routed in reverse", true, 1, 0},
		{"steer answer, then turn result", false, 1, 0},
		{"steer answer, then turn result, routed in reverse", false, 0, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conv, host := startScriptedHostConversation(t, "")
			answerOrder(conv, tc.first, tc.second)
			_ = nextEvent(t, conv.Events()) // controller.ready
			turn, root := startScriptedTurn(t, conv, host)
			steer, outcome := steerAsync(t, conv, host, turn)

			if tc.rootFirst {
				host.send(plainResult(root))
				host.send(receiptResult(steer, "acp-host:2"))
			} else {
				host.send(plainResult(steer))
				host.send(receiptResult(root, "acp-host:2"))
			}
			if err := steerOutcome(t, outcome); err != nil {
				t.Fatalf("Steer = %v, want delivered", err)
			}
			done := completionsOf(t, conv, turn)
			if len(done) != 1 || done[0].TurnState != domain.TurnStateCompleted || done[0].ProviderEventID != "acp-host:2" {
				t.Fatalf("turn completions = %+v, want one completed with the host's receipt", done)
			}
		})
	}
}

// After a daemon restart mid-turn, the reattached conversation can steer the
// turn it adopted. The earlier attachment's prompt answers through the host.
func TestACPSteerPromptSteersAnAdoptedTurn(t *testing.T) {
	t.Run("the adopted prompt hands off", func(t *testing.T) {
		conv, host := startScriptedHostConversation(t, "durable-turn")
		_ = nextEvent(t, conv.Events()) // controller.ready
		steer, outcome := steerAsync(t, conv, host, "durable-turn")
		host.send(`{"jsonrpc":"2.0","method":"` + persistenthost.ACPPromptHandoffMethod + `","params":{"result":{"stopReason":"end_turn"}}}`)
		if err := steerOutcome(t, outcome); err != nil {
			t.Fatalf("Steer = %v, want delivered at the handoff", err)
		}
		host.send(receiptResult(steer, "acp-host:3"))
		done := completionsOf(t, conv, "durable-turn")
		if len(done) != 1 || done[0].TurnState != domain.TurnStateCompleted || done[0].ProviderEventID != "acp-host:3" {
			t.Fatalf("turn completions = %+v, want one completed with the steer prompt's receipt", done)
		}
	})
	t.Run("the adopted prompt finishes the turn", func(t *testing.T) {
		conv, host := startScriptedHostConversation(t, "durable-turn")
		_ = nextEvent(t, conv.Events()) // controller.ready
		steer, outcome := steerAsync(t, conv, host, "durable-turn")
		host.send(plainResult(steer))
		if err := steerOutcome(t, outcome); err != nil {
			t.Fatalf("Steer = %v, want delivered", err)
		}
		// The steer prompt has returned, but the host still runs the turn.
		if done := turnCompletions(t, conv.Events(), "durable-turn", 100*time.Millisecond); len(done) != 0 {
			t.Fatalf("adopted turn completed before the host's result: %+v", done)
		}
		host.send(`{"jsonrpc":"2.0","method":"` + persistenthost.ACPPromptResultMethod + `","params":{"eventId":"acp-host:4","result":{"stopReason":"end_turn"}}}`)
		done := completionsOf(t, conv, "durable-turn")
		if len(done) != 1 || done[0].TurnState != domain.TurnStateCompleted || done[0].ProviderEventID != "acp-host:4" {
			t.Fatalf("turn completions = %+v, want one completed with the host's receipt", done)
		}
	})
}

// A steer the agent has not answered by the wait is not reported delivered:
// it may yet be refused.
func TestACPSteerPromptUnansweredSteerIsUncertain(t *testing.T) {
	for _, tc := range []struct {
		name string
		ctx  func() (context.Context, context.CancelFunc)
	}{
		{"the wait runs out", func() (context.Context, context.CancelFunc) { return context.Background(), func() {} }},
		{"the caller gives up", func() (context.Context, context.CancelFunc) {
			return context.WithTimeout(context.Background(), 30*time.Millisecond)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conv, host := startScriptedHostConversation(t, "")
			conv.steering.prompt = testSteerPrompt(80 * time.Millisecond)
			_ = nextEvent(t, conv.Events()) // controller.ready
			turn, root := startScriptedTurn(t, conv, host)

			ctx, cancel := tc.ctx()
			defer cancel()
			outcome := make(chan error, 1)
			go func() {
				_, err := conv.Steer(ctx, turn, ports.ChatUserMessage{Text: "edit staging.yaml instead"})
				outcome <- err
			}()
			steer := host.nextPrompt()
			err := steerOutcome(t, outcome)
			var refusal interface{ ChatRefusal() bool }
			if !errors.Is(err, errSteerUnconfirmed) || errors.As(err, &refusal) || errors.Is(err, ports.ErrChatNoSteerableTurn) {
				t.Fatalf("Steer = %v, want an uncertain delivery", err)
			}
			conv.mu.Lock()
			segment := conv.steerSegment
			conv.mu.Unlock()
			if segment != 0 {
				t.Fatalf("steer segment = %d, want 0 for guidance not known to have landed", segment)
			}

			// The agent refuses it after all; the turn's own prompt carries on.
			host.send(`{"jsonrpc":"2.0","id":` + steer + `,"error":{"code":-32600,"message":"a prompt is already running"}}`)
			if done := turnCompletions(t, conv.Events(), turn, 100*time.Millisecond); len(done) != 0 {
				t.Fatalf("turn completed on the late refusal: %+v", done)
			}
			host.send(receiptResult(root, "acp-host:5"))
			done := completionsOf(t, conv, turn)
			if len(done) != 1 || done[0].TurnState != domain.TurnStateCompleted || done[0].ProviderEventID != "acp-host:5" {
				t.Fatalf("turn completions = %+v, want one completed", done)
			}
		})
	}
}

// The host's stall watchdog answers every prompt of a steered turn. The turn
// ends once, failed, with the host's receipt.
func TestACPSteerPromptStallFailureEndsTheTurnOnce(t *testing.T) {
	conv, host := startScriptedHostConversation(t, "")
	_ = nextEvent(t, conv.Events()) // controller.ready
	turn, root := startScriptedTurn(t, conv, host)
	steer, outcome := steerAsync(t, conv, host, turn)
	host.send(`{"jsonrpc":"2.0","id":` + root + `,"error":{"code":-32603,"message":"provider stopped responding"}}`)
	host.send(`{"jsonrpc":"2.0","id":` + steer + `,"error":{"code":-32603,"message":"provider stopped responding","data":{"` +
		persistenthost.ACPEventIDMetaKey + `":"acp-host:6"}}}`)
	if err := steerOutcome(t, outcome); err == nil || !strings.Contains(err.Error(), "stopped responding") {
		t.Fatalf("Steer = %v, want the stall failure", err)
	}
	done := completionsOf(t, conv, turn)
	if len(done) != 1 || done[0].TurnState != domain.TurnStateFailed || done[0].ProviderEventID != "acp-host:6" {
		t.Fatalf("turn completions = %+v, want one failed with the host's receipt", done)
	}
}

// Once Stop has begun, a steer prompt for the turn is refused and never sent.
func TestACPSteerPromptAfterStopBeganIsNotSent(t *testing.T) {
	conv, host := startScriptedHostConversation(t, "")
	_ = nextEvent(t, conv.Events()) // controller.ready
	turn, root := startScriptedTurn(t, conv, host)
	conv.WithdrawPendingSteers()
	if _, err := conv.Steer(context.Background(), turn, ports.ChatUserMessage{Text: "too late"}); !errors.Is(err, ports.ErrChatTurnNotSteerable) {
		t.Fatalf("Steer after Stop began = %v, want ErrChatTurnNotSteerable", err)
	}
	host.send(receiptResult(root, "acp-host:10"))
	done := completionsOf(t, conv, turn)
	if len(done) != 1 || done[0].ProviderEventID != "acp-host:10" {
		t.Fatalf("turn completions = %+v, want the turn's own result", done)
	}
	select {
	case frame := <-host.frames:
		var method string
		_ = json.Unmarshal(frame["method"], &method)
		if method == "session/prompt" {
			t.Fatalf("a steer prompt reached the host after Stop began: %v", frame)
		}
	default:
	}
}

// With two steer prompts in flight, a handoff reaches only the next one. The
// later steer still waits for its own answer, and a refusal of it stands.
func TestACPSteerPromptHandoffSettlesOnlyTheNextSteer(t *testing.T) {
	conv, host := startScriptedHostConversation(t, "")
	_ = nextEvent(t, conv.Events()) // controller.ready
	turn, root := startScriptedTurn(t, conv, host)
	first, firstOutcome := steerAsync(t, conv, host, turn)
	second, secondOutcome := steerAsync(t, conv, host, turn)

	host.send(plainResult(root)) // the turn's prompt hands off to the first steer
	if err := steerOutcome(t, firstOutcome); err != nil {
		t.Fatalf("first Steer = %v, want delivered at the handoff", err)
	}
	select {
	case err := <-secondOutcome:
		t.Fatalf("second Steer returned %v at a handoff that went to the first", err)
	case <-time.After(100 * time.Millisecond):
	}
	host.send(`{"jsonrpc":"2.0","id":` + second + `,"error":{"code":-32600,"message":"a prompt is already running"}}`)
	err := steerOutcome(t, secondOutcome)
	var refusal interface{ ChatRefusal() bool }
	if !errors.As(err, &refusal) || !refusal.ChatRefusal() {
		t.Fatalf("second Steer = %v, want the agent's refusal", err)
	}
	host.send(receiptResult(first, "acp-host:11"))
	done := completionsOf(t, conv, turn)
	if len(done) != 1 || done[0].TurnState != domain.TurnStateCompleted || done[0].ProviderEventID != "acp-host:11" {
		t.Fatalf("turn completions = %+v, want one completed with the host's receipt", done)
	}
}

// A steer prompt answered as cancelled was not shown to take: Stop got there
// first. An earlier prompt's cancelled answer is no handoff either.
func TestACPSteerPromptCancelledAnswerIsUncertain(t *testing.T) {
	conv, host := startScriptedHostConversation(t, "")
	_ = nextEvent(t, conv.Events()) // controller.ready
	turn, root := startScriptedTurn(t, conv, host)
	steer, outcome := steerAsync(t, conv, host, turn)

	host.send(`{"jsonrpc":"2.0","id":` + root + `,"result":{"stopReason":"cancelled"}}`)
	select {
	case err := <-outcome:
		t.Fatalf("Steer returned %v on the turn's cancelled answer", err)
	case <-time.After(100 * time.Millisecond):
	}
	host.send(`{"jsonrpc":"2.0","id":` + steer + `,"result":{"stopReason":"cancelled","_meta":{"` +
		persistenthost.ACPEventIDMetaKey + `":"acp-host:12"}}}`)
	err := steerOutcome(t, outcome)
	var refusal interface{ ChatRefusal() bool }
	if !errors.Is(err, errSteerUnconfirmed) || errors.As(err, &refusal) {
		t.Fatalf("Steer = %v, want an uncertain delivery", err)
	}
	done := completionsOf(t, conv, turn)
	if len(done) != 1 || done[0].TurnState != domain.TurnStateInterrupted || done[0].ProviderEventID != "acp-host:12" {
		t.Fatalf("turn completions = %+v, want one interrupted with the host's receipt", done)
	}
}

// Stop does not wait out a steer prompt the agent has not answered: the Steer
// returns at once, its delivery uncertain.
func TestACPSteerPromptStopReleasesAWaitingSteer(t *testing.T) {
	conv, host := startScriptedHostConversation(t, "")
	conv.steering.prompt = testSteerPrompt(time.Minute)
	_ = nextEvent(t, conv.Events()) // controller.ready
	turn, root := startScriptedTurn(t, conv, host)
	steer, outcome := steerAsync(t, conv, host, turn)

	started := time.Now()
	conv.WithdrawPendingSteers()
	err := steerOutcome(t, outcome)
	if !errors.Is(err, errSteerUnconfirmed) || time.Since(started) > time.Second {
		t.Fatalf("Steer after Stop began = %v after %s, want an uncertain delivery at once", err, time.Since(started))
	}
	host.send(`{"jsonrpc":"2.0","id":` + root + `,"result":{"stopReason":"cancelled"}}`)
	host.send(`{"jsonrpc":"2.0","id":` + steer + `,"result":{"stopReason":"cancelled","_meta":{"` +
		persistenthost.ACPEventIDMetaKey + `":"acp-host:13"}}}`)
	done := completionsOf(t, conv, turn)
	if len(done) != 1 || done[0].TurnState != domain.TurnStateInterrupted {
		t.Fatalf("turn completions = %+v, want one interrupted", done)
	}
}

// Stop can begin after a steer prompt is registered but before it is written.
// It is then never written: sent after the cancel, the agent would run it as
// fresh work. Its Steer is a definite refusal, and the turn ends on the turn's
// own prompt, whether that answers before or after the steer is dropped.
func TestACPSteerPromptOvertakenByStopIsNeverSent(t *testing.T) {
	for _, rootFirst := range []bool{false, true} {
		name := "dropped before the turn's prompt answers"
		if rootFirst {
			name = "dropped after the turn's prompt answers"
		}
		t.Run(name, func(t *testing.T) {
			conv, host := startScriptedHostConversation(t, "")
			_ = nextEvent(t, conv.Events()) // controller.ready
			turn, root := startScriptedTurn(t, conv, host)
			registered := make(chan struct{})
			release := make(chan struct{})
			conv.promptSending = func(int) {
				close(registered)
				<-release
			}
			outcome := make(chan error, 1)
			go func() {
				_, err := conv.Steer(context.Background(), turn, ports.ChatUserMessage{Text: "edit staging.yaml instead"})
				outcome <- err
			}()
			<-registered

			conv.WithdrawPendingSteers()
			err := steerOutcome(t, outcome)
			if !errors.Is(err, ports.ErrChatTurnNotSteerable) || errors.Is(err, errSteerUnconfirmed) {
				t.Fatalf("Steer overtaken by Stop = %v, want not steerable and not sent", err)
			}
			if rootFirst {
				host.send(`{"jsonrpc":"2.0","id":` + root + `,"result":{"stopReason":"cancelled","_meta":{"` +
					persistenthost.ACPEventIDMetaKey + `":"acp-host:16"}}}`)
				// The answer is held until the unsent steer is dropped.
				if again := turnCompletions(t, conv.Events(), turn, 200*time.Millisecond); len(again) != 0 {
					t.Fatalf("the turn ended while its steer prompt was still registered: %+v", again)
				}
				close(release)
			} else {
				close(release)
				host.send(`{"jsonrpc":"2.0","id":` + root + `,"result":{"stopReason":"cancelled","_meta":{"` +
					persistenthost.ACPEventIDMetaKey + `":"acp-host:16"}}}`)
			}
			done := completionsOf(t, conv, turn)
			if len(done) != 1 || done[0].TurnState != domain.TurnStateInterrupted || done[0].ProviderEventID != "acp-host:16" {
				t.Fatalf("turn completions = %+v, want one interrupted with the host's receipt", done)
			}
			select {
			case frame := <-host.frames:
				var method string
				_ = json.Unmarshal(frame["method"], &method)
				if method == "session/prompt" {
					t.Fatalf("the overtaken steer prompt reached the host: %v", frame)
				}
			default:
			}
		})
	}
}

// A steer prompt answered "Session not found" was not delivered, and the
// provider can never serve the conversation again, so it is stopped.
func TestACPSteerPromptIntoALostSessionIsARefusalAndStopsTheProvider(t *testing.T) {
	conv, host := startScriptedHostConversation(t, "")
	_ = nextEvent(t, conv.Events()) // controller.ready
	turn, _ := startScriptedTurn(t, conv, host)
	steer, outcome := steerAsync(t, conv, host, turn)

	host.send(`{"jsonrpc":"2.0","id":` + steer + `,"error":{"code":-32603,"message":"Internal error","data":{"details":"Session not found"}}}`)
	err := steerOutcome(t, outcome)
	var refusal interface{ ChatRefusal() bool }
	if !errors.Is(err, ports.ErrChatProviderSessionLost) || !errors.As(err, &refusal) || !refusal.ChatRefusal() {
		t.Fatalf("Steer into a lost session = %v, want a refusal carrying ErrChatProviderSessionLost", err)
	}
	waitProviderStoppedForLostSession(t, conv)
}

// waitProviderStoppedForLostSession waits for the conversation to record the
// lost session and stop its provider.
func waitProviderStoppedForLostSession(t *testing.T, conv *conversation) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		conv.mu.Lock()
		lost, closed := conv.sessionLost, conv.closed
		conv.mu.Unlock()
		if lost && closed {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("after a lost session: sessionLost=%v closed=%v, want the provider stopped", lost, closed)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A compaction the host was running when the daemon restarted takes no steer,
// as a local Compact does not, and it still ends with the host's result.
func TestACPSteerPromptAdoptedCompactionIsNotSteerable(t *testing.T) {
	conv, host := startScriptedHostConversationWith(t, "durable-turn", func(state *persistenthost.ACPState) {
		state.ActiveCompaction = true
	})
	_ = nextEvent(t, conv.Events()) // controller.ready
	if _, err := conv.Steer(context.Background(), "durable-turn", ports.ChatUserMessage{Text: "keep the tests"}); !errors.Is(err, ports.ErrChatNoSteerableTurn) {
		t.Fatalf("Steer into an adopted compaction = %v, want ErrChatNoSteerableTurn", err)
	}
	host.send(`{"jsonrpc":"2.0","method":"_ao/persistent_prompt_result","params":{"eventId":"acp-host:17","result":{"stopReason":"end_turn"}}}`)
	done := completionsOf(t, conv, "durable-turn")
	if len(done) != 1 || done[0].ProviderEventID != "acp-host:17" {
		t.Fatalf("turn completions = %+v, want one with the host's receipt", done)
	}
	select {
	case frame := <-host.frames:
		var method string
		_ = json.Unmarshal(frame["method"], &method)
		if method == "session/prompt" {
			t.Fatalf("a steer prompt reached the host during a compaction: %v", frame)
		}
	default:
	}
}

// The reply's next segment starts in the same critical section that settles a
// steer as taken, so text the agent streams without a message id right after
// it renders below the steer even before Steer has returned. The waiter here
// has no Steer reading it, which is the moment that matters.
func TestACPSteerPromptTakenStartsTheNextSegmentAtOnce(t *testing.T) {
	conv, host := startScriptedHostConversation(t, "")
	_ = nextEvent(t, conv.Events()) // controller.ready
	turn, _ := startScriptedTurn(t, conv, host)

	waiter := &promptSteer{turn: turn, seq: 1, result: make(chan error, 1), sent: true}
	conv.mu.Lock()
	conv.promptSeq = 1
	conv.outstanding[1] = true
	conv.promptSteers = append(conv.promptSteers, waiter)
	before := conv.steerSegment
	conv.mu.Unlock()

	// The turn's own prompt hands the work on to the steer prompt.
	conv.finishTurnPrompt(turn, 0, acpsdk.PromptResponse{StopReason: acpsdk.StopReasonEndTurn}, nil)
	conv.mu.Lock()
	after := conv.steerSegment
	conv.mu.Unlock()
	if err := <-waiter.result; err != nil {
		t.Fatalf("handoff settled the steer with %v, want taken", err)
	}
	if after != before+1 {
		t.Fatalf("steer segment = %d after the steer took, want %d", after, before+1)
	}
}

// A "Session not found" answer that comes after Steer stopped waiting still
// stops the provider, though the turn's own prompt has not answered.
func TestACPSteerPromptLostSessionAfterTheWaitStillStopsTheProvider(t *testing.T) {
	conv, host := startScriptedHostConversation(t, "")
	conv.steering.prompt = testSteerPrompt(80 * time.Millisecond)
	_ = nextEvent(t, conv.Events()) // controller.ready
	turn, _ := startScriptedTurn(t, conv, host)
	steer, outcome := steerAsync(t, conv, host, turn)
	if err := steerOutcome(t, outcome); !errors.Is(err, errSteerUnconfirmed) {
		t.Fatalf("Steer = %v, want an uncertain delivery after the wait", err)
	}

	host.send(`{"jsonrpc":"2.0","id":` + steer + `,"error":{"code":-32603,"message":"Internal error","data":{"details":"Session not found"}}}`)
	waitProviderStoppedForLostSession(t, conv)
}

// The work can only go to a prompt the agent has. A steer prompt that is
// registered but not yet written is no handoff target, so an earlier prompt's
// answer does not report it taken before it is even sent.
func TestACPSteerPromptHandoffSkipsAPromptNotYetSent(t *testing.T) {
	conv, host := startScriptedHostConversation(t, "")
	_ = nextEvent(t, conv.Events()) // controller.ready
	turn, root := startScriptedTurn(t, conv, host)
	registered := make(chan struct{})
	release := make(chan struct{})
	conv.promptSending = func(int) {
		close(registered)
		<-release
	}
	outcome := make(chan error, 1)
	go func() {
		_, err := conv.Steer(context.Background(), turn, ports.ChatUserMessage{Text: "edit staging.yaml instead"})
		outcome <- err
	}()
	<-registered

	host.send(plainResult(root)) // an end_turn that could read as a handoff
	select {
	case err := <-outcome:
		t.Fatalf("Steer = %v before its prompt was written, want it still waiting", err)
	case <-time.After(200 * time.Millisecond):
	}
	close(release)
	steer := host.nextPrompt()
	host.send(receiptResult(steer, "acp-host:21"))
	if err := steerOutcome(t, outcome); err != nil {
		t.Fatalf("Steer = %v, want delivered by its own answer", err)
	}
	done := completionsOf(t, conv, turn)
	if len(done) != 1 || done[0].ProviderEventID != "acp-host:21" {
		t.Fatalf("turn completions = %+v, want one with the steer prompt's receipt", done)
	}
}

// After a restart, the host passes the adopted prompt's answer on. A cancelled
// answer hands nothing on, so the steer waits for its own answer.
func TestACPSteerPromptCancelledAdoptedAnswerIsNoHandoff(t *testing.T) {
	conv, host := startScriptedHostConversation(t, "durable-turn")
	_ = nextEvent(t, conv.Events()) // controller.ready
	steer, outcome := steerAsync(t, conv, host, "durable-turn")
	host.send(`{"jsonrpc":"2.0","method":"` + persistenthost.ACPPromptHandoffMethod + `","params":{"result":{"stopReason":"cancelled"}}}`)
	select {
	case err := <-outcome:
		t.Fatalf("Steer = %v after a cancelled adopted answer, want it still waiting", err)
	case <-time.After(200 * time.Millisecond):
	}
	host.send(`{"jsonrpc":"2.0","id":` + steer + `,"result":{"stopReason":"cancelled","_meta":{"` +
		persistenthost.ACPEventIDMetaKey + `":"acp-host:22"}}}`)
	if err := steerOutcome(t, outcome); !errors.Is(err, errSteerCancelled) {
		t.Fatalf("Steer = %v, want uncertain because its own answer was cancelled", err)
	}
}

// A turn the straggler wait ended remembers the prompts that never answered.
// If one answers after a later steered turn has settled, it is still dropped,
// so it cannot complete the old turn again under the newer turn's items.
func TestACPSteerPromptStragglerAnswersAfterALaterSteeredTurnAreDropped(t *testing.T) {
	conv, host := startScriptedHostConversation(t, "")
	conv.stragglerWait = 100 * time.Millisecond
	_ = nextEvent(t, conv.Events()) // controller.ready

	first, firstRoot := startScriptedTurn(t, conv, host)
	firstSteer, outcome := steerAsync(t, conv, host, first)
	// The host's result arrives on the steer prompt; the turn's own prompt
	// never answers in time.
	host.send(receiptResult(firstSteer, "acp-host:23"))
	if err := steerOutcome(t, outcome); err != nil {
		t.Fatalf("first Steer = %v, want delivered", err)
	}
	if done := completionsOf(t, conv, first); len(done) != 1 {
		t.Fatalf("first turn completions = %+v, want one", done)
	}
	if err := conv.AcknowledgeProviderEvent(context.Background(), "acp-host:23"); err != nil {
		t.Fatalf("AcknowledgeProviderEvent: %v", err)
	}

	second, secondRoot := startScriptedTurn(t, conv, host)
	secondSteer, outcome := steerAsync(t, conv, host, second)
	host.send(plainResult(secondRoot))
	if err := steerOutcome(t, outcome); err != nil {
		t.Fatalf("second Steer = %v, want delivered at the handoff", err)
	}
	host.send(receiptResult(secondSteer, "acp-host:24"))
	if done := completionsOf(t, conv, second); len(done) != 1 {
		t.Fatalf("second turn completions = %+v, want one", done)
	}

	host.send(plainResult(firstRoot))
	deadline := time.After(300 * time.Millisecond)
	for {
		select {
		case event := <-conv.Events():
			if event.Kind == ports.ChatEventTurnCompleted || event.Kind == ports.ChatEventControllerState {
				t.Fatalf("the first turn's late answer emitted %+v", event)
			}
		case <-deadline:
			conv.mu.Lock()
			left := len(conv.stragglers)
			conv.mu.Unlock()
			if left != 0 {
				t.Fatalf("stragglers left = %d, want the answered one forgotten", left)
			}
			return
		}
	}
}
