package conpty

import (
	"io"
	"sync"
)

const maxPipeBuffered = 1 << 20

type bytePipe struct {
	mu      sync.Mutex
	changed *sync.Cond
	buf     []byte
	err     error
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
