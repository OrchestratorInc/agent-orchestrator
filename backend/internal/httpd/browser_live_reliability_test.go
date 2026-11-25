package httpd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/browserstream"
	"github.com/aoagents/agent-orchestrator/backend/internal/mobilebridge"
)

// A still page sends nothing, and the WebSocket's own pings are invisible to
// the phone's JavaScript, so the stream says it is there on a schedule. The
// phone treats a heartbeat host's silence as a dead stream.
func TestBrowserLiveSendsHeartbeatsWhileIdle(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	hub := NewBrowserLiveHub(nil, nil, nil, nil)
	hub.heartbeatEvery = 20 * time.Millisecond
	events := make(chan browserstream.Event)
	frames := make(chan browserstream.Event)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.CloseNow() }()
		_ = hub.writeBrowserEvents(ctx, conn, events, frames)
	}))
	defer server.Close()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { cancel(); _ = conn.CloseNow() }()
	for i := 0; i < 2; i++ {
		kind, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var message struct{ Type string }
		if kind != websocket.MessageText || json.Unmarshal(data, &message) != nil || message.Type != "heartbeat" {
			t.Fatalf("message %d = %v %s, want a heartbeat", i, kind, data)
		}
	}
}

func TestBrowserLiveHeartbeatDefaultsToTwoSeconds(t *testing.T) {
	if got := NewBrowserLiveHub(nil, nil, nil, nil).heartbeatEvery; got != 2*time.Second {
		t.Fatalf("heartbeat = %v, want 2s", got)
	}
}

// A phone away from home reaches the daemon through cloudflared, whose
// connector targets the LAN listener's own port, or through the HTTP/2 tunnel
// inside /api/v1/tunnel. Both pass the LAN chain, so /browser/live is reached
// with the same auth: with sharing off it answers 403, not the 404 loopback gets.
func TestBrowserLiveIsReachableThroughTheLANChainAndItsTunnel(t *testing.T) {
	hub := NewBrowserLiveHub(nil, &browserLiveSessionStub{}, func() bool { return false }, nil)
	router := chi.NewRouter()
	mountBrowserLive(router, hub)
	st := &authState{}
	st.setHash(mobilebridge.HashPassword("secret12"))
	m := NewLANManager(router, st, 0, slog.Default(), nil)
	port, err := m.Start(0)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer m.Stop(context.Background())
	path := "/api/v1/sessions/s1/browser/live"

	direct := func(auth string) int {
		req, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d%s", port, path), nil)
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		return resp.StatusCode
	}
	if got := direct(""); got != http.StatusUnauthorized {
		t.Fatalf("direct without auth = %d, want 401", got)
	}
	if got := direct("Bearer secret12"); got != http.StatusForbidden {
		t.Fatalf("direct (cloudflared's path) = %d, want 403 from the live route", got)
	}

	ws, _, err := websocket.Dial(context.Background(), fmt.Sprintf("ws://127.0.0.1:%d%s", port, lanTunnelPath), &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": {"Bearer secret12"}},
	})
	if err != nil {
		t.Fatalf("tunnel upgrade: %v", err)
	}
	conn := websocket.NetConn(context.Background(), ws, websocket.MessageBinary)
	defer conn.Close()
	protocols := new(http.Protocols)
	protocols.SetUnencryptedHTTP2(true)
	client := &http.Client{Transport: &http.Transport{
		Protocols:   protocols,
		DialContext: func(context.Context, string, string) (net.Conn, error) { return conn, nil },
	}}
	req, _ := http.NewRequest(http.MethodGet, "http://tunnel"+path, nil)
	req.Header.Set("Authorization", "Bearer secret12")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request inside the tunnel: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden || !strings.Contains(string(body), "BROWSER_REMOTE_DISABLED") {
		t.Fatalf("inside the tunnel = %d %s, want 403 BROWSER_REMOTE_DISABLED", resp.StatusCode, body)
	}
}
