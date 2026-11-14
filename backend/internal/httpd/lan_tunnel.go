package httpd

import (
	"context"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
)

const lanTunnelPath = "/api/v1/tunnel"

func (m *LANManager) serveTunnel(w http.ResponseWriter, r *http.Request) {
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		return
	}
	_, port, _ := net.SplitHostPort(r.RemoteAddr)
	conn := &tunnelConn{
		Conn:   websocket.NetConn(context.WithoutCancel(r.Context()), ws, websocket.MessageBinary),
		remote: tunnelAddr(net.JoinHostPort(clientSource(r), port)),
		closed: make(chan struct{}),
	}
	base, cancel := context.WithCancel(context.WithValue(context.Background(), lanConnContextKey{}, r.Context().Value(lanConnContextKey{})))
	defer cancel()
	defer context.AfterFunc(r.Context(), cancel)()
	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Header.Del("CF-Connecting-IP")
			m.handler.ServeHTTP(w, r)
		}),
		ReadHeaderTimeout: 10 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return base },
	}
	srv.Protocols = new(http.Protocols)
	srv.Protocols.SetUnencryptedHTTP2(true)
	_ = srv.Serve(&oneConnListener{conn: conn})
}

type tunnelConn struct {
	net.Conn
	remote    net.Addr
	closeOnce sync.Once
	closed    chan struct{}
}

func (c *tunnelConn) RemoteAddr() net.Addr { return c.remote }

func (c *tunnelConn) Close() error {
	err := c.Conn.Close()
	c.closeOnce.Do(func() { close(c.closed) })
	return err
}

type tunnelAddr string

func (a tunnelAddr) Network() string { return "tcp" }
func (a tunnelAddr) String() string  { return string(a) }

type oneConnListener struct {
	conn   *tunnelConn
	handed sync.Once
}

func (l *oneConnListener) Accept() (net.Conn, error) {
	var first net.Conn
	l.handed.Do(func() { first = l.conn })
	if first != nil {
		return first, nil
	}
	<-l.conn.closed
	return nil, net.ErrClosed
}

func (l *oneConnListener) Close() error   { return nil }
func (l *oneConnListener) Addr() net.Addr { return l.conn.LocalAddr() }
