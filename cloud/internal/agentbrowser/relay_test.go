package agentbrowser

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// fakeControlPlane accepts the worker's control socket and its stream
// sockets, and dials the worker's relay as the control plane does: one "open"
// per connection its transport needs.
type fakeControlPlane struct {
	control chan *websocket.Conn
	streams chan net.Conn
	opens   atomic.Int32
}

func (cp *fakeControlPlane) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Worker worker-token" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	if r.URL.Path == "/worker/agent-browser" {
		cp.control <- conn
		for {
			if _, _, err := conn.Read(r.Context()); err != nil {
				return
			}
		}
	}
	stream := websocket.NetConn(r.Context(), conn, websocket.MessageBinary)
	cp.streams <- stream
	<-r.Context().Done()
}

func (cp *fakeControlPlane) dial(ctx context.Context, _, _ string) (net.Conn, error) {
	var control *websocket.Conn
	select {
	case control = <-cp.control:
		cp.control <- control
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	var id [16]byte
	_, _ = rand.Read(id[:])
	open, _ := json.Marshal(map[string]string{"type": "open", "stream": hex.EncodeToString(id[:])})
	if err := control.Write(ctx, websocket.MessageText, open); err != nil {
		return nil, err
	}
	cp.opens.Add(1)
	select {
	case stream := <-cp.streams:
		return stream, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestRelayServesKeepAliveRequestsAndTheViewerSocketOverStreams(t *testing.T) {
	// The browser service: a viewer socket that echoes.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		kind, message, err := conn.Read(r.Context())
		if err == nil {
			_ = conn.Write(r.Context(), kind, append([]byte("echo "), message...))
		}
		<-r.Context().Done()
	}))
	defer upstream.Close()
	cp := &fakeControlPlane{control: make(chan *websocket.Conn, 1), streams: make(chan net.Conn)}
	server := httptest.NewServer(cp)
	defer server.Close()

	agent, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var starts atomic.Int32
	s := &Service{token: "service-token", agent: agent, logger: slog.New(slog.DiscardHandler), start: fakeStart(upstream, &starts)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ran := make(chan struct{})
	go func() {
		s.Run(ctx, ControlPlane{URL: server.URL + "/worker/agent-browser", Token: func() string { return "worker-token" }, HTTP: server.Client()})
		close(ran)
	}()

	client := &http.Client{Transport: &http.Transport{DialContext: cp.dial}, Timeout: 10 * time.Second}
	for range 2 {
		response, err := client.Get("http://agent-browser/stats")
		if err != nil {
			t.Fatalf("stats over the relay: %v", err)
		}
		body, _ := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if string(body) != `{"running":false}` {
			t.Fatalf("stats = %q, want {\"running\":false}", body)
		}
	}
	if opens := cp.opens.Load(); opens != 1 {
		t.Fatalf("two requests took %d streams, want one kept alive", opens)
	}

	dialCtx, dialCancel := context.WithTimeout(ctx, 10*time.Second)
	defer dialCancel()
	viewer, _, err := websocket.Dial(dialCtx, "ws://agent-browser/ws/cdp", &websocket.DialOptions{HTTPClient: client})
	if err != nil {
		t.Fatalf("viewer socket over the relay: %v", err)
	}
	defer viewer.CloseNow()
	if err := viewer.Write(dialCtx, websocket.MessageText, []byte("frame")); err != nil {
		t.Fatal(err)
	}
	if _, message, err := viewer.Read(dialCtx); err != nil || string(message) != "echo frame" {
		t.Fatalf("viewer read %q, %v; want the echo", message, err)
	}
	if starts.Load() != 1 {
		t.Fatalf("browser started %d times, want once", starts.Load())
	}

	cancel()
	select {
	case <-ran:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after its context ended")
	}
}

func TestRelayStopsWhenTheControlPlaneRejectsTheToken(t *testing.T) {
	var dials atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dials.Add(1)
		http.Error(w, `{"code":"STALE_WORKER_TOKEN"}`, http.StatusUnauthorized)
	}))
	defer server.Close()
	s := &Service{logger: slog.New(slog.DiscardHandler)}
	stopped := make(chan struct{})
	go func() {
		s.relayLoop(context.Background(), ControlPlane{URL: server.URL, Token: func() string { return "stale" }, HTTP: server.Client()}, newStreamListener())
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("relay kept redialing a rejected token")
	}
	if dials.Load() != 1 {
		t.Fatalf("dialed %d times, want once", dials.Load())
	}
}

// A control plane that accepts the control socket and drops it at once (two
// workers replacing each other's) gets backed-off redials, not a tight loop.
func TestRelayBacksOffWhenTheControlSocketDropsAtOnce(t *testing.T) {
	var dials atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dials.Add(1)
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		_ = conn.Close(websocket.StatusPolicyViolation, "replaced")
	}))
	defer server.Close()
	s := &Service{logger: slog.New(slog.DiscardHandler)}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	s.relayLoop(ctx, ControlPlane{URL: server.URL, Token: func() string { return "worker-token" }, HTTP: server.Client()}, newStreamListener())
	// 0.5 s, 1 s, then 2 s apart: three dials in two seconds.
	if got := dials.Load(); got > 3 {
		t.Fatalf("dialed %d times in 2 s, want at most 3", got)
	}
}
