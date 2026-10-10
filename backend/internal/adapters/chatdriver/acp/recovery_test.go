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
