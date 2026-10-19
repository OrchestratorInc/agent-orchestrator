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
		if len(entry.text) > MaxRingLineBytes {
			t.Fatalf("entry %d is %d bytes, want <= %d", i, len(entry.text), MaxRingLineBytes)
		}
	}
	if len(r.lines) < 3 {
		t.Fatalf("oversized partial stored as %d entries, want it split", len(r.lines))
	}
	if len(r.partialLine) > MaxRingLineBytes {
		t.Fatalf("partialLine is %d bytes, want <= %d", len(r.partialLine), MaxRingLineBytes)
	}
}

// TestRingTailReassemblesAChunkedCompletedLine: a completed logical line that
// was stored as several MaxRingLineBytes chunks must come back whole from
// Tail/Snapshot, not as its last chunk.
func TestRingTailReassemblesAChunkedCompletedLine(t *testing.T) {
	r := NewRing()
	blob := bytes.Repeat([]byte("L"), 130<<10) // two 64 KiB chunks plus a 2 KiB remainder
	r.Append(blob)                             // no newline: forces chunked storage
	r.Append([]byte("\n"))                     // completes the logical line

	want := string(blob) + "\n"
	tail := r.Tail(1)
	t.Logf("len(Tail(1)) = %d, want %d", len(tail), len(want))
	if tail != want {
		t.Fatalf("Tail(1) = %d bytes, want the whole %d-byte line", len(tail), len(want))
	}
	if len(want) != 133121 {
		t.Fatalf("test setup: want is %d bytes, want 133121", len(want))
	}
	if got := string(r.Snapshot()); got != want {
		t.Fatalf("Snapshot = %d bytes, want the completed line", len(got))
	}
	if got := string(r.Replay()); got != want {
		t.Fatalf("Replay changed by chunking: %d bytes, want %d", len(got), len(want))
	}
}

// TestRingSnapshotExcludesAChunkedPartialLine: the in-progress, newline-free
// line must stay out of Snapshot and out of the logical tail even when it is
// stored as several chunks.
func TestRingSnapshotExcludesAChunkedPartialLine(t *testing.T) {
	r := NewRing()
	r.Append([]byte("start\n"))
	blob := bytes.Repeat([]byte("p"), 3*MaxRingLineBytes+17)
	r.Append(blob)

	if got := string(r.Snapshot()); got != "start\n" {
		t.Fatalf("Snapshot = %d bytes, want the completed line only", len(got))
	}
	if got := r.Tail(1); got != "start\n" {
		t.Fatalf("Tail(1) = %d bytes, want the last completed line", len(got))
	}
	if got := r.Tail(2); got != "start\n" {
		t.Fatalf("Tail(2) = %d bytes, want the last completed line", len(got))
	}
	want := "start\n" + string(blob)
	if got := string(r.Replay()); got != want {
		t.Fatalf("Replay = %d bytes, want %d", len(got), len(want))
	}
}

// TestRingTailCountsLogicalLinesAcrossChunks: Tail's n counts completed
// logical lines even when each is stored as multiple chunks. Each long line is
// appended newline-free and completed separately so the chunking path runs.
func TestRingTailCountsLogicalLinesAcrossChunks(t *testing.T) {
	r := NewRing()
	longLine := func(fill byte) string {
		blob := bytes.Repeat([]byte{fill}, 130<<10)
		r.Append(blob)         // no newline: forces chunked storage
		r.Append([]byte("\n")) // completes the logical line
		return string(blob) + "\n"
	}
	first := longLine('1')
	second := longLine('2')
	r.Append([]byte("short\n"))

	if got := r.Tail(1); got != "short\n" {
		t.Fatalf("Tail(1) = %d bytes, want %q", len(got), "short\n")
	}
	if got, want := r.Tail(2), second+"short\n"; got != want {
		t.Fatalf("Tail(2) = %d bytes, want %d", len(got), len(want))
	}
	if got, want := r.Tail(10), first+second+"short\n"; got != want {
		t.Fatalf("Tail(10) = %d bytes, want %d", len(got), len(want))
	}
}

// TestRingFlushPartialCompletesAChunkedLine: flushing a newline-free chunked
// line marks it completed, so the whole line enters Snapshot/Tail, and a
// second flush stays a no-op.
func TestRingFlushPartialCompletesAChunkedLine(t *testing.T) {
	r := NewRing()
	blob := bytes.Repeat([]byte("f"), 130<<10)
	r.Append(blob)
	r.FlushPartial()

	want := string(blob)
	if got := string(r.Snapshot()); got != want {
		t.Fatalf("Snapshot = %d bytes, want the whole flushed line", len(got))
	}
	if got := r.Tail(1); got != want {
		t.Fatalf("Tail(1) = %d bytes, want the whole flushed line", len(got))
	}
	r.FlushPartial() // no-op
	if got := string(r.Snapshot()); got != want {
		t.Fatalf("second FlushPartial changed Snapshot: %d bytes", len(got))
	}
	if got := r.Tail(1); got != want {
		t.Fatalf("second FlushPartial changed Tail(1): %d bytes", len(got))
	}
}

// TestRingChunkBoundaryEdgeCases covers terminator placement when a chunked
// line completes via a later Append or FlushPartial, empty logical lines, a
// line whose length is an exact chunk multiple, and mid-line head eviction
// under the byte cap.
func TestRingChunkBoundaryEdgeCases(t *testing.T) {
	t.Run("later newlines complete the chunked line then start new ones", func(t *testing.T) {
		r := NewRing()
		blob := bytes.Repeat([]byte("x"), MaxRingLineBytes+5)
		r.Append(blob)
		r.Append([]byte("tail\n\nlast\n"))

		whole := string(blob) + "tail\n\nlast\n"
		if got := string(r.Snapshot()); got != whole {
			t.Fatalf("Snapshot = %d bytes, want %d", len(got), len(whole))
		}
		if got, want := r.Tail(1), "last\n"; got != want {
			t.Fatalf("Tail(1) = %q, want %q", got, want)
		}
		if got, want := r.Tail(2), "\nlast\n"; got != want {
			t.Fatalf("Tail(2) = %q, want %q", got, want)
		}
		if got := r.Tail(3); got != whole {
			t.Fatalf("Tail(3) = %d bytes, want the whole stream", len(got))
		}
		if got := string(r.Replay()); got != whole {
			t.Fatalf("Replay = %d bytes, want %d", len(got), len(whole))
		}
	})

	t.Run("empty line is its own logical line", func(t *testing.T) {
		r := NewRing()
		r.Append([]byte("a\n"))
		r.Append([]byte("\n"))
		r.Append([]byte("b\n"))

		if got, want := r.Tail(1), "b\n"; got != want {
			t.Fatalf("Tail(1) = %q, want %q", got, want)
		}
		if got, want := r.Tail(2), "\nb\n"; got != want {
			t.Fatalf("Tail(2) = %q, want %q", got, want)
		}
		if got, want := string(r.Snapshot()), "a\n\nb\n"; got != want {
			t.Fatalf("Snapshot = %q, want %q", got, want)
		}
	})

	t.Run("exact chunk multiple completed by a newline append", func(t *testing.T) {
		r := NewRing()
		r.Append([]byte("first\n"))
		blob := bytes.Repeat([]byte("m"), MaxRingLineBytes) // exactly one chunk
		r.Append(blob)
		r.Append([]byte("\n"))

		want := "first\n" + string(blob) + "\n"
		if got := string(r.Snapshot()); got != want {
			t.Fatalf("Snapshot = %d bytes, want %d", len(got), len(want))
		}
		if got, want := r.Tail(1), string(blob)+"\n"; got != want {
			t.Fatalf("Tail(1) = %d bytes, want the completed chunked line", len(got))
		}
	})

	t.Run("flush marks a boundary chunk as the line end", func(t *testing.T) {
		r := NewRing()
		blob := bytes.Repeat([]byte("m"), MaxRingLineBytes) // exactly one chunk
		r.Append(blob)
		r.FlushPartial() // partialLine is empty; the trailing chunk ends the line
		r.Append([]byte("after\n"))

		if got, want := r.Tail(1), "after\n"; got != want {
			t.Fatalf("Tail(1) = %q, want %q", got, want)
		}
		if got, want := r.Tail(2), string(blob)+"after\n"; got != want {
			t.Fatalf("Tail(2) = %d bytes, want the flushed line plus after", len(got))
		}
		r.FlushPartial() // no-op
		if got, want := string(r.Snapshot()), string(blob)+"after\n"; got != want {
			t.Fatalf("second FlushPartial changed Snapshot: %d bytes", len(got))
		}
	})

	t.Run("mid-line head eviction keeps a bounded retained suffix", func(t *testing.T) {
		r := NewRing()
		// 4 MiB fills the byte cap exactly; the extra bytes evict the oldest
		// 64 KiB chunk, leaving a truncated head of one logical line.
		r.Append(bytes.Repeat([]byte("a"), MaxRingBytes))
		r.Append(bytes.Repeat([]byte("b"), 32<<10))
		r.Append([]byte("\n"))

		want := strings.Repeat("a", 63*MaxRingLineBytes) + strings.Repeat("b", 32<<10) + "\n"
		tail := r.Tail(1)
		if tail != want {
			t.Fatalf("Tail(1) = %d bytes, want the retained %d-byte suffix", len(tail), len(want))
		}
		if len(tail) > MaxRingBytes {
			t.Fatalf("Tail(1) = %d bytes, want <= the %d-byte cap", len(tail), MaxRingBytes)
		}
		if got := string(r.Snapshot()); got != want {
			t.Fatalf("Snapshot = %d bytes, want the retained suffix", len(got))
		}
	})
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
	for _, entry := range r.lines {
		total += len(entry.text)
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
