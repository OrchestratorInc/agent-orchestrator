package controllers_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/coder/websocket"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
)

// The agent browser reaches the desktop through the daemon: the browser's
// token is added on the way, AO's credential and the desktop's origin are not
// passed on, the stream socket works, and only the viewer's routes are open.
func TestSessionsAPI_AgentBrowserProxy(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	browser := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.URL.RequestURI()+" auth="+r.Header.Get("Authorization")+" origin="+r.Header.Get("Origin")+" cookie="+r.Header.Get("Cookie"))
		mu.Unlock()
		if r.URL.Path == "/ws/cdp" {
			conn, err := websocket.Accept(w, r, nil) // refuses a foreign Origin, as rbhost does
			if err != nil {
				t.Errorf("accept: %v", err)
				return
			}
			defer func() { _ = conn.CloseNow() }()
			kind, msg, err := conn.Read(r.Context())
			if err == nil {
				err = conn.Write(r.Context(), kind, msg)
			}
			if err != nil {
				t.Errorf("echo: %v", err)
			}
			return
		}
		_, _ = io.WriteString(w, "viewer")
	}))
	t.Cleanup(browser.Close)
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{AllowedOrigins: []string{"app://renderer"}, AgentBrowserURL: browser.URL, AgentBrowserToken: "rb-secret"},
		slog.New(slog.NewTextHandler(io.Discard, nil)), nil, httpd.APIDeps{Sessions: newFakeSessionService()}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)

	get := func(path string) int {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, srv.URL+path, nil)
		req.Header.Set("Authorization", "Bearer host-password")
		req.Header.Set("Origin", "app://renderer")
		req.Header.Set("Cookie", "ao_conn=host-password")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		return res.StatusCode
	}
	if code := get("/api/v1/sessions/ao-1/agent-browser/v/cdp?token=null&sq=70"); code != http.StatusOK {
		t.Fatalf("viewer page status = %d, want 200", code)
	}
	mu.Lock()
	first := seen[0]
	mu.Unlock()
	if first != "/v/cdp?sq=70&token=rb-secret auth= origin= cookie=" {
		t.Fatalf("browser saw %q, want the viewer path with its own token and no AO credential, origin or cookie", first)
	}
	for _, path := range []string{"agent", "debug/reset", "download/a/b", "devtools/page/x"} {
		if code := get("/api/v1/sessions/ao-1/agent-browser/" + path); code != http.StatusNotFound {
			t.Fatalf("%s status = %d, want 404 (not a viewer route)", path, code)
		}
	}
	if code := get("/api/v1/sessions/missing/agent-browser/stats"); code != http.StatusNotFound {
		t.Fatalf("unknown session status = %d, want 404", code)
	}

	conn, _, err := websocket.Dial(context.Background(), "ws"+strings.TrimPrefix(srv.URL, "http")+"/api/v1/sessions/ao-1/agent-browser/ws/cdp", &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": {"Bearer host-password"}, "Origin": {"app://renderer"}},
	})
	if err != nil {
		t.Fatalf("stream socket through the daemon: %v", err)
	}
	defer func() { _ = conn.CloseNow() }()
	if err := conn.Write(context.Background(), websocket.MessageBinary, []byte("frame")); err != nil {
		t.Fatal(err)
	}
	if _, msg, err := conn.Read(context.Background()); err != nil || string(msg) != "frame" {
		t.Fatalf("echo = %q, %v; want frame", msg, err)
	}
}

func TestSessionsAPI_AgentBrowserAbsent(t *testing.T) {
	srv := newSessionTestServer(t, newFakeSessionService())
	res, err := http.Get(srv.URL + "/api/v1/sessions/ao-1/agent-browser/stats")
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 on a host with no agent browser", res.StatusCode)
	}
}

// The browser's mux cleans "." and ".." with a redirect that repeats the query,
// the browser's token with it. Such a download never reaches the browser, and
// a redirect the browser does send comes back without the token.
func TestSessionsAPI_AgentBrowserKeepsItsTokenOnTheHost(t *testing.T) {
	var mu sync.Mutex
	var reached []string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /download/{guid}", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("guid") == "moved" {
			http.Redirect(w, r, "/download/elsewhere?"+r.URL.RawQuery, http.StatusTemporaryRedirect)
			return
		}
		_, _ = io.WriteString(w, "file")
	})
	browser := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		reached = append(reached, r.URL.RequestURI())
		mu.Unlock()
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(browser.Close)
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{AgentBrowserURL: browser.URL, AgentBrowserToken: "rb-secret"},
		slog.New(slog.NewTextHandler(io.Discard, nil)), nil, httpd.APIDeps{Sessions: newFakeSessionService()}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	get := func(tail string) (*http.Response, string) {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/sessions/ao-1/agent-browser/"+tail, nil)
		req.Header.Set("Authorization", "Bearer host-password")
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = res.Body.Close() }()
		body, _ := io.ReadAll(res.Body)
		return res, string(body)
	}

	for _, guid := range []string{"..", ".", "%2e%2e", "%2E%2E", "a.b", "a_b", strings.Repeat("a", 65)} {
		if res, _ := get("download/" + guid); res.StatusCode != http.StatusNotFound {
			t.Errorf("download/%s status = %d, want 404", guid, res.StatusCode)
		}
	}
	mu.Lock()
	leaked := append([]string(nil), reached...)
	mu.Unlock()
	if len(leaked) != 0 {
		t.Fatalf("the browser was asked for %q", leaked)
	}
	if res, body := get("download/8c4b1e2a-37f3-4f43-9d55-0123456789ab"); res.StatusCode != http.StatusOK || body != "file" {
		t.Fatalf("download = %d %q, want the file", res.StatusCode, body)
	}
	res, body := get("download/moved?keep=1")
	if res.StatusCode != http.StatusTemporaryRedirect || res.Header.Get("Location") != "/download/elsewhere?keep=1" || strings.Contains(body, "rb-secret") {
		t.Fatalf("redirect = %d, Location %q, body %q; want the token in neither", res.StatusCode, res.Header.Get("Location"), body)
	}
}
