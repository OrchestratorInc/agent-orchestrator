package httpd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/requestscope"
	"github.com/aoagents/agent-orchestrator/backend/internal/mobilebridge"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/systeminstall"
)

type LANManager struct {
	handler     http.Handler
	tunnel      http.Handler
	defaultPort int
	log         *slog.Logger
	state       *authState
	lock        *lockout

	transitionMu sync.Mutex
	mu           sync.Mutex
	srv          *http.Server
	ln           *lanListener
	cancel       context.CancelFunc
	bound        int
	bindHost     string
}

func NewLANManager(handler http.Handler, state *authState, defaultPort int, log *slog.Logger, sink ports.EventSink) *LANManager {
	lock := newLockout(time.Now)
	auth := authMiddleware(state, lock, newMobileConnectReporter(sink, time.Now))
	m := &LANManager{
		handler:     lanControlBlock(auth(remoteJSONCompression(handler))),
		defaultPort: defaultPort,
		log:         loggerOrDefault(log),
		state:       state,
		lock:        lock,
	}
	m.tunnel = auth(http.HandlerFunc(m.serveTunnel))
	return m
}

var lanControlBlockedPrefixes = []string{
	"/api/v1/awake",
	"/shutdown",
	"/internal/",
	"/api/v1/mobile",
	"/api/v1/dev",
	"/api/v1/browser",
	"/api/v1/desktop",
	"/api/v1/side-chats/launch",
	"/api/v1/system/install",
	"/api/v1/computer-use",
	"/api/v1/settings/computer-use",
	"/api/v1/agents/codex/accounts",
	"/api/v1/agents/codex/account-switches",
}

func lanControlBlock(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isLANControlBlockedPath(r.URL.Path) || isLANControlBlockedRequest(r.Method, r.URL.Path) {
			notFoundJSON(w, r)
			return
		}
		next.ServeHTTP(w, r.WithContext(requestscope.WithLAN(r.Context())))
	})
}

func isLANControlBlockedRequest(method, path string) bool {
	trimmed := strings.TrimSuffix(path, "/")
	if method != http.MethodPost || !strings.HasPrefix(trimmed, "/api/v1/agents/") || !strings.HasSuffix(trimmed, "/install") {
		return false
	}
	target := strings.TrimSuffix(strings.TrimPrefix(trimmed, "/api/v1/agents/"), "/install")
	return !systeminstall.IsAgentTarget(systeminstall.Target(target))
}

func isLANControlBlockedPath(path string) bool {
	if strings.HasPrefix(path, "/api/v1/sessions/") && strings.HasSuffix(strings.TrimSuffix(path, "/"), "/preview/server") {
		return true
	}
	for _, prefix := range lanControlBlockedPrefixes {
		trimmed := prefix
		if len(trimmed) > 1 && trimmed[len(trimmed)-1] == '/' {
			trimmed = trimmed[:len(trimmed)-1]
		}
		if path == trimmed || strings.HasPrefix(path, trimmed+"/") {
			return true
		}
	}
	return false
}

func IsLANControlBlockedPathForTest(path string) bool { return isLANControlBlockedPath(path) }

func NewMobileLAN(handler http.Handler, hostID string, defaultPort int, log *slog.Logger, sink ports.EventSink) *LANManager {
	return NewLANManager(expectedHostGuard(hostID)(handler), &authState{}, defaultPort, log, sink)
}

func expectedHostGuard(hostID string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if expected := r.Header.Get("X-AO-Expected-Host-ID"); expected != "" && expected != hostID {
				envelope.WriteAPIError(w, r, http.StatusMisdirectedRequest, "conflict", "HOST_ID_MISMATCH", "This address belongs to another Orchestrator host; reconnect to the intended host.", nil)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func (m *LANManager) SetPasswordHash(hash string) {
	m.transitionMu.Lock()
	defer m.transitionMu.Unlock()
	if m.state.currentHash() == hash {
		return
	}
	m.state.setHash(hash)
	m.lock.resetAll()
	m.mu.Lock()
	ln := m.ln
	m.mu.Unlock()
	if ln != nil {
		if err := ln.closeConnections(""); err != nil {
			m.log.Warn("close LAN connections after password rotation", "err", err)
		}
	}
}

func (m *LANManager) SetAccountTokenHash(hash string) {
	m.transitionMu.Lock()
	defer m.transitionMu.Unlock()
	previous := m.state.accountHash()
	if previous == hash {
		return
	}
	m.state.setAccountHash(hash)
	m.lock.resetAll()
	m.mu.Lock()
	ln := m.ln
	m.mu.Unlock()
	if ln != nil && previous != "" {
		_ = ln.closeConnections(previous)
	}
}

func (m *LANManager) UseDeviceCredentials(devices *mobilebridge.AccessDevices, codes *mobilebridge.PairingCodes) {
	m.state.devices, m.state.codes = devices, codes
}

func (m *LANManager) SetPasswordLoginDisabled(off bool) {
	m.transitionMu.Lock()
	defer m.transitionMu.Unlock()
	if m.state.passwordOff.Swap(off) == off || !off {
		return
	}
	m.DisconnectCredential(m.state.currentHash())
}

func (m *LANManager) DisconnectCredential(hash string) {
	if hash == "" {
		return
	}
	m.mu.Lock()
	ln := m.ln
	m.mu.Unlock()
	if ln != nil {
		_ = ln.closeConnections(hash)
	}
}

func (m *LANManager) ConnectedCredentials() map[string]bool {
	m.mu.Lock()
	ln := m.ln
	m.mu.Unlock()
	connected := map[string]bool{}
	if ln == nil {
		return connected
	}
	ln.mu.Lock()
	defer ln.mu.Unlock()
	for conn := range ln.conns {
		if conn.tokenHash != "" {
			connected[conn.tokenHash] = true
		}
	}
	return connected
}

func (m *LANManager) AccountTokenHash() string { return m.state.accountHash() }

func (m *LANManager) PasswordHash() string {
	return m.state.currentHash()
}

func (m *LANManager) Start(port int) (int, error) {
	return m.start(port, "0.0.0.0")
}

func (m *LANManager) StartLoopback(port int) (int, error) {
	return m.start(port, "127.0.0.1")
}

func (m *LANManager) start(port int, host string) (int, error) {
	m.transitionMu.Lock()
	defer m.transitionMu.Unlock()
	m.mu.Lock()
	if m.srv != nil {
		defer m.mu.Unlock()
		if m.bindHost != host {
			return 0, fmt.Errorf("authenticated listener already bound to %s; disable it before changing modes", m.bindHost)
		}
		return m.bound, nil
	}
	if port == 0 {
		port = m.defaultPort
	}
	addr := fmt.Sprintf("%s:%d", host, port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		if !isAddrInUse(err) {
			m.mu.Unlock()
			return 0, fmt.Errorf("bind authenticated listener %s: %w", addr, err)
		}
		if ln, err = net.Listen("tcp", fmt.Sprintf("%s:0", host)); err != nil {
			m.mu.Unlock()
			return 0, fmt.Errorf("bind authenticated listener ephemeral on %s: %w", host, err)
		}
		m.log.Warn("LAN port in use; bound ephemeral", "wanted", port, "bound", ln.Addr())
	}
	tcpAddr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		m.mu.Unlock()
		_ = ln.Close()
		return 0, fmt.Errorf("bind LAN: unexpected listener address type %T", ln.Addr())
	}
	streamCtx, cancel := context.WithCancel(context.Background())
	tracked := &lanListener{Listener: ln, conns: make(map[*lanConn]struct{})}
	m.ln, m.cancel = tracked, cancel
	m.bound = tcpAddr.Port
	m.bindHost = host
	m.srv = &http.Server{
		Handler:           m.dispatch(),
		ReadHeaderTimeout: 10 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return streamCtx },
		ConnContext: func(ctx context.Context, conn net.Conn) context.Context {
			return context.WithValue(ctx, lanConnContextKey{}, conn)
		},
	}
	m.srv.Protocols = new(http.Protocols)
	m.srv.Protocols.SetHTTP1(true)
	m.srv.Protocols.SetUnencryptedHTTP2(true)
	srv := m.srv
	boundPort := m.bound
	m.mu.Unlock()
	go func() {
		if err := srv.Serve(tracked); err != nil && !errors.Is(err, http.ErrServerClosed) {
			m.log.Error("LAN listener serve", "err", err)
		}
	}()
	m.log.Info("LAN listener started", "addr", ln.Addr())
	return boundPort, nil
}

func (m *LANManager) Stop(ctx context.Context) error {
	m.transitionMu.Lock()
	defer m.transitionMu.Unlock()
	m.mu.Lock()
	srv, ln, cancel := m.srv, m.ln, m.cancel
	m.mu.Unlock()
	if srv == nil {
		return nil
	}
	cancel()
	err := srv.Shutdown(ctx)
	if err != nil {
		err = errors.Join(err, srv.Close())
	}
	err = errors.Join(err, ln.Close())
	err = errors.Join(err, ln.closeConnections(""))
	m.mu.Lock()
	m.srv, m.ln, m.cancel, m.bound, m.bindHost = nil, nil, nil, 0, ""
	m.mu.Unlock()
	return err
}

func (m *LANManager) dispatch() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == lanTunnelPath {
			m.tunnel.ServeHTTP(w, r)
			return
		}
		m.handler.ServeHTTP(w, r)
	})
}

type lanListener struct {
	net.Listener
	mu        sync.Mutex
	conns     map[*lanConn]struct{}
	closed    bool
	closeOnce sync.Once
	closeErr  error
}

func (l *lanListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		_ = conn.Close()
		return nil, net.ErrClosed
	}
	tracked := &lanConn{Conn: conn, owner: l}
	l.conns[tracked] = struct{}{}
	return tracked, nil
}

func (l *lanListener) Close() error {
	l.closeOnce.Do(func() {
		l.mu.Lock()
		l.closed = true
		l.mu.Unlock()
		l.closeErr = l.Listener.Close()
	})
	return l.closeErr
}

func (l *lanListener) closeConnections(tokenHash string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	var errs []error
	for conn := range l.conns {
		if tokenHash != "" && conn.tokenHash != tokenHash {
			continue
		}
		if err := conn.Conn.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			errs = append(errs, err)
		}
		delete(l.conns, conn)
	}
	return errors.Join(errs...)
}

type lanConnContextKey struct{}

type lanConn struct {
	net.Conn
	owner     *lanListener
	tokenHash string
}

func (c *lanConn) ReadFrom(r io.Reader) (int64, error) { return io.Copy(c.Conn, r) }

func (c *lanConn) CloseWrite() error {
	if conn, ok := c.Conn.(interface{ CloseWrite() error }); ok {
		return conn.CloseWrite()
	}
	return nil
}

func (c *lanConn) Close() error {
	err := c.Conn.Close()
	c.owner.mu.Lock()
	delete(c.owner.conns, c)
	c.owner.mu.Unlock()
	return err
}

func (m *LANManager) Running() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.srv != nil
}

func (m *LANManager) BoundPort() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.bound
}
