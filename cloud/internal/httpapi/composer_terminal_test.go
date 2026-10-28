package httpapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
)

// terminalGateStore admits agent terminal input while allow is set, counts
// the admission checks, and records the input that reached the agent.
type terminalGateStore struct {
	Store
	mu      sync.Mutex
	allow   bool
	checks  int
	queued  []string
	queueCh chan string
}

func newTerminalGateStore(allow bool) *terminalGateStore {
	return &terminalGateStore{allow: allow, queueCh: make(chan string, 16)}
}

func (s *terminalGateStore) ComposerTerminalInput(context.Context, string, string, string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.checks++
	return s.allow, nil
}

func (s *terminalGateStore) QueueTerminalInput(_ context.Context, _ domain.TerminalSession, _ string, data []byte) error {
	s.mu.Lock()
	s.queued = append(s.queued, string(data))
	s.mu.Unlock()
	s.queueCh <- string(data)
	return nil
}

func (s *terminalGateStore) setAllow(allow bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.allow = allow
}

func (s *terminalGateStore) checkCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.checks
}

func agentTerminal(userID string, scopes ...string) domain.TerminalSession {
	return domain.TerminalSession{
		ID: "term", OrgID: "org", SessionID: "session", WorkerEpoch: 1,
		Kind: "agent", UserID: userID, Scopes: scopes,
	}
}

// openTerminalInput runs readTerminalInput for terminal behind a websocket
// and returns the client end.
func openTerminalInput(t *testing.T, server *Server, terminal domain.TerminalSession) *websocket.Conn {
	t.Helper()
	listener := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer connection.CloseNow()
		var writeMu sync.Mutex
		_ = server.readTerminalInput(r.Context(), connection, terminal, &writeMu)
	}))
	t.Cleanup(listener.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	connection, _, err := websocket.Dial(ctx, "ws"+listener.URL[len("http"):], nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.CloseNow() })
	return connection
}

func typeInto(t *testing.T, connection *websocket.Conn, inputID, data string) {
	t.Helper()
	payload, _ := json.Marshal(map[string]string{"type": "input", "inputId": inputID, "data": data})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := connection.Write(ctx, websocket.MessageText, payload); err != nil {
		t.Fatal(err)
	}
}

func readTerminalMessage(t *testing.T, connection *websocket.Conn) terminalServerMessage {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, payload, err := connection.Read(ctx)
	if err != nil {
		t.Fatalf("read terminal message: %v", err)
	}
	var message terminalServerMessage
	if err := json.Unmarshal(payload, &message); err != nil {
		t.Fatal(err)
	}
	return message
}

func gateTestServer(store Store) *Server {
	return &Server{store: store, logger: slog.Default(), sessionLiveWaiters: newWorkWaiters()}
}

// Only agent terminals a person may operate are gated: a workspace shell, a
// read-only terminal, and a ticket that names no user are not.
func TestComposerInputGateAppliesOnlyToOperatedAgentTerminals(t *testing.T) {
	t.Parallel()
	server := gateTestServer(newTerminalGateStore(false))
	ctx := context.Background()
	if gate := server.newComposerInputGate(ctx, agentTerminal("user-1", "terminal:read", "terminal:operate")); gate == nil {
		t.Fatal("an operated agent terminal is not gated")
	} else {
		gate.close()
	}
	workspace := agentTerminal("user-1", "terminal:read", "terminal:operate")
	workspace.Kind = "workspace"
	for name, terminal := range map[string]domain.TerminalSession{
		"workspace shell": workspace,
		"read-only":       agentTerminal("user-1", "terminal:read"),
		"no user":         agentTerminal("", "terminal:read", "terminal:operate"),
	} {
		if gate := server.newComposerInputGate(ctx, terminal); gate != nil {
			t.Fatalf("%s terminal is gated", name)
		}
	}
	if gate := gateTestServer(&growingEventStore{}).newComposerInputGate(ctx, agentTerminal("user-1", "terminal:operate")); gate != nil {
		t.Fatal("gated without store support")
	}
}

// An admitted keystroke admits the ones after it for a short grace without
// asking the store again; a lease change ends the grace at once.
func TestComposerInputGateCachesAdmissionUntilTheLeaseChanges(t *testing.T) {
	t.Parallel()
	store := newTerminalGateStore(true)
	server := gateTestServer(store)
	gate := server.newComposerInputGate(context.Background(), agentTerminal("user-1", "terminal:operate"))
	defer gate.close()
	now := time.Now()
	for i := 0; i < 5; i++ {
		if admitted, err := gate.admit(context.Background(), now.Add(time.Duration(i)*100*time.Millisecond)); !admitted || err != nil {
			t.Fatalf("keystroke %d: %v %v", i, admitted, err)
		}
	}
	if checks := store.checkCount(); checks != 1 {
		t.Fatalf("store checked %d times within the grace, want 1", checks)
	}
	if admitted, _ := gate.admit(context.Background(), now.Add(composerInputGrace+time.Millisecond)); !admitted || store.checkCount() != 2 {
		t.Fatalf("after the grace: checks = %d", store.checkCount())
	}

	// Someone takes over: the lease change wakes the gate, which asks again.
	store.setAllow(false)
	server.HandleSessionLiveNotify("session")
	deadline := time.Now().Add(2 * time.Second)
	for gate.admitUntil.Load() != 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if admitted, _ := gate.admit(context.Background(), now.Add(composerInputGrace+2*time.Millisecond)); admitted {
		t.Fatal("still admitted after the lease changed")
	}
}

// Without the lease, keystrokes never reach the agent; the client is told
// which input was refused and the terminal stays open. With the lease they
// go through.
func TestAgentTerminalInputNeedsTheComposerLease(t *testing.T) {
	t.Parallel()
	store := newTerminalGateStore(false)
	connection := openTerminalInput(t, gateTestServer(store), agentTerminal("user-1", "terminal:read", "terminal:operate"))

	typeInto(t, connection, "in-1", "rm -rf")
	refused := readTerminalMessage(t, connection)
	if refused.Type != "input_refused" || refused.InputID != "in-1" || refused.Message == "" {
		t.Fatalf("refusal = %+v", refused)
	}

	store.setAllow(true)
	typeInto(t, connection, "in-2", "ls\r")
	select {
	case data := <-store.queueCh:
		if data != "ls\r" {
			t.Fatalf("queued %q", data)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("admitted input never reached the agent")
	}
	if ack := readTerminalMessage(t, connection); ack.Type != "input_ack" || ack.InputID != "in-2" {
		t.Fatalf("ack = %+v", ack)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.queued) != 1 {
		t.Fatalf("queued %v, want only the admitted input", store.queued)
	}
}
