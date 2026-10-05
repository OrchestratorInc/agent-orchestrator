package conpty

import (
	"errors"
	"fmt"
	"io"
	"math"
	"sync"
)

// maxDeferredInput bounds input buffered before a deferred process starts.
const maxDeferredInput = 64 * 1024

// errDeferredInputFull reports input the not-yet-started process could not
// take: the pre-start buffer is full.
var errDeferredInputFull = errors.New("deferred pty: input buffer full before the process started")

// deferredPTY is a ptyConn whose process starts on the first Resize, at the
// grid the host applies for its first sized client. A shell terminal is created
// as soon as the user opens its tab, before that tab's terminal has measured
// itself; starting the shell at a guessed grid lays its first prompt out for a
// different width than the viewer shows (zsh's partial-line marker then leaks
// as a stray "%"). Starting on the first real grid gives every shell its true
// size from its first byte, with no client-side bookkeeping.
type deferredPTY struct {
	start func(cols, rows uint16) (ptyConn, error)

	mu      sync.Mutex
	conn    ptyConn // nil until started
	pending []byte  // input written before the start
	closed  bool    // closed before the process started
	failed  bool    // the process could not be started

	startedOnce sync.Once
	startedC    chan struct{} // closed once conn is set, the start failed, or it closed first
	doneOnce    sync.Once
	doneC       chan struct{} // closed when the process exits, the start fails, or it closed first
}

func newDeferredPTY(start func(cols, rows uint16) (ptyConn, error)) *deferredPTY {
	return &deferredPTY{start: start, startedC: make(chan struct{}), doneC: make(chan struct{})}
}

func (d *deferredPTY) markStarted() { d.startedOnce.Do(func() { close(d.startedC) }) }
func (d *deferredPTY) markDone()    { d.doneOnce.Do(func() { close(d.doneC) }) }

// Read blocks until the process starts, then reads its output. A process that
// never starts (closed first, or failed to start) reads as EOF.
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

// Write forwards input once the process runs and buffers it until then, so
// keystrokes typed while the terminal is still sizing itself are not lost.
// Input past the buffer's bound is reported as a short write, not accepted.
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

// Resize starts the process at the first grid it is given and resizes it after.
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

// ExitCode reports a not-yet-started process as running: the terminal exists
// and is waiting for its first viewer. A failed start reports exit code -1.
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

// PID is 0 until the process starts.
func (d *deferredPTY) PID() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.conn == nil {
		return 0
	}
	return d.conn.PID()
}
