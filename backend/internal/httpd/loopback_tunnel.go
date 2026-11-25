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
	loopbackRefusedHeader = "X-AO-Loopback-Refused"
	loopbackDialTimeout   = 5 * time.Second
)

func (m *LANManager) serveLoopbackTunnel(w http.ResponseWriter, r *http.Request) {
	port, err := strconv.Atoi(r.URL.Query().Get("port"))
	if err != nil || port < 1 || port > 65535 || !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		http.Error(w, "a WebSocket upgrade with port 1-65535 is required", http.StatusBadRequest)
		return
	}
	if m.isOwnPort(port) {
		refuseLoopback(w, fmt.Sprintf("Port %d is this Orchestrator daemon's own port.", port))
		return
	}
	upstream, err := dialLoopback(r.Context(), port)
	if err != nil {
		refuseLoopback(w, fmt.Sprintf("Nothing accepted a connection on loopback port %d.", port))
		return
	}
	defer func() { _ = upstream.Close() }()
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

func refuseLoopback(w http.ResponseWriter, message string) {
	w.Header().Set(loopbackRefusedHeader, "1")
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
