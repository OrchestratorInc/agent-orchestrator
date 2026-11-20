package persistenthost

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"sync"
	"time"
)

// controllerConn serializes complete frames and allows read receipts to renew a
// blocked write without acquiring the host mutex (which replay already holds).
// Receipts prove consumption only, never durable projection or prompt completion.
type controllerConn struct {
	net.Conn
	writeMu  sync.Mutex
	mu       sync.Mutex
	writing  bool
	closed   bool
	progress net.Conn
}

func (c *controllerConn) Write(p []byte) (int, error) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return 0, net.ErrClosed
	}
	err := c.SetWriteDeadline(time.Now().Add(clientWriteTimeout))
	c.writing = err == nil
	c.mu.Unlock()
	if err != nil {
		return 0, err
	}
	n, err := c.Conn.Write(p)
	c.mu.Lock()
	c.writing = false
	if err == nil {
		_ = c.SetWriteDeadline(time.Time{})
	}
	c.mu.Unlock()
	return n, err
}

func (c *controllerConn) Close() error {
	c.mu.Lock()
	c.closed = true
	if c.progress != nil {
		_ = c.progress.Close()
	}
	c.mu.Unlock()
	return c.Conn.Close()
}

func (c *controllerConn) serveProgress(conn net.Conn, reader *bufio.Reader) {
	defer func() { _ = conn.Close() }()
	c.mu.Lock()
	if c.closed || c.progress != nil {
		c.mu.Unlock()
		_ = json.NewEncoder(conn).Encode(helloResponse{Error: ErrUnauthorized.Error()})
		return
	}
	c.progress = conn
	c.mu.Unlock()
	_ = conn.SetWriteDeadline(time.Now().Add(handshakeTimeout))
	if err := json.NewEncoder(conn).Encode(helloResponse{OK: true}); err != nil {
		return
	}
	for {
		if _, err := reader.ReadByte(); err != nil {
			return
		}
		c.mu.Lock()
		if c.writing && !c.closed {
			_ = c.SetWriteDeadline(time.Now().Add(clientWriteTimeout))
		}
		c.mu.Unlock()
	}
}

// connectReadProgress uses the existing authenticated listener and a fresh
// per-attachment capability. A stale controller cannot renew its successor.
// Older hosts omit the optional capability and retain their existing transport.
func connectReadProgress(ctx context.Context, d Descriptor, token string) (net.Conn, error) {
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", d.Address)
	if err != nil {
		return nil, err
	}
	finish := bindConnToContext(ctx, conn)
	err = json.NewEncoder(conn).Encode(hello{Version: ProtocolVersion, Token: d.Token, Action: "progress", ProgressToken: token})
	if err == nil {
		var response helloResponse
		err = json.NewDecoder(conn).Decode(&response)
		if err == nil && !response.OK {
			err = ErrUnauthorized
		}
	}
	if err = finish(err); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return conn, nil
}

type readProgress struct {
	conn net.Conn
	once sync.Once
}

func (p *readProgress) close() { p.once.Do(func() { _ = p.conn.Close() }) }

type progressTransport struct {
	net.Conn
	signal *readProgress
}

func (p *progressTransport) Close() error {
	p.signal.close()
	return p.Conn.Close()
}

type progressReader struct {
	reader     io.Reader
	signal     *readProgress
	lastReport time.Time
}

func (r *progressReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	// No timer/heartbeat: a controller that stops reading must stop renewing.
	// Coalesce receipts to avoid a control-plane round trip per provider frame.
	if n > 0 && time.Since(r.lastReport) >= 100*time.Millisecond {
		r.lastReport = time.Now()
		_ = r.signal.conn.SetWriteDeadline(time.Now().Add(handshakeTimeout))
		if _, writeErr := r.signal.conn.Write([]byte{1}); writeErr != nil {
			r.signal.close()
		}
	}
	if err != nil {
		r.signal.close()
	}
	return n, err
}
