package connectors

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func newProxiedService(t *testing.T) (*Service, *httptest.Server) {
	t.Helper()
	var svc *Service
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, proxyPrefix), "/")
		if !strings.HasPrefix(r.URL.Path, proxyPrefix) || len(parts) != 3 || parts[1] != "connectors" {
			http.NotFound(w, r)
			return
		}
		svc.ServeProxy(w, r, parts[0], parts[2])
	}))
	t.Cleanup(daemon.Close)
	svc = New(t.TempDir(), daemon.URL)
	return svc, daemon
}

type bearer struct {
	token string
}

func (b bearer) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set("Authorization", b.token)
	return http.DefaultTransport.RoundTrip(req)
}

func signInFake(t *testing.T, svc *Service, fake *fakeServer, in Input) Connector {
	t.Helper()
	ctx := context.Background()
	in.Transport, in.URL, in.Auth = TransportHTTP, fake.URL+"/mcp", AuthOAuth
	created, err := svc.Create(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	page, err := svc.StartSignIn(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	parsed := mustParse(t, page)
	done, err := svc.CompleteSignIn(ctx, parsed.Query().Get("state"), "code-1", "", "")
	if err != nil {
		t.Fatal(err)
	}
	return done
}

func TestASessionCallsAToolThroughTheProxy(t *testing.T) {
	fake := newFakeServer(t, true)
	svc, _ := newProxiedService(t)
	ctx := context.Background()
	signInFake(t, svc, fake, Input{Name: "fake"})
	if _, err := svc.Create(ctx, Input{Name: "local", Transport: TransportStdio, Command: "npx", Args: []string{"-y", "local-mcp"}, Env: map[string]string{"K": "v"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Create(ctx, Input{Name: "elsewhere", Transport: TransportHTTP, URL: "https://x.example/mcp", ProjectID: "other-project"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Create(ctx, Input{Name: "context7", Transport: TransportHTTP, URL: "https://c7.example/mcp"}); err != nil {
		t.Fatal(err)
	}
	servers := svc.ForSession(ctx, SessionInput{SessionID: "ao-12", ProjectID: "demo", Native: map[string]bool{"context7": true}})
	if len(servers) != 2 || servers[0].Name != "fake" || servers[0].Type != "http" || servers[1].Name != "local" || servers[1].Command != "npx" || servers[1].Env["K"] != "v" {
		t.Fatalf("servers = %+v", servers)
	}
	if strings.Contains(servers[0].Headers["Authorization"], "access-") || strings.Contains(servers[0].URL, fake.URL) {
		t.Fatalf("the session entry carries the connector's own address or token: %+v", servers[0])
	}

	client := mcp.NewClient(&mcp.Implementation{Name: "agent", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint: servers[0].URL, HTTPClient: &http.Client{Transport: bearer{servers[0].Headers["Authorization"]}},
		MaxRetries: -1, DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "echo", Arguments: map[string]any{"text": "hello"}})
	if err != nil || len(result.Content) != 1 {
		t.Fatalf("call = %+v, %v", result, err)
	}
	if text, ok := result.Content[0].(*mcp.TextContent); !ok || text.Text != "hello" {
		t.Fatalf("content = %+v", result.Content[0])
	}
	used, err := svc.SessionConnectors(ctx, "ao-12")
	if err != nil || len(used) != 2 || used[0].Key != "fake" || used[0].Calls != 1 || used[0].LastTool != "echo" || used[0].Status != StatusConnected {
		t.Fatalf("session connectors = %+v, %v", used, err)
	}
}

func proxyPost(t *testing.T, url, token, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if token != "" {
		req.Header.Set("Authorization", token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

func TestTheProxyRefusesWhatTheSessionMayNotReach(t *testing.T) {
	fake := newFakeServer(t, true)
	svc, daemon := newProxiedService(t)
	ctx := context.Background()
	signInFake(t, svc, fake, Input{Name: "fake", ProjectID: "demo"})
	open := map[string]bool{"ao-12": true, "ao-13": true}
	svc.SetSessionLookup(func(_ context.Context, id string) (string, bool) {
		if id == "ao-13" {
			return "other-project", open[id]
		}
		return "demo", open[id]
	})
	mine := svc.ForSession(ctx, SessionInput{SessionID: "ao-12", ProjectID: "demo"})[0]
	other := svc.ForSession(ctx, SessionInput{SessionID: "ao-13", ProjectID: "other-project"})
	if len(other) != 0 {
		t.Fatalf("a session of another project got %+v", other)
	}
	token := mine.Headers["Authorization"]
	call := `{"jsonrpc":"2.0","id":7,"method":"tools/list"}`

	if status, _ := proxyPost(t, mine.URL, "", call); status != http.StatusUnauthorized {
		t.Fatalf("no token = %d", status)
	}
	if status, _ := proxyPost(t, strings.Replace(mine.URL, "ao-12", "ao-13", 1), token, call); status != http.StatusUnauthorized {
		t.Fatalf("a token of another session = %d", status)
	}
	otherToken, _ := svc.sessionToken("ao-13")
	if status, body := proxyPost(t, daemon.URL+proxyPrefix+"ao-13/connectors/fake", "Bearer "+otherToken, call); status != http.StatusOK || !strings.Contains(body, "has no connector named fake") {
		t.Fatalf("a session of another project = %d %s", status, body)
	}
	open["ao-12"] = false
	if status, _ := proxyPost(t, mine.URL, token, call); status != http.StatusUnauthorized {
		t.Fatalf("a closed session = %d", status)
	}
	open["ao-12"] = true

	if _, err := svc.SetEnabled(ctx, "fake", false); err != nil {
		t.Fatal(err)
	}
	if status, body := proxyPost(t, mine.URL, token, call); status != http.StatusOK || !strings.Contains(body, `"id":7`) || !strings.Contains(body, "is turned off") {
		t.Fatalf("turned off = %d %s", status, body)
	}
	if _, err := svc.SetEnabled(ctx, "fake", true); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SignOut(ctx, "fake"); err != nil {
		t.Fatal(err)
	}
	status, body := proxyPost(t, mine.URL, token, call)
	if status != http.StatusOK || !strings.Contains(body, "fake needs sign-in. Ask the user to open Settings, Connectors, and press Sign in.") || strings.Contains(body, "WWW-Authenticate") {
		t.Fatalf("signed out = %d %s", status, body)
	}
	if got, _ := svc.Get(ctx, "fake"); got.Status != StatusNeedsSignIn {
		t.Fatalf("status after a refused call = %s", got.Status)
	}
}
