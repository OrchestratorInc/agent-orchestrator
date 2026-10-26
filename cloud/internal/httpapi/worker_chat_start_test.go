package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
)

type chatStartStore struct {
	reconnectStore
	moved     bool
	calls     int
	code      string
	detail    string
	workerID  string
	workerErr error
}

func (s *chatStartStore) FallBackChatStart(_ context.Context, _, _, code, detail string) (bool, error) {
	s.calls++
	s.code, s.detail = code, detail
	return s.moved, nil
}

func (s *chatStartStore) WorkerFallBackChatStart(_ context.Context, _, _, workerID string, _ int64, code, detail string) (bool, error) {
	s.calls++
	s.code, s.detail, s.workerID = code, detail, workerID
	return s.moved, s.workerErr
}

func reconnectLaunch(t *testing.T, srv *Server, capabilities string) worker.LaunchContext {
	t.Helper()
	req := workerRequest(t, http.MethodGet, "/worker/reconnect", "", "worker:connect")
	if capabilities != "" {
		req.Header.Set(worker.CapabilitiesHeader, capabilities)
	}
	w := httptest.NewRecorder()
	srv.workerReconnect(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", w.Code, w.Body)
	}
	var resp worker.BootstrapResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	return resp.Launch
}

// bootstrapChatStartStore serves a bootstrap and records Chat-start
// fallbacks.
type bootstrapChatStartStore struct {
	installedCleanupStore
	moved bool
	calls int
	code  string
}

func (s *bootstrapChatStartStore) FallBackChatStart(_ context.Context, _, _, code, _ string) (bool, error) {
	s.calls++
	s.code = code
	return s.moved, nil
}

func (s *bootstrapChatStartStore) WorkerFallBackChatStart(context.Context, string, string, string, int64, string, string) (bool, error) {
	return false, nil
}

func bootstrapLaunch(t *testing.T, store *bootstrapChatStartStore, capabilities ...string) worker.LaunchContext {
	t.Helper()
	srv := testServer(store)
	srv.workerTokens = worker.NewTokenManager([]byte(strings.Repeat("k", 32)))
	body, err := json.Marshal(worker.BootstrapRequest{BootstrapToken: "ticket", Capabilities: capabilities})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	srv.workerBootstrap(w, httptest.NewRequest(http.MethodPost, "/worker/bootstrap", strings.NewReader(string(body))))
	if w.Code != http.StatusOK {
		t.Fatalf("bootstrap status = %d; body = %s", w.Code, w.Body)
	}
	var resp worker.BootstrapResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	return resp.Launch
}

// A bootstrapping worker that does not report it can start in Chat gets a
// Claude session created in Chat moved to the TUI first: it would start in
// Chat without setting up the Claude ACP adapter. A worker that reports it
// gets Chat, and so does a Codex session, which such a worker runs in Chat.
func TestWorkerBootstrapMovesClaudeChatOffAWorkerThatCannotStartIt(t *testing.T) {
	launch := func(harness string, iface domain.SessionInterface) domain.WorkerLaunch {
		return domain.WorkerLaunch{SessionID: testOrchestratorID, Kind: "worker", Harness: harness, Interface: iface}
	}

	older := &bootstrapChatStartStore{installedCleanupStore: installedCleanupStore{launch: launch("claude-code", domain.SessionInterfaceChat)}, moved: true}
	if got := bootstrapLaunch(t, older, worker.CapabilityMultiRepo); got.Interface != "tui" {
		t.Fatalf("older worker launch interface = %q, want tui", got.Interface)
	}
	if older.calls != 1 || older.code != worker.ChatStartUnsupported {
		t.Fatalf("fallback calls=%d code=%q", older.calls, older.code)
	}

	// A session the person switched to Chat does not move, and keeps Chat.
	switched := &bootstrapChatStartStore{installedCleanupStore: installedCleanupStore{launch: launch("claude-code", domain.SessionInterfaceChat)}}
	if got := bootstrapLaunch(t, switched); got.Interface != "chat" {
		t.Fatalf("switched session launch interface = %q, want chat", got.Interface)
	}

	current := &bootstrapChatStartStore{installedCleanupStore: installedCleanupStore{launch: launch("claude-code", domain.SessionInterfaceChat)}, moved: true}
	if got := bootstrapLaunch(t, current, worker.CapabilityMultiRepo, worker.CapabilityChatStart); got.Interface != "chat" || current.calls != 0 {
		t.Fatalf("current worker launch interface = %q after %d fallbacks, want chat and none", got.Interface, current.calls)
	}

	codex := &bootstrapChatStartStore{installedCleanupStore: installedCleanupStore{launch: launch("codex", domain.SessionInterfaceChat)}, moved: true}
	if got := bootstrapLaunch(t, codex); got.Interface != "chat" || codex.calls != 0 {
		t.Fatalf("codex launch interface = %q after %d fallbacks, want chat and none", got.Interface, codex.calls)
	}

	tui := &bootstrapChatStartStore{installedCleanupStore: installedCleanupStore{launch: launch("claude-code", domain.SessionInterfaceTUI)}}
	if bootstrapLaunch(t, tui); tui.calls != 0 {
		t.Fatal("a TUI session asked to move")
	}
}

// A reconnecting worker may be live (a reopened session refreshing its
// scripts after a Setup retry) and keeps the interface it started in, so the
// session is not moved: moving it would settle a first turn the worker, still
// in Chat, then never runs.
func TestWorkerReconnectLeavesAChatSessionsInterface(t *testing.T) {
	store := &chatStartStore{reconnectStore: reconnectStore{launch: domain.WorkerLaunch{
		SessionID: testOrchestratorID, Kind: "worker", Harness: "claude-code", Interface: domain.SessionInterfaceChat,
	}}, moved: true}
	if launch := reconnectLaunch(t, testServer(store), worker.CapabilityMultiRepo); launch.Interface != "chat" {
		t.Fatalf("reconnect launch interface = %q, want chat", launch.Interface)
	}
	if store.calls != 0 {
		t.Fatalf("reconnect asked to move the session %d times", store.calls)
	}
}

func TestWorkerChatStartFallback(t *testing.T) {
	store := &chatStartStore{moved: true}
	w := httptest.NewRecorder()
	testServer(store).workerChatStartFallback(w, workerRequest(t, http.MethodPost, "/worker/interface/chat-start-fallback",
		`{"code":"`+worker.ChatStartAdapterUnavailable+`"}`, "worker:connect"))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", w.Code, w.Body)
	}
	var resp worker.ChatStartFallbackResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil || !resp.MovedToTUI {
		t.Fatalf("response = %+v, %v; want moved", resp, err)
	}
	if store.workerID != "w1" || store.code != worker.ChatStartAdapterUnavailable ||
		store.detail != chatStartFallbackDetails[worker.ChatStartAdapterUnavailable] {
		t.Fatalf("store got worker=%q code=%q detail=%q", store.workerID, store.code, store.detail)
	}

	for name, req := range map[string]*http.Request{
		"unknown code": workerRequest(t, http.MethodPost, "/worker/interface/chat-start-fallback", `{"code":"WHATEVER"}`, "worker:connect"),
		"no scope":     workerRequest(t, http.MethodPost, "/worker/interface/chat-start-fallback", `{"code":"`+worker.ChatStartAdapterUnavailable+`"}`),
	} {
		store := &chatStartStore{}
		w := httptest.NewRecorder()
		testServer(store).workerChatStartFallback(w, req)
		if w.Code < 400 || store.calls != 0 {
			t.Fatalf("%s: status = %d calls = %d, want refused", name, w.Code, store.calls)
		}
	}
}

// The public contract names every code the endpoint accepts.
func TestChatStartFallbackCodesMatchTheContract(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "contracts", "cloud", "openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	contract := string(raw)
	start := strings.Index(contract, "\n    WorkerChatStartFallbackInput:\n")
	if start < 0 {
		t.Fatal("WorkerChatStartFallbackInput is not in the contract")
	}
	schema := contract[start:]
	enumAt := strings.Index(schema, "enum: [")
	if enumAt < 0 {
		t.Fatal("WorkerChatStartFallbackInput.code has no enum")
	}
	line := schema[enumAt+len("enum: [") : enumAt+strings.Index(schema[enumAt:], "]")]
	var documented []string
	for _, code := range strings.Split(line, ",") {
		documented = append(documented, strings.TrimSpace(code))
	}
	var accepted []string
	for code := range chatStartFallbackDetails {
		accepted = append(accepted, code)
	}
	slices.Sort(documented)
	slices.Sort(accepted)
	if !slices.Equal(documented, accepted) {
		t.Fatalf("contract enum = %v, accepted codes = %v", documented, accepted)
	}
}
