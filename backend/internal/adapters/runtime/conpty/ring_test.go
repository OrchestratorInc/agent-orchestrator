package conpty

import (
	"bytes"
	"strings"
	"sync"
	"testing"
)

// TestRingAppendPartialThenComplete verifies partial-line accumulation and
// that Snapshot/Tail reflect only completed lines.
func TestRingAppendPartialThenComplete(t *testing.T) {
	r := NewRing()
	r.Append([]byte("hel"))
	r.Append([]byte("lo\nwor"))

	snap := string(r.Snapshot())
	if snap != "hello\n" {
		t.Errorf("Snapshot = %q, want %q", snap, "hello\n")
	}

	tail := r.Tail(10)
	if tail != "hello\n" {
		t.Errorf("Tail(10) = %q, want %q", tail, "hello\n")
	}

	// Flush the partial "wor"
	r.FlushPartial()
	snap = string(r.Snapshot())
	if snap != "hello\nwor" {
		t.Errorf("after FlushPartial Snapshot = %q, want %q", snap, "hello\nwor")
	}
}

// TestRingExceedsMaxOutputLines verifies the buffer trims to MaxOutputLines.
func TestRingExceedsMaxOutputLines(t *testing.T) {
	r := NewRing()
	// Push 1005 lines.
	for i := 0; i < 1005; i++ {
		r.Append([]byte("x\n"))
	}

	snap := r.Snapshot()
	got := strings.Count(string(snap), "\n")
	if got != MaxOutputLines {
		t.Errorf("stored %d lines, want %d", got, MaxOutputLines)
	}
}

// TestRingFlushPartialNoNewline verifies FlushPartial pushes a trailing line.
func TestRingFlushPartialNoNewline(t *testing.T) {
	r := NewRing()
	r.Append([]byte("line1\npartial"))
	r.FlushPartial()

	snap := string(r.Snapshot())
	if !strings.Contains(snap, "partial") {
		t.Errorf("Snapshot missing 'partial': %q", snap)
	}

	// Calling FlushPartial again is a no-op.
	r.FlushPartial()
	snap2 := string(r.Snapshot())
	if snap2 != snap {
		t.Errorf("second FlushPartial changed snapshot: %q -> %q", snap, snap2)
	}
}

// TestRingTailEdgeCases covers n > stored count and n <= 0.
func TestRingTailEdgeCases(t *testing.T) {
	r := NewRing()
	r.Append([]byte("a\nb\n"))

	if got := r.Tail(100); got != "a\nb\n" {
		t.Errorf("Tail(100) = %q, want %q", got, "a\nb\n")
	}
	if got := r.Tail(0); got != "" {
		t.Errorf("Tail(0) = %q, want empty", got)
	}
	if got := r.Tail(-1); got != "" {
		t.Errorf("Tail(-1) = %q, want empty", got)
	}
}

// TestRingANSIRoundTrip verifies raw ANSI escape sequences survive storage intact.
func TestRingANSIRoundTrip(t *testing.T) {
	ansi := "\x1b[31mhi\x1b[0m\n"
	r := NewRing()
	r.Append([]byte(ansi))

	snap := string(r.Snapshot())
	if snap != ansi {
		t.Errorf("Snapshot = %q, want %q", snap, ansi)
	}
	tail := r.Tail(1)
	if tail != ansi {
		t.Errorf("Tail(1) = %q, want %q", tail, ansi)
	}
}

// TestRingTailSubset verifies Tail returns exactly the last n lines.
func TestRingTailSubset(t *testing.T) {
	r := NewRing()
	for i := 0; i < 10; i++ {
		r.Append([]byte("line\n"))
	}

	tail3 := r.Tail(3)
	if got := strings.Count(tail3, "\n"); got != 3 {
		t.Errorf("Tail(3) contains %d newlines, want 3", got)
	}
}

// TestRingSnapshotExcludesPartial verifies the in-progress partial line is NOT
// included in Snapshot (matches TS semantics: only outputBuffer, not partialLine).
func TestRingSnapshotExcludesPartial(t *testing.T) {
	r := NewRing()
	r.Append([]byte("complete\npartial"))

	snap := string(r.Snapshot())
	if strings.Contains(snap, "partial") {
		t.Errorf("Snapshot includes partial line: %q", snap)
	}
	if !strings.Contains(snap, "complete\n") {
		t.Errorf("Snapshot missing complete line: %q", snap)
	}
}

func TestRingReplayIncludesPartialTUIOutput(t *testing.T) {
	r := NewRing()
	r.Append([]byte("complete\n\x1b[?1049h\rcurrent tui"))

	if got, want := string(r.Replay()), "complete\n\x1b[?1049h\rcurrent tui"; got != want {
		t.Errorf("Replay = %q, want %q", got, want)
	}
}

// TestRingByteCapEvictsOldestAndKeepsTail: a newline-free repaint cannot grow
// the ring past MaxRingBytes; eviction drops the oldest bytes and keeps the
// newest, which is what a viewer replays and a probe reads.
func TestRingByteCapEvictsOldestAndKeepsTail(t *testing.T) {
	r := NewRing()
	const chunkBytes = 512 << 10
	for i := 0; i < 10; i++ { // 5 MiB in total, past the 4 MiB cap
		r.Append(bytes.Repeat([]byte{byte('a' + i)}, chunkBytes))
	}

	got := r.Replay()
	if len(got) > MaxRingBytes {
		t.Fatalf("Replay = %d bytes, want <= the %d-byte cap", len(got), MaxRingBytes)
	}
	if len(got) <= MaxRingBytes-chunkBytes {
		t.Fatalf("Replay = %d bytes, want the cap to be used", len(got))
	}
	if !bytes.HasSuffix(got, bytes.Repeat([]byte("j"), chunkBytes)) {
		t.Fatal("the newest chunk was evicted")
	}
	if bytes.Contains(got, bytes.Repeat([]byte("a"), chunkBytes)) {
		t.Fatal("the oldest chunk survived the byte cap")
	}
}

// TestRingSplitsAnOversizedPartialLine: a newline-free repaint is stored as
// MaxRingLineBytes-sized entries; every entry is bounded and the joined stream
// is unchanged.
func TestRingSplitsAnOversizedPartialLine(t *testing.T) {
	r := NewRing()
	r.Append([]byte("start\n"))
	blob := bytes.Repeat([]byte("y"), MaxRingLineBytes*3+17)
	r.Append(blob)

	want := "start\n" + string(blob)
	if got := string(r.Replay()); got != want {
		t.Fatalf("Replay changed by the split: %d bytes, want %d", len(got), len(want))
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, entry := range r.lines {
		if len(entry) > MaxRingLineBytes {
			t.Fatalf("entry %d is %d bytes, want <= %d", i, len(entry), MaxRingLineBytes)
		}
	}
	if len(r.lines) < 3 {
		t.Fatalf("oversized partial stored as %d entries, want it split", len(r.lines))
	}
	if len(r.partialLine) > MaxRingLineBytes {
		t.Fatalf("partialLine is %d bytes, want <= %d", len(r.partialLine), MaxRingLineBytes)
	}
}

// TestRingByteAccountingMatchesContent verifies the internal byte counter used
// by eviction always equals the retained content's length.
func TestRingByteAccountingMatchesContent(t *testing.T) {
	r := NewRing()
	r.Append([]byte("a\n"))
	r.Append(bytes.Repeat([]byte("b"), MaxRingLineBytes+1)) // forces a split
	r.Append([]byte("c\n"))
	r.FlushPartial()

	r.mu.Lock()
	defer r.mu.Unlock()
	total := len(r.partialLine)
	for _, line := range r.lines {
		total += len(line)
	}
	if r.bytes != total {
		t.Fatalf("bytes = %d, content = %d", r.bytes, total)
	}
}

// TestCapTailBytes verifies the response-tail cap used by the GetOutput
// handlers.
func TestCapTailBytes(t *testing.T) {
	short := []byte("short")
	if got := capTailBytes(short); !bytes.Equal(got, short) {
		t.Fatalf("capTailBytes(short) = %q", got)
	}
	long := bytes.Repeat([]byte("x"), MaxTailBytes+10)
	got := capTailBytes(long)
	if len(got) != MaxTailBytes || !bytes.Equal(got, long[10:]) {
		t.Fatalf("capTailBytes(long) = %d bytes, want the last %d", len(got), MaxTailBytes)
	}
}

// TestRingConcurrent validates the advertised goroutine-safety of Ring under the
// race detector. It spawns 10 writer goroutines (Append) and 10 reader goroutines
// (Snapshot + Tail) that all run concurrently; any data race will be caught by
// "go test -race". The test itself only asserts no panic and no race.
func TestRingConcurrent(t *testing.T) {
	const goroutines = 10
	const iters = 100

	r := NewRing()
	var wg sync.WaitGroup

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iters; j++ {
				r.Append([]byte("line\n"))
			}
		}()
	}

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iters; j++ {
				_ = r.Snapshot()
				_ = r.Tail(10)
			}
		}()
	}

	wg.Wait()
}
