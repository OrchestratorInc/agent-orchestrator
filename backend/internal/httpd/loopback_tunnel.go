package httpd

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"
)

const loopbackTunnelPath = "/api/v1/loopback-tunnel"

const (
	loopbackRefusedHeader  = "X-AO-Loopback-Refused"
	loopbackDisabledHeader = "X-AO-Loopback-Disabled"
	loopbackDialTimeout    = 5 * time.Second
)

func (m *LANManager) serveLoopbackTunnel(w http.ResponseWriter, r *http.Request) {
	port, err := strconv.Atoi(r.URL.Query().Get("port"))
	if err != nil || port < 1 || port > 65535 || !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		http.Error(w, "a WebSocket upgrade with port 1-65535 is required", http.StatusBadRequest)
		return
	}
	if m.loopbackOff.Load() {
		refuseLoopback(w, loopbackDisabledHeader, "The localhost tunnel is turned off on this host.")
		return
	}
	if m.isOwnPort(port) {
		refuseLoopback(w, loopbackRefusedHeader, fmt.Sprintf("Port %d is this Orchestrator daemon's own port.", port))
		return
	}
	upstream, err := dialLoopback(r.Context(), port)
	if err != nil {
		refuseLoopback(w, loopbackRefusedHeader, fmt.Sprintf("Nothing accepted a connection on loopback port %d.", port))
		return
	}
	defer func() { _ = upstream.Close() }()
	if !m.trackLoopback(upstream) {
		refuseLoopback(w, loopbackDisabledHeader, "The localhost tunnel is turned off on this host.")
		return
	}
	defer m.untrackLoopback(upstream)
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		return
	}
	client := websocket.NetConn(r.Context(), ws, websocket.MessageBinary)
	defer func() { _ = client.Close() }()
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(client, upstream); done <- struct{}{} }()
	go func() { _, _ = io.Copy(upstream, client); done <- struct{}{} }()
	<-done
}

func refuseLoopback(w http.ResponseWriter, header, message string) {
	w.Header().Set(header, "1")
	http.Error(w, message, http.StatusForbidden)
}

func dialLoopback(ctx context.Context, port int) (net.Conn, error) {
	dialer := net.Dialer{Timeout: loopbackDialTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		conn, err = dialer.DialContext(ctx, "tcp", net.JoinHostPort("::1", strconv.Itoa(port)))
	}
	return conn, err
}

// SetDaemonPort names the daemon's API port, which no tunnel may reach.
func (m *LANManager) SetDaemonPort(port int) { m.daemonPort.Store(int64(port)) }

func (m *LANManager) isOwnPort(port int) bool {
	return int64(port) == m.daemonPort.Load() || port == m.BoundPort()
}

// SetLoopbackTunnelOff turns the localhost tunnel off or on. Off closes open tunnels.
func (m *LANManager) SetLoopbackTunnelOff(off bool) {
	if m.loopbackOff.Swap(off) == off || !off {
		return
	}
	m.loopbackMu.Lock()
	defer m.loopbackMu.Unlock()
	for conn := range m.loopbackConns {
		_ = conn.Close()
	}
}

func (m *LANManager) trackLoopback(conn net.Conn) bool {
	m.loopbackMu.Lock()
	if m.loopbackConns == nil {
		m.loopbackConns = map[net.Conn]struct{}{}
	}
	m.loopbackConns[conn] = struct{}{}
	m.loopbackMu.Unlock()
	return !m.loopbackOff.Load()
}

func (m *LANManager) untrackLoopback(conn net.Conn) {
	m.loopbackMu.Lock()
	delete(m.loopbackConns, conn)
	m.loopbackMu.Unlock()
}
