package httpd

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/aoagents/agent-orchestrator/backend/internal/mobilebridge"
)

func startLoopbackLAN(t *testing.T) (*LANManager, int) {
	t.Helper()
	st := &authState{}
	st.setHash(mobilebridge.HashPassword("secret12"))
	m := NewLANManager(http.NotFoundHandler(), st, 0, slog.Default(), nil)
	port, err := m.Start(0)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = m.Stop(context.Background()) })
	return m, port
}

func startEcho(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { _, _ = io.Copy(conn, conn); _ = conn.Close() }()
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port
}

func dialLoopbackTunnel(lanPort, port int, password string) (net.Conn, *http.Response, error) {
	header := http.Header{}
	if password != "" {
		header.Set("Authorization", "Bearer "+password)
	}
	ws, resp, err := websocket.Dial(context.Background(), fmt.Sprintf("ws://127.0.0.1:%d%s?port=%d", lanPort, loopbackTunnelPath, port), &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		return nil, resp, err
	}
	return websocket.NetConn(context.Background(), ws, websocket.MessageBinary), resp, nil
}

func TestLoopbackTunnelCarriesBytesForAnAuthorizedClient(t *testing.T) {
	_, lanPort := startLoopbackLAN(t)
	echo := startEcho(t)

	if _, resp, err := dialLoopbackTunnel(lanPort, echo, ""); err == nil || resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("tunnel without a password: err=%v resp=%v, want 401", err, resp)
	}
	conn, _, err := dialLoopbackTunnel(lanPort, echo, "secret12")
	if err != nil {
		t.Fatalf("tunnel: %v", err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatalf("write: %v", err)
	}
	got := make([]byte, 4)
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.ReadFull(conn, got); err != nil || string(got) != "ping" {
		t.Fatalf("echo through the tunnel: %q, %v", got, err)
	}
}

func TestLoopbackTunnelRefusesBeforeTheUpgrade(t *testing.T) {
	m, lanPort := startLoopbackLAN(t)
	daemon := startEcho(t)
	m.SetDaemonPort(daemon)
	closed, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	closedPort := closed.Addr().(*net.TCPAddr).Port
	_ = closed.Close()

	for _, tc := range []struct {
		name   string
		port   int
		status int
		header string
	}{
		{"the LAN listener's own port", lanPort, http.StatusForbidden, loopbackRefusedHeader},
		{"the daemon's API port", daemon, http.StatusForbidden, loopbackRefusedHeader},
		{"a port nothing listens on", closedPort, http.StatusForbidden, loopbackRefusedHeader},
		{"port 0", 0, http.StatusBadRequest, ""},
	} {
		_, resp, err := dialLoopbackTunnel(lanPort, tc.port, "secret12")
		if err == nil || resp == nil || resp.StatusCode != tc.status {
			t.Errorf("%s: err=%v resp=%v, want %d", tc.name, err, resp, tc.status)
			continue
		}
		if tc.header != "" && resp.Header.Get(tc.header) != "1" {
			t.Errorf("%s: missing %s", tc.name, tc.header)
		}
	}
}

func TestLoopbackTunnelClosesWhenThePasswordIsRotated(t *testing.T) {
	m, lanPort := startLoopbackLAN(t)
	conn, _, err := dialLoopbackTunnel(lanPort, startEcho(t), "secret12")
	if err != nil {
		t.Fatalf("tunnel: %v", err)
	}
	defer conn.Close()
	m.SetPasswordHash(mobilebridge.HashPassword("rotated1"))
	read := make(chan error, 1)
	go func() {
		_, err := conn.Read(make([]byte, 1))
		read <- err
	}()
	select {
	case err := <-read:
		if err == nil {
			t.Fatal("tunnel still open")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("tunnel still open after 5 s")
	}
}

func TestLoopbackTunnelRefusesAnotherHost(t *testing.T) {
	m := NewMobileLAN(http.NotFoundHandler(), "h_this", 0, slog.Default(), nil)
	m.SetPasswordHash(mobilebridge.HashPassword("secret12"))
	lanPort, err := m.Start(0)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = m.Stop(context.Background()) })
	for expected, want := range map[string]int{"h_this": http.StatusSwitchingProtocols, "h_other": http.StatusMisdirectedRequest} {
		header := http.Header{"Authorization": {"Bearer secret12"}, "X-Ao-Expected-Host-Id": {expected}}
		ws, resp, _ := websocket.Dial(context.Background(), fmt.Sprintf("ws://127.0.0.1:%d%s?port=%d", lanPort, loopbackTunnelPath, startEcho(t)), &websocket.DialOptions{HTTPHeader: header})
		if ws != nil {
			_ = ws.CloseNow()
		}
		if resp == nil || resp.StatusCode != want {
			t.Errorf("%s: resp=%v, want %d", expected, resp, want)
		}
	}
}
