package minimaxacp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/minimaxcode"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/persistenthost"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/processalive"
)

func TestMain(m *testing.M) {
	if len(os.Args) >= 8 && os.Args[1] == "chat-host" {
		if os.Args[5] != string(persistenthost.ProtocolACP) || os.Args[7] != "--" {
			os.Exit(2)
		}
		err := persistenthost.Run(context.Background(), persistenthost.Config{
			SessionID: os.Args[2], DataDir: os.Args[3], Workdir: os.Args[4],
			Env: os.Environ(), Argv: os.Args[8:], Protocol: persistenthost.ProtocolACP,
			OwnershipFingerprint: os.Args[6],
		})
		if err != nil {
			_, _ = fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// This opt-in conformance test runs the released native binary, never a mock ACP
// executable. Only its model endpoint is scripted and credential free. It proves
// protocol, native tools/history/cancellation and delivered context, not real
// account authorization, model reasoning, or an actual AO UI journey.
//
// AO_NATIVE_MINIMAX_ACP=1 go test -count=1 -timeout=8m ./internal/adapters/chatdriver/minimaxacp -run '^TestNativeMiniMaxACPConformance$'
// Requires official @minimax-ai/code@0.6.5, Node 24, and Python 3 on Linux.
func TestNativeMiniMaxACPConformance(t *testing.T) {
	if os.Getenv("AO_NATIVE_MINIMAX_ACP") != "1" {
		t.Skip("set AO_NATIVE_MINIMAX_ACP=1 to qualify the released native binary against a controlled loopback endpoint")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 6*time.Minute)
	defer cancel()
	source, data, workspace, home, protected := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
	endpoint := &nativeEndpoint{t: t, protected: protected}
	server := httptest.NewServer(endpoint)
	defer server.Close()
	config := fmt.Sprintf(`defaultModel: custom_provider:ao/native-test
permissionMode: default
custom_provider:
  ao:
    enabled: true
    api: anthropic-messages
    options:
      baseURL: %q
      apiKey: ao-fixture-key
    models:
      native-test:
        name: AO native conformance
        context_window: 65536
        max_output_tokens: 4096
`, server.URL)
	if err := os.WriteFile(filepath.Join(source, "config.yaml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{
		"MINIMAX_DATA_DIR": source, "MAVIS_DATA_DIR": source,
		"HOME": home, "USERPROFILE": home, "XDG_CONFIG_HOME": home,
		"MCODE_LLM_FIRST_EVENT_TIMEOUT_MS": "10000", "MCODE_LLM_STREAM_IDLE_TIMEOUT_MS": "10000",
	}
	plugin := minimaxcode.New()
	binding := New(plugin, nil)
	const id = "native-minimax-acp"
	t.Cleanup(func() { _ = persistenthost.Shutdown(context.Background(), data, id) })
	conv, err := binding.Start(ctx, ports.ChatStartConfig{
		SessionID: id, DataDir: data, WorkspacePath: workspace, Env: env,
		Model: "custom_provider:ao/native-test", Permissions: ports.PermissionModeDefault,
		SystemPrompt: "--AO_FIRST_CONTEXT_7061\nPreserve native defaults and project instructions.",
	})
	if err != nil {
		t.Fatal(err)
	}
	nativeID := conv.ProviderConversationID()
	if !strings.HasPrefix(nativeID, "mvs_") {
		t.Fatalf("missing native identity: %q", nativeID)
	}
	nativeTurn(ctx, t, conv, "ao-seed", nil)
	endpoint.assertRequestContains(t, "ao-seed", "--AO_FIRST_CONTEXT_7061")
	// Native default permits workspace writes. Use an owned scratch target
	// outside the authorized workspace to exercise an actual pending request.
	for _, tc := range []struct{ prompt, file string; decision ports.ChatDecisionKind }{
		{"ao-approve", "approved.txt", ports.ChatDecisionAllowOnce},
		{"ao-reject", "rejected.txt", ports.ChatDecisionRejectOnce},
	} {
		seen := false
		nativeTurn(ctx, t, conv, tc.prompt, func(event ports.ChatEvent) {
			if event.Kind != ports.ChatEventApprovalRequested {
				return
			}
			seen = true
			if _, err := os.Stat(filepath.Join(protected, tc.file)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("protected side effect occurred before approval: %v", err)
			}
			decision := ""
			for _, option := range event.Decisions {
				if option.Kind == tc.decision {
					decision = option.ID
				}
			}
			if decision == "" {
				t.Fatalf("native approval did not offer %s", tc.decision)
			}
			if err := conv.ResolveRequest(ctx, event.RequestID, ports.ChatDecision{ID: decision}); err != nil {
				t.Fatal(err)
			}
		})
		if !seen {
			t.Fatalf("%s never requested native permission", tc.prompt)
		}
		_, statErr := os.Stat(filepath.Join(protected, tc.file))
		if tc.decision == ports.ChatDecisionAllowOnce && statErr != nil {
			t.Fatal("approved operation did not execute")
		}
		if tc.decision == ports.ChatDecisionRejectOnce && !errors.Is(statErr, os.ErrNotExist) {
			t.Fatal("rejected operation executed")
		}
	}

	// The process witness is written by the actual executing tool. It carries
	// Linux's kernel start time as well as PID, so reuse cannot satisfy the gate.
	ref := nativeSend(ctx, t, conv, "ao-cancel")
	var witness struct { PID int `json:"pid"`; Start string `json:"start"` }
	deadline := time.NewTimer(20 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(25 * time.Millisecond)
	defer tick.Stop()
	waiting := true
	for waiting {
		select {
		case event := <-conv.Events():
			if event.Kind == ports.ChatEventApprovalRequested {
				for _, option := range event.Decisions {
					if option.Kind == ports.ChatDecisionAllowOnce {
						if err := conv.ResolveRequest(ctx, event.RequestID, ports.ChatDecision{ID: option.ID}); err != nil { t.Fatal(err) }
						break
					}
				}
			}
			if event.Kind == ports.ChatEventTurnCompleted { t.Fatal("cancel tool settled before the running witness") }
		case <-tick.C:
			b, err := os.ReadFile(filepath.Join(workspace, "started.json"))
			if err == nil && json.Unmarshal(b, &witness) == nil && witness.PID > 0 { waiting = false }
		case <-deadline.C:
			t.Fatal("native active-tool witness was not produced")
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	if !sameLinuxProcess(witness.PID, witness.Start) { t.Fatal("tool witness did not identify a running process") }
	if err := conv.Interrupt(ctx, ref.ProviderTurnID); err != nil { t.Fatal(err) }
	stopDeadline := time.Now().Add(5 * time.Second)
	for sameLinuxProcess(witness.PID, witness.Start) && time.Now().Before(stopDeadline) { time.Sleep(25 * time.Millisecond) }
	if sameLinuxProcess(witness.PID, witness.Start) { t.Fatal("cancel left the exact tool process alive") }
	nativeWait(ctx, t, conv, ref, nil, false)
	nativeTurn(ctx, t, conv, "ao-after-cancel", nil)

	// Replace the provider process, then exact-load the same native history.
	if err := conv.(ports.ChatProviderHibernator).Hibernate(); err != nil { t.Fatal(err) }
	resume := ports.ChatResumeConfig{
		SessionID: id, ProviderConversationID: nativeID, DataDir: data,
		WorkspacePath: workspace, Env: env, Model: "custom_provider:ao/native-test",
		Permissions: ports.PermissionModeDefault,
		SystemPrompt: "--AO_FRESH_RESTORE_CONTEXT_8102\nPreserve native defaults and project instructions.",
	}
	conv, err = binding.Resume(ctx, resume)
	if err != nil { t.Fatal(err) }
	defer conv.(ports.ChatProviderTerminator).Terminate()
	if conv.ProviderConversationID() != nativeID || conv.(ports.ChatLiveReconnector).ReconnectedLive() { t.Fatal("restore did not exact-load in a replacement process") }
	history, err := conv.(ports.ChatHistoryReader).ReadHistory(ctx)
	if err != nil { t.Fatal(err) }
	seed := 0
	for _, event := range history {
		if event.Kind == ports.ChatEventMessageCompleted && strings.Contains(event.Text, "native streamed answer") { seed++ }
		if strings.Contains(event.Text, "AO_FIRST_CONTEXT_7061") || strings.Contains(event.Text, "AO_FRESH_RESTORE_CONTEXT_8102") { t.Fatal("private instructions entered native transcript") }
	}
	if seed == 0 { t.Fatal("native session/load lost earlier assistant history") }
	users := make([]string, 0)
	seenEvents := map[string]bool{}
	for _, event := range history {
		if event.ProviderEventID != "" {
			if seenEvents[event.ProviderEventID] { t.Fatal("native replay duplicated an event identity") }
			seenEvents[event.ProviderEventID] = true
		}
		if event.Kind == ports.ChatEventUserMessageCompleted { users = append(users, strings.TrimSpace(event.Text)) }
	}
	if len(users) < 5 || users[0] != "ao-seed" || users[len(users)-1] != "ao-after-cancel" { t.Fatalf("native replay lost user order: %q", users) }
	again, err := conv.(ports.ChatHistoryReader).ReadHistory(ctx)
	if err != nil || len(again) != len(history) { t.Fatalf("native history read was not repeatable: events=%d/%d, err=%v", len(again), len(history), err) }
	for i := range again {
		if again[i].ProviderEventID != history[i].ProviderEventID || again[i].Kind != history[i].Kind || again[i].Text != history[i].Text { t.Fatalf("native replay changed identity/order/content at event %d", i) }
	}
	nativeTurn(ctx, t, conv, "ao-resume", nil)
	endpoint.assertRequestContains(t, "ao-resume", "--AO_FRESH_RESTORE_CONTEXT_8102")
	endpoint.assertRequestContains(t, "ao-resume", "ao-seed")
	if err := conv.(ports.ChatProviderHibernator).Hibernate(); err != nil { t.Fatal(err) }
	for _, negative := range []ports.ChatResumeConfig{
		func() ports.ChatResumeConfig { c := resume; c.WorkspacePath = t.TempDir(); return c }(),
		func() ports.ChatResumeConfig { c := resume; c.ProviderConversationID = "mvs_00000000000000000000000000000000"; c.FreshIfMissing = true; return c }(),
	} {
		candidate, err := binding.Resume(ctx, negative)
		if candidate != nil { _ = candidate.(ports.ChatProviderTerminator).Terminate() }
		if !errors.Is(err, ports.ErrChatResumeFailed) { t.Fatalf("invalid exact restore was not refused: %v", err) }
		_ = persistenthost.Shutdown(ctx, data, id)
	}
	// The tool would write its late artifact at 12 seconds. Wait beyond that
	// deadline to prove stopping the turn did not merely hide running work.
	time.Sleep(13 * time.Second)
	if _, err := os.Stat(filepath.Join(workspace, "late.txt")); !errors.Is(err, os.ErrNotExist) { t.Fatal("cancelled native tool produced a delayed side effect") }
}

func sameLinuxProcess(pid int, start string) bool {
	if !processalive.Alive(pid) { return false }
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil { return false }
	end := strings.LastIndexByte(string(b), ')')
	if end < 0 { return false }
	fields := strings.Fields(string(b)[end+1:])
	return len(fields) > 19 && fields[0] != "Z" && fields[19] == start
}

func nativeSend(ctx context.Context, t *testing.T, conv ports.ChatConversation, prompt string) ports.ChatTurnRef {
	t.Helper()
	ref, err := conv.SendTurn(ctx, ports.ChatUserMessage{Text: prompt, ClientMessageID: prompt, Origin: domain.MessageOriginHuman})
	if err != nil { t.Fatal(err) }
	if err := conv.(ports.ChatDeferredTurnStarter).StartDeferredTurn(ref.ProviderTurnID); err != nil { t.Fatal(err) }
	return ref
}

func nativeTurn(ctx context.Context, t *testing.T, conv ports.ChatConversation, prompt string, handle func(ports.ChatEvent)) {
	t.Helper()
	nativeWait(ctx, t, conv, nativeSend(ctx, t, conv, prompt), handle, true)
}

func nativeWait(ctx context.Context, t *testing.T, conv ports.ChatConversation, ref ports.ChatTurnRef, handle func(ports.ChatEvent), completed bool) {
	t.Helper()
	deltas := 0
	for {
		select {
		case event, ok := <-conv.Events():
			if !ok { t.Fatal("native controller closed before settlement") }
			if event.ProviderTurnID != "" && event.ProviderTurnID != ref.ProviderTurnID { continue }
			if handle != nil { handle(event) }
			if event.Kind == ports.ChatEventError { t.Fatalf("native turn error: %v", event.Err) }
			if event.Kind == ports.ChatEventMessageDelta { deltas++ }
			if event.Kind == ports.ChatEventTurnCompleted {
				if completed && (event.TurnState != domain.TurnStateCompleted || deltas < 2) { t.Fatalf("native stream/settlement failed: state=%s, partial chunks=%d", event.TurnState, deltas) }
				return
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
}

// nativeEndpoint speaks only the Anthropic stream used by the explicitly
// configured test model. Tool selection comes from the native request catalog.
type nativeEndpoint struct {
	t *testing.T
	protected string
	mu sync.Mutex
	requests map[string][]byte
}

func (e *nativeEndpoint) assertRequestContains(t *testing.T, prompt, text string) {
	t.Helper()
	e.mu.Lock()
	defer e.mu.Unlock()
	if !strings.Contains(string(e.requests[prompt]), text) {
		keys := make([]string, 0, len(e.requests))
		for key := range e.requests {
			if len(key) > 160 { key = key[:160] + "..." }
			keys = append(keys, key)
		}
		sort.Strings(keys)
		t.Fatalf("native request for %q missing synthetic marker %q; captured prompt keys=%q; matching request bytes=%d", prompt, text, keys, len(e.requests[prompt]))
	}
}

func (e *nativeEndpoint) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/messages") { http.Error(w, "unexpected native endpoint", http.StatusNotFound); return }
	body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err != nil { http.Error(w, "read request", http.StatusBadRequest); return }
	var request struct {
		Messages []struct { Role string `json:"role"`; Content json.RawMessage `json:"content"` } `json:"messages"`
		Tools []struct { Name string `json:"name"` } `json:"tools"`
	}
	if json.Unmarshal(body, &request) != nil || len(request.Messages) == 0 { http.Error(w, "invalid request", http.StatusBadRequest); return }
	latest := request.Messages[len(request.Messages)-1].Content
	var blocks []struct { Type string `json:"type"`; Text string `json:"text"` }
	_ = json.Unmarshal(latest, &blocks)
	prompt := ""
	toolResult := false
	for _, block := range blocks {
		if block.Type == "text" { prompt += block.Text }
		if block.Type == "tool_result" { toolResult = true }
	}
	if prompt == "" { _ = json.Unmarshal(latest, &prompt) }
	e.mu.Lock()
	if e.requests == nil { e.requests = map[string][]byte{} }
	e.requests[prompt] = append([]byte(nil), body...)
	e.mu.Unlock()
	w.Header().Set("Content-Type", "text/event-stream")
	flush := w.(http.Flusher)
	emit := func(name string, data any) {
		encoded, _ := json.Marshal(data)
		_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, encoded)
		flush.Flush()
	}
	emit("message_start", map[string]any{"type":"message_start", "message":map[string]any{"id":"native-fixture", "type":"message", "role":"assistant", "model":"native-test", "content":[]any{}, "usage":map[string]int{"input_tokens":10, "output_tokens":0}}})
	command := ""
	if !toolResult {
		switch strings.TrimSpace(prompt) {
		case "ao-approve": command = fmt.Sprintf("printf approved > %q", filepath.Join(e.protected, "approved.txt"))
		case "ao-reject": command = fmt.Sprintf("printf rejected > %q", filepath.Join(e.protected, "rejected.txt"))
		case "ao-cancel": command = `python3 -c 'import os,json,time; s=open("/proc/self/stat").read().rsplit(")",1)[1].split()[19]; open("started.json","w").write(json.dumps({"pid":os.getpid(),"start":s})); time.sleep(12); open("late.txt","w").write("late")'`
		}
	}
	stop := "end_turn"
	if command != "" {
		tool := ""
		for _, candidate := range request.Tools { if strings.EqualFold(candidate.Name, "bash") { tool = candidate.Name } }
		if tool == "" { e.t.Error("released native request did not advertise Bash"); return }
		input, _ := json.Marshal(map[string]any{"command":command, "timeout":60000, "description":"AO controlled native conformance"})
		emit("content_block_start", map[string]any{"type":"content_block_start", "index":0, "content_block":map[string]any{"type":"tool_use", "id":"tool-"+strings.TrimSpace(prompt), "name":tool, "input":map[string]any{}}})
		emit("content_block_delta", map[string]any{"type":"content_block_delta", "index":0, "delta":map[string]any{"type":"input_json_delta", "partial_json":string(input)}})
		stop = "tool_use"
	} else {
		emit("content_block_start", map[string]any{"type":"content_block_start", "index":0, "content_block":map[string]any{"type":"text", "text":""}})
		for _, delta := range []string{"native streamed ", "answer"} {
			emit("content_block_delta", map[string]any{"type":"content_block_delta", "index":0, "delta":map[string]any{"type":"text_delta", "text":delta}})
			time.Sleep(100 * time.Millisecond)
		}
	}
	emit("content_block_stop", map[string]any{"type":"content_block_stop", "index":0})
	emit("message_delta", map[string]any{"type":"message_delta", "delta":map[string]any{"stop_reason":stop, "stop_sequence":nil}, "usage":map[string]int{"output_tokens":10}})
	emit("message_stop", map[string]any{"type":"message_stop"})
}
