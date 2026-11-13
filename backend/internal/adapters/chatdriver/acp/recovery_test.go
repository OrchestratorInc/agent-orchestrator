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
	"sync/atomic"
	"testing"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/persistenthost"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestPersistentACPReplayBackpressure(t *testing.T) {
	initialize, _ := json.Marshal(acpsdk.InitializeResponse{ProtocolVersion: acpsdk.ProtocolVersionNumber})
	session, _ := json.Marshal(acpsdk.NewSessionResponse{SessionId: "provider-session"})
	daemon, host := net.Pipe()
	t.Cleanup(func() { _ = host.Close() })
	driver := New(Config{Harness: domain.HarnessOpenCode}, slog.New(slog.DiscardHandler))
	driver.connectHost = func(context.Context, persistenthost.Config) (*persistenthost.Transport, error) {
		return &persistenthost.Transport{
			Stdin: daemon, Stdout: daemon, Reconnected: true,
			ACPState: &persistenthost.ACPState{
				InitializeResult: initialize, SessionResult: session,
				SessionID: "provider-session", ActivePrompt: true,
			},
		}, nil
	}
	opened, err := driver.Resume(context.Background(), ports.ChatResumeConfig{
		SessionID: "ao-session", DataDir: t.TempDir(), WorkspacePath: t.TempDir(),
		ProviderConversationID: "provider-session",
	})
	if err != nil {
		t.Fatal(err)
	}
	conv := opened.(*conversation)
	t.Cleanup(func() { _ = conv.Close() })
	if err := conv.ActivateLiveReconnect(context.Background(), "durable-turn"); err != nil {
		t.Fatal(err)
	}
	_ = nextEvent(t, conv.Events()) // Initialization ready event.

	// A real failed restart retained 6,375 updates, exceeding both AO's event
	// buffer and the SDK's notification queue. Pause projection during replay.
	const frames = 6375
	sent := make(chan error, 1)
	go func() {
		for i := range frames {
			_, err := fmt.Fprintf(host, `{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"provider-session","_meta":{"ao.persistentEventId":"replay-%d"},"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"x"}}}}`+"\n", i)
			if err != nil {
				sent <- err
				return
			}
		}
		_, err := fmt.Fprintln(host, `{"jsonrpc":"2.0","method":"_ao/persistent_prompt_result","params":{"eventId":"terminal","result":{"stopReason":"end_turn"}}}`)
		sent <- err
	}()
	select {
	case <-conv.conn.Done():
		t.Fatal("replay disconnected instead of applying backpressure")
	case <-time.After(200 * time.Millisecond):
	}
	for i := range frames {
		event := nextEvent(t, conv.Events())
		if event.Kind != ports.ChatEventMessageDelta || event.Delta != "x" ||
			event.ProviderTurnID != "durable-turn" || event.ProviderEventID != fmt.Sprintf("replay-%d:0", i) {
			t.Fatalf("replay event %d lost or reordered: %+v", i, event)
		}
	}
	for {
		event := nextEvent(t, conv.Events())
		if event.Kind == ports.ChatEventTurnCompleted {
			if event.ProviderTurnID != "durable-turn" || event.ProviderEventID != "terminal" || event.TurnState != domain.TurnStateCompleted {
				t.Fatalf("terminal event: %+v", event)
			}
			break
		}
	}
	if err := <-sent; err != nil {
		t.Fatal(err)
	}
	select {
	case <-conv.conn.Done():
		t.Fatal("replay closed a healthy connection")
	default:
	}
}

func TestACPDisconnectReleasesTransport(t *testing.T) {
	for _, persistent := range []bool{false, true} {
		t.Run(fmt.Sprint(persistent), func(t *testing.T) {
			daemon, host := net.Pipe()
			defer host.Close()
			defer daemon.Close()
			var stops, terminations atomic.Int32
			proc := &process{
				stdin: daemon, stdout: strings.NewReader(""),
				stop: func() error { stops.Add(1); return daemon.Close() },
			}
			if persistent {
				proc.terminate = func() error { terminations.Add(1); return nil }
			}
			conv := newConversation(proc, slog.New(slog.DiscardHandler), "", nil, nil)
			_ = host.SetReadDeadline(time.Now().Add(time.Second))
			if _, err := host.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
				t.Fatalf("stopped SDK retained host socket: %v", err)
			}
			event := nextEvent(t, conv.Events())
			if event.ControllerState != ports.ChatControllerStopped || errors.Is(event.Err, ports.ErrChatRecoveryInconclusive) != persistent {
				t.Fatalf("disconnect state: %+v", event)
			}
			if stops.Load() != 1 || terminations.Load() != 0 {
				t.Fatalf("disconnect stop=%d terminate=%d", stops.Load(), terminations.Load())
			}
		})
	}
}

func TestPersistentACPPromptDisconnectPreservesTurn(t *testing.T) {
	daemon, host := net.Pipe()
	defer host.Close()
	proc := &process{
		stdin: daemon, stdout: daemon, stop: daemon.Close,
		terminate: func() error { return nil },
	}
	conv := newConversation(proc, slog.New(slog.DiscardHandler), "", nil, nil)
	conv.sessionID = "provider-session"
	conv.activeTurn = "durable-turn"
	go conv.runTurn(context.Background(), conv.sessionID, preparedTurn{id: conv.activeTurn})
	_ = host.SetReadDeadline(time.Now().Add(time.Second))
	request, err := bufio.NewReader(host).ReadBytes('\n')
	if err != nil || !strings.Contains(string(request), "session/prompt") {
		t.Fatalf("prompt request: %s, %v", request, err)
	}
	_ = host.Close()
	for event := range conv.Events() {
		if event.Kind == ports.ChatEventTurnCompleted {
			t.Fatalf("lost attachment falsely completed provider turn: %+v", event)
		}
	}
	conv.mu.Lock()
	defer conv.mu.Unlock()
	if conv.activeTurn != "durable-turn" {
		t.Fatal("disconnect discarded live prompt ownership")
	}
}

func TestPersistentACPPromptFailsWhenTheHostExited(t *testing.T) {
	for _, tc := range []struct {
		name       string
		free       uint64
		code       domain.ConversationFailureCode
		message    string
		compaction bool
	}{
		{name: "plenty of disk", free: 40 << 30, message: "the agent's process exited before this turn finished"},
		{name: "disk nearly full", free: 100 << 20, code: domain.ConversationFailureDiskFull,
			message: "the agent's process exited while the disk was nearly full (100 MB free)"},
		{name: "compaction", free: 40 << 30, message: "the agent's process exited before this turn finished", compaction: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			previous := diskFree
			diskFree = func(string) (uint64, error) { return tc.free, nil }
			t.Cleanup(func() { diskFree = previous })
			daemon, host := net.Pipe()
			defer host.Close()
			proc := &process{
				stdin: daemon, stdout: daemon, stop: daemon.Close,
				terminate:  func() error { return nil },
				hostExited: func() bool { return true },
			}
			conv := newConversation(proc, slog.New(slog.DiscardHandler), "", nil, nil)
			conv.sessionID = "provider-session"
			conv.activeTurn = "durable-turn"
			if tc.compaction {
				conv.compactingTurnID = conv.activeTurn
			}
			go conv.runTurn(context.Background(), conv.sessionID, preparedTurn{id: conv.activeTurn})
			_ = host.SetReadDeadline(time.Now().Add(time.Second))
			request, err := bufio.NewReader(host).ReadBytes('\n')
			if err != nil || !strings.Contains(string(request), "session/prompt") {
				t.Fatalf("prompt request: %s, %v", request, err)
			}
			_ = host.Close()
			var completed, stopped, failure *ports.ChatEvent
			for event := range conv.Events() {
				switch {
				case event.Kind == ports.ChatEventError:
					failure = &event
				case event.Kind == ports.ChatEventTurnCompleted:
					if tc.compaction && failure == nil {
						t.Fatal("compaction completion arrived before its error row")
					}
					completed = &event
				case event.ControllerState == ports.ChatControllerStopped:
					stopped = &event
				}
			}
			if completed == nil || completed.ProviderTurnID != "durable-turn" || completed.TurnState != domain.TurnStateFailed ||
				completed.FailureCode != tc.code || completed.Err == nil || completed.Err.Error() != tc.message {
				t.Fatalf("turn after the host exited: %+v", completed)
			}
			if stopped == nil || errors.Is(stopped.Err, ports.ErrChatRecoveryInconclusive) {
				t.Fatalf("stop after the host exited: %+v", stopped)
			}
			if tc.compaction != (failure != nil) || failure != nil && (failure.ProviderTurnID != "durable-turn" || failure.Err.Error() != tc.message) {
				t.Fatalf("error row after the host exited: %+v", failure)
			}
		})
	}
}

func TestPersistentACPHostExitKeepsAResultAlreadyReceived(t *testing.T) {
	daemon, host := net.Pipe()
	defer host.Close()
	conv := newConversation(&process{
		stdin: daemon, stdout: daemon, stop: daemon.Close,
		terminate:  func() error { return nil },
		hostExited: func() bool { return true },
	}, slog.New(slog.DiscardHandler), "", nil, nil)
	conv.sessionID = "provider-session"
	conv.activeTurn = "durable-turn"
	conv.turnModelID, conv.turnModel = conv.activeTurn, "selected-model"
	settling := make(chan struct{})
	conv.servedModel = func(context.Context, string, string) (string, string) {
		close(settling)
		for deadline := time.Now().Add(2 * time.Second); !conv.hostGone.Load() && time.Now().Before(deadline); {
			time.Sleep(time.Millisecond)
		}
		return "", ""
	}
	go conv.runTurn(context.Background(), conv.sessionID, preparedTurn{id: conv.activeTurn})
	_ = host.SetDeadline(time.Now().Add(time.Second))
	request, err := bufio.NewReader(host).ReadBytes('\n')
	var prompt struct {
		ID json.RawMessage `json:"id"`
	}
	if err != nil || json.Unmarshal(request, &prompt) != nil {
		t.Fatalf("prompt request: %s, %v", request, err)
	}
	if _, err := fmt.Fprintf(host, `{"jsonrpc":"2.0","id":%s,"result":{"stopReason":"end_turn"}}`+"\n", prompt.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-settling:
	case <-time.After(time.Second):
		t.Fatal("the prompt result never reached settlement")
	}
	_ = host.Close()
	var completions []ports.ChatEvent
	for event := range conv.Events() {
		if event.Kind == ports.ChatEventTurnCompleted {
			completions = append(completions, event)
		}
	}
	if len(completions) != 1 || completions[0].TurnState != domain.TurnStateCompleted || completions[0].Err != nil {
		t.Fatalf("completions after the host exited mid-settlement: %+v", completions)
	}
}

func TestACPDisconnectWithFullEventBufferCanClose(t *testing.T) {
	daemon, host := net.Pipe()
	defer host.Close()
	conv := newConversation(&process{
		stdin: daemon, stdout: daemon, stop: daemon.Close,
		terminate: func() error { return nil },
	}, slog.New(slog.DiscardHandler), "", nil, nil)
	for range eventBuffer {
		conv.events <- ports.ChatEvent{Kind: ports.ChatEventMessageDelta}
	}
	written := make(chan error, 1)
	go func() {
		_, err := fmt.Fprintln(host, `{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"provider-session","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"x"}}}}`)
		written <- err
	}()
	if err := <-written; err != nil {
		t.Fatal(err)
	}
	_ = conv.Close() // A failed projector stops consuming before Close.
	select {
	case <-conv.conn.Done():
	case <-time.After(time.Second):
		t.Fatal("detach could not stop replay without an event consumer")
	}
	_ = host.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := host.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
	done := make(chan int, 1)
	go func() {
		drained := 0
		for range conv.Events() {
			drained++
		}
		done <- drained
	}()
	select {
	case drained := <-done:
		if drained < eventBuffer {
			t.Fatalf("only drained %d buffered events", drained)
		}
	case <-time.After(time.Second):
		t.Fatal("disconnect leaked event stream after projection stopped")
	}
}

func TestPersistentACPReceivedPromptReceiptSurvivesDisconnect(t *testing.T) {
	const receipt = "terminal-before-eof"
	for _, test := range []struct {
		name     string
		response acpsdk.PromptResponse
		err      error
		settled  bool
	}{
		{
			name: "successful response",
			response: acpsdk.PromptResponse{StopReason: acpsdk.StopReasonEndTurn,
				Meta: map[string]any{persistenthost.ACPEventIDMetaKey: receipt}},
			settled: true,
		},
		{
			name: "provider failure response",
			err: &acpsdk.RequestError{Code: -32000, Message: "provider rejected prompt",
				Data: map[string]any{persistenthost.ACPEventIDMetaKey: receipt}},
			settled: true,
		},
		{
			name: "transport failure without receipt",
			err:  acpsdk.NewInternalError(map[string]any{"error": "peer disconnected before response"}),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			// runTurn can obtain a terminal response before EOF closes SDK Done,
			// then reach finishPrompt after that disconnect. Keep the receipt
			// boundary separate from the event stream's independent close race.
			conn := acpsdk.NewClientSideConnection(nil, io.Discard, strings.NewReader(""))
			select {
			case <-conn.Done():
			case <-time.After(time.Second):
				t.Fatal("EOF did not close SDK connection")
			}
			conv := &conversation{
				conn: conn, proc: &process{terminate: func() error { return nil }},
				log: slog.New(slog.DiscardHandler), events: make(chan ports.ChatEvent, 16),
				activeTurn: "durable-turn", closing: make(chan struct{}),
			}
			conv.finishPrompt("durable-turn", test.response, test.err)
			if test.settled {
				if conv.activeTurn != "" || conv.terminalEventID != receipt {
					t.Fatalf("received terminal receipt discarded after EOF: active=%q receipt=%q",
						conv.activeTurn, conv.terminalEventID)
				}
			} else if conv.activeTurn != "durable-turn" || conv.terminalEventID != "" {
				t.Fatalf("transport failure supplied an unsupported outcome: active=%q receipt=%q",
					conv.activeTurn, conv.terminalEventID)
			}
		})
	}
}

// A daemon stopped mid-start can leave a persistent host that never got a
// session. It holds no conversation, so it is replaced rather than blocking
// every later start as an inconclusive recovery.
func TestResumeReplacesAHostThatNeverFinishedStarting(t *testing.T) {
	agent := &fakeAgent{capabilities: &acpsdk.AgentCapabilities{LoadSession: true}}
	driver := New(Config{
		Harness:      domain.HarnessClaudeCode,
		Capabilities: ports.ChatCapabilities{ports.ChatCapabilityStreaming: true},
		Probe:        func(context.Context) error { return nil },
		Launch:       func(context.Context, LaunchConfig) (Launch, error) { return Launch{Command: "fake"}, nil },
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	spawns, terminated := 0, false
	fresh := fakeSpawn(agent)
	driver.useTestProcess(func(launch Launch, workdir string) (*process, error) {
		spawns++
		if spawns > 1 {
			return fresh(launch, workdir)
		}
		stdinR, stdinW := io.Pipe()
		stdoutR, stdoutW := io.Pipe()
		t.Cleanup(func() { _ = stdinR.Close(); _ = stdoutW.Close() })
		stop := func() error { _ = stdinW.Close(); _ = stdoutR.Close(); return nil }
		return &process{
			stdin: stdinW, stdout: stdoutR, reconnected: true, acpState: &persistenthost.ACPState{},
			stop: stop, terminate: func() error { terminated = true; return stop() },
		}, nil
	})

	conv, err := driver.Resume(context.Background(), ports.ChatResumeConfig{
		ProviderConversationID: "provider-session-1", WorkspacePath: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	defer func() { _ = conv.Close() }()
	if !terminated || spawns != 2 {
		t.Fatalf("host terminated=%v spawns=%d, want the empty host stopped and the agent started again", terminated, spawns)
	}
}
