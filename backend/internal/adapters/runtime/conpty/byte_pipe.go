package conpty

import (
	"io"
	"sync"
)

// maxPipeBuffered bounds output buffered between the host connection and the
// attach stream's reader; past it the writer waits, so back-pressure still
// reaches the pty-host.
const maxPipeBuffered = 1 << 20

// bytePipe is an in-memory pipe whose Read returns all output buffered so far
// (up to len(p)). io.Pipe hands the reader one Write at a time, so every small
// pty-host frame became its own read and, downstream, its own WebSocket
// message; under a burst that is thousands of tiny messages.
type bytePipe struct {
	mu      sync.Mutex
	changed *sync.Cond
	buf     []byte
	err     error // set once closed; Read returns it after the buffer drains
}

func newBytePipe() *bytePipe {
	p := &bytePipe{}
	p.changed = sync.NewCond(&p.mu)
	return p
}

func (p *bytePipe) Write(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for p.err == nil && len(p.buf) >= maxPipeBuffered {
		p.changed.Wait()
	}
	if p.err != nil {
		return 0, io.ErrClosedPipe
	}
	p.buf = append(p.buf, b...)
	p.changed.Broadcast()
	return len(b), nil
}

func (p *bytePipe) Read(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for len(p.buf) == 0 && p.err == nil {
		p.changed.Wait()
	}
	if len(p.buf) == 0 {
		return 0, p.err
	}
	n := copy(b, p.buf)
	p.buf = p.buf[n:]
	if len(p.buf) == 0 {
		p.buf = nil
	}
	p.changed.Broadcast()
	return n, nil
}

// CloseWithError ends the pipe; buffered output is still delivered before err.
func (p *bytePipe) CloseWithError(err error) error {
	if err == nil {
		err = io.EOF
	}
	p.mu.Lock()
	if p.err == nil {
		p.err = err
	}
	p.changed.Broadcast()
	p.mu.Unlock()
	return nil
}

func (p *bytePipe) Close() error { return p.CloseWithError(io.EOF) }
