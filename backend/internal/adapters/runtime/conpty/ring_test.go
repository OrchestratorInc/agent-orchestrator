package conpty

import (
	"bytes"
	"fmt"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
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

func TestRingPartialLineCapNoNewline(t *testing.T) {
	const chunkBytes = 64 << 10
	// Cap the test payload to 2 MiB (32 chunks). This provides ample iterations
	// to exercise the 256 KiB truncation boundary while avoiding excessive memory
	// allocation overhead under the race detector.
	const chunks = (2 << 20) / chunkBytes
	r := NewRing()
	r.Append([]byte("complete\n"))
	want := make([]byte, MaxPartialLineBytes)
	for i := 0; i < chunks; i++ {
		chunk := bytes.Repeat([]byte{byte('a' + i%26)}, chunkBytes)
		r.Append(chunk)
		if len(r.partialLine) > MaxPartialLineBytes {
			t.Fatalf("append %d: partial line has %d bytes, cap is %d", i+1, len(r.partialLine), MaxPartialLineBytes)
		}
		if i >= chunks-len(want)/chunkBytes {
			copy(want[(i-(chunks-len(want)/chunkBytes))*chunkBytes:], chunk)
		}
	}
	if got := r.Replay(); !bytes.Equal(got, append([]byte("complete\n"), want...)) {
		t.Fatalf("Replay has %d bytes and does not match the completed line plus the true input tail", len(got))
	}
	if got := string(r.Snapshot()); got != "complete\n" {
		t.Fatalf("Snapshot = %q, want completed line only", got)
	}
}

func TestRingPartialLineCapUTF8(t *testing.T) {
	for _, runeText := range []string{"é", "界", "🙂"} {
		for cut := 1; cut < len(runeText); cut++ {
			for _, split := range []int{0, 9, MaxPartialLineBytes} {
				t.Run(fmt.Sprintf("%s/cut%d/split%d", runeText, cut, split), func(t *testing.T) {
					want := strings.Repeat("z", MaxPartialLineBytes-cut)
					input := []byte(strings.Repeat("x", 8) + runeText + want)
					r := NewRing()
					if split > 0 {
						// Split inside the rune or fill the old partial to its cap.
						// The eventual cut falls on each continuation byte.
						r.Append(input[:split])
						r.Append(input[split:])
					} else {
						r.Append(input)
					}
					if got := r.Replay(); !utf8.Valid(got) || string(got) != want {
						t.Fatalf("Replay valid UTF-8 = %t, bytes = %d, want %d-byte suffix", utf8.Valid(got), len(got), len(want))
					}
				})
			}
		}
	}
}

func TestRingPartialLineCapControlBoundary(t *testing.T) {
	for _, tc := range []struct {
		name   string
		offset int
		marker string
		trim   bool
	}{
		{"carriage return", 123, "\r", true},
		{"escape", 123, "\x1b[2J", true},
		{"earliest marker", 17, "\rfirst\x1b[2J", true},
		{"escape first", 17, "\x1b[2J\r", true},
		{"at cut", 0, "\x1b[2J", true},
		{"last searched byte", (4 << 10) - 1, "\r", true},
		{"outside search", 4 << 10, "\x1b[2J", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tail := strings.Repeat("x", tc.offset) + tc.marker
			tail += strings.Repeat("z", MaxPartialLineBytes-len(tail))
			r := NewRing()
			r.Append([]byte("discard" + tail))
			want := tail
			if tc.trim {
				want = tail[tc.offset:]
			}
			if got := string(r.Replay()); got != want {
				t.Fatalf("Replay has %d bytes, want %d-byte control-aligned suffix", len(got), len(want))
			}
		})
	}
}

func TestRingPartialLineCapPreservesCompletedLines(t *testing.T) {
	r := NewRing()
	partial := strings.Repeat("a", MaxPartialLineBytes)
	r.Append([]byte(partial))
	completed := partial + strings.Repeat("b", 64<<10) + "\n"
	tail := strings.Repeat("z", MaxPartialLineBytes)
	// Completed lines keep their existing semantics, even in a large chunk.
	r.Append([]byte(strings.Repeat("b", 64<<10) + "\n" + strings.Repeat("discard", 100000) + tail))
	if got := string(r.Snapshot()); got != completed {
		t.Fatalf("Snapshot has %d bytes, want %d-byte completed line", len(got), len(completed))
	}
	if got := string(r.Replay()); got != completed+tail {
		t.Fatalf("Replay has %d bytes, want completed line plus capped suffix", len(got))
	}
	if got := r.Tail(1); got != completed {
		t.Fatal("Tail changed completed-line content")
	}
	r.FlushPartial()
	if got := string(r.Snapshot()); got != completed+tail {
		t.Fatal("FlushPartial did not preserve the capped suffix")
	}
}

func BenchmarkRingAppendNoNewline(b *testing.B) {
	for _, mib := range []int{1, 16, 100} {
		b.Run(fmt.Sprintf("%dMiB", mib), func(b *testing.B) {
			const chunkBytes = 64 << 10
			chunk := bytes.Repeat([]byte("x"), chunkBytes)
			chunks := (mib << 20) / chunkBytes
			b.SetBytes(int64(mib << 20))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				r := NewRing()
				for j := 0; j < chunks; j++ {
					r.Append(chunk)
				}
			}
		})
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
