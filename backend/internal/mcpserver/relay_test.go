package mcpserver

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
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
