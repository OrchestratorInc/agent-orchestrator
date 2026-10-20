package agentbrowser

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// The worker opens no port: it holds a control socket to the control plane,
// which sends {"type":"open","stream":id} for each connection it needs to the
// browser; the worker dials that stream socket, and the bytes on it are one
// HTTP/1.1 connection with the control plane as the client.

const (
	// The terminal stream's backoff.
	redialFloor   = 500 * time.Millisecond
	redialCeiling = 5 * time.Second
	pingInterval  = 20 * time.Second
	// A control socket held this long was a working one: its drop starts the
	// backoff over. One dropped sooner backs off like a failed dial.
	steadyAfter = pingInterval
)

// errRejected is the control plane refusing the worker's token for good: a
// stale epoch (401) or a token without the transport scope (403).
var errRejected = errors.New("control plane rejected the agent browser relay")

// ControlPlane is where the worker dials the relay's sockets.
type ControlPlane struct {
	URL   string        // the control socket: {public URL}/api/cloud/v1/worker/agent-browser
	Token func() string // the worker's current token
	HTTP  *http.Client
}

func (cp ControlPlane) dial(ctx context.Context, path string) (*websocket.Conn, error) {
	header := http.Header{}
	header.Set("Authorization", "Worker "+cp.Token())
	conn, response, err := websocket.Dial(ctx, cp.URL+path, &websocket.DialOptions{HTTPClient: cp.HTTP, HTTPHeader: header})
	if err != nil && response != nil &&
		(response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden) {
		return nil, errors.Join(errRejected, err)
	}
	return conn, err
}

// relayLoop holds the control socket, redialing until the control plane
// rejects the worker's token. Every redial waits: a control plane that accepts
// the socket and drops it at once (two workers replacing each other's) must
// not see a tight loop of authenticated dials.
func (s *Service) relayLoop(ctx context.Context, cp ControlPlane, streams *streamListener) {
	backoff := redialFloor
	for {
		conn, err := cp.dial(ctx, "")
		if errors.Is(err, errRejected) {
			s.logger.Warn("agent browser relay stopped", "error", err)
			return
		}
		if err == nil {
			held := time.Now()
			s.readControl(ctx, cp, conn, streams)
			if time.Since(held) >= steadyAfter {
				backoff = redialFloor
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, redialCeiling)
	}
}

// readControl opens a stream for each "open" until the control socket closes.
// Reading is also what answers the control plane's pings; the worker pings
// too, to notice a dead path.
func (s *Service) readControl(ctx context.Context, cp ControlPlane, conn *websocket.Conn, streams *streamListener) {
	defer conn.CloseNow()
	socketCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		ticker := time.NewTicker(pingInterval)
		defer ticker.Stop()
		for {
			select {
			case <-socketCtx.Done():
				return
			case <-ticker.C:
				pingCtx, pingCancel := context.WithTimeout(socketCtx, pingInterval)
				err := conn.Ping(pingCtx)
				pingCancel()
				if err != nil {
					_ = conn.CloseNow()
					return
				}
			}
		}
	}()
	for {
		_, message, err := conn.Read(socketCtx)
		if err != nil {
			return
		}
		var open struct {
			Type   string `json:"type"`
			Stream string `json:"stream"`
		}
		if json.Unmarshal(message, &open) == nil && open.Type == "open" && open.Stream != "" {
			// Streams live on ctx: a viewer's socket outlasts a control redial.
			go s.openStream(ctx, cp, open.Stream, streams)
		}
	}
}

// openStream dials the stream socket the control plane asked for and hands it
// to the relay's HTTP server as one connection.
func (s *Service) openStream(ctx context.Context, cp ControlPlane, id string, streams *streamListener) {
	conn, err := cp.dial(ctx, "/streams/"+url.PathEscape(id))
	if err != nil {
		s.logger.Warn("open agent browser stream", "error", err)
		return
	}
	socket := websocket.NetConn(ctx, conn, websocket.MessageBinary)
	// The HTTP server ends a pending read with a past deadline (after each
	// request, and to hijack the ws/cdp upgrade); a NetConn closes on that,
	// so the server reads a pipe instead.
	served, pipe := net.Pipe()
	go func() {
		_, _ = io.Copy(pipe, socket)
		_ = pipe.Close()
	}()
	go func() {
		_, _ = io.Copy(socket, pipe)
		_ = socket.Close()
	}()
	streams.put(served)
}

// streamListener hands stream sockets to the relay's HTTP server.
type streamListener struct {
	conns     chan net.Conn
	closed    chan struct{}
	closeOnce sync.Once
}

func newStreamListener() *streamListener {
	return &streamListener{conns: make(chan net.Conn), closed: make(chan struct{})}
}

func (l *streamListener) put(conn net.Conn) {
	select {
	case l.conns <- conn:
	case <-l.closed:
		_ = conn.Close()
	}
}

func (l *streamListener) Accept() (net.Conn, error) {
	select {
	case conn := <-l.conns:
		return conn, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *streamListener) Close() error {
	l.closeOnce.Do(func() { close(l.closed) })
	return nil
}

func (l *streamListener) Addr() net.Addr {
	return &net.UnixAddr{Name: "agent-browser-relay", Net: "relay"}
}
