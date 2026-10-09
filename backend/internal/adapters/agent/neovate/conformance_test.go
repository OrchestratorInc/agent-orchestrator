//go:build !windows

package neovate

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
	Messages []struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"messages"`
}

// TestReleasedNeovateTUIConformance runs only in the dedicated remote workflow.
// It drives the released TUI through a PTY against a local, credential-free model.
func TestReleasedNeovateTUIConformance(t *testing.T) {
	binary := os.Getenv("AO_NEOVATE_BINARY")
	if binary == "" {
		t.Skip("set AO_NEOVATE_BINARY to the released Neovate executable")
	}
	workspace, home := t.TempDir(), t.TempDir()
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
		if len(request.Messages) == 0 {
			return
		}
		last := request.Messages[len(request.Messages)-1].Content
		if strings.Contains(string(last), "AO_CANCEL_THIS_TURN") {
			<-r.Context().Done()
			select {
			case canceled <- struct{}{}:
			default:
			}
			return
		}
		_, _ = io.WriteString(w, "data: {\"id\":\"ao-response\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"test\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":100,\"completion_tokens\":3,\"total_tokens\":103}}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)
	config := map[string]any{
		"model": "ao/test", "smallModel": "ao/test", "autoUpdate": false, "checkpoints": false,
		"provider": map[string]any{"ao": map[string]any{"apiFormat": "openai", "options": map[string]string{"apiKey": "local-test-only", "baseURL": server.URL + "/v1"}, "models": map[string]any{"test": map[string]any{"limit": map[string]int{"context": 128000, "output": 1024}}}}},
	}
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(home, ".neovate", "config.json"), string(data))
	writeTestFile(t, filepath.Join(workspace, "AGENTS.md"), "AO_PROJECT_RULE_PRESERVED")
	hookLog, stub := installConformanceHookRecorder(t, home)
	env := []string{"HOME=" + home, "USERPROFILE=" + home, "PATH=" + filepath.Dir(stub) + string(os.PathListSeparator) + os.Getenv("PATH"), "TERM=xterm-256color", "LANG=C.UTF-8", "NEOVATE_SELF_UPDATE=none", "AO_RUNTIME_LAUNCH_ID=launch-neovate-conformance", "AO_TEST_HOOK_LOG=" + hookLog}
	plugin := &Plugin{resolvedBinary: binary}
	install := func(prompt string) {
		t.Helper()
		if err := plugin.GetAgentHooks(context.Background(), ports.WorkspaceHookConfig{WorkspacePath: workspace, SessionID: "ao-session", SystemPrompt: prompt}); err != nil {
			t.Fatal(err)
		}
	}
	install("AO_HIDDEN_FIRST")
	initialTask := "--help AO_INITIAL_TASK\nKeep 'quotes' and $(literal) intact."
	argv, err := plugin.GetLaunchCommand(context.Background(), ports.LaunchConfig{WorkspacePath: workspace, SessionID: "ao-session", Prompt: initialTask, Config: ports.AgentConfig{Model: "ao/test"}})
	if err != nil {
		t.Fatal(err)
	}
	first := startConformanceTUI(t, workspace, env, argv)
	request := receiveConformanceRequest(t, requests, first)
	assertConformancePrompt(t, request, initialTask, "AO_HIDDEN_FIRST")
	waitConformanceEvent(t, hookLog, "stop", 1, first)
	markerData, err := os.ReadFile(pluginPath(workspace, "ao-session") + ".session.json")
	if err != nil {
		t.Fatal(err)
	}
	var marker sessionMarker
	if err := json.Unmarshal(markerData, &marker); err != nil {
		t.Fatal(err)
	}
	if !validNativeID(marker.NativeID) {
		t.Fatalf("native ID = %q", marker.NativeID)
	}
	first.stop()

	install("AO_HIDDEN_RESTORED")
	argv, ok, err := plugin.GetRestoreCommand(context.Background(), ports.RestoreConfig{Session: ports.SessionRef{ID: "ao-session", WorkspacePath: workspace, Metadata: map[string]string{ports.MetadataKeyAgentSessionID: marker.NativeID}}, Prompt: "AO_RESTORE_FOLLOWUP", Config: ports.AgentConfig{Model: "ao/test"}})
	if err != nil || !ok {
		t.Fatalf("restore = %v, %v", ok, err)
	}
	restored := startConformanceTUI(t, workspace, env, argv)
	request = receiveConformanceRequest(t, requests, restored)
	assertConformancePrompt(t, request, "AO_RESTORE_FOLLOWUP", "AO_HIDDEN_RESTORED")
	if countRequestText(request, initialTask) != 1 || countRequestText(request, "AO_HIDDEN_FIRST") != 0 {
		t.Fatal("resume lost or duplicated original task, or retained stale hidden instructions")
	}
	waitConformanceEvent(t, hookLog, "stop", 2, restored)
	if err := validateRestore(workspace, "ao-session", marker.NativeID); err != nil {
		t.Fatal(err)
	}

	// Escape is the native cancellation key. Verify it cancels a request
	// without destroying the TUI by accepting a prompt in the same process.
	sendConformanceInput(t, restored, "AO_CANCEL_THIS_TURN")
	_ = receiveConformanceRequest(t, requests, restored)
	if _, err := restored.terminal.WriteString(plugin.InterruptInput()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-canceled:
	case <-time.After(15 * time.Second):
		t.Fatalf("Escape did not cancel the model request\n%s", restored.output())
	}
	waitConformanceEvent(t, hookLog, "stop", 3, restored)
	sendConformanceInput(t, restored, "AO_AFTER_CANCEL")
	request = receiveConformanceRequest(t, requests, restored)
	if countRequestText(request, "AO_AFTER_CANCEL") != 1 {
		t.Fatal("TUI did not accept a prompt after cancellation")
	}
	waitConformanceEvent(t, hookLog, "stop", 4, restored)
	select {
	case extra := <-requests:
		t.Fatalf("unexpected duplicate model request: %#v", extra)
	default:
	}
	data, err = os.ReadFile(hookLog)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range bytes.Split(bytes.TrimSpace(data), []byte("\n")) {
		var entry struct {
			Event     string `json:"event"`
			Persisted bool   `json:"persisted"`
			Payload   struct {
				SessionID string `json:"session_id"`
				LaunchID  string `json:"launch_id"`
			} `json:"payload"`
		}
		if err := json.Unmarshal(line, &entry); err != nil {
			t.Fatal(err)
		}
		if entry.Event == "user-prompt-submit" && !entry.Persisted {
			t.Fatalf("premature native acceptance: %s", line)
		}
		if entry.Payload.SessionID != marker.NativeID || entry.Payload.LaunchID != "launch-neovate-conformance" {
			t.Fatalf("hook identity changed: %s", line)
		}
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
	if countRequestText(request, task) != 1 || countRequestText(request, hidden) != 1 || countRequestText(request, "AO_PROJECT_RULE_PRESERVED") == 0 {
		t.Fatalf("missing or duplicated task/instruction/project rule: %#v", request)
	}
	found := false
	for _, message := range request.Messages {
		var content string
		_ = json.Unmarshal(message.Content, &content)
		if strings.Contains(content, hidden) {
			if message.Role != "system" || !strings.Contains(content, "You are an interactive CLI tool") {
				t.Fatal("hidden instructions replaced defaults or became a visible user turn")
			}
			found = true
		}
	}
	if !found {
		t.Fatal("hidden instructions absent from the system prompt")
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
func (p *conformanceTUI) output() string { p.mu.Lock(); defer p.mu.Unlock(); return p.buffer.String() }
func (p *conformanceTUI) stop() {
	p.stopOnce.Do(func() {
		_ = p.command.Process.Signal(syscall.SIGTERM)
		select {
		case <-p.done:
		case <-time.After(5 * time.Second):
			_ = syscall.Kill(-p.command.Process.Pid, syscall.SIGKILL)
			<-p.done
		}
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
func waitConformanceEvent(t *testing.T, path, event string, want int, process *conformanceTUI) {
	t.Helper()
	deadline := time.NewTimer(30 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		data, _ := os.ReadFile(path)
		if strings.Count(string(data), fmt.Sprintf(`"event":%q`, event)) >= want {
			return
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

func sendConformanceInput(t *testing.T, process *conformanceTUI, prompt string) {
	t.Helper()
	// Let the native completion event settle and allow Ink's paste buffer to
	// flush before sending Enter as a distinct key event.
	time.Sleep(200 * time.Millisecond)
	if _, err := process.terminal.WriteString(prompt); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if _, err := process.terminal.WriteString("\r"); err != nil {
		t.Fatal(err)
	}
}

func installConformanceHookRecorder(t *testing.T, home string) (string, string) {
	t.Helper()
	hookLog, stub := filepath.Join(home, "hooks.jsonl"), filepath.Join(home, "bin", "ao")
	writeTestFile(t, stub, `#!/usr/bin/env node
const fs = require('fs');
const event = process.argv[4];
const payload = JSON.parse(fs.readFileSync(0, 'utf8'));
let persisted = false;
if (event === 'user-prompt-submit' && payload.transcript_path) {
  const rows = fs.readFileSync(payload.transcript_path, 'utf8').split('\n').filter(Boolean).map(JSON.parse);
  persisted = rows.some(row => row.type === 'message' && row.role === 'user' && row.uuid === payload.native_message_id && row.sessionId === payload.session_id);
}
fs.appendFileSync(process.env.AO_TEST_HOOK_LOG, JSON.stringify({event, payload, persisted}) + '\n');
`)
	if err := os.Chmod(stub, 0o700); err != nil {
		t.Fatal(err)
	}
	return hookLog, stub
}

// Failed model setup and errors before the native JSONL append must not produce
// durable semantic acceptance merely because the process or composer exists.
func TestReleasedNeovateRejectsUnacceptedTasks(t *testing.T) {
	binary := os.Getenv("AO_NEOVATE_BINARY")
	if binary == "" {
		t.Skip("set AO_NEOVATE_BINARY to the released Neovate executable")
	}
	for _, tc := range []struct {
		name, model, errorText string
		failContext            bool
	}{
		{name: "missing-model", errorText: "model configuration is required"},
		{name: "invalid-model", model: "no-such-provider/no-such-model", errorText: "Provider no-such-provider not found"},
		{name: "context-failure", model: "ao/test", failContext: true, errorText: "AO_CONTEXT_ABORT"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			workspace, home := t.TempDir(), t.TempDir()
			hookLog, stub := installConformanceHookRecorder(t, home)
			requests := make(chan struct{}, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				select {
				case requests <- struct{}{}:
				default:
				}
				http.Error(w, "unexpected model request", http.StatusInternalServerError)
			}))
			t.Cleanup(server.Close)
			config := map[string]any{"autoUpdate": false, "checkpoints": false, "provider": map[string]any{"ao": map[string]any{"apiFormat": "openai", "options": map[string]string{"apiKey": "local-test-only", "baseURL": server.URL + "/v1"}, "models": map[string]any{"test": map[string]any{"limit": map[string]int{"context": 128000, "output": 1024}}}}}}
			if tc.model != "" {
				config["model"] = tc.model
			}
			if tc.failContext {
				path := filepath.Join(home, "context-failure.mjs")
				writeTestFile(t, path, `export default {name:'ao-test-context-failure',context(){throw new Error('AO_CONTEXT_ABORT');}};`)
				config["plugins"] = []string{path}
			}
			data, err := json.Marshal(config)
			if err != nil {
				t.Fatal(err)
			}
			writeTestFile(t, filepath.Join(home, ".neovate", "config.json"), string(data))
			plugin := &Plugin{resolvedBinary: binary}
			if err := plugin.GetAgentHooks(context.Background(), ports.WorkspaceHookConfig{WorkspacePath: workspace, SessionID: "ao-session", SystemPrompt: "AO_HIDDEN_NEGATIVE_CASE"}); err != nil {
				t.Fatal(err)
			}
			argv, err := plugin.GetLaunchCommand(context.Background(), ports.LaunchConfig{WorkspacePath: workspace, SessionID: "ao-session", Prompt: "AO_UNACCEPTED_TASK"})
			if err != nil {
				t.Fatal(err)
			}
			env := []string{"HOME=" + home, "USERPROFILE=" + home, "PATH=" + filepath.Dir(stub) + string(os.PathListSeparator) + os.Getenv("PATH"), "TERM=xterm-256color", "LANG=C.UTF-8", "NEOVATE_SELF_UPDATE=none", "AO_TEST_HOOK_LOG=" + hookLog}
			process := startConformanceTUI(t, workspace, env, argv)
			deadline := time.Now().Add(30 * time.Second)
			for !strings.Contains(process.output(), tc.errorText) && time.Now().Before(deadline) {
				time.Sleep(25 * time.Millisecond)
			}
			if !strings.Contains(process.output(), tc.errorText) {
				t.Fatalf("missing native error %q\n%s", tc.errorText, process.output())
			}
			process.stop()
			data, _ = os.ReadFile(hookLog)
			if strings.Contains(string(data), `"event":"user-prompt-submit"`) {
				t.Fatalf("failed task was reported accepted: %s", data)
			}
			if _, err := os.Stat(pluginPath(workspace, "ao-session") + ".session.json"); !os.IsNotExist(err) {
				t.Fatalf("failed task acquired native binding: %v", err)
			}
			select {
			case <-requests:
				t.Fatal("unaccepted task reached model provider")
			default:
			}
		})
	}
}
