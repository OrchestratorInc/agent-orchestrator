package acp

import (
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// persistentPipeConversation builds a conversation over a persistent-host style
// transport whose provider side the test writes directly, as a reconnecting host
// replays its journal. stopped closes when AO releases the transport.
func persistentPipeConversation(t *testing.T) (*conversation, *io.PipeWriter, <-chan struct{}) {
	t.Helper()
	agentToClientR, agentToClientW := io.Pipe()
	clientToAgentR, clientToAgentW := io.Pipe()
	go func() { _, _ = io.Copy(io.Discard, clientToAgentR) }()
	stopped := make(chan struct{})
	var once sync.Once
	proc := &process{stdin: clientToAgentW, stdout: agentToClientR}
	proc.stop = func() error {
		once.Do(func() { close(stopped) })
		return nil
	}
	proc.terminate = proc.stop
	conv := newConversation(proc, slog.New(slog.NewTextHandler(io.Discard, nil)), "", nil, nil)
	t.Cleanup(func() {
		_ = agentToClientW.Close()
		_ = clientToAgentW.Close()
		_ = clientToAgentR.Close()
	})
	return conv, agentToClientW, stopped
}

func sessionUpdateFrame(i int) string {
	return fmt.Sprintf(`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"s1",`+
		`"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"%d\n"}}}}`+"\n", i)
}

// Issue #6200: a persistent host replays an in-flight prompt's whole journal on
// reconnect. AO projects persistent events with backpressure, so the replay
// outran it, overflowed the SDK's 1,024-slot notification queue, and the SDK
// closed the connection. The burst must instead wait in the transport and arrive
// complete once AO catches up.
func TestPersistentReplayBurstDoesNotOverflowSDKNotificationQueue(t *testing.T) {
	conv, provider, _ := persistentPipeConversation(t)
	const updates = eventBuffer + 2000 // fills the event buffer, then the SDK queue
	go func() {
		for i := 0; i < updates; i++ {
			if _, err := io.WriteString(provider, sessionUpdateFrame(i)); err != nil {
				return
			}
		}
	}()

	// The controller is stalled, as when it projects each event into SQLite on a
	// machine under memory pressure.
	select {
	case <-conv.conn.Done():
		t.Fatal("SDK closed the connection while AO was still projecting the replay")
	case <-time.After(500 * time.Millisecond):
	}

	received := 0
	deadline := time.After(10 * time.Second)
	for received < updates {
		select {
		case event := <-conv.Events():
			if event.Kind == ports.ChatEventMessageDelta {
				received++
			}
		case <-deadline:
			t.Fatalf("received %d of %d replayed updates", received, updates)
		}
	}
	select {
	case <-conv.conn.Done():
		t.Fatal("connection closed after the replay drained")
	default:
	}
}

// When the SDK stops reading for any reason while the host socket stays open,
// AO must release the transport. Otherwise the host blocks writing into it and
// every later attach times out (#6200).
func TestPersistentTransportIsReleasedWhenSDKConnectionEnds(t *testing.T) {
	conv, provider, stopped := persistentPipeConversation(t)
	oversized := `{"jsonrpc":"2.0","method":"session/update","params":"` +
		strings.Repeat("x", maxACPFrameSize) + `"}` + "\n"
	go func() { _, _ = io.WriteString(provider, oversized) }()

	select {
	case <-conv.conn.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("SDK connection did not end on an oversized frame")
	}
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("persistent transport was left open after the SDK connection ended")
	}
}

func TestDispatchesSessionUpdateMatchesSDKDispatch(t *testing.T) {
	for _, tc := range []struct {
		name  string
		frame string
		want  bool
	}{
		{"notification", sessionUpdateFrame(1), true},
		{"null id", `{"jsonrpc":"2.0","id":null,"method":"session/update","params":{"sessionId":"s1","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"a"}}}}`, true},
		{"request", `{"jsonrpc":"2.0","id":7,"method":"session/update","params":{"sessionId":"s1","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"a"}}}}`, false},
		{"params the SDK rejects", `{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":5}}`, false},
		{"other notification", `{"jsonrpc":"2.0","method":"_ao/prompt_ack","params":{}}`, false},
		{"response", `{"jsonrpc":"2.0","id":3,"result":{}}`, false},
		{"malformed", `{not json`, false},
	} {
		if got := dispatchesSessionUpdate([]byte(tc.frame)); got != tc.want {
			t.Errorf("%s: dispatchesSessionUpdate = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestSessionUpdateFlowReaderPassesFramesThrough(t *testing.T) {
	input := sessionUpdateFrame(1) + `{"jsonrpc":"2.0","id":3,"result":{}}` + "\n"
	flow := newSessionUpdateFlow(4)
	out, err := io.ReadAll(newSessionUpdateFlowReader(strings.NewReader(input), flow))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, []byte(input)) {
		t.Fatalf("reader output = %q, want %q", out, input)
	}
	if len(flow.slots) != 1 {
		t.Fatalf("reserved slots = %d, want 1 for the one session/update", len(flow.slots))
	}
}
