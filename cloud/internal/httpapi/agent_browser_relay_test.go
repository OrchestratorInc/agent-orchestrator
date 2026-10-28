package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
	"github.com/coder/websocket"
)

const (
	agentBrowserTestOrg     = "5f0c6a52-2d5e-4c1e-9b44-3f0f2f6a1a01"
	agentBrowserTestSession = "0b7d0f0e-8f7a-4a51-8a0f-9d5f8f2f6b02"
	// agentBrowserTestReadTimeout stands in for ao-cloud's server ReadTimeout,
	// which bounds reading a request body and so a proxied upload.
	agentBrowserTestReadTimeout = 200 * time.Millisecond
)

// agentBrowserStore serves exactly the store surface the relay routes touch:
// local bearer auth, the session write check and the worker epoch check.
type agentBrowserStore struct{ Store }

func (agentBrowserStore) PrincipalFromLocalToken(context.Context, []byte) (domain.Principal, error) {
	return domain.Principal{UserID: "user"}, nil
}

func (agentBrowserStore) CheckSessionWriteAccess(context.Context, domain.Principal, string, string) error {
	return nil
}

func (agentBrowserStore) WorkerConnectionCurrent(context.Context, string, string, string, int64) (bool, error) {
	return true, nil
}

type agentBrowserTest struct {
	server *Server
	url    string
	token  string
}

func newAgentBrowserTest(t *testing.T) agentBrowserTest {
	t.Helper()
	tokens := worker.NewTokenManager([]byte("agent-browser-test-key"))
	server := New(Options{
		Store:            agentBrowserStore{},
		WorkerTokens:     tokens,
		LocalAuthEnabled: true,
		Logger:           slog.New(slog.DiscardHandler),
	})
	listener := httptest.NewUnstartedServer(server.Handler())
	listener.Config.ReadTimeout = agentBrowserTestReadTimeout
	listener.Start()
	t.Cleanup(listener.Close)
	token, err := tokens.Issue(worker.Claims{
		OrgID: agentBrowserTestOrg, SessionID: agentBrowserTestSession, WorkerID: "worker", Epoch: 1,
		Scopes: []string{"worker:transport"},
	}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	return agentBrowserTest{server: server, url: listener.URL, token: token}
}

func (a agentBrowserTest) desktopURL(tail string) string {
	return a.url + "/api/cloud/v1/orgs/" + agentBrowserTestOrg + "/sessions/" + agentBrowserTestSession + "/agent-browser/" + tail
}

func (a agentBrowserTest) workerDial(ctx context.Context, path string) (*websocket.Conn, *http.Response, error) {
	return websocket.Dial(ctx, "ws"+strings.TrimPrefix(a.url, "http")+"/api/cloud/v1/worker/agent-browser"+path,
		&websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Worker " + a.token}}})
}

// waitForRelay waits until a relay other than previous holds the session's
// slot; registration happens just after the control socket's upgrade.
func (a agentBrowserTest) waitForRelay(t *testing.T, previous *agentBrowserRelay) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if relay := a.server.agentBrowserRelays.lookup(agentBrowserTestSession); relay != nil && relay != previous {
			return
		}
	}
	t.Fatal("agent browser relay never registered")
}

// startFakeAgentBrowserWorker holds a control socket and, like the sandbox
// worker, answers each "open" by dialing that stream and piping it to a local
// HTTP server. The returned channel closes when the control socket does.
func (a agentBrowserTest) startFakeAgentBrowserWorker(t *testing.T, ctx context.Context, name string) <-chan struct{} {
	t.Helper()
	control, _, err := a.workerDial(ctx, "")
	if err != nil {
		t.Fatalf("dial control socket: %v", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/ws/cdp", func(w http.ResponseWriter, r *http.Request) {
		// Default origin checks refuse a cross-origin Origin, so this upgrade
		// succeeding proves the control plane stripped it.
		connection, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer connection.CloseNow()
		kind, message, err := connection.Read(ctx)
		if err != nil {
			return
		}
		_ = connection.Write(ctx, kind, append([]byte(name+":"), message...))
		_ = connection.Close(websocket.StatusNormalClosure, "")
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		writeJSON(w, http.StatusOK, map[string]string{
			"worker": name, "method": r.Method, "path": r.URL.Path, "query": r.URL.RawQuery, "body": string(body),
			"authorization": r.Header.Get("Authorization"), "cookie": r.Header.Get("Cookie"),
			"origin": r.Header.Get("Origin"), "custom": r.Header.Get("X-Custom"),
		})
	})
	backend := httptest.NewServer(mux)
	t.Cleanup(backend.Close)

	closed := make(chan struct{})
	go func() {
		defer close(closed)
		for {
			_, message, err := control.Read(ctx)
			if err != nil {
				return
			}
			var open struct{ Type, Stream string }
			if json.Unmarshal(message, &open) != nil || open.Type != "open" {
				continue
			}
			go func() {
				stream, _, err := a.workerDial(ctx, "/streams/"+open.Stream)
				if err != nil {
					return
				}
				tunnel := websocket.NetConn(ctx, stream, websocket.MessageBinary)
				defer tunnel.Close()
				local, err := net.Dial("tcp", backend.Listener.Addr().String())
				if err != nil {
					return
				}
				go func() {
					_, _ = io.Copy(local, tunnel)
					_ = local.Close()
				}()
				_, _ = io.Copy(tunnel, local)
			}()
		}
	}()
	return closed
}

func (a agentBrowserTest) desktopRequest(t *testing.T, ctx context.Context, method, tail string, body io.Reader) (int, map[string]string) {
	t.Helper()
	request, err := http.NewRequestWithContext(ctx, method, a.desktopURL(tail), body)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer ao_local_desktop")
	request.Header.Set("Cookie", "session=secret")
	request.Header.Set("Origin", "https://desktop.example")
	request.Header.Set("X-Custom", "kept")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var decoded map[string]string
	_ = json.NewDecoder(response.Body).Decode(&decoded)
	return response.StatusCode, decoded
}

func TestAgentBrowserRelayRoundTrip(t *testing.T) {
	relay := newAgentBrowserTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	relay.startFakeAgentBrowserWorker(t, ctx, "a")
	relay.waitForRelay(t, nil)

	status, got := relay.desktopRequest(t, ctx, http.MethodGet, "v/cdp?a=1&b=two%20words", nil)
	if status != http.StatusOK {
		t.Fatalf("GET status %d, body %v", status, got)
	}
	want := map[string]string{
		"worker": "a", "method": http.MethodGet, "path": "/v/cdp", "query": "a=1&b=two%20words", "body": "",
		"authorization": "", "cookie": "", "origin": "", "custom": "kept",
	}
	for key, value := range want {
		if got[key] != value {
			t.Errorf("GET %s = %q, want %q", key, got[key], value)
		}
	}

	// An upload still streaming after the server's ReadTimeout must arrive whole.
	upload, uploading := io.Pipe()
	go func() {
		_, _ = uploading.Write([]byte("slow "))
		time.Sleep(3 * agentBrowserTestReadTimeout)
		_, _ = uploading.Write([]byte("upload"))
		_ = uploading.Close()
	}()
	status, got = relay.desktopRequest(t, ctx, http.MethodPost, "upload?dir=abc&name=f.txt", upload)
	if status != http.StatusOK || got["method"] != http.MethodPost || got["path"] != "/upload" || got["body"] != "slow upload" {
		t.Fatalf("POST status %d, body %v", status, got)
	}

	socket, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(relay.desktopURL("ws/cdp"), "http"), &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": {"Bearer ao_local_desktop"}, "Origin": {"https://desktop.example"}},
	})
	if err != nil {
		t.Fatalf("dial ws/cdp: %v", err)
	}
	defer socket.CloseNow()
	if err := socket.Write(ctx, websocket.MessageText, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	_, message, err := socket.Read(ctx)
	if err != nil || string(message) != "a:hello" {
		t.Fatalf("ws/cdp echo %q, %v", message, err)
	}
}

func TestAgentBrowserRelayUnavailableAndInvalidTails(t *testing.T) {
	relay := newAgentBrowserTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)

	// No connected agent browser is a state of the session, not a gateway
	// failure: 409 with when to ask again, never a 5xx.
	status, got := relay.desktopRequest(t, ctx, http.MethodGet, "v/cdp", nil)
	if status != http.StatusConflict || got["code"] != "AGENT_BROWSER_UNAVAILABLE" {
		t.Fatalf("no relay: status %d, body %v", status, got)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, relay.desktopURL("stats"), nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer ao_local_desktop")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusConflict || response.Header.Get("Retry-After") != "30" {
		t.Fatalf("no relay stats: status %d, Retry-After %q", response.StatusCode, response.Header.Get("Retry-After"))
	}
	for _, tail := range []string{"", "../secret", "a/%2e%2e/b"} {
		if status, got := relay.desktopRequest(t, ctx, http.MethodGet, tail, nil); status != http.StatusNotFound {
			t.Errorf("tail %q: status %d, body %v", tail, status, got)
		}
	}
}

func TestAgentBrowserRelayRefusesUnrequestedStream(t *testing.T) {
	relay := newAgentBrowserTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	relay.startFakeAgentBrowserWorker(t, ctx, "a")
	relay.waitForRelay(t, nil)

	_, response, err := relay.workerDial(ctx, "/streams/0123456789abcdef0123456789abcdef")
	if err == nil {
		t.Fatal("an unrequested stream was accepted")
	}
	if response == nil || response.StatusCode != http.StatusNotFound {
		t.Fatalf("unrequested stream response %v, want 404", response)
	}
}

func TestAgentBrowserRelayNewerControlSocketReplacesOlder(t *testing.T) {
	relay := newAgentBrowserTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	firstClosed := relay.startFakeAgentBrowserWorker(t, ctx, "a")
	relay.waitForRelay(t, nil)
	first := relay.server.agentBrowserRelays.lookup(agentBrowserTestSession)

	relay.startFakeAgentBrowserWorker(t, ctx, "b")
	relay.waitForRelay(t, first)
	select {
	case <-firstClosed:
	case <-ctx.Done():
		t.Fatal("the replaced control socket stayed open")
	}
	if status, got := relay.desktopRequest(t, ctx, http.MethodGet, "v/cdp", nil); status != http.StatusOK || got["worker"] != "b" {
		t.Fatalf("after replacement: status %d, body %v", status, got)
	}
}
