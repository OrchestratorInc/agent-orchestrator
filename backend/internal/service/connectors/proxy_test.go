package connectors

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func newProxiedService(t *testing.T) (*Service, *httptest.Server) {
	t.Helper()
	var svc *Service
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, proxyPrefix), "/")
		switch {
		case !strings.HasPrefix(r.URL.Path, proxyPrefix) || len(parts) < 3 || parts[1] != "connectors":
			http.NotFound(w, r)
		case len(parts) == 4 && parts[3] == "launch" && r.Method == http.MethodGet:
			svc.ServeLaunch(w, r, parts[0], parts[2])
		case len(parts) == 3:
			svc.ServeProxy(w, r, parts[0], parts[2])
		default:
			http.NotFound(w, r)
		}
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
	if len(servers) != 2 || servers[0].Name != "fake" || servers[0].Type != "http" || servers[1].Name != "local" || servers[1].Type != "stdio" ||
		servers[1].Command != "" || len(servers[1].Env) != 0 || !strings.HasSuffix(servers[1].URL, "/mcp/s/ao-12/connectors/local/launch") {
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
	if _, err := svc.setStatus("fake", StatusUnavailable, "reach", "502 Bad Gateway"); err != nil {
		t.Fatal(err)
	}
	if _, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "echo", Arguments: map[string]any{"text": "again"}}); err != nil {
		t.Fatal(err)
	}
	if got, _ := svc.Get(ctx, "fake"); got.Status != StatusConnected || len(got.Tools) == 0 {
		t.Fatalf("after an answered call = %+v", got)
	}
	used, err := svc.SessionConnectors(ctx, "ao-12")
	if err != nil || len(used) != 2 || used[0].Key != "fake" || used[0].Calls != 2 || used[0].LastTool != "echo" || used[0].Status != StatusConnected ||
		used[0].Transport != TransportHTTP || used[1].Transport != TransportStdio {
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
	if status, _ := proxyPost(t, mine.URL, token, call); status != http.StatusOK {
		t.Fatalf("a session the daemon starts now = %d", status)
	}
	clock := svc.now
	svc.now = func() time.Time { return clock().Add(startGrace + time.Second) }
	if status, _ := proxyPost(t, mine.URL, token, call); status != http.StatusUnauthorized {
		t.Fatalf("a closed session = %d", status)
	}
	svc.now = clock
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

func launchGet(t *testing.T, url, token string) (int, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
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

func TestOnlyTheSessionGetsTheCommandAndValuesOfACommandConnector(t *testing.T) {
	svc, daemon := newProxiedService(t)
	ctx := context.Background()
	for _, in := range []Input{
		{Name: "local", Transport: TransportStdio, Command: "npx", Args: []string{"-y", "local-mcp"}, Env: map[string]string{"K": "stored-value"}},
		{Name: "remote", Transport: TransportHTTP, URL: "https://x.example/mcp"},
	} {
		if _, err := svc.Create(ctx, in); err != nil {
			t.Fatal(err)
		}
	}
	open := true
	svc.SetSessionLookup(func(context.Context, string) (string, bool) { return "demo", open })
	servers := svc.ForSession(ctx, SessionInput{SessionID: "ao-12", ProjectID: "demo"})
	if len(servers) != 2 || servers[0].Name != "local" {
		t.Fatalf("servers = %+v", servers)
	}
	launch, token := servers[0].URL, servers[0].Headers["Authorization"]
	status, body := launchGet(t, launch, token)
	if status != http.StatusOK || !strings.Contains(body, `"command":"npx"`) || !strings.Contains(body, `"args":["-y","local-mcp"]`) || !strings.Contains(body, `"K":"stored-value"`) {
		t.Fatalf("launch = %d %s", status, body)
	}
	otherToken, _ := svc.sessionToken("ao-13")
	for name, got := range map[string]int{
		"no token":                 first(launchGet(t, launch, "")),
		"token of another session": first(launchGet(t, launch, "Bearer "+otherToken)),
		"a server URL connector":   first(launchGet(t, daemon.URL+proxyPrefix+"ao-12/connectors/remote/launch", token)),
		"an unknown connector":     first(launchGet(t, daemon.URL+proxyPrefix+"ao-12/connectors/none/launch", token)),
	} {
		if got == http.StatusOK {
			t.Errorf("%s got the launch details", name)
		}
	}
	if status, body := proxyPost(t, strings.TrimSuffix(launch, "/launch"), token, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`); strings.Contains(body, "stored-value") || !strings.Contains(body, "has no connector named local") {
		t.Fatalf("proxy call to a command connector = %d %s", status, body)
	}
	if _, err := svc.SetEnabled(ctx, "local", false); err != nil {
		t.Fatal(err)
	}
	if status, body := launchGet(t, launch, token); status != http.StatusForbidden || strings.Contains(body, "stored-value") {
		t.Fatalf("turned off = %d %s", status, body)
	}
	if _, err := svc.SetEnabled(ctx, "local", true); err != nil {
		t.Fatal(err)
	}
	open = false
	clock := svc.now
	svc.now = func() time.Time { return clock().Add(startGrace + time.Second) }
	if status, body := launchGet(t, launch, token); status != http.StatusUnauthorized || strings.Contains(body, "stored-value") {
		t.Fatalf("closed session = %d %s", status, body)
	}
}

func first(status int, _ string) int { return status }

func TestSessionEntriesAndTheCallbackUseTheAddressTheDaemonBound(t *testing.T) {
	svc := New(t.TempDir(), "http://127.0.0.1:3001")
	if _, err := svc.Create(context.Background(), Input{Name: "docs", Transport: TransportHTTP, URL: "https://docs.example/mcp"}); err != nil {
		t.Fatal(err)
	}
	svc.SetBaseURL("http://127.0.0.1:52991/")
	servers := svc.ForSession(context.Background(), SessionInput{SessionID: "ao-1"})
	if len(servers) != 1 || servers[0].URL != "http://127.0.0.1:52991/mcp/s/ao-1/connectors/docs" {
		t.Fatalf("servers = %+v", servers)
	}
	if got := svc.CallbackURL(); got != "http://127.0.0.1:52991/oauth/mcp/callback" {
		t.Fatalf("callback = %s", got)
	}
}

func TestACallIsCountedForASessionTheDaemonHasNotSeenStart(t *testing.T) {
	fake := newFakeServer(t, false)
	svc, daemon := newProxiedService(t)
	ctx := context.Background()
	if _, err := svc.Create(ctx, Input{Name: "open", Transport: TransportHTTP, URL: fake.URL + "/mcp"}); err != nil {
		t.Fatal(err)
	}
	token, err := svc.sessionToken("ao-9")
	if err != nil {
		t.Fatal(err)
	}
	if used, _ := svc.SessionConnectors(ctx, "ao-9"); len(used) != 0 {
		t.Fatalf("before a call = %+v", used)
	}
	status, body := proxyPost(t, daemon.URL+proxyPrefix+"ao-9/connectors/open", "Bearer "+token, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"echo","arguments":{"text":"x"}}}`)
	if status == http.StatusUnauthorized {
		t.Fatalf("call = %d %s", status, body)
	}
	used, err := svc.SessionConnectors(ctx, "ao-9")
	if err != nil || len(used) != 1 || used[0].Key != "open" || used[0].Calls != 1 || used[0].LastTool != "echo" {
		t.Fatalf("after a call = %+v, %v", used, err)
	}
}

func TestARefusedHeaderTellsTheAgentToHaveItEditedNotSignedIn(t *testing.T) {
	fake := newFakeServer(t, true)
	svc, _ := newProxiedService(t)
	ctx := context.Background()
	if _, err := svc.Create(ctx, Input{Name: "docs", Transport: TransportHTTP, URL: fake.URL + "/mcp", Auth: AuthHeader, Headers: map[string]string{"Authorization": "Bearer wrong"}}); err != nil {
		t.Fatal(err)
	}
	entry := svc.ForSession(ctx, SessionInput{SessionID: "ao-12"})[0]
	status, body := proxyPost(t, entry.URL, entry.Headers["Authorization"], `{"jsonrpc":"2.0","id":3,"method":"tools/list"}`)
	if status != http.StatusOK || !strings.Contains(body, "docs refused the saved sign-in value") || !strings.Contains(body, "edit docs") || strings.Contains(body, "press Sign in") {
		t.Fatalf("refused header = %d %s", status, body)
	}
	if _, err := svc.StartSignIn(ctx, "docs"); err == nil {
		t.Fatal("a header connector started a sign-in")
	}
}

func TestClosedSessionsAreForgottenWhenManyAreTracked(t *testing.T) {
	svc := New(t.TempDir(), testBaseURL)
	ctx := context.Background()
	svc.SetSessionLookup(func(_ context.Context, id string) (string, bool) { return "demo", id == "ao-0" })
	for i := range maxTrackedSessions {
		svc.ForSession(ctx, SessionInput{SessionID: "ao-" + strconv.Itoa(i), ProjectID: "demo"})
	}
	if len(svc.sessions) != maxTrackedSessions {
		t.Fatalf("tracked while they start = %d", len(svc.sessions))
	}
	clock := svc.now
	svc.now = func() time.Time { return clock().Add(startGrace + time.Second) }
	svc.ForSession(ctx, SessionInput{SessionID: "ao-new", ProjectID: "demo"})
	if _, kept := svc.sessions["ao-0"]; !kept || len(svc.sessions) != 2 {
		t.Fatalf("tracked after the closed ones aged = %d", len(svc.sessions))
	}
}
