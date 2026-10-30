package conpty

import (
	"strings"
	"sync"
	"testing"
	"time"
)

// recordingSurface records what reaches it; release gates each Write so tests
// can model an emulator slower than the PTY.
type recordingSurface struct {
	mu      sync.Mutex
	ops     []string
	release chan struct{}
}

func (r *recordingSurface) Write(p []byte) {
	if r.release != nil {
		<-r.release
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ops = append(r.ops, "w:"+string(p))
}

func (r *recordingSurface) Resize(cols, rows int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ops = append(r.ops, "r:"+itoa(cols)+"x"+itoa(rows))
}

func (r *recordingSurface) Tail(int) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.Join(r.ops, ",")
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}

// The PTY read loop hands output to the surface and moves on; a slow emulator
// must not hold it, or the program writing the output is paused.
func TestAsyncSurfaceWriteDoesNotWaitForTheEmulator(t *testing.T) {
	blocked := &recordingSurface{release: make(chan struct{})}
	s := newAsyncSurface(80, 24, func(int, int) surfaceSink { return blocked }, func() []byte { return nil }, false)
	defer s.stop()

	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			s.Write([]byte("x"))
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Write blocked on the emulator")
	}
	close(blocked.release)
}

// Reads stay exact: they see every queued update, in order, resizes included.
func TestAsyncSurfaceTailWaitsForQueuedOutputInOrder(t *testing.T) {
	slow := &recordingSurface{release: make(chan struct{})}
	s := newAsyncSurface(80, 24, func(int, int) surfaceSink { return slow }, func() []byte { return nil }, false)
	defer s.stop()

	s.Write([]byte("a"))
	s.Resize(100, 30)
	s.Write([]byte("b"))

	tail := make(chan string, 1)
	go func() { tail <- s.Tail(10) }()
	select {
	case got := <-tail:
		t.Fatalf("Tail returned %q before the queued output was applied", got)
	case <-time.After(50 * time.Millisecond):
	}
	close(slow.release)
	select {
	case got := <-tail:
		if got != "w:a,r:100x30,w:b" {
			t.Fatalf("Tail = %q, want the updates in order", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Tail did not return after the queue drained")
	}
}

// A backlog the emulator cannot catch up with is dropped; the next read rebuilds
// the surface from the ring's retained output at the current grid.
func TestAsyncSurfaceRebuildsFromTheRingWhenTheBacklogOverflows(t *testing.T) {
	first := &recordingSurface{release: make(chan struct{})}
	var rebuilt *recordingSurface
	var mu sync.Mutex
	newSurface := func(cols, rows int) surfaceSink {
		mu.Lock()
		defer mu.Unlock()
		if rebuilt == nil && cols == 80 {
			return first
		}
		rebuilt = &recordingSurface{}
		rebuilt.Resize(cols, rows)
		return rebuilt
	}
	s := newAsyncSurface(80, 24, newSurface, func() []byte { return []byte("retained") }, false)
	defer s.stop()

	s.Write([]byte("held")) // the worker takes this and blocks in the emulator
	for !s.workerHolds() {
		time.Sleep(time.Millisecond)
	}
	s.Write(make([]byte, maxSurfaceBacklog/2))
	s.Write(make([]byte, maxSurfaceBacklog/2))
	s.Write([]byte("overflow"))
	s.Resize(120, 40)
	close(first.release)

	if got := s.Tail(10); got != "r:120x40,w:retained" {
		t.Fatalf("Tail after overflow = %.80q, want a surface rebuilt from the ring at 120x40", got)
	}
	s.Write([]byte("more"))
	if got := s.Tail(10); got != "r:120x40,w:retained,w:more" {
		t.Fatalf("Tail after rebuild = %.80q, want later output applied to the rebuilt surface", got)
	}
}

func (s *asyncSurface) workerHolds() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.applying
}

// A terminal nothing probes for styled output must not run the emulator: a lazy
// surface builds itself from the ring only when first read, then stays current.
func TestLazyAsyncSurfaceBuildsOnFirstRead(t *testing.T) {
	built := 0
	var surface *recordingSurface
	newSurface := func(cols, rows int) surfaceSink {
		built++
		surface = &recordingSurface{}
		surface.Resize(cols, rows)
		return surface
	}
	s := newAsyncSurface(80, 24, newSurface, func() []byte { return []byte("retained") }, true)
	defer s.stop()

	s.Write([]byte("unread output"))
	s.Resize(100, 30)
	if built != 0 {
		t.Fatalf("lazy surface built %d emulators before any read", built)
	}
	if got := s.Tail(10); got != "r:100x30,w:retained" {
		t.Fatalf("first Tail = %q, want a surface built from the ring at the current grid", got)
	}
	s.Write([]byte("live"))
	if got := s.Tail(10); got != "r:100x30,w:retained,w:live" || built != 1 {
		t.Fatalf("second Tail = %q (built %d), want later output applied to the same surface", got, built)
	}
}

// The real emulator still renders the same screen through the async path.
func TestAsyncSurfaceRendersRealOutput(t *testing.T) {
	s := newAsyncSurface(40, 5, func(cols, rows int) surfaceSink { return newRenderedSurface(cols, rows) }, func() []byte { return nil }, false)
	defer s.stop()
	for i := 1; i <= 20; i++ {
		s.Write([]byte("line " + itoa(i) + "\r\n"))
	}
	s.Write([]byte("prompt %"))
	got := s.Tail(2)
	if !strings.Contains(got, "line 20") || !strings.Contains(got, "prompt %") {
		t.Fatalf("Tail = %q, want the last screen lines", got)
	}
}
