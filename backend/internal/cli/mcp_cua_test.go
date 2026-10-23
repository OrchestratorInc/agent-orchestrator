package cli

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
)

// cuaDaemon serves the computer-use routes `ao mcp` uses. A nil tools body
// answers agent-tools the way a daemon without Cua Driver does.
func cuaDaemon(t *testing.T, tools *string, call func(request cuaCallRequest) string) {
	t.Helper()
	cfg := setConfigEnv(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/computer-use/agent-tools":
			if tools == nil {
				w.WriteHeader(http.StatusConflict)
				_, _ = io.WriteString(w, `{"error":"conflict","code":"COMPUTER_USE_NOT_INSTALLED","message":"Cua Driver is not installed.","requestId":"r"}`)
				return
			}
			_, _ = io.WriteString(w, *tools)
		case "/api/v1/computer-use/call":
			var request cuaCallRequest
			_ = json.NewDecoder(r.Body).Decode(&request)
			_, _ = io.WriteString(w, call(request))
		}
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)
}

const cuaToolsBody = `{"tools":[
	{"name":"get_window_state","description":"Snapshot a window.","inputSchema":{"type":"object","properties":{"pid":{"type":"integer"},"window_id":{"type":"integer"}},"required":["pid","window_id"]}},
	{"name":"click","description":"Click a target pid.","inputSchema":{"type":"object","properties":{"element_token":{"type":"string"}}}}]}`

func TestMCPOffersComputerUseToolsWhenTheSessionHasIt(t *testing.T) {
	t.Setenv(config.EnvComputerUse, "1")
	tools := cuaToolsBody
	cuaDaemon(t, &tools, nil)
	listed := runMCP(t, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)[0].Result.Tools
	names := []string{}
	for _, tool := range listed {
		names = append(names, tool.Name)
	}
	if strings.Join(names, " ") != "html_preview html_render get_window_state click" {
		t.Fatalf("tools = %v", names)
	}
	if listed[2].InputSchema.Required[0] != "pid" || listed[2].Annotations["readOnlyHint"] != true || listed[3].Annotations["destructiveHint"] != true {
		t.Fatalf("get_window_state = %+v, click = %+v", listed[2], listed[3])
	}
}

func TestMCPHasNoComputerUseToolsWithoutIt(t *testing.T) {
	for name, setup := range map[string]func(t *testing.T){
		"setting off": func(t *testing.T) {
			tools := cuaToolsBody
			cuaDaemon(t, &tools, nil)
		},
		"driver missing": func(t *testing.T) {
			t.Setenv(config.EnvComputerUse, "1")
			cuaDaemon(t, nil, nil)
		},
	} {
		t.Run(name, func(t *testing.T) {
			setup(t)
			replies := runMCP(t, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, mcpCall("click", map[string]any{"element_token": "s1:2"}))
			if len(replies[0].Result.Tools) != 2 {
				t.Fatalf("tools = %+v", replies[0].Result.Tools)
			}
			if replies[1].Error == nil || !strings.Contains(replies[1].Error.Message, `unknown tool "click"`) {
				t.Fatalf("click reply = %+v", replies[1])
			}
		})
	}
}

// A call goes to the daemon for this AO session and comes back as text plus
// the screenshot itself.
func TestMCPComputerUseCallReturnsTextAndTheScreenshot(t *testing.T) {
	t.Setenv(config.EnvComputerUse, "1")
	t.Setenv("AO_SESSION_ID", "aa-47")
	shot := filepath.Join(t.TempDir(), "shot.png")
	if err := os.WriteFile(shot, []byte(pngSignature), 0o600); err != nil {
		t.Fatal(err)
	}
	tools := cuaToolsBody
	var sent cuaCallRequest
	cuaDaemon(t, &tools, func(request cuaCallRequest) string {
		sent = request
		encoded, _ := json.Marshal(map[string]any{"tool": "click", "result": map[string]any{
			"summary": "✅ Performed AXPress on [5] AXButton.",
			"observation": map[string]any{"ok": true, "state": map[string]any{
				"window_id": 9, "pid": 7, "snapshot_id": "s2", "screenshot_file_path": shot,
				"tree_diff": "since s1: 0 added, 1 changed, 0 removed.", "invalidated_snapshot_ids": []string{"s1"},
			}},
		}, "images": []map[string]string{{"path": shot, "mimeType": "image/png"}}})
		return string(encoded)
	})
	reply := runMCP(t, mcpCall("click", map[string]any{"element_token": "s1:5"}))[0]
	if sent.Tool != "click" || sent.SessionID != "aa-47" || sent.Arguments["element_token"] != "s1:5" {
		t.Fatalf("daemon request = %+v", sent)
	}
	content := reply.Result.Content
	if reply.Result.IsError || len(content) != 2 || content[0].Type != "text" || content[1].Type != "image" {
		t.Fatalf("result = %+v", reply.Result)
	}
	want := "✅ Performed AXPress on [5] AXButton.\n\nwindow_id=9 pid=7 snapshot_id=s2\n\nsince s1: 0 added, 1 changed, 0 removed.\nInvalidated snapshots s1: their element_tokens are stale."
	if content[0].Text != want {
		t.Fatalf("text =\n%s\nwant\n%s", content[0].Text, want)
	}
	if content[1].MimeType != "image/png" || content[1].Data != base64.StdEncoding.EncodeToString([]byte(pngSignature)) {
		t.Fatalf("image = %+v", content[1])
	}
}

func TestMCPComputerUseFailureIsAToolResult(t *testing.T) {
	t.Setenv(config.EnvComputerUse, "1")
	tools := cuaToolsBody
	cuaDaemon(t, &tools, func(cuaCallRequest) string {
		return `{"tool":"list_windows","result":{"windows":[{"pid":7}]},"images":[]}`
	})
	reply := runMCP(t, mcpCall("get_window_state", map[string]any{"pid": 7, "window_id": 9}))[0]
	// A tool without a text layout comes back as its JSON.
	if reply.Result.IsError || !strings.Contains(reply.Result.Content[0].Text, `"windows"`) {
		t.Fatalf("result = %+v", reply.Result)
	}
	if bad := runMCP(t, `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"click","arguments":[1]}}`)[0]; !bad.Result.IsError {
		t.Fatalf("non-object arguments = %+v", bad.Result)
	}
}
