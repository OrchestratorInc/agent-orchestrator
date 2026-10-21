package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
)

// agentBrowserStreamTimeout bounds how long a desktop request waits for the
// worker to dial the stream socket it was asked to open.
const agentBrowserStreamTimeout = 10 * time.Second

// agentBrowserRetryAfterSeconds is how long the desktop waits before asking
// again for a session whose agent browser is not connected.
const agentBrowserRetryAfterSeconds = 30

// agentBrowserRelays maps a session to the control socket its worker holds for
// the sandbox's agent browser. Like the terminal relay it is process-local and
// nothing it carries is persisted: a desktop request is served only when it
// lands on the replica that holds the worker's control socket.
type agentBrowserRelays struct {
	mu     sync.Mutex
	relays map[string]*agentBrowserRelay
}

// agentBrowserRelay is one worker control socket. Its transport dials by
// asking the worker to open a stream socket, so its keep-alive pool only ever
// holds this session's streams.
type agentBrowserRelay struct {
	control   *websocket.Conn
	transport *http.Transport
	done      chan struct{}
	stopOnce  sync.Once

	mu      sync.Mutex
	pending map[string]chan net.Conn
}

func newAgentBrowserRelays() *agentBrowserRelays {
	return &agentBrowserRelays{relays: make(map[string]*agentBrowserRelay)}
}

func newAgentBrowserRelay(control *websocket.Conn) *agentBrowserRelay {
	relay := &agentBrowserRelay{
		control: control,
		done:    make(chan struct{}),
		pending: make(map[string]chan net.Conn),
	}
	relay.transport = &http.Transport{DialContext: relay.dial, IdleConnTimeout: 90 * time.Second}
	return relay
}

// register installs relay as the session's relay, stopping any previous one —
// a worker redial supersedes its old socket. The returned func deregisters,
// but only if relay still owns the slot.
func (a *agentBrowserRelays) register(sessionID string, relay *agentBrowserRelay) func() {
	a.mu.Lock()
	previous := a.relays[sessionID]
	a.relays[sessionID] = relay
	a.mu.Unlock()
	if previous != nil {
		previous.stop()
	}
	return func() {
		a.mu.Lock()
		if a.relays[sessionID] == relay {
			delete(a.relays, sessionID)
		}
		a.mu.Unlock()
		relay.stop()
	}
}

func (a *agentBrowserRelays) lookup(sessionID string) *agentBrowserRelay {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.relays[sessionID]
}

func (r *agentBrowserRelay) stop() {
	r.stopOnce.Do(func() { close(r.done) })
	r.transport.CloseIdleConnections()
}

// dial asks the worker for a stream socket and waits for it to arrive. Each
// stream carries exactly one HTTP/1.1 connection.
func (r *agentBrowserRelay) dial(ctx context.Context, _, _ string) (net.Conn, error) {
	var raw [16]byte
	_, _ = rand.Read(raw[:])
	id := hex.EncodeToString(raw[:])
	arrived := make(chan net.Conn, 1)
	r.mu.Lock()
	r.pending[id] = arrived
	r.mu.Unlock()

	ctx, cancel := context.WithTimeout(ctx, agentBrowserStreamTimeout)
	defer cancel()
	open, _ := json.Marshal(map[string]string{"type": "open", "stream": id})
	err := r.control.Write(ctx, websocket.MessageText, open)
	if err == nil {
		select {
		case conn := <-arrived:
			return conn, nil
		case <-r.done:
			err = errors.New("agent browser relay closed")
		case <-ctx.Done():
			err = ctx.Err()
		}
	}
	// deliver hands a stream over under the lock, so once the id is gone no
	// late stream can be stranded in the channel.
	r.mu.Lock()
	delete(r.pending, id)
	r.mu.Unlock()
	select {
	case conn := <-arrived:
		_ = conn.Close()
	default:
	}
	return nil, err
}

func (r *agentBrowserRelay) awaiting(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.pending[id]
	return ok
}

func (r *agentBrowserRelay) deliver(id string, conn net.Conn) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	arrived, ok := r.pending[id]
	if ok {
		delete(r.pending, id)
		arrived <- conn // buffered for exactly this send
	}
	return ok
}

// agentBrowserStreamConn reports when the transport closes a stream, which is
// when the worker's stream handler may return.
type agentBrowserStreamConn struct {
	net.Conn
	closeOnce sync.Once
	closed    chan struct{}
}

func (c *agentBrowserStreamConn) Close() error {
	c.closeOnce.Do(func() { close(c.closed) })
	return c.Conn.Close()
}

// proxyAgentBrowser relays a desktop request to the session's agent browser
// as "/{tail}" plus the original query. The worker enforces which tails exist.
func (s *Server) proxyAgentBrowser(w http.ResponseWriter, r *http.Request) {
	orgID, sessionID, ok := workspaceRoute(w, r)
	if !ok {
		return
	}
	tail := chi.URLParam(r, "*")
	if r.URL.RawPath != "" {
		// chi matched the escaped path; the proxied URL wants it decoded.
		var err error
		if tail, err = url.PathUnescape(tail); err != nil {
			tail = ""
		}
	}
	if tail == "" || strings.Contains(tail, "..") {
		writeError(w, r, http.StatusNotFound, "not_found", "The requested resource was not found.")
		return
	}
	// Driving the agent browser acts on the session, so viewers and read-only
	// shares are refused.
	if err := s.store.CheckSessionWriteAccess(r.Context(), principalFrom(r), orgID, sessionID); err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	relay := s.agentBrowserRelays.lookup(sessionID)
	if relay == nil {
		// Not a gateway failure: the session has no agent browser right now
		// (its sandbox has no Chromium, as on a bring-your-own Coder image, or
		// it is asleep or still starting). The desktop asks again after
		// Retry-After rather than counting a server error every few seconds.
		w.Header().Set("Retry-After", strconv.Itoa(agentBrowserRetryAfterSeconds))
		writeError(w, r, http.StatusConflict, "AGENT_BROWSER_UNAVAILABLE", "The session's agent browser is not connected.")
		return
	}
	// The server's ReadTimeout covers reading the request body, so it would cut
	// off a long upload; a response or an upgraded ws/cdp socket outlives it.
	_ = http.NewResponseController(w).SetReadDeadline(time.Time{})
	proxy := &httputil.ReverseProxy{
		Transport: relay.transport,
		Rewrite: func(request *httputil.ProxyRequest) {
			request.Out.URL = &url.URL{Scheme: "http", Host: "agent-browser", Path: "/" + tail, RawQuery: request.In.URL.RawQuery}
			request.Out.Host = ""
			for _, header := range []string{"Authorization", "Proxy-Authorization", "Cookie", "Origin"} {
				request.Out.Header.Del(header)
			}
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			if !errors.Is(err, context.Canceled) {
				s.logger.Warn("agent browser relay request failed",
					"error", err, "session_id", sessionID, "request_id", requestID(r))
			}
			writeError(w, r, http.StatusBadGateway, "AGENT_BROWSER_UNAVAILABLE", "The session's agent browser did not answer.")
		},
	}
	proxy.ServeHTTP(w, r)
}

// workerAgentBrowserControl holds the worker's control socket for its
// session's agent browser. The control plane writes one "open" message per
// connection it needs; the worker answers by dialing the stream route.
func (s *Server) workerAgentBrowserControl(w http.ResponseWriter, r *http.Request) {
	claims := workerFrom(r)
	if !worker.HasScope(claims, "worker:transport") {
		writeError(w, r, http.StatusForbidden, "SCOPE_REQUIRED", "The worker:transport scope is required.")
		return
	}
	connection, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		CompressionMode:    websocket.CompressionDisabled,
		InsecureSkipVerify: true,
	})
	if err != nil {
		return
	}
	defer connection.CloseNow()
	relay := newAgentBrowserRelay(connection)
	deregister := s.agentBrowserRelays.register(claims.SessionID, relay)
	defer deregister()

	// The worker sends nothing the relay needs, but reading is what answers
	// pings and notices the socket closing.
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	go func() {
		defer cancel()
		for {
			if _, _, err := connection.Read(ctx); err != nil {
				return
			}
		}
	}()
	select {
	case <-ctx.Done():
	case <-relay.done:
		_ = connection.Close(websocket.StatusNormalClosure, "replaced")
	}
}

// workerAgentBrowserStream accepts the stream socket the worker dialed for a
// pending "open" and hands it to the waiting transport as a net.Conn. It
// returns once the transport closes that connection.
func (s *Server) workerAgentBrowserStream(w http.ResponseWriter, r *http.Request) {
	claims := workerFrom(r)
	if !worker.HasScope(claims, "worker:transport") {
		writeError(w, r, http.StatusForbidden, "SCOPE_REQUIRED", "The worker:transport scope is required.")
		return
	}
	stream := chi.URLParam(r, "stream")
	relay := s.agentBrowserRelays.lookup(claims.SessionID)
	if relay == nil || !relay.awaiting(stream) {
		writeError(w, r, http.StatusNotFound, "not_found", "No agent browser stream is pending with that id.")
		return
	}
	connection, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		CompressionMode:    websocket.CompressionDisabled,
		InsecureSkipVerify: true,
	})
	if err != nil {
		return
	}
	// NetConn lifts the read limit: one frame may carry a whole body chunk.
	conn := &agentBrowserStreamConn{
		Conn:   websocket.NetConn(r.Context(), connection, websocket.MessageBinary),
		closed: make(chan struct{}),
	}
	if !relay.deliver(stream, conn) {
		// The request gave up between the check and the upgrade.
		_ = conn.Close()
		return
	}
	<-conn.closed
}
