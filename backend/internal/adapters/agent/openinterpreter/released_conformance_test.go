//go:build openinterpreterconformance && linux

package openinterpreter

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
	"testing"
	"time"

	"github.com/creack/pty"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const privateCanary = "AO_PRIVATE_CONTEXT_8fd23b"

// The released CLI executes the exact production hook command and trust hash.
// Only the hook receiver is substituted; CLI callback handling has its own tests.
func TestMain(m *testing.M) {
	if len(os.Args) == 4 && os.Args[1] == "hooks" && os.Args[2] == "open-interpreter" {
		payload, err := io.ReadAll(io.LimitReader(os.Stdin, 1<<20))
		if err != nil {
			os.Exit(2)
		}
		if os.Args[3] == "session-start" {
			if err := os.WriteFile(os.Getenv("AO_CONFORMANCE_HOOK"), payload, 0o600); err != nil {
				os.Exit(3)
			}
			fmt.Printf(`{"hookSpecificOutput":{"hookEventName":"SessionStart","additionalContext":%q}}`, privateCanary)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestReleasedCLIConformance(t *testing.T) {
	binary := os.Getenv("OPEN_INTERPRETER_CONFORMANCE_BINARY")
	if binary == "" {
		t.Fatal("CI must provide the checksum-pinned released interpreter binary")
	}
	help, err := exec.Command(binary, "--help").CombinedOutput()
	if err != nil || !isRustCLIHelp(string(help)) {
		t.Fatalf("released Rust CLI identity: %s, %v", help, err)
	}
	home, workspace := t.TempDir(), t.TempDir()
	hookPath := filepath.Join(home, "root-hook.json")
	const projectCanary = "PROJECT_AGENTS_34ea51"
	const prompt = "--AO_DASH_TASK_428ea1"
	const reply = "AO_FAKE_REPLY_9c3421"
	requests := make(chan []byte, 8)
	cancelled := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			http.NotFound(w, r)
			return
		}
		body, _ := io.ReadAll(io.LimitReader(r.Body, 4<<20))
		requests <- body
		w.Header().Set("Content-Type", "text/event-stream")
		if bytes.Contains(body, []byte("AO_CANCEL_TASK_67ac12")) && !bytes.Contains(body, []byte("AO_AFTER_CANCEL_02d871")) {
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			cancelled <- struct{}{}
			return
		}
		fmt.Fprintf(w, "data: {\"id\":\"chatcmpl-test\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"gpt-5.1-codex\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":%q},\"finish_reason\":null}]}\n\n", reply)
		fmt.Fprint(w, "data: {\"id\":\"chatcmpl-test\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"gpt-5.1-codex\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	config := fmt.Sprintf("model_provider = \"mock\"\nmodel = \"gpt-5.1-codex\"\n[model_providers.mock]\nname = \"CI local mock\"\nbase_url = %q\nwire_api = \"chat\"\nrequires_openai_auth = false\n[projects.%q]\ntrust_level = \"trusted\"\n", server.URL+"/v1", workspace)
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "AGENTS.md"), []byte(projectCanary), 0o600); err != nil {
		t.Fatal(err)
	}
	// This project hook has no trust state. Trusting AO's three exact definitions
	// must not authorize it, despite the workspace itself being trusted.
	if err := os.Mkdir(filepath.Join(workspace, ".openinterpreter"), 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(home, "untrusted-ran")
	untrusted := fmt.Sprintf("[hooks]\nSessionStart = [{ hooks = [{ type = \"command\", command = %q }] }]\n", "touch "+marker)
	if err := os.WriteFile(filepath.Join(workspace, ".openinterpreter", "config.toml"), []byte(untrusted), 0o600); err != nil {
		t.Fatal(err)
	}
	env := append(os.Environ(), "INTERPRETER_HOME="+home, "AO_CONFORMANCE_HOOK="+hookPath, "TERM=xterm-256color", "NO_COLOR=1")
	p := &Plugin{resolvedBinary: binary}
	cmd, err := p.GetLaunchCommand(context.Background(), ports.LaunchConfig{WorkspacePath: workspace, Prompt: prompt})
	if err != nil {
		t.Fatal(err)
	}
	fresh := startNativeTUI(t, cmd, env)
	// Native setup must retain its own trust choice, without receiving task
	// keystrokes. Decline the one unrelated project hook in the test fixture.
	fresh.waitFor(t, "Hooks need review")
	select {
	case <-requests:
		t.Fatal("initial task ran before native hook review completed")
	default:
	}
	fresh.terminal.Write([]byte("3"))
	first := awaitModelRequest(t, requests, fresh)
	for _, canary := range []string{privateCanary, projectCanary, prompt} {
		if !bytes.Contains(first, []byte(canary)) {
			t.Fatalf("model request missing %s: %s\nterminal: %s", canary, first, fresh.output())
		}
	}
	if bytes.Count(first, []byte(prompt)) != 1 {
		t.Fatalf("initial task delivered more than once: %s", first)
	}
	assertPrivateModelContext(t, first)
	fresh.waitFor(t, reply)
	if strings.Contains(fresh.output(), privateCanary) {
		t.Fatal("private SessionStart context rendered in the TUI")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("AO hook trust authorized an unrelated project hook")
	}
	data, err := os.ReadFile(hookPath)
	if err != nil {
		t.Fatalf("native SessionStart did not run: %v", err)
	}
	var hook struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(data, &hook); err != nil || !IsRootHook(data) {
		t.Fatalf("native root identity: %s, %v", data, err)
	}
	assertNativeHiddenContext(t, p, home, hook.SessionID)
	fresh.stop()
	if err := os.Remove(hookPath); err != nil {
		t.Fatal(err)
	}
	resume, ok, err := p.GetRestoreCommand(context.Background(), ports.RestoreConfig{Env: map[string]string{"INTERPRETER_HOME": home}, Session: ports.SessionRef{WorkspacePath: workspace, Metadata: map[string]string{ports.MetadataKeyAgentSessionID: hook.SessionID}}, Prompt: "AO_RESUME_TASK_325bd1"})
	if err != nil || !ok {
		t.Fatalf("exact native restore: %v, %v", ok, err)
	}
	restored := startNativeTUI(t, resume, env)
	restored.waitFor(t, "Hooks need review")
	restored.terminal.Write([]byte("3"))
	second := awaitModelRequest(t, requests, restored)
	if !bytes.Contains(second, []byte(prompt)) || !bytes.Contains(second, []byte("AO_RESUME_TASK_325bd1")) || !bytes.Contains(second, []byte(privateCanary)) {
		t.Fatalf("resume lost native history or private context: %s", second)
	}
	assertPrivateModelContext(t, second)
	restored.waitFor(t, reply)
	data, err = os.ReadFile(hookPath)
	if err != nil {
		t.Fatal(err)
	}
	var resumed struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(data, &resumed); err != nil || resumed.SessionID != hook.SessionID {
		t.Fatalf("resume changed root identity: %s, %v", data, err)
	}
	if strings.Contains(restored.output(), privateCanary) {
		t.Fatal("private context rendered on restore")
	}
	// Interrupt a native active request, then deliver one follow-up through the
	// same composer. This covers the terminal cancellation path used by AO.
	restored.terminal.Write([]byte("AO_CANCEL_TASK_67ac12\r"))
	active := awaitModelRequest(t, requests, restored)
	if !bytes.Contains(active, []byte("AO_CANCEL_TASK_67ac12")) {
		t.Fatalf("cancel test did not start its intended turn: %s", active)
	}
	restored.terminal.Write([]byte{3})
	select {
	case <-cancelled:
	case <-time.After(15 * time.Second):
		t.Fatalf("Ctrl-C did not cancel native provider request: %s", restored.output())
	}
	restored.terminal.Write([]byte("AO_AFTER_CANCEL_02d871\r"))
	followup := awaitModelRequest(t, requests, restored)
	if bytes.Count(followup, []byte("AO_AFTER_CANCEL_02d871")) != 1 {
		t.Fatalf("follow-up after cancellation was lost or duplicated: %s", followup)
	}
	restored.stop()
}

func assertPrivateModelContext(t *testing.T, body []byte) {
	t.Helper()
	var request struct {
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &request); err != nil {
		t.Fatal(err)
	}
	privateRole, nativeDefault := false, false
	for _, message := range request.Messages {
		if bytes.Contains(message.Content, []byte(privateCanary)) {
			// Native chat-wire-compat::chat_message_role maps every developer
			// item to user for Chat Completions. Assert this wire contract and
			// separately verify the tagged native developer record below.
			if message.Role != "user" {
				t.Fatalf("unexpected native Chat Completions mapping: %s", message.Role)
			}
			privateRole = true
		}
		if (message.Role == "system" || message.Role == "developer") && bytes.Contains(message.Content, []byte("You are")) && len(message.Content) > 1000 {
			nativeDefault = true
		}
	}
	if !privateRole || !nativeDefault {
		t.Fatalf("native default instructions/private context missing: %s", body)
	}
}

func assertNativeHiddenContext(t *testing.T, p *Plugin, home, id string) {
	t.Helper()
	path, ok, err := p.LocateTranscript(context.Background(), ports.NativeSessionRef{ConfigDir: home, NativeSessionID: id})
	if err != nil || !ok {
		t.Fatalf("native context transcript: %v, %v", ok, err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, line := range bytes.Split(data, []byte{'\n'}) {
		if !bytes.Contains(line, []byte(privateCanary)) {
			continue
		}
		var record struct {
			Type    string `json:"type"`
			Payload struct {
				Type     string `json:"type"`
				Role     string `json:"role"`
				Metadata struct {
					Kinds []string `json:"content_item_kinds"`
				} `json:"internal_chat_message_metadata_passthrough"`
			} `json:"payload"`
		}
		if err := json.Unmarshal(line, &record); err != nil {
			t.Fatal(err)
		}
		if record.Type != "response_item" || record.Payload.Type != "message" {
			continue
		}
		if record.Payload.Role != "developer" {
			t.Fatalf("private native context became %s message", record.Payload.Role)
		}
		for _, kind := range record.Payload.Metadata.Kinds {
			if kind == "hooks.additional_context" {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("missing tagged native developer hook context")
	}
}

type nativeTUI struct {
	cmd      *exec.Cmd
	terminal *os.File
	mu       sync.Mutex
	buf      bytes.Buffer
	once     sync.Once
}

func startNativeTUI(t *testing.T, argv, env []string) *nativeTUI {
	t.Helper()
	// Inline rendering makes failures readable; all behavioral adapter flags
	// and its exact hook executable/hash remain unchanged.
	args := append([]string{"--no-alt-screen"}, argv[1:]...)
	cmd := exec.Command(argv[0], args...)
	cmd.Env = env
	terminal, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 40, Cols: 160})
	if err != nil {
		t.Fatal(err)
	}
	run := &nativeTUI{cmd: cmd, terminal: terminal}
	t.Cleanup(run.stop)
	go func() {
		buffer := make([]byte, 8192)
		for {
			n, err := terminal.Read(buffer)
			if n > 0 {
				run.mu.Lock()
				run.buf.Write(buffer[:n])
				run.mu.Unlock()
				if bytes.Contains(buffer[:n], []byte("\x1b[6n")) {
					terminal.Write([]byte("\x1b[1;1R"))
				}
			}
			if err != nil {
				return
			}
		}
	}()
	return run
}

func (run *nativeTUI) output() string {
	run.mu.Lock()
	defer run.mu.Unlock()
	return run.buf.String()
}

func (run *nativeTUI) stop() {
	run.once.Do(func() {
		run.cmd.Process.Kill()
		run.cmd.Wait()
		run.terminal.Close()
	})
}

func (run *nativeTUI) waitFor(t *testing.T, value string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(run.output(), value) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("native TUI did not render %q: %s", value, run.output())
}

func awaitModelRequest(t *testing.T, requests <-chan []byte, run *nativeTUI) []byte {
	t.Helper()
	select {
	case request := <-requests:
		return request
	case <-time.After(45 * time.Second):
		t.Fatalf("native CLI did not reach local provider: %s", run.output())
		return nil
	}
}
