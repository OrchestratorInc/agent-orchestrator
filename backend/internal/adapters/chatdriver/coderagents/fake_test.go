package coderagents

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/coderagents/coderapi"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const fakeToken = "fake-session-token"

// fakeCoder is an in-process Coder deployment speaking the v2.37 chat API
// subset the driver uses. Tests push stream batches through push.
type fakeCoder struct {
	t      *testing.T
	server *httptest.Server
	push   chan []coderapi.StreamEvent

	mu              sync.Mutex
	noChatsAPI      bool
	chats           map[string]*coderapi.Chat
	history         []coderapi.ChatMessage
	creates         []map[string]any
	sent            []map[string]any
	results         [][]coderapi.ToolResult
	interrupts      int
	interruptStatus string
}

func newFakeCoder(t *testing.T) *fakeCoder {
	t.Helper()
	f := &fakeCoder{
		t: t, push: make(chan []coderapi.StreamEvent, 64),
		chats: map[string]*coderapi.Chat{}, interruptStatus: coderapi.ChatStatusInterrupting,
	}
	f.server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeCoder) Connect(context.Context, map[string]string) (*coderapi.Client, coderapi.Credentials, error) {
	base, err := coderapi.ParseDeploymentURL(f.server.URL)
	if err != nil {
		return nil, coderapi.Credentials{}, err
	}
	creds := coderapi.Credentials{URL: base, Token: fakeToken}
	return coderapi.NewClient(creds, f.server.Client().Transport), creds, nil
}

func (f *fakeCoder) writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func (f *fakeCoder) serve(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get(coderapi.SessionTokenHeader) != fakeToken {
		f.writeJSON(w, http.StatusUnauthorized, map[string]string{"message": "unauthorized"})
		return
	}
	path := r.URL.Path
	switch {
	case path == "/api/v2/users/me":
		f.writeJSON(w, http.StatusOK, map[string]any{"id": "u1", "username": "dev", "organization_ids": []string{"o1"}})
	case path == "/api/v2/users/me/organizations":
		f.writeJSON(w, http.StatusOK, []map[string]any{{"id": "o1", "name": "coder", "is_default": true}})
	case path == "/api/v2/organizations/o1/chats/models":
		f.mu.Lock()
		missing := f.noChatsAPI
		f.mu.Unlock()
		if missing {
			http.NotFound(w, r)
			return
		}
		f.writeJSON(w, http.StatusOK, map[string]any{"models": []map[string]any{
			{"id": "m1", "model": "glm-5", "display_name": "GLM 5", "enabled": true, "is_default": true, "reasoning_efforts": []string{"low", "high"}},
		}})
	case path == "/api/v2/chats" && r.Method == http.MethodPost:
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		labels := map[string]string{}
		if raw, ok := body["labels"].(map[string]any); ok {
			for key, value := range raw {
				labels[key], _ = value.(string)
			}
		}
		f.mu.Lock()
		f.creates = append(f.creates, body)
		chat := &coderapi.Chat{ID: "chat-1", OwnerID: "u1", Status: coderapi.ChatStatusRunning, Labels: labels}
		f.chats[chat.ID] = chat
		f.mu.Unlock()
		f.writeJSON(w, http.StatusCreated, chat)
	case path == "/api/v2/chats" && r.Method == http.MethodGet:
		want := map[string]string{}
		for _, label := range r.URL.Query()["label"] {
			key, value, _ := strings.Cut(label, ":")
			want[key] = value
		}
		f.mu.Lock()
		out := []coderapi.Chat{}
		for _, chat := range f.chats {
			match := true
			for key, value := range want {
				match = match && chat.Labels[key] == value
			}
			if match {
				out = append(out, *chat)
			}
		}
		f.mu.Unlock()
		f.writeJSON(w, http.StatusOK, out)
	case strings.HasPrefix(path, "/api/v2/chats/"):
		f.serveChat(w, r, strings.Split(strings.TrimPrefix(path, "/api/v2/chats/"), "/"))
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeCoder) serveChat(w http.ResponseWriter, r *http.Request, parts []string) {
	f.mu.Lock()
	chat, ok := f.chats[parts[0]]
	f.mu.Unlock()
	if !ok {
		http.NotFound(w, r)
		return
	}
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}
	switch action {
	case "":
		f.mu.Lock()
		copied := *chat
		f.mu.Unlock()
		f.writeJSON(w, http.StatusOK, copied)
	case "messages":
		if r.Method == http.MethodPost {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			f.mu.Lock()
			f.sent = append(f.sent, body)
			f.mu.Unlock()
			f.writeJSON(w, http.StatusOK, map[string]any{"queued": false})
			return
		}
		f.mu.Lock()
		history := append([]coderapi.ChatMessage(nil), f.history...)
		f.mu.Unlock()
		f.writeJSON(w, http.StatusOK, map[string]any{"messages": history})
	case "interrupt":
		f.mu.Lock()
		f.interrupts++
		copied := *chat
		copied.Status = f.interruptStatus
		f.mu.Unlock()
		f.writeJSON(w, http.StatusOK, copied)
	case "tool-results":
		var body coderapi.SubmitToolResultsRequest
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.results = append(f.results, body.Results)
		f.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	case "stream":
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		// The request context of a hijacked connection is not canceled when
		// the client leaves; CloseRead's context is.
		ctx := conn.CloseRead(r.Context())
		for {
			select {
			case batch := <-f.push:
				if err := wsjson.Write(ctx, conn, batch); err != nil {
					return
				}
			case <-ctx.Done():
				return
			}
		}
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeCoder) snapshot() (creates, sent []map[string]any, results [][]coderapi.ToolResult, interrupts int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]map[string]any(nil), f.creates...), append([]map[string]any(nil), f.sent...),
		append([][]coderapi.ToolResult(nil), f.results...), f.interrupts
}

func (f *fakeCoder) waitResults(t *testing.T, n int) [][]coderapi.ToolResult {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, _, results, _ := f.snapshot(); len(results) >= n {
			return results
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("Coder never received %d tool result batches", n)
	return nil
}

// nextEvent returns the next event of kind, skipping others.
func nextEvent(t *testing.T, events <-chan ports.ChatEvent, kind ports.ChatEventKind) ports.ChatEvent {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case event, ok := <-events:
			if !ok {
				t.Fatalf("events closed before %s", kind)
			}
			if event.Kind == kind {
				return event
			}
		case <-timeout:
			t.Fatalf("no %s event", kind)
		}
	}
}

func status(value string) coderapi.StreamEvent {
	return coderapi.StreamEvent{Type: coderapi.EventStatus, Status: &coderapi.StreamStatus{Status: value}}
}

func actionRequired(calls ...coderapi.ToolCall) coderapi.StreamEvent {
	return coderapi.StreamEvent{Type: coderapi.EventActionRequired, ActionRequired: &coderapi.StreamActionRequired{ToolCalls: calls}}
}
