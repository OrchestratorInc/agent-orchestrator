package httpd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/aoagents/agent-orchestrator/backend/internal/mobilebridge"
)

func TestLANTunnelAuthorizesTheUpgradeAndEveryRequestInside(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, r.Proto+" "+r.URL.Path)
	})
	st := &authState{}
	st.setHash(mobilebridge.HashPassword("secret12"))
	m := NewLANManager(inner, st, 0, slog.Default(), nil)
	port, err := m.Start(0)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer m.Stop(context.Background())
	tunnelURL := fmt.Sprintf("ws://127.0.0.1:%d%s", port, lanTunnelPath)

	if _, resp, err := websocket.Dial(context.Background(), tunnelURL, nil); err == nil || resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("upgrade without a password: err=%v resp=%v, want 401", err, resp)
	}

	ws, _, err := websocket.Dial(context.Background(), tunnelURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": {"Bearer secret12"}},
	})
	if err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	conn := websocket.NetConn(context.Background(), ws, websocket.MessageBinary)
	defer conn.Close()
	protocols := new(http.Protocols)
	protocols.SetUnencryptedHTTP2(true)
	dialed := false
	client := &http.Client{Transport: &http.Transport{
		Protocols: protocols,
		DialContext: func(context.Context, string, string) (net.Conn, error) {
			if dialed {
				return nil, fmt.Errorf("tunnel already in use")
			}
			dialed = true
			return conn, nil
		},
	}}
	get := func(auth string) (int, string) {
		req, _ := http.NewRequest(http.MethodGet, "http://tunnel/anything", nil)
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("request through the tunnel: %v", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(body)
	}
	if status, _ := get(""); status != http.StatusUnauthorized {
		t.Fatalf("request without a password inside the tunnel: got %d, want 401", status)
	}
	if status, body := get("Bearer secret12"); status != http.StatusOK || body != "HTTP/2.0 /anything" {
		t.Fatalf("authorized request inside the tunnel: got %d %q", status, body)
	}
}

// Behind cloudflared every tunnel arrives from loopback, and Cloudflare writes
// the visitor's address on the upgrade only. Requests inside a tunnel count
// against the tunnel's client: a CF-Connecting-IP written inside cannot move
// them, and one tunnel's failures do not lock out another's visitor.
func TestLANTunnelLocksOutTheTunnelsClient(t *testing.T) {
	st := &authState{}
	st.setHash(mobilebridge.HashPassword("secret12"))
	m := NewLANManager(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), st, 0, slog.Default(), nil)
	port, err := m.StartLoopback(0)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer m.Stop(context.Background())
	tunnel := func(visitor string) func(password, claimed string) int {
		header := http.Header{"Authorization": {"Bearer secret12"}}
		if visitor != "" {
			header.Set("CF-Connecting-IP", visitor)
		}
		ws, _, err := websocket.Dial(context.Background(), fmt.Sprintf("ws://127.0.0.1:%d%s", port, lanTunnelPath), &websocket.DialOptions{HTTPHeader: header})
		if err != nil {
			t.Fatalf("upgrade: %v", err)
		}
		conn := websocket.NetConn(context.Background(), ws, websocket.MessageBinary)
		t.Cleanup(func() { conn.Close() })
		protocols := new(http.Protocols)
		protocols.SetUnencryptedHTTP2(true)
		client := &http.Client{Transport: &http.Transport{
			Protocols:   protocols,
			DialContext: func(context.Context, string, string) (net.Conn, error) { return conn, nil },
		}}
		return func(password, claimed string) int {
			req, _ := http.NewRequest(http.MethodGet, "http://tunnel/anything", nil)
			req.Header.Set("Authorization", "Bearer "+password)
			if claimed != "" {
				req.Header.Set("CF-Connecting-IP", claimed)
			}
			resp, err := client.Do(req)
			if err != nil {
				t.Fatalf("request through the tunnel: %v", err)
			}
			resp.Body.Close()
			return resp.StatusCode
		}
	}
	local, visitor := tunnel(""), tunnel("203.0.113.2")
	for range 5 {
		if status := local("wrong123", ""); status != http.StatusUnauthorized {
			t.Fatalf("wrong password inside the tunnel: got %d, want 401", status)
		}
	}
	if status := local("secret12", "198.51.100.7"); status != http.StatusTooManyRequests {
		t.Errorf("locked-out tunnel claiming another address: got %d, want 429", status)
	}
	if status := visitor("secret12", ""); status != http.StatusOK {
		t.Errorf("a visitor's tunnel after another tunnel's failures: got %d, want 200", status)
	}
}

func TestLANTunnelClosesWhenThePasswordRotates(t *testing.T) {
	st := &authState{}
	st.setHash(mobilebridge.HashPassword("secret12"))
	m := NewLANManager(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}), st, 0, slog.Default(), nil)
	port, err := m.Start(0)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer m.Stop(context.Background())
	ws, _, err := websocket.Dial(context.Background(), fmt.Sprintf("ws://127.0.0.1:%d%s", port, lanTunnelPath), &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": {"Bearer secret12"}},
	})
	if err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	conn := websocket.NetConn(context.Background(), ws, websocket.MessageBinary)
	defer conn.Close()

	m.SetPasswordHash(mobilebridge.HashPassword("rotated1"))
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Read(make([]byte, 1)); err == nil || errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("tunnel still open after the password rotated (read: %v)", err)
	}
}

// One WebSocket message may carry more than 1 MiB of HTTP/2 frames: the tunnel
// sets no message size limit.
func TestLANTunnelCarriesMessagesOverOneMiB(t *testing.T) {
	st := &authState{}
	st.setHash(mobilebridge.HashPassword("secret12"))
	m := NewLANManager(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "ok")
	}), st, 0, slog.Default(), nil)
	port, err := m.Start(0)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer m.Stop(context.Background())
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second) // fail, never hang
	defer cancel()
	ws, _, err := websocket.Dial(ctx, fmt.Sprintf("ws://127.0.0.1:%d%s", port, lanTunnelPath), &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": {"Bearer secret12"}},
	})
	if err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	conn := &paddedOpening{Conn: websocket.NetConn(ctx, ws, websocket.MessageBinary)}
	defer conn.Close()
	protocols := new(http.Protocols)
	protocols.SetUnencryptedHTTP2(true)
	client := &http.Client{Transport: &http.Transport{
		Protocols:   protocols,
		DialContext: func(context.Context, string, string) (net.Conn, error) { return conn, nil },
	}}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://tunnel/anything", nil)
	req.Header.Set("Authorization", "Bearer secret12")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request after a %d-byte message: %v", conn.sent.Load(), err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || string(body) != "ok" || conn.sent.Load() <= 1<<20 {
		t.Fatalf("got %d %q after a %d-byte message", resp.StatusCode, body, conn.sent.Load())
	}
}

// paddedOpening sends the HTTP/2 client's opening flush (preface, SETTINGS,
// WINDOW_UPDATE) as one message over 1 MiB, padded with connection
// WINDOW_UPDATEs of 1.
type paddedOpening struct {
	net.Conn
	sent atomic.Int64 // size of that message, once sent
}

func (c *paddedOpening) Write(p []byte) (int, error) {
	if c.sent.Load() > 0 {
		return c.Conn.Write(p)
	}
	windowUpdate := []byte{0, 0, 4, 0x8, 0, 0, 0, 0, 0, 0, 0, 0, 1} // length 4, WINDOW_UPDATE, stream 0, increment 1
	message := append(bytes.Clone(p), bytes.Repeat(windowUpdate, 1<<20/len(windowUpdate)+1)...)
	if _, err := c.Conn.Write(message); err != nil {
		return 0, err
	}
	c.sent.Store(int64(len(message)))
	return len(p), nil
}
