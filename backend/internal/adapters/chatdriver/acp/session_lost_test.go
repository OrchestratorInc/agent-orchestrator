package acp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestIsACPSessionMissing(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		// claude-agent-acp throws Error("Session not found"); the SDK sends it as
		// -32603 with data.details, exactly as the user-visible error reads.
		{"adapter", &acpsdk.RequestError{Code: -32603, Message: "Internal error",
			Data: map[string]any{"details": "Session not found"}}, true},
		// AO's persistent host wraps prompt error data to add its event id.
		{"host wrapped", &acpsdk.RequestError{Code: -32603, Message: "Internal error",
			Data: map[string]any{"providerData": map[string]any{"details": "Session not found"}, "ao.persistentEventId": "e1"}}, true},
		{"wrapped in fmt", fmt.Errorf("set ACP session option %q: %w", "model", &acpsdk.RequestError{
			Code: -32603, Data: map[string]any{"details": "Session not found"}}), true},
		{"other internal error", &acpsdk.RequestError{Code: -32603, Data: map[string]any{"details": "context deadline exceeded"}}, false},
		{"internal error without data", &acpsdk.RequestError{Code: -32603, Message: "Internal error"}, false},
		{"other code", &acpsdk.RequestError{Code: -32602, Data: map[string]any{"details": "Session not found"}}, false},
		{"plain error", errors.New("Session not found"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := isACPSessionMissing(tc.err); got != tc.want {
				t.Fatalf("isACPSessionMissing(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// lostSessionHost is a persistent provider host whose agent answers every
// request with claude-agent-acp's "Session not found".
type lostSessionHost struct {
	conv         *conversation
	host         net.Conn
	terminations atomic.Int32
	requests     chan string
}

func newLostSessionHost(t *testing.T) *lostSessionHost {
	t.Helper()
	daemon, host := net.Pipe()
	t.Cleanup(func() { _ = host.Close(); _ = daemon.Close() })
	h := &lostSessionHost{host: host, requests: make(chan string, 8)}
	proc := &process{
		stdin: daemon, stdout: daemon, stop: daemon.Close, reconnected: true,
		terminate: func() error { h.terminations.Add(1); return daemon.Close() },
	}
	h.conv = newConversation(proc, slog.New(slog.DiscardHandler), "", nil, nil)
	h.conv.sessionID = "provider-session"
	go func() {
		reader := bufio.NewReader(host)
		for {
			line, err := reader.ReadBytes('\n')
			if err != nil {
				return
			}
			var request struct {
				ID     json.RawMessage `json:"id"`
				Method string          `json:"method"`
			}
			if json.Unmarshal(line, &request) != nil || len(request.ID) == 0 {
				continue
			}
			h.requests <- request.Method
			reply := fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"error":{"code":-32603,"message":"Internal error","data":{"details":"Session not found"}}}`+"\n", request.ID)
			if _, err := host.Write([]byte(reply)); err != nil {
				return
			}
		}
	}()
	return h
}

// assertStoppedForResume checks the provider was terminated and the stream
// ended as an ordinary stop. The controller treats the stream closing as the
// stop signal and reports the session exited; an explicit stop event may or may
// not precede it after a deliberate close. An inconclusive stop would preserve
// the dead provider instead, and readiness would drain queued work into it.
func (h *lostSessionHost) assertStoppedForResume(t *testing.T) []ports.ChatEvent {
	t.Helper()
	var seen []ports.ChatEvent
	deadline := time.After(5 * time.Second)
	for {
		select {
		case event, ok := <-h.conv.Events():
			if !ok {
				if got := h.terminations.Load(); got != 1 {
					t.Fatalf("provider terminations = %d, want 1", got)
				}
				return seen
			}
			seen = append(seen, event)
			if event.Kind == ports.ChatEventControllerState && event.ControllerState == ports.ChatControllerReady {
				t.Fatalf("lost session announced readiness: %+v", seen)
			}
			if event.Kind == ports.ChatEventControllerState && event.ControllerState == ports.ChatControllerStopped &&
				errors.Is(event.Err, ports.ErrChatRecoveryInconclusive) {
				t.Fatalf("lost session stopped as inconclusive, which preserves the dead provider: %+v", event)
			}
		case <-deadline:
			t.Fatalf("provider was not stopped; events: %+v", seen)
		}
	}
}

func TestSendTurnStopsProviderThatLostSession(t *testing.T) {
	h := newLostSessionHost(t)
	h.conv.optionsFor = func(ports.ChatTurnSettings) []SessionOption {
		return []SessionOption{{ID: "model", Value: "opus"}}
	}
	_, err := h.conv.SendTurn(context.Background(), ports.ChatUserMessage{
		Text: "hello", Settings: ports.ChatTurnSettings{Model: "opus"},
	})
	if !errors.Is(err, ports.ErrChatProviderSessionLost) {
		t.Fatalf("SendTurn error = %v, want ErrChatProviderSessionLost", err)
	}
	if method := <-h.requests; method != "session/set_config_option" {
		t.Fatalf("first request = %q", method)
	}
	h.assertStoppedForResume(t)
	// The stopped provider took its journal with it; the controller's ACK must
	// not fail and turn the stop into a preserved provider.
	if err := h.conv.AcknowledgeProviderEvent(context.Background(), "e1"); err != nil {
		t.Fatalf("ack after lost session: %v", err)
	}
}

func TestPromptFailsTurnAndStopsProviderThatLostSession(t *testing.T) {
	h := newLostSessionHost(t)
	h.conv.activeTurn = "durable-turn"
	go h.conv.runTurn(context.Background(), h.conv.sessionID, preparedTurn{id: "durable-turn"})
	if method := <-h.requests; method != "session/prompt" {
		t.Fatalf("first request = %q", method)
	}
	events := h.assertStoppedForResume(t)
	var completed *ports.ChatEvent
	for i := range events {
		if events[i].Kind == ports.ChatEventTurnCompleted {
			completed = &events[i]
		}
	}
	if completed == nil || completed.ProviderTurnID != "durable-turn" || completed.TurnState != domain.TurnStateFailed ||
		!errors.Is(completed.Err, ports.ErrChatProviderSessionLost) {
		t.Fatalf("turn was not failed with the lost-session cause: %+v", events)
	}
	h.conv.mu.Lock()
	defer h.conv.mu.Unlock()
	if h.conv.activeTurn != "" {
		t.Fatalf("lost session kept the turn active: %q", h.conv.activeTurn)
	}
}

func TestInterruptedPromptIntoLostSessionStillStopsProvider(t *testing.T) {
	h := newLostSessionHost(t)
	h.conv.activeTurn = "durable-turn"
	// session/cancel was accepted before the prompt answered "Session not found".
	done := make(chan struct{})
	close(done)
	h.conv.interrupt = &interruptAttempt{turnID: "durable-turn", done: done}
	go h.conv.runTurn(context.Background(), h.conv.sessionID, preparedTurn{id: "durable-turn"})
	if method := <-h.requests; method != "session/prompt" {
		t.Fatalf("first request = %q", method)
	}
	events := h.assertStoppedForResume(t)
	for _, event := range events {
		if event.Kind == ports.ChatEventTurnCompleted {
			if event.TurnState != domain.TurnStateFailed || !errors.Is(event.Err, ports.ErrChatProviderSessionLost) {
				t.Fatalf("interrupted lost-session turn: %+v", event)
			}
			return
		}
	}
	t.Fatalf("no turn completion: %+v", events)
}

func sessionNotFoundError() error {
	return &acpsdk.RequestError{Code: -32603, Message: "Internal error",
		Data: map[string]any{"details": "Session not found"}}
}

// A Close that detached first (daemon shutdown) consumed closeOnce. The lost
// session must still terminate the host, or the next daemon reattaches to it.
func TestLostSessionEscalatesAnEarlierDetach(t *testing.T) {
	h := newLostSessionHost(t)
	if err := h.conv.Close(); err != nil {
		t.Fatal(err)
	}
	if got := h.terminations.Load(); got != 0 {
		t.Fatalf("detach terminated the host: %d", got)
	}
	err := h.conv.providerSessionLost(fmt.Errorf("set ACP session option %q: %w", "model", sessionNotFoundError()))
	if !errors.Is(err, ports.ErrChatProviderSessionLost) {
		t.Fatalf("error = %v, want ErrChatProviderSessionLost", err)
	}
	if got := h.terminations.Load(); got != 1 {
		t.Fatalf("provider terminations after lost session = %d, want 1", got)
	}
}

func TestLostSessionReportsAFailedStop(t *testing.T) {
	h := newLostSessionHost(t)
	stopErr := errors.New("host shutdown timed out")
	h.conv.proc.terminate = func() error { h.terminations.Add(1); return stopErr }
	err := h.conv.providerSessionLost(sessionNotFoundError())
	if !errors.Is(err, ports.ErrChatProviderSessionLost) || !errors.Is(err, stopErr) {
		t.Fatalf("error = %v, want both the lost session and the failed stop", err)
	}
}

func TestSteerIntoLostSessionIsARefusalAndStopsProvider(t *testing.T) {
	h := newLostSessionHost(t)
	h.conv.activeTurn = "durable-turn"
	_, err := h.conv.Steer(context.Background(), "durable-turn", ports.ChatUserMessage{Text: "focus on the backend"})
	if !errors.Is(err, ports.ErrChatProviderSessionLost) {
		t.Fatalf("Steer error = %v, want ErrChatProviderSessionLost", err)
	}
	// A refusal settles the steer's delivery handle as rejected instead of
	// leaving it uncertain, so recovery cannot report it as accepted.
	var refusal interface{ ChatRefusal() bool }
	if !errors.As(err, &refusal) || !refusal.ChatRefusal() {
		t.Fatalf("Steer error is not a definitive refusal: %v", err)
	}
	if method := <-h.requests; !strings.HasPrefix(method, "_session/steering") {
		t.Fatalf("first request = %q", method)
	}
	h.assertStoppedForResume(t)
}
