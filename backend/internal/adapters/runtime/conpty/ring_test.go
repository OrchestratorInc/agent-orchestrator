package conpty

import (
	"bytes"
	"math/rand/v2"
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
	for i := 0; i < MaxOutputLines+5; i++ {
		r.Append([]byte("x\n"))
	}

	snap := r.Snapshot()
	got := strings.Count(string(snap), "\n")
	if got != MaxOutputLines {
		t.Errorf("stored %d lines, want %d", got, MaxOutputLines)
	}
	r = NewRingOfLines(5000)
	for i := 0; i < 5005; i++ {
		r.Append([]byte("x\n"))
	}
	if got := strings.Count(string(r.Snapshot()), "\n"); got != 5000 {
		t.Errorf("ring of 5000 lines stored %d", got)
	}
}

func TestRingTrimsToMaxOutputBytes(t *testing.T) {
	r := NewRingOfLines(5000)
	line := strings.Repeat("y", 1023) + "\n"
	for i := 0; i < 2000; i++ {
		r.Append([]byte(line))
	}
	r.Append([]byte("last\n"))

	snap := string(r.Snapshot())
	if len(snap) > MaxOutputBytes {
		t.Fatalf("stored %d bytes, want at most %d", len(snap), MaxOutputBytes)
	}
	if len(snap) < MaxOutputBytes-len(line) {
		t.Fatalf("stored %d bytes, trimmed more than needed", len(snap))
	}
	if !strings.HasSuffix(snap, "last\n") {
		t.Fatalf("newest line dropped: %q", snap[len(snap)-20:])
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

func TestRingBoundsNewlineFreeOutput(t *testing.T) {
	r := NewRing()
	chunk := strings.Repeat("a", MaxOutputBytes)
	r.Append([]byte("line\n"))
	r.Append([]byte(chunk))
	r.Append([]byte(strings.Repeat("b", MaxOutputBytes/2)))
	got := r.Replay()
	if len(got) > MaxOutputBytes {
		t.Fatalf("replay = %d bytes, want at most %d", len(got), MaxOutputBytes)
	}
	if !strings.HasSuffix(string(got), strings.Repeat("b", MaxOutputBytes/2)) {
		t.Fatal("replay lost the newest output")
	}
}

// refRing is the string-based Ring this package used before the partial line
// became an in-place buffer; TestRingMatchesReference checks the two agree.
type refRing struct {
	lines       []string
	size        int
	maxLines    int
	partialLine string
}

func (r *refRing) append(raw []byte) {
	parts := strings.Split(r.partialLine+string(raw), "\n")
	r.partialLine = parts[len(parts)-1]
	for _, line := range parts[:len(parts)-1] {
		r.lines = append(r.lines, line+"\n")
		r.size += len(line) + 1
	}
	r.trim()
}

func (r *refRing) trim() {
	drop := 0
	for drop < len(r.lines) && (len(r.lines)-drop > r.maxLines || r.size+len(r.partialLine) > MaxOutputBytes) {
		r.size -= len(r.lines[drop])
		drop++
	}
	r.lines = r.lines[drop:]
	if len(r.partialLine) > MaxOutputBytes {
		r.partialLine = r.partialLine[len(r.partialLine)-MaxOutputBytes:]
	}
}

func (r *refRing) flushPartial() {
	if r.partialLine == "" {
		return
	}
	r.lines = append(r.lines, r.partialLine)
	r.size += len(r.partialLine)
	r.partialLine = ""
	r.trim()
}

func TestRingMatchesReference(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	for _, maxLines := range []int{MaxOutputLines, 5000} {
		r, ref := NewRingOfLines(maxLines), &refRing{maxLines: maxLines}
		for step := 0; step < 1500; step++ {
			var chunk []byte
			switch rng.IntN(5) {
			case 0: // short lines
				chunk = []byte(strings.Repeat("ab\n", 1+rng.IntN(200)))
			case 1: // newline-free repaints, often past the byte cap
				chunk = bytes.Repeat([]byte("\x1b[H\x1b[2Kx"), 1+rng.IntN(200000))
			case 2: // a long line ending mid-chunk
				chunk = append(bytes.Repeat([]byte("y"), rng.IntN(300000)), "\ntail"...)
			case 3:
				chunk = []byte("\n")
			default: // random bytes with sparse newlines
				chunk = make([]byte, rng.IntN(32<<10))
				const alphabet = "abcd\r\x1b\n"
				for i := range chunk {
					chunk[i] = alphabet[rng.IntN(len(alphabet))]
				}
			}
			if rng.IntN(50) == 0 {
				r.FlushPartial()
				ref.flushPartial()
			} else {
				r.Append(chunk)
				ref.append(chunk)
			}
			if step%25 != 0 {
				continue
			}
			wantReplay := strings.Join(ref.lines, "") + ref.partialLine
			if got := r.Replay(); string(got) != wantReplay {
				t.Fatalf("maxLines %d step %d: Replay differs (%d bytes, want %d)", maxLines, step, len(got), len(wantReplay))
			}
			if got := r.Snapshot(); string(got) != strings.Join(ref.lines, "") {
				t.Fatalf("maxLines %d step %d: Snapshot differs", maxLines, step)
			}
			if got := r.Tail(7); got != strings.Join(ref.lines[max(0, len(ref.lines)-7):], "") {
				t.Fatalf("maxLines %d step %d: Tail differs", maxLines, step)
			}
		}
	}
}

// BenchmarkRingAppendRedraw measures a full-screen program repainting with
// cursor moves and no newline, once the partial line has reached the byte cap.
func BenchmarkRingAppendRedraw(b *testing.B) {
	chunk := []byte(strings.Repeat("\x1b[H\x1b[2Kframe text ", 4096/21))
	r := NewRing()
	for len(r.Replay()) < MaxOutputBytes {
		r.Append(chunk)
	}
	b.SetBytes(int64(len(chunk)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r.Append(chunk)
	}
}
