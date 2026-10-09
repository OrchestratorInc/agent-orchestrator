//go:build !windows

package tau

import (
	"bytes"
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

	"github.com/creack/pty"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type conformanceRequest struct {
	Model    string `json:"model"`
	Messages []struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"messages"`
}

type conformanceEvent struct {
	Event   string `json:"event"`
	Payload struct {
		SessionID string `json:"session_id"`
		Prompt    string `json:"prompt"`
	} `json:"payload"`
}

// TestReleasedTauTUIConformance catches incompatible released CLI arguments,
// private-context leaks, false native identity, and cancellation that kills the
// composer. Only the model HTTP boundary and AO hook receiver are substitutes.
func TestReleasedTauTUIConformance(t *testing.T) {
	binary := os.Getenv("AO_TAU_BINARY")
	if binary == "" {
		t.Skip("set AO_TAU_BINARY to the pinned released Tau executable")
	}
	workspace, home := t.TempDir(), t.TempDir()
	tauHome := filepath.Join(home, ".tau")
	requests := make(chan conformanceRequest, 8)
	canceled := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.Error(w, "unexpected model route", http.StatusNotFound)
			return
		}
		defer r.Body.Close()
		var request conformanceRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		select {
		case requests <- request:
		default:
			http.Error(w, "too many model requests", http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"id\":\"ao-response\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"test\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"AO fake response\"},\"finish_reason\":null}]}\n\n")
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		if len(request.Messages) > 0 && strings.Contains(string(request.Messages[len(request.Messages)-1].Content), "AO_CANCEL_THIS_TURN") {
			select {
			case <-r.Context().Done():
				select {
				case canceled <- struct{}{}:
				default:
				}
			case <-time.After(45 * time.Second):
			}
			return
		}
		_, _ = io.WriteString(w, "data: {\"id\":\"ao-response\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"test\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":100,\"completion_tokens\":3,\"total_tokens\":103}}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)
	// This is Tau's documented user catalog schema, preserving native defaults.
	writeConformanceFile(t, filepath.Join(tauHome, "catalog.toml"), fmt.Sprintf(`schema_version = 1
[[providers]]
name = "ao"
display_name = "AO local fixture"
kind = "openai-compatible"
base_url = %q
docs_url = "https://example.invalid/ao-conformance"
api_key_env = "AO_TEST_API_KEY"
models = ["test"]
default_model = "test"
api = "openai-completions"
[providers.context_windows]
test = 128000
[providers.model_metadata.test]
name = "test"
reasoning = false
input = ["text"]
context_window = 128000
max_tokens = 1024
`, server.URL+"/v1"))
	writeConformanceFile(t, filepath.Join(workspace, "AGENTS.md"), "AO_PROJECT_RULE_PRESERVED")
	// These discovered extensions must stay disabled while the explicit AO
	// observer loads. Executing either would leave evidence in the hook log.
	discoveryTripwire := "import os\nfrom pathlib import Path\nPath(os.environ['AO_TEST_DISCOVERY_MARKER']).write_text('unrelated extension executed')\n"
	writeConformanceFile(t, filepath.Join(tauHome, "extensions", "unrelated.py"), discoveryTripwire)
	writeConformanceFile(t, filepath.Join(workspace, ".tau", "extensions", "unrelated.py"), discoveryTripwire)
	hookLog, recorder := installConformanceHookRecorder(t, home)
	envMap := map[string]string{
		"HOME": home, "USERPROFILE": home, "TAU_HOME": tauHome,
		"PATH": filepath.Dir(recorder) + string(os.PathListSeparator) + os.Getenv("PATH"),
		"TERM": "xterm-256color", "LANG": "C.UTF-8", "AO_TEST_API_KEY": "local-test-only",
		"AO_SESSION_ID": "ao-tau-conformance", "AO_TEST_HOOK_LOG": hookLog,
		"AO_TEST_DISCOVERY_MARKER": filepath.Join(home, "unexpected-extension"),
	}
	var env []string
	for key, value := range envMap {
		env = append(env, key+"="+value)
	}
	plugin := &Plugin{resolvedBinary: binary}
	if err := plugin.GetAgentHooks(context.Background(), ports.WorkspaceHookConfig{WorkspacePath: workspace, SessionID: "ao-tau-conformance"}); err != nil {
		t.Fatal(err)
	}
	initialTask := "--help AO_INITIAL_TASK\nKeep 'quotes' and $(literal) intact."
	launch := ports.LaunchConfig{WorkspacePath: workspace, SessionID: "ao-tau-conformance", Prompt: initialTask, SystemPrompt: "AO_HIDDEN_FIRST", Permissions: ports.PermissionModeBypassPermissions, Config: ports.AgentConfig{Model: "ao/test"}, Env: envMap}
	argv, err := plugin.GetLaunchCommand(context.Background(), launch)
	if err != nil {
		t.Fatal(err)
	}
	first := startConformanceTUI(t, workspace, env, argv)
	started := waitConformanceEvent(t, hookLog, "session-start", 1, first)
	if !nativeIDPattern.MatchString(started.Payload.SessionID) {
		t.Fatalf("invalid native identity: %#v", started)
	}
	waitConformanceComposer(t, first)
	sendConformanceInput(t, first, initialTask)
	request := receiveConformanceRequest(t, requests, first)
	assertConformancePrompt(t, request, initialTask, "AO_HIDDEN_FIRST")
	waitConformanceEvent(t, hookLog, "agent-start", 1, first)
	accepted := waitConformanceEvent(t, hookLog, "user-prompt-submit", 1, first)
	if accepted.Payload.Prompt != initialTask {
		t.Fatalf("native accepted task changed: %q", accepted.Payload.Prompt)
	}
	waitConformanceEvent(t, hookLog, "stop", 1, first)
	first.stop() // SIGKILL: prove restore does not rely on a graceful quit hook.

	restore := ports.RestoreConfig{Session: ports.SessionRef{ID: "ao-tau-conformance", WorkspacePath: workspace, Metadata: map[string]string{ports.MetadataKeyAgentSessionID: started.Payload.SessionID}}, SystemPrompt: "AO_HIDDEN_RESTORED", Permissions: ports.PermissionModeBypassPermissions, Config: ports.AgentConfig{Model: "ao/test"}, Env: envMap}
	argv, ok, err := plugin.GetRestoreCommand(context.Background(), restore)
	if err != nil || !ok {
		t.Fatalf("exact persisted restore = %v, %v", ok, err)
	}
	restored := startConformanceTUI(t, workspace, env, argv)
	waitConformanceEvent(t, hookLog, "session-start", 2, restored)
	waitConformanceComposer(t, restored)
	sendConformanceInput(t, restored, "AO_RESTORE_FOLLOWUP")
	request = receiveConformanceRequest(t, requests, restored)
	assertConformancePrompt(t, request, "AO_RESTORE_FOLLOWUP", "AO_HIDDEN_RESTORED")
	if countRequestText(request, initialTask) != 1 || countRequestText(request, "AO_HIDDEN_FIRST") != 0 {
		t.Fatal("restore lost/duplicated history or retained stale hidden instructions")
	}
	waitConformanceEvent(t, hookLog, "stop", 2, restored)
	sendConformanceInput(t, restored, "AO_CANCEL_THIS_TURN")
	_ = receiveConformanceRequest(t, requests, restored)
	if _, err := restored.terminal.WriteString(plugin.InterruptInput()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-canceled:
	case <-time.After(15 * time.Second):
		t.Fatalf("native Escape did not cancel HTTP stream\n%s", restored.output())
	}
	waitConformanceEvent(t, hookLog, "stop", 3, restored)
	sendConformanceInput(t, restored, "AO_AFTER_CANCEL")
	request = receiveConformanceRequest(t, requests, restored)
	assertConformancePrompt(t, request, "AO_AFTER_CANCEL", "AO_HIDDEN_RESTORED")
	waitConformanceEvent(t, hookLog, "agent-start", 4, restored)
	waitConformanceEvent(t, hookLog, "stop", 4, restored)
	restored.stop()
	if _, err := os.Stat(envMap["AO_TEST_DISCOVERY_MARKER"]); !os.IsNotExist(err) {
		t.Fatalf("unrelated discovered extension executed: %v", err)
	}
	for _, event := range readConformanceEvents(t, hookLog) {
		if event.Payload.SessionID != started.Payload.SessionID {
			t.Fatalf("native identity changed across restore: %#v", event)
		}
	}
	for _, process := range []*conformanceTUI{first, restored} {
		for _, private := range []string{"AO_HIDDEN_FIRST", "AO_HIDDEN_RESTORED", "AO_UNEXPECTED_EXTENSION_DISCOVERY"} {
			if strings.Contains(process.output(), private) {
				t.Fatalf("private context leaked or unrelated extension loaded: %s", private)
			}
		}
	}
	select {
	case extra := <-requests:
		t.Fatalf("unexpected duplicate request: %#v", extra)
	default:
	}
	// Run the real native metadata probe for absent identity and foreign cwd.
	restore.Session.Metadata[ports.MetadataKeyAgentSessionID] = strings.Repeat("0", 32)
	if _, ok, err := plugin.GetRestoreCommand(context.Background(), restore); err == nil || ok {
		t.Fatalf("missing native session accepted: %v, %v", ok, err)
	}
	restore.Session.Metadata[ports.MetadataKeyAgentSessionID] = started.Payload.SessionID
	restore.Session.WorkspacePath = t.TempDir()
	if _, ok, err := plugin.GetRestoreCommand(context.Background(), restore); err == nil || ok {
		t.Fatalf("foreign native workspace accepted: %v, %v", ok, err)
	}
}

func countRequestText(request conformanceRequest, needle string) int {
	count := 0
	for _, message := range request.Messages {
		var text string
		if json.Unmarshal(message.Content, &text) == nil {
			count += strings.Count(text, needle)
			continue
		}
		var parts []struct {
			Text string `json:"text"`
		}
		if json.Unmarshal(message.Content, &parts) == nil {
			for _, part := range parts {
				count += strings.Count(part.Text, needle)
			}
		}
	}
	return count
}

func assertConformancePrompt(t *testing.T, request conformanceRequest, task, hidden string) {
	t.Helper()
	if request.Model != "test" || countRequestText(request, task) != 1 || countRequestText(request, hidden) != 1 || countRequestText(request, "AO_PROJECT_RULE_PRESERVED") == 0 {
		t.Fatalf("model, task, private instructions or project rules changed: %#v", request)
	}
	found := false
	for _, message := range request.Messages {
		var content string
		_ = json.Unmarshal(message.Content, &content)
		if strings.Contains(content, hidden) {
			if message.Role != "system" || !strings.Contains(content, "You are an expert coding assistant operating inside Tau") {
				t.Fatal("private context replaced native defaults or became a visible user turn")
			}
			found = true
		}
	}
	if !found {
		t.Fatal("private context missing from native system prompt")
	}
}

type conformanceTUI struct {
	terminal *os.File
	command  *exec.Cmd
	done     chan struct{}
	mu       sync.Mutex
	buffer   bytes.Buffer
	stopOnce sync.Once
}

func startConformanceTUI(t *testing.T, workspace string, env, argv []string) *conformanceTUI {
	t.Helper()
	command := exec.Command(argv[0], argv[1:]...)
	command.Dir, command.Env = workspace, env
	terminal, err := pty.StartWithSize(command, &pty.Winsize{Rows: 40, Cols: 140})
	if err != nil {
		t.Fatal(err)
	}
	process := &conformanceTUI{terminal: terminal, command: command, done: make(chan struct{})}
	go func() { _, _ = io.Copy(process, terminal) }()
	go func() { _ = command.Wait(); close(process.done) }()
	t.Cleanup(process.stop)
	return process
}

func (p *conformanceTUI) Write(data []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.buffer.Len() < 1024*1024 {
		_, _ = p.buffer.Write(data)
	}
	return len(data), nil
}

func (p *conformanceTUI) output() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.buffer.String()
}

func (p *conformanceTUI) stop() {
	p.stopOnce.Do(func() {
		_ = syscall.Kill(-p.command.Process.Pid, syscall.SIGKILL)
		<-p.done
		_ = p.terminal.Close()
	})
}

func receiveConformanceRequest(t *testing.T, requests <-chan conformanceRequest, process *conformanceTUI) conformanceRequest {
	t.Helper()
	select {
	case request := <-requests:
		return request
	case <-process.done:
		t.Fatalf("TUI exited before model request\n%s", process.output())
	case <-time.After(45 * time.Second):
		t.Fatalf("TUI did not send a model request\n%s", process.output())
	}
	return conformanceRequest{}
}

func readConformanceEvents(t *testing.T, path string) []conformanceEvent {
	t.Helper()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var events []conformanceEvent
	for _, line := range bytes.Split(bytes.TrimSpace(data), []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		var event conformanceEvent
		if json.Unmarshal(line, &event) == nil {
			events = append(events, event)
		}
	}
	return events
}

func waitConformanceEvent(t *testing.T, path, event string, want int, process *conformanceTUI) conformanceEvent {
	t.Helper()
	deadline := time.NewTimer(30 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		count := 0
		for _, entry := range readConformanceEvents(t, path) {
			if entry.Event == event {
				count++
				if count == want {
					return entry
				}
			}
		}
		select {
		case <-ticker.C:
		case <-process.done:
			t.Fatalf("TUI exited before %s\n%s", event, process.output())
		case <-deadline.C:
			t.Fatalf("missing native %s event %d\n%s", event, want, process.output())
		}
	}
}

func waitConformanceComposer(t *testing.T, process *conformanceTUI) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(process.output(), "Ask Tau…") {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("native composer readiness marker missing\n%s", process.output())
}

func sendConformanceInput(t *testing.T, process *conformanceTUI, prompt string) {
	t.Helper()
	time.Sleep(200 * time.Millisecond)
	if _, err := process.terminal.WriteString("\x1b[200~" + prompt + "\x1b[201~"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if _, err := process.terminal.WriteString("\r"); err != nil {
		t.Fatal(err)
	}
}

func writeConformanceFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
}

func installConformanceHookRecorder(t *testing.T, home string) (string, string) {
	t.Helper()
	log, path := filepath.Join(home, "hooks.jsonl"), filepath.Join(home, "bin", "ao")
	writeConformanceFile(t, path, `#!/usr/bin/env python3
import json, os, sys
payload = json.load(sys.stdin)
with open(os.environ["AO_TEST_HOOK_LOG"], "a", encoding="utf-8") as output:
    output.write(json.dumps({"event": sys.argv[3], "payload": payload}) + "\n")
`)
	if err := os.Chmod(path, 0700); err != nil {
		t.Fatal(err)
	}
	return log, path
}
