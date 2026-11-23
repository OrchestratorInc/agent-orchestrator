package mcpserver

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type relayEcho struct {
	Text string `json:"text"`
}

func TestRelayPassesAnAgentsCallsToAnHTTPToolServer(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "upstream", Version: "1"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "echo", Description: "Echo the text"},
		func(_ context.Context, _ *mcp.CallToolRequest, in relayEcho) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "echo: " + in.Text}}}, nil, nil
		})
	streamable := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	var mu sync.Mutex
	var authorizations, versions []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		authorizations = append(authorizations, r.Header.Get("Authorization"))
		versions = append(versions, r.Header.Get("Mcp-Protocol-Version"))
		mu.Unlock()
		streamable.ServeHTTP(w, r)
	}))
	defer upstream.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	relayIn, agentOut := io.Pipe()
	agentIn, relayOut := io.Pipe()
	done := make(chan error, 1)
	go func() { done <- Relay(ctx, relayIn, relayOut, upstream.URL, "Bearer session-token") }()

	client := mcp.NewClient(&mcp.Implementation{Name: "agent", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.IOTransport{Reader: agentIn, Writer: agentOut}, nil)
	if err != nil {
		t.Fatal(err)
	}
	tools, err := session.ListTools(ctx, nil)
	if err != nil || len(tools.Tools) != 1 || tools.Tools[0].Name != "echo" {
		t.Fatalf("tools = %+v, %v", tools, err)
	}
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "echo", Arguments: map[string]any{"text": "hi"}})
	if err != nil || len(result.Content) != 1 {
		t.Fatalf("call = %+v, %v", result, err)
	}
	if text, ok := result.Content[0].(*mcp.TextContent); !ok || text.Text != "echo: hi" {
		t.Fatalf("content = %+v", result.Content[0])
	}
	_ = agentOut.Close()
	if err := <-done; err != nil {
		t.Fatalf("relay ended with %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, authorization := range authorizations {
		if authorization != "Bearer session-token" {
			t.Fatalf("authorizations = %v", authorizations)
		}
	}
	if len(versions) < 3 || versions[len(versions)-1] == "" {
		t.Fatalf("protocol versions sent = %v", versions)
	}
}

func TestRelayAnswersACallWithAnErrorWhenTheServerRefuses(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unknown session", http.StatusUnauthorized)
	}))
	defer upstream.Close()
	relayIn, agentOut := io.Pipe()
	agentIn, relayOut := io.Pipe()
	done := make(chan error, 1)
	go func() { done <- Relay(context.Background(), relayIn, relayOut, upstream.URL, "") }()
	go func() {
		_, _ = io.WriteString(agentOut, `{"jsonrpc":"2.0","method":"notifications/initialized"}`+"\n"+`{"jsonrpc":"2.0","id":7,"method":"tools/list"}`+"\n")
	}()
	line, err := bufio.NewReader(agentIn).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	_ = agentOut.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	got := strings.TrimSpace(line)
	if strings.Count(got, "\n") != 0 || !strings.Contains(got, `"id":7`) || !strings.Contains(got, "401 Unauthorized: unknown session") {
		t.Fatalf("out = %q", got)
	}
}

func TestLaunchRunsTheCommandTheDaemonGivesWithoutTheSessionToken(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX shell")
	}
	t.Setenv(EnvConnectorToken, "session-token")
	t.Setenv(EnvLaunchURL, "http://127.0.0.1:1/launch")
	var authorization string
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get("Authorization")
		_, _ = io.WriteString(w, `{"command":"/bin/sh","args":["-c","cat; echo \"token=[$AO_CONNECTOR_TOKEN] url=[$AO_MCP_LAUNCH_URL] k=[$K]\" >&2"],"env":{"K":"stored-value"}}`)
	}))
	defer daemon.Close()
	var out, errOut strings.Builder
	err := Launch(context.Background(), strings.NewReader("hello from the agent\n"), &out, &errOut, daemon.URL, "Bearer session-token")
	if err != nil {
		t.Fatal(err)
	}
	if out.String() != "hello from the agent\n" || strings.TrimSpace(errOut.String()) != "token=[] url=[] k=[stored-value]" || authorization != "Bearer session-token" {
		t.Fatalf("out=%q err=%q authorization=%q", out.String(), errOut.String(), authorization)
	}
}

func TestLaunchSaysWhyTheDaemonRefused(t *testing.T) {
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "paper is turned off. Ask the user to open Settings, Connectors, and turn it on.", http.StatusForbidden)
	}))
	defer daemon.Close()
	err := Launch(context.Background(), strings.NewReader(""), io.Discard, io.Discard, daemon.URL, "")
	if err == nil || !strings.Contains(err.Error(), "paper is turned off") {
		t.Fatalf("err = %v", err)
	}
}

func TestRelayStartsANewSessionWhenTheServerDropsTheOldOne(t *testing.T) {
	var mu sync.Mutex
	valid, issued, starts := "", 0, 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(string(body), `"method":"initialize"`):
			issued++
			starts++
			valid = "session-" + string(rune('0'+issued))
			w.Header().Set("Mcp-Session-Id", valid)
			_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-06-18","capabilities":{},"serverInfo":{"name":"up","version":"1"}}}`)
		case r.Header.Get("Mcp-Session-Id") != valid:
			http.Error(w, "session not found", http.StatusNotFound)
		case strings.Contains(string(body), `"method":"notifications/initialized"`):
			w.WriteHeader(http.StatusAccepted)
		default:
			_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":2,"result":{"tools":[{"name":"echo","inputSchema":{"type":"object"}}]}}`)
		}
	}))
	defer upstream.Close()
	relayIn, agentOut := io.Pipe()
	agentIn, relayOut := io.Pipe()
	done := make(chan error, 1)
	go func() { done <- Relay(context.Background(), relayIn, relayOut, upstream.URL, "") }()
	replies := bufio.NewReader(agentIn)
	ask := func(line string) string {
		t.Helper()
		go func() { _, _ = io.WriteString(agentOut, line+"\n") }()
		reply, err := replies.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		return reply
	}
	if reply := ask(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"a","version":"1"}}}`); !strings.Contains(reply, `"protocolVersion"`) {
		t.Fatalf("initialize = %s", reply)
	}
	if reply := ask(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`); !strings.Contains(reply, `"echo"`) {
		t.Fatalf("first list = %s", reply)
	}
	mu.Lock()
	valid = "gone"
	mu.Unlock()
	if reply := ask(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`); !strings.Contains(reply, `"echo"`) || strings.Contains(reply, `"error"`) {
		t.Fatalf("list after the server dropped the session = %s", reply)
	}
	_ = agentOut.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if starts != 2 {
		t.Fatalf("the relay started %d sessions", starts)
	}
}

func TestRelaySendsANotificationBeforeTheRequestThatFollowsIt(t *testing.T) {
	var mu sync.Mutex
	var order []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var msg mcpRequest
		_ = json.Unmarshal(body, &msg)
		if msg.Method == "notifications/initialized" {
			time.Sleep(150 * time.Millisecond)
		}
		mu.Lock()
		order = append(order, msg.Method)
		mu.Unlock()
		if len(msg.ID) == 0 {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":2,"result":{"tools":[]}}`)
	}))
	defer upstream.Close()
	var out strings.Builder
	in := strings.NewReader(`{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n" + `{"jsonrpc":"2.0","id":2,"method":"tools/list"}` + "\n")
	relayIn, feed := io.Pipe()
	done := make(chan error, 1)
	go func() { done <- Relay(context.Background(), relayIn, &out, upstream.URL, "") }()
	_, _ = io.Copy(feed, in)
	for range 100 {
		mu.Lock()
		seen := len(order)
		mu.Unlock()
		if seen == 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	_ = feed.Close()
	<-done
	mu.Lock()
	defer mu.Unlock()
	if len(order) != 2 || order[0] != "notifications/initialized" || order[1] != "tools/list" {
		t.Fatalf("the server got %v", order)
	}
}
