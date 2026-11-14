package conpty

import "sync"

const maxSurfaceBacklog = 8 << 20

type surfaceSink interface {
	Write(p []byte)
	Resize(cols, rows int)
	Tail(lines int) string
}

type surfaceOp struct {
	data       []byte
	cols, rows int
}

type asyncSurface struct {
	newSurface func(cols, rows int) surfaceSink
	replay     func() []byte

	mu          sync.Mutex
	changed     *sync.Cond
	surface     surfaceSink
	queue       []surfaceOp
	queuedBytes int
	applying    bool
	stale       bool
	stopped     bool
	cols, rows  int
}

func newAsyncSurface(cols, rows int, newSurface func(cols, rows int) surfaceSink, replay func() []byte, lazy bool) *asyncSurface {
	s := &asyncSurface{newSurface: newSurface, replay: replay, cols: cols, rows: rows, stale: lazy}
	if !lazy {
		s.surface = newSurface(cols, rows)
	}
	s.changed = sync.NewCond(&s.mu)
	go s.run()
	return s
}

func (s *asyncSurface) Write(p []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped || s.stale {
		return
	}
	if s.queuedBytes+len(p) > maxSurfaceBacklog {
		s.queue, s.queuedBytes, s.stale = nil, 0, true
		s.changed.Broadcast()
		return
	}
	s.queue = append(s.queue, surfaceOp{data: p})
	s.queuedBytes += len(p)
	s.changed.Broadcast()
}

func (s *asyncSurface) Resize(cols, rows int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cols, s.rows = cols, rows
	if s.stopped || s.stale {
		return
	}
	s.queue = append(s.queue, surfaceOp{cols: cols, rows: rows})
	s.changed.Broadcast()
}

func (s *asyncSurface) Tail(lines int) string {
	s.mu.Lock()
	for !s.stopped && !s.stale && (len(s.queue) > 0 || s.applying) {
		s.changed.Wait()
	}
	if s.stale {
		surface := s.newSurface(s.cols, s.rows)
		surface.Write(s.replay())
		s.surface, s.stale = surface, false
	}
	surface := s.surface
	s.mu.Unlock()
	return surface.Tail(lines)
}

func (s *asyncSurface) stop() {
	s.mu.Lock()
	s.stopped = true
	s.queue, s.queuedBytes = nil, 0
	s.changed.Broadcast()
	s.mu.Unlock()
}

func (s *asyncSurface) run() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for {
		for !s.stopped && len(s.queue) == 0 {
			s.changed.Wait()
		}
		if s.stopped {
			return
		}
		ops, surface := s.queue, s.surface
		s.queue, s.queuedBytes, s.applying = nil, 0, true
		s.mu.Unlock()
		for _, op := range ops {
			if op.data == nil {
				surface.Resize(op.cols, op.rows)
			} else {
				surface.Write(op.data)
			}
		}
		s.mu.Lock()
		s.applying = false
		s.changed.Broadcast()
	}
}
