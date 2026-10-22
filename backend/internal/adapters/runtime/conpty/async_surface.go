package conpty

import "sync"

// maxSurfaceBacklog bounds output queued for the rendered surface. The
// emulator parses scrolling output far slower than a PTY can produce it, so a
// sustained burst can outrun it; past this backlog the queue is dropped and the
// surface is rebuilt from the ring on its next read.
const maxSurfaceBacklog = 8 << 20

// surfaceSink is the rendered-surface interface the host drives.
type surfaceSink interface {
	Write(p []byte)
	Resize(cols, rows int)
	Tail(lines int) string
}

// surfaceOp is one queued surface update: output bytes, or a resize when data
// is nil.
type surfaceOp struct {
	data       []byte
	cols, rows int
}

// asyncSurface applies PTY output to the rendered surface on its own goroutine,
// in the order it was produced, so the PTY read loop never waits on the
// emulator. The emulator is the slow part of the output path (scrolling output
// parses at well under a megabyte a second), and running it inline stalled the
// read loop: the PTY buffer filled and the program writing the output was
// paused until the emulator caught up.
//
// Reads stay exact: Tail waits until every queued update has been applied. If
// the backlog ever exceeds maxSurfaceBacklog, the queue is dropped and the next
// Tail rebuilds the surface from the ring's retained output, which is what a
// newly attached viewer would show.
//
// A lazy surface starts in that rebuild-on-read state, so a terminal nothing
// probes for styled output never runs the emulator at all.
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

// Write queues output for the surface without waiting for it to be applied.
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

// Resize queues a grid change in order with the output around it.
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

// Tail returns the surface's last lines once all queued output is applied.
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

// stop ends the worker; queued output is discarded.
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
