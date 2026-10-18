//go:build !windows

package neovate

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
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// TestReleasedNeovateACPAdditiveInstructions qualifies only the AO plugin's
// fresh ACP configuration boundary against the pinned official executable and a
// local model fixture. It does not qualify real-provider consumption, restore,
// permissions, executing-child cancellation, or production Chat registration.
func TestReleasedNeovateACPAdditiveInstructions(t *testing.T) {
	binary := os.Getenv("AO_NEOVATE_BINARY")
	if binary == "" {
		t.Skip("set AO_NEOVATE_BINARY to the released Neovate executable")
	}
	workspace, home := t.TempDir(), t.TempDir()
	requests := make(chan conformanceRequest, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		if r.URL.Path != "/v1/chat/completions" {
			http.Error(w, "unexpected model route", http.StatusNotFound)
			return
		}
		var request conformanceRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		select {
		case requests <- request:
		default:
			http.Error(w, "unexpected extra model request", http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"id\":\"acp-fixture\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"fixture response\"},\"finish_reason\":null}]}\n\ndata: {\"id\":\"acp-fixture\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	config, err := json.Marshal(map[string]any{
		"model": "ao/test", "smallModel": "ao/test", "autoUpdate": false, "checkpoints": false,
		"provider": map[string]any{"ao": map[string]any{"apiFormat": "openai", "options": map[string]string{"apiKey": "local-test-only", "baseURL": server.URL + "/v1"}, "models": map[string]any{"test": map[string]any{"limit": map[string]int{"context": 128000, "output": 1024}}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(home, ".neovate", "config.json"), string(config))
	writeTestFile(t, filepath.Join(workspace, "AGENTS.md"), "AO_PROJECT_RULE_PRESERVED")
	plugin := &Plugin{resolvedBinary: binary}
	if err := plugin.GetAgentHooks(context.Background(), ports.WorkspaceHookConfig{WorkspacePath: workspace, SessionID: "ao-acp-session", SystemPrompt: "AO_ACP_PRIVATE_ADDITION"}); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, binary, "acp", "--cwd", workspace, "--model", "ao/test", "--plugin", pluginPath(workspace, "ao-acp-session"))
	command.Dir = workspace
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "PATH=" + os.Getenv("PATH"), "NEOVATE_SELF_UPDATE=none"}
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	// Keep stderr out of the protocol stream; CommandContext bounds failures.
	command.Stderr = io.Discard
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = stdin.Close()
		_ = command.Process.Kill()
		_ = command.Wait()
	}()
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	call := func(id int, method string, params any, target any) {
		t.Helper()
		if err := json.NewEncoder(stdin).Encode(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
			t.Fatal(err)
		}
		for scanner.Scan() {
			var response struct {
				ID     int             `json:"id"`
				Method string          `json:"method"`
				Result json.RawMessage `json:"result"`
				Error  json.RawMessage `json:"error"`
			}
			if err := json.Unmarshal(scanner.Bytes(), &response); err != nil {
				t.Fatalf("invalid ACP response: %v", err)
			}
			if response.Method != "" {
				// No tools are requested by the fixture. Unexpected native
				// requests must fail, rather than silently approving them.
				if response.ID != 0 {
					t.Fatalf("unexpected native request %q", response.Method)
				}
				continue
			}
			if response.ID != id || len(response.Error) != 0 {
				t.Fatalf("%s failed: %s", method, scanner.Text())
			}
			if err := json.Unmarshal(response.Result, target); err != nil {
				t.Fatal(err)
			}
			return
		}
		t.Fatal(fmt.Errorf("%s: ACP ended before response: %v (context: %v)", method, scanner.Err(), ctx.Err()))
	}
	var initialized map[string]any
	call(1, "initialize", map[string]any{"protocolVersion": 1, "clientCapabilities": map[string]any{}}, &initialized)
	var session struct {
		ID string `json:"sessionId"`
	}
	call(2, "session/new", map[string]any{"cwd": workspace, "mcpServers": []any{}}, &session)
	if session.ID == "" {
		t.Fatal("native ACP returned no session identity")
	}
	var turn struct {
		StopReason string `json:"stopReason"`
	}
	call(3, "session/prompt", map[string]any{"sessionId": session.ID, "prompt": []any{map[string]string{"type": "text", "text": "Summarize this project."}}}, &turn)
	if turn.StopReason != "end_turn" {
		t.Fatalf("stop reason = %q", turn.StopReason)
	}
	select {
	case request := <-requests:
		assertConformancePrompt(t, request, "Summarize this project.", "AO_ACP_PRIVATE_ADDITION")
	case <-ctx.Done():
		t.Fatal("ACP did not reach the model fixture")
	}
}
