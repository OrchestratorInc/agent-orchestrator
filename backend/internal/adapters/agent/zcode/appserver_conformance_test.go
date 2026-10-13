//go:build !windows

package zcode

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// This is an independent qualification of the stock native protocol. It does
// not claim AO Chat registration, ACP conformance, or authenticated consumption.
// The TUI positive test remains separate so its upstream failure is preserved.
func TestSourceBuiltZCodeAppServerConformance(t *testing.T) {
	entry, node := os.Getenv("AO_ZCODE_ENTRY"), os.Getenv("AO_ZCODE_NODE")
	if entry == "" || node == "" {
		t.Skip("requires CI-built pinned native runtime")
	}
	workspace, fixtureHome := t.TempDir(), t.TempDir()
	requests := make(chan conformanceRequest, 16)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		if r.URL.Path != "/v1/chat/completions" {
			http.Error(w, "unexpected provider route", http.StatusNotFound)
			return
		}
		var request conformanceRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, "invalid synthetic request", http.StatusBadRequest)
			return
		}
		select {
		case requests <- request:
		default:
			http.Error(w, "unexpected duplicate request", http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"id\":\"fixture\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"ao-test\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"Native fixture complete\"},\"finish_reason\":null}]}\n\ndata: {\"id\":\"fixture\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"ao-test\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":3,\"total_tokens\":13}}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)
	builtin := filepath.Join(fixtureHome, "provider", "builtin.json")
	data, err := os.ReadFile(filepath.Join(filepath.Dir(entry), "provider", "zcode-builtin.json"))
	if err != nil {
		t.Fatal(err)
	}
	writeConformanceFile(t, builtin, string(data))
	personal := filepath.Join(fixtureHome, "provider", "personal.json")
	writeConformanceFile(t, personal, fmt.Sprintf(`{"schemaVersion":1,"config":{"providerConfigRules":{"providerRules":[{"providerId":"ao-local","providerName":"AO synthetic provider","enabled":true,"config":{"group":"standard-personal","access":{"type":"api-key","apiKey":"synthetic-only"},"api":{"type":"openai-chat-completions","baseUrl":%q},"personalModelIds":["ao-test"]}}]},"modelConfigRules":{"providerModelRules":[],"manualProviderModelRules":[]},"defaultModelSelection":{"providerId":"ao-local","modelId":"ao-test","options":{"reasoningLevel":"disabled"}}}}`, server.URL+"/v1"))
	// CI fixtures have no real user credentials/rules. No production HOME/config
	// is changed by the adapter or by the VPS provider qualification.
	writeConformanceFile(t, filepath.Join(fixtureHome, ".zcode", "cli", "config.json"), `{"plugins":{"enabled":false},"features":{"mcp":false,"memory":false}}`)
	writeConformanceFile(t, filepath.Join(fixtureHome, ".zcode", "AGENTS.md"), "AO_USER_RULE_PRESERVED")
	project := "AO_PROJECT_RULE_PRESERVED"
	writeConformanceFile(t, filepath.Join(workspace, "AGENTS.md"), project)
	_, recorder := installConformanceHookRecorder(t, fixtureHome)
	hiddenFile := filepath.Join(fixtureHome, "standing.txt")
	writeConformanceFile(t, hiddenFile, "AO_HIDDEN_FIRST")
	envMap := map[string]string{
		"HOME": fixtureHome, "USERPROFILE": fixtureHome, "ZCODE_DATA_BASE_DIR": fixtureHome,
		"ZCODE_SESSION_DB_PATH":              filepath.Join(fixtureHome, "session.sqlite"),
		"ZCODE_BUILTIN_PROVIDER_CONFIG_FILE": builtin, "ZCODE_PERSONAL_PROVIDER_CONFIG_FILE": personal,
		"PATH":          filepath.Dir(recorder) + string(os.PathListSeparator) + os.Getenv("PATH"),
		"AO_SESSION_ID": "ao-protocol-fixture", "AO_TEST_HOOK_LOG": filepath.Join(fixtureHome, "hooks.jsonl"), "AO_TEST_HIDDEN_FILE": hiddenFile,
	}
	var env []string
	for k, v := range envMap {
		env = append(env, k+"="+v)
	}
	binary := filepath.Join(fixtureHome, "bin", "zcode")
	writeConformanceFile(t, binary, fmt.Sprintf("#!/bin/sh\nexec %q %q \"$@\"\n", node, entry))
	if err := os.Chmod(binary, 0700); err != nil {
		t.Fatal(err)
	}
	plugin := &Plugin{resolvedBinary: binary}
	if err := updateHooks(context.Background(), workspace, true); err != nil {
		t.Fatal(err)
	}
	if err := plugin.prepareHookTrust(context.Background(), binary, workspace, envMap); err != nil {
		t.Fatal(err)
	}
	first := startProtocolFixture(t, binary, workspace, env)
	workspaceRef := map[string]string{"workspacePath": workspace, "workspaceKey": workspace}
	var snapshot struct {
		Session struct {
			SessionID string `json:"sessionId"`
		}
		Messages []json.RawMessage
	}
	first.call(t, "session/create", map[string]any{"workspace": workspaceRef, "mode": "build", "titleGenerationEnabled": false}, &snapshot)
	id := snapshot.Session.SessionID
	if !nativeIDPattern.MatchString(id) {
		t.Fatalf("native identity unavailable: %q", id)
	}
	first.call(t, "session/subscribe", map[string]any{"sessionId": id, "deliveryKind": "web-remote-replayable"}, nil)
	task := "--help AO_INITIAL_TASK\nKeep 'quotes' and $(literal) intact."
	first.call(t, "session/send", map[string]any{"sessionId": id, "content": task, "inputId": "first", "queryId": "first"}, nil)
	request := protocolProviderRequest(t, requests)
	assertProtocolPrivateRole(t, request, task, "AO_HIDDEN_FIRST")
	first.waitCompleted(t)
	first.mu.Lock()
	streamed := strings.Join(first.textDeltas, "")
	first.mu.Unlock()
	if streamed != "Native fixture complete" {
		t.Fatalf("native text streaming missing or duplicated: %q", streamed)
	}
	deadline := time.Now().Add(15 * time.Second)
	for validateNativeRestore(context.Background(), workspace, id, envMap) != nil {
		if time.Now().After(deadline) {
			t.Fatal("native history not durable")
		}
		time.Sleep(50 * time.Millisecond)
	}
	first.stop()
	writeConformanceFile(t, hiddenFile, "AO_HIDDEN_RESTORED")
	restored := startProtocolFixture(t, binary, workspace, env)
	restored.call(t, "session/resume", map[string]any{"sessionId": id, "workspace": workspaceRef}, &snapshot)
	if snapshot.Session.SessionID != id || len(snapshot.Messages) == 0 {
		t.Fatal("resume did not load exact history")
	}
	restored.call(t, "session/subscribe", map[string]any{"sessionId": id, "deliveryKind": "web-remote-replayable"}, nil)
	restored.call(t, "session/send", map[string]any{"sessionId": id, "content": "AO_RESTORE_FOLLOWUP", "inputId": "restored", "queryId": "restored"}, nil)
	request = protocolProviderRequest(t, requests)
	assertProtocolPrivateRole(t, request, "AO_RESTORE_FOLLOWUP", "AO_HIDDEN_RESTORED")
	if countRequestText(request, task) != 1 {
		t.Fatal("native restore lost or duplicated original visible task")
	}
	restored.waitCompleted(t)
	if err := restored.request("session/resume", map[string]any{"sessionId": "sess_ao_missing"}, nil); err == nil {
		t.Fatal("unknown history silently accepted")
	}
	data, err = os.ReadFile(filepath.Join(workspace, "AGENTS.md"))
	if err != nil || string(data) != project {
		t.Fatal("project rules changed")
	}
	select {
	case extra := <-requests:
		t.Fatalf("duplicate provider request: %#v", extra)
	default:
	}
}

func protocolProviderRequest(t *testing.T, requests <-chan conformanceRequest) conformanceRequest {
	t.Helper()
	select {
	case r := <-requests:
		return r
	case <-time.After(45 * time.Second):
		t.Fatal("stock protocol did not reach synthetic provider")
	}
	return conformanceRequest{}
}

func assertProtocolPrivateRole(t *testing.T, r conformanceRequest, task, hidden string) {
	t.Helper()
	assertConformancePrompt(t, r, task, hidden)
	if countRequestText(r, "AO_USER_RULE_PRESERVED") == 0 {
		t.Fatal("native user rules missing")
	}
	roleFound := false
	for _, m := range r.Messages {
		if strings.Contains(string(m.Content), hidden) {
			if m.Role != "user" {
				t.Fatalf("fallback hook_context role=%q, expected hidden user system-reminder", m.Role)
			}
			roleFound = true
		}
	}
	if !roleFound {
		t.Fatal("private provider role unavailable")
	}
}

type protocolFixtureFrame struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  json.RawMessage `json:"error,omitempty"`
}
type protocolFixture struct {
	command    *exec.Cmd
	input      io.WriteCloser
	frames     chan protocolFixtureFrame
	completed  chan struct{}
	done       chan struct{}
	mu         sync.Mutex
	stderr     strings.Builder
	next       int
	stopOnce   sync.Once
	textDeltas []string
}

func startProtocolFixture(t *testing.T, binary, workspace string, env []string) *protocolFixture {
	t.Helper()
	p := &protocolFixture{frames: make(chan protocolFixtureFrame, 32), completed: make(chan struct{}, 8), done: make(chan struct{})}
	p.command = exec.Command(binary, "app-server")
	p.command.Dir = workspace
	p.command.Env = env
	p.command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	p.command.Stderr = &p.stderr
	var err error
	p.input, err = p.command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := p.command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = p.command.Start(); err != nil {
		t.Fatal(err)
	}
	go func() {
		scanner := bufio.NewScanner(output)
		scanner.Buffer(make([]byte, 64<<10), 8<<20)
		for scanner.Scan() {
			var frame protocolFixtureFrame
			if json.Unmarshal(scanner.Bytes(), &frame) != nil {
				continue
			}
			if frame.Method != "" && len(frame.ID) > 0 {
				var response map[string]any
				if frame.Method == "session/requestRuntimePreferences" {
					response = map[string]any{"id": frame.ID, "result": map[string]any{"nativeSearchEnhancementsEnabled": true, "memoryEnabled": false, "askUserQuestionAutoResolutionEnabled": false, "modelContextBudgetStrategy": "preflight-v1"}}
				} else {
					response = map[string]any{"id": frame.ID, "error": map[string]any{"code": -32601, "message": "unsupported fixture host method"}}
				}
				_ = p.write(response)
				continue
			}
			if frame.Method == "session/event" {
				var event struct {
					Type    string
					Payload struct {
						Kind  string
						Delta string
					}
				}
				_ = json.Unmarshal(frame.Params, &event)
				if event.Type == "model.streaming" && event.Payload.Kind == "text_delta" {
					p.mu.Lock()
					p.textDeltas = append(p.textDeltas, event.Payload.Delta)
					p.mu.Unlock()
				}
				if event.Type == "turn.completed" || event.Type == "turn.failed" {
					select {
					case p.completed <- struct{}{}:
					default:
					}
				}
			}
			if frame.Method == "" && len(frame.ID) > 0 {
				p.frames <- frame
			}
		}
		_ = p.command.Wait()
		close(p.done)
	}()
	t.Cleanup(p.stop)
	return p
}
func (p *protocolFixture) write(value any) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return json.NewEncoder(p.input).Encode(value)
}
func (p *protocolFixture) request(method string, params, out any) error {
	p.next++
	id := fmt.Sprintf("fixture-%d", p.next)
	if err := p.write(map[string]any{"id": id, "method": method, "params": params}); err != nil {
		return err
	}
	timer := time.NewTimer(45 * time.Second)
	defer timer.Stop()
	for {
		select {
		case f := <-p.frames:
			var got string
			_ = json.Unmarshal(f.ID, &got)
			if got != id {
				continue
			}
			if len(f.Error) > 0 {
				return fmt.Errorf("native protocol %s: %s", method, f.Error)
			}
			if out != nil {
				return json.Unmarshal(f.Result, out)
			}
			return nil
		case <-p.done:
			return fmt.Errorf("native protocol exited")
		case <-timer.C:
			return fmt.Errorf("native protocol %s timeout", method)
		}
	}
}
func (p *protocolFixture) call(t *testing.T, method string, params, out any) {
	t.Helper()
	if err := p.request(method, params, out); err != nil {
		t.Fatal(err)
	}
}
func (p *protocolFixture) waitCompleted(t *testing.T) {
	t.Helper()
	select {
	case <-p.completed:
	case <-p.done:
		t.Fatal("native exited before completion")
	case <-time.After(45 * time.Second):
		t.Fatal("native completion missing")
	}
}
func (p *protocolFixture) stop() {
	p.stopOnce.Do(func() { _ = syscall.Kill(-p.command.Process.Pid, syscall.SIGKILL); <-p.done; _ = p.input.Close() })
}
