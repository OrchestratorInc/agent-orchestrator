package conpty

import (
	"errors"
	"fmt"
	"io"
	"math"
	"sync"
)

const maxDeferredInput = 64 * 1024

var errDeferredInputFull = errors.New("deferred pty: input buffer full before the process started")

type deferredPTY struct {
	start func(cols, rows uint16) (ptyConn, error)

	mu      sync.Mutex
	conn    ptyConn
	pending []byte
	closed  bool
	failed  bool

	startedOnce sync.Once
	startedC    chan struct{}
	doneOnce    sync.Once
	doneC       chan struct{}
}

func newDeferredPTY(start func(cols, rows uint16) (ptyConn, error)) *deferredPTY {
	return &deferredPTY{start: start, startedC: make(chan struct{}), doneC: make(chan struct{})}
}

func (d *deferredPTY) markStarted() { d.startedOnce.Do(func() { close(d.startedC) }) }
func (d *deferredPTY) markDone()    { d.doneOnce.Do(func() { close(d.doneC) }) }

func (d *deferredPTY) Read(b []byte) (int, error) {
	<-d.startedC
	d.mu.Lock()
	conn := d.conn
	d.mu.Unlock()
	if conn == nil {
		return 0, io.EOF
	}
	return conn.Read(b)
}

func (d *deferredPTY) Write(b []byte) (int, error) {
	d.mu.Lock()
	conn := d.conn
	if conn == nil {
		defer d.mu.Unlock()
		if d.closed || d.failed {
			return 0, io.ErrClosedPipe
		}
		n := min(len(b), maxDeferredInput-len(d.pending))
		d.pending = append(d.pending, b[:n]...)
		if n < len(b) {
			return n, errDeferredInputFull
		}
		return n, nil
	}
	d.mu.Unlock()
	return conn.Write(b)
}

func (d *deferredPTY) Resize(cols, rows int) error {
	d.mu.Lock()
	if conn := d.conn; conn != nil {
		d.mu.Unlock()
		return conn.Resize(cols, rows)
	}
	defer d.mu.Unlock()
	if d.closed || d.failed {
		return nil
	}
	if cols <= 0 || rows <= 0 || cols > math.MaxUint16 || rows > math.MaxUint16 {
		return fmt.Errorf("deferred pty: invalid size %dx%d", cols, rows)
	}
	conn, err := d.start(uint16(cols), uint16(rows))
	if err != nil {
		d.failed = true
		d.pending = nil
		d.markStarted()
		d.markDone()
		return err
	}
	d.conn = conn
	if len(d.pending) > 0 {
		_, _ = conn.Write(d.pending)
		d.pending = nil
	}
	d.markStarted()
	go func() {
		<-conn.Done()
		d.markDone()
	}()
	return nil
}

func (d *deferredPTY) Close() error {
	d.mu.Lock()
	if conn := d.conn; conn != nil {
		d.mu.Unlock()
		return conn.Close()
	}
	d.closed = true
	d.pending = nil
	d.mu.Unlock()
	d.markStarted()
	d.markDone()
	return nil
}

func (d *deferredPTY) Done() <-chan struct{} { return d.doneC }

func (d *deferredPTY) ExitCode() (int, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	switch {
	case d.conn != nil:
		return d.conn.ExitCode()
	case d.failed:
		return -1, true
	case d.closed:
		return 0, true
	default:
		return 0, false
	}
}

func (d *deferredPTY) PID() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.conn == nil {
		return 0
	}
	return d.conn.PID()
}

func (d *deferredPTY) ForegroundPGID() (int, bool) {
	d.mu.Lock()
	conn := d.conn
	d.mu.Unlock()
	reader, ok := conn.(foregroundReader)
	if !ok {
		return 0, false
	}
	return reader.ForegroundPGID()
}
