package cli

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

type mcpReply struct {
	ID     json.RawMessage `json:"id"`
	Result struct {
		ProtocolVersion string                     `json:"protocolVersion"`
		Capabilities    map[string]json.RawMessage `json:"capabilities"`
		ServerInfo      struct{ Name string }      `json:"serverInfo"`
		Tools           []struct {
			Name        string
			InputSchema struct {
				Properties map[string]map[string]any
				Required   []string
			}
			Annotations map[string]any
		} `json:"tools"`
		Content []struct {
			Type, Text, MimeType, Data string
		} `json:"content"`
		IsError bool `json:"isError"`
	} `json:"result"`
	Error *mcpError `json:"error"`
}

// runMCP feeds lines to `ao mcp` on stdin and returns its replies.
func runMCP(t *testing.T, lines ...string) []mcpReply {
	t.Helper()
	stdout, stderr, err := executeCLI(t, Deps{
		In:           strings.NewReader(strings.Join(lines, "\n") + "\n"),
		ProcessAlive: func(int) bool { return true },
	}, "mcp")
	if err != nil || stderr != "" {
		t.Fatalf("ao mcp: err=%v stderr=%s", err, stderr)
	}
	var replies []mcpReply
	for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
		var reply mcpReply
		if err := json.Unmarshal([]byte(line), &reply); err != nil {
			t.Fatalf("reply %q: %v", line, err)
		}
		replies = append(replies, reply)
	}
	return replies
}

func mcpCall(tool string, args map[string]any) string {
	params, _ := json.Marshal(map[string]any{"name": tool, "arguments": args})
	return `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":` + string(params) + `}`
}

func TestMCPInitializeNegotiatesTheProtocolVersion(t *testing.T) {
	for client, want := range map[string]string{
		"2025-06-18": "2025-06-18",
		"2025-03-26": "2025-03-26",
		"2024-11-05": "2024-11-05",
		"2099-01-01": "2025-06-18",
	} {
		replies := runMCP(t,
			`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"`+client+`","capabilities":{},"clientInfo":{"name":"codex"}}}`,
			`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
			`{"jsonrpc":"2.0","id":2,"method":"ping"}`)
		// The notification gets no reply.
		if len(replies) != 2 || string(replies[0].ID) != "1" || string(replies[1].ID) != "2" || replies[1].Error != nil {
			t.Fatalf("client %s: replies = %+v", client, replies)
		}
		got := replies[0].Result
		if got.ProtocolVersion != want || got.ServerInfo.Name != "ao" || got.Capabilities["tools"] == nil {
			t.Fatalf("client %s: initialize = %+v", client, got)
		}
	}
}

func TestMCPListsBothTools(t *testing.T) {
	tools := runMCP(t, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)[0].Result.Tools
	if len(tools) != 2 || tools[0].Name != "html_preview" || tools[1].Name != "html_render" {
		t.Fatalf("tools = %+v", tools)
	}
	for _, tool := range tools {
		// html_render can write an artifact file, so only html_preview is read-only.
		preview := tool.Name == "html_preview"
		want := map[string]any{"readOnlyHint": preview, "destructiveHint": false, "openWorldHint": true,
			"idempotentHint": preview}
		for key, value := range want {
			if tool.Annotations[key] != value {
				t.Errorf("%s %s = %v, want %v", tool.Name, key, tool.Annotations[key], value)
			}
		}
		if html := tool.InputSchema.Properties["html"]; html["maxLength"] != float64(512_000) || html["minLength"] != float64(1) {
			t.Errorf("%s html schema = %v", tool.Name, html)
		}
	}
	if got := tools[0].InputSchema.Required; !slices.Equal(got, []string{"html"}) || tools[0].InputSchema.Properties["width"] == nil {
		t.Errorf("html_preview schema: required %q, properties %v", got, tools[0].InputSchema.Properties)
	}
	if got := tools[1].InputSchema.Required; !slices.Equal(got, []string{"html", "title"}) || tools[1].InputSchema.Properties["height"] == nil ||
		tools[1].InputSchema.Properties["artifact"]["type"] != "boolean" {
		t.Errorf("html_render schema: required %q, properties %v", got, tools[1].InputSchema.Properties)
	}
}

func TestMCPHTMLRenderPublishesThePageWithItsImages(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "aa-47")
	cfg := setConfigEnv(t)
	srv, capture := renderServer(t, http.StatusCreated,
		`{"renderId":"r1","activityId":"a1","path":"/api/v1/sessions/aa-47/renders/r1"}`)
	writeRunFileFor(t, cfg, srv)
	shot := writeImage(t, t.TempDir(), "shot.png", pngSignature)

	reply := runMCP(t, mcpCall("html_render", map[string]any{"html": `<img src="` + shot + `">`, "title": "Turns"}))[0]
	if capture.method != http.MethodPost || capture.path != "/api/v1/sessions/aa-47/renders" {
		t.Fatalf("hit %s %s", capture.method, capture.path)
	}
	var req renderAPIRequest
	if err := json.Unmarshal([]byte(capture.body), &req); err != nil {
		t.Fatal(err)
	}
	if want := (renderAPIRequest{HTML: `<img src="data:image/png;base64,iVBORw0KGgo=">`, Title: "Turns", Height: 400}); req != want {
		t.Fatalf("request = %+v, want %+v", req, want)
	}
	if got := reply.Result; got.IsError || len(got.Content) != 1 || got.Content[0].Text != renderShownText("r1") {
		t.Fatalf("result = %+v", got)
	}
}

// A harness may start the server with exactly the env AO hands it (session,
// run file, data dir) and no $HOME; a tool call must still reach the daemon.
func TestMCPToolCallNeedsNoHome(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "aa-47")
	cfg := setConfigEnv(t)
	t.Setenv("AO_PORT", "")
	t.Setenv("HOME", "")
	srv, capture := renderServer(t, http.StatusCreated,
		`{"renderId":"r1","activityId":"a1","path":"/api/v1/sessions/aa-47/renders/r1"}`)
	writeRunFileFor(t, cfg, srv)

	reply := runMCP(t, mcpCall("html_render", map[string]any{"html": "<p>hi</p>", "title": "Turns"}))[0]
	if reply.Result.IsError || capture.path != "/api/v1/sessions/aa-47/renders" {
		t.Fatalf("result = %+v, hit %s", reply.Result, capture.path)
	}
}

func TestMCPHTMLRenderWithArtifactNamesTheFileOrTheError(t *testing.T) {
	for _, tc := range []struct{ name, resp, want string }{
		{"kept", `{"renderId":"r1","artifactPath":"/ao/artifacts/aa-47/Turns.html"}`, renderShownText("r1") + "\nsaved as artifact: /ao/artifacts/aa-47/Turns.html"},
		{"not kept", `{"renderId":"r1","artifactError":"save render artifact: disk full"}`,
			renderShownText("r1") + "\nwarning: not saved as artifact: save render artifact: disk full"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("AO_SESSION_ID", "aa-47")
			cfg := setConfigEnv(t)
			srv, capture := renderServer(t, http.StatusCreated, tc.resp)
			writeRunFileFor(t, cfg, srv)

			got := runMCP(t, mcpCall("html_render", map[string]any{"html": "<p>x</p>", "title": "Turns", "artifact": true}))[0].Result
			var req renderAPIRequest
			if err := json.Unmarshal([]byte(capture.body), &req); err != nil || !req.Artifact {
				t.Fatalf("request = %s, want artifact", capture.body)
			}
			if got.IsError || len(got.Content) != 1 || got.Content[0].Text != tc.want {
				t.Fatalf("result = %+v, want %q", got, tc.want)
			}
		})
	}
}

func TestMCPHTMLPreviewReturnsTheScreenshotAndReport(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "aa-47")
	cfg := setConfigEnv(t)
	srv, capture := renderServer(t, http.StatusOK,
		`{"screenshot":{"mimeType":"image/png","data":"iVBORw0KGgo=","width":390,"height":412},"contentHeight":412,`+
			`"consoleMessages":[{"level":"error","text":"d3 is not defined"}]}`)
	writeRunFileFor(t, cfg, srv)
	gone := filepath.Join(t.TempDir(), "gone.png")

	got := runMCP(t, mcpCall("html_preview", map[string]any{"html": `<img src="` + gone + `">`, "width": 390}))[0].Result
	if capture.path != "/api/v1/sessions/aa-47/renders/check" || !strings.Contains(capture.body, `"width":390`) {
		t.Fatalf("hit %s with %s", capture.path, capture.body)
	}
	if got.IsError || len(got.Content) != 2 || got.Content[0].Type != "image" || got.Content[0].MimeType != "image/png" ||
		got.Content[0].Data != "iVBORw0KGgo=" {
		t.Fatalf("result = %+v", got)
	}
	// JSON-encoded, so a Windows path's backslashes are escaped as in the report.
	goneJSON, _ := json.Marshal(gone)
	want := `{"width":390,"contentHeight":412,"consoleMessages":[{"level":"error","text":"d3 is not defined"}],"missingImages":[` + string(goneJSON) + `]}`
	if got.Content[1].Type != "text" || got.Content[1].Text != want {
		t.Fatalf("report = %s, want %s", got.Content[1].Text, want)
	}
}

// A check that ran without network says so, so the agent knows why remote
// resources did not load.
func TestMCPHTMLPreviewSaysWhenTheCheckRanWithoutNetwork(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "aa-47")
	cfg := setConfigEnv(t)
	srv, _ := renderServer(t, http.StatusOK,
		`{"screenshot":{"mimeType":"image/png","data":"iVBORw0KGgo=","width":720,"height":120},"contentHeight":120,"consoleMessages":[],"network":"none"}`)
	writeRunFileFor(t, cfg, srv)

	reply := runMCP(t, mcpCall("html_preview", map[string]any{"html": "<p>x</p>"}))[0]
	if len(reply.Result.Content) != 2 || !strings.Contains(reply.Result.Content[1].Text, renderCheckOfflineNote) {
		t.Fatalf("result = %+v, want the offline note", reply.Result)
	}
	stdout, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }},
		"render", "--check", writePage(t, []byte("<p>x</p>")), "--out", filepath.Join(t.TempDir(), "c.png"))
	if err != nil || !strings.Contains(stdout, renderCheckOfflineNote) {
		t.Fatalf("render --check: err=%v stdout=%s, want the offline note", err, stdout)
	}
}

func TestMCPToolFailuresAreToolResults(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "aa-47")
	cfg := setConfigEnv(t)
	srv, capture := renderServer(t, http.StatusBadRequest,
		`{"message":"invalid render: a title is required","code":"RENDER_INVALID","requestId":"q1"}`)
	writeRunFileFor(t, cfg, srv)
	gone := filepath.Join(t.TempDir(), "gone.png")

	cases := []struct {
		name string
		call string
		want string
	}{
		{"daemon 4xx", mcpCall("html_render", map[string]any{"html": "<p>x</p>"}),
			"invalid render: a title is required (RENDER_INVALID) [request q1]"},
		{"missing image", mcpCall("html_render", map[string]any{"html": `<img src="` + gone + `">`, "title": "x"}),
			"These local images could not be read: " + gone + ". Use absolute paths to existing image files, or remove them."},
		{"html over the cap", mcpCall("html_preview", map[string]any{"html": strings.Repeat("a", maxMCPHTMLChars+1)}),
			"html is too long; the limit is 512000 characters and 1048576 bytes"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			capture.called = false
			reply := runMCP(t, tc.call)[0]
			if reply.Error != nil || !reply.Result.IsError || len(reply.Result.Content) != 1 || reply.Result.Content[0].Text != tc.want {
				t.Fatalf("reply = %+v, want tool error %q", reply, tc.want)
			}
			if tc.name != "daemon 4xx" && capture.called {
				t.Fatal("called the daemon")
			}
		})
	}
}

func TestMCPProtocolErrors(t *testing.T) {
	replies := runMCP(t,
		`{"jsonrpc":"2.0","id":1,"method":"resources/list"}`,
		`{"jsonrpc":"2.0","id":2,"method":`,
		mcpCall("rm_rf", nil),
		`[1]`,
		`{"jsonrpc":"2.0","id":3,"method":5}`)
	if len(replies) != 5 {
		t.Fatalf("replies = %+v", replies)
	}
	for i, want := range []struct {
		id   string
		code int
	}{{"1", -32601}, {"null", -32700}, {"7", -32602}, {"null", -32600}, {"3", -32600}} {
		if got := replies[i]; string(got.ID) != want.id || got.Error == nil || got.Error.Code != want.code {
			t.Errorf("reply %d = id %s error %+v, want id %s code %d", i, got.ID, got.Error, want.id, want.code)
		}
	}
}
