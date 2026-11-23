package conpty

import (
	"bytes"
	"strings"
	"sync"
)

// MaxOutputLines is the rolling line-buffer cap, matching MAX_OUTPUT_LINES in pty-host.ts.
const MaxOutputLines = 1000

// MaxOutputBytes caps the bytes a Ring keeps, whatever its line cap.
const MaxOutputBytes = 1 << 20

// partialKeepCap is the largest partialLine buffer kept for reuse after a newline.
const partialKeepCap = 64 << 10

// Ring is a bounded rolling buffer of terminal output lines, ANSI codes preserved.
// It mirrors the appendOutput state machine from pty-host.ts.
// Concurrent Append and Snapshot/Tail calls are safe.
type Ring struct {
	mu          sync.Mutex
	lines       []string // each entry is "line\n" (or bare text on FlushPartial)
	size        int
	maxLines    int
	partialLine []byte // appended in place; only its last MaxOutputBytes count
}

// NewRing returns an empty Ring.
func NewRing() *Ring {
	return NewRingOfLines(MaxOutputLines)
}

// NewRingOfLines returns an empty Ring that keeps up to maxLines lines, or
// MaxOutputLines when maxLines is not positive.
func NewRingOfLines(maxLines int) *Ring {
	if maxLines <= 0 {
		maxLines = MaxOutputLines
	}
	return &Ring{maxLines: maxLines}
}

// Append mirrors appendOutput from pty-host.ts: prepend the current partialLine,
// split on newlines, store completed lines with "\n" re-appended, keep the last
// element as the new partialLine, then trim to the line cap and MaxOutputBytes.
func (r *Ring) Append(raw []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for {
		i := bytes.IndexByte(raw, '\n')
		if i < 0 {
			break
		}
		r.pushLocked(raw[:i+1])
		raw = raw[i+1:]
	}
	r.partialLine = append(r.partialLine, raw...)
	r.trimLocked()
}

// pushLocked stores partialLine, ended by end, as a completed line.
func (r *Ring) pushLocked(end []byte) {
	partial := r.partialLocked()
	var line strings.Builder
	line.Grow(len(partial) + len(end))
	line.Write(partial)
	line.Write(end)
	r.lines = append(r.lines, line.String())
	r.size += line.Len()
	if cap(r.partialLine) > partialKeepCap {
		r.partialLine = nil
	} else {
		r.partialLine = r.partialLine[:0]
	}
}

// partialLocked returns the part of partialLine the ring keeps.
func (r *Ring) partialLocked() []byte {
	if len(r.partialLine) > MaxOutputBytes {
		return r.partialLine[len(r.partialLine)-MaxOutputBytes:]
	}
	return r.partialLine
}

func (r *Ring) trimLocked() {
	// Compact only once the dropped head is as long as the kept part, so appends stay linear.
	if len(r.partialLine) >= 2*MaxOutputBytes {
		r.partialLine = append(r.partialLine[:0], r.partialLocked()...)
	}
	partial := len(r.partialLocked())
	drop := 0
	for drop < len(r.lines) && (len(r.lines)-drop > r.maxLines || r.size+partial > MaxOutputBytes) {
		r.size -= len(r.lines[drop])
		drop++
	}
	if drop > 0 {
		// Let the dropped strings be collected; the backing array outlives the slice head.
		clear(r.lines[:drop])
		// ponytail: slice off the head; ceiling: O(n) copy on every trim cycle.
		// Upgrade path: circular buffer if trim rate is very high.
		r.lines = r.lines[drop:]
	}
}

// FlushPartial pushes any in-progress partial line as a final entry.
// Called on PTY exit to mirror the pty-host.ts onExit handler.
func (r *Ring) FlushPartial() {
	r.mu.Lock()
	defer r.mu.Unlock()

	if len(r.partialLine) == 0 {
		return
	}
	r.pushLocked(nil)
	r.trimLocked()
}

// Snapshot returns all stored lines concatenated as raw bytes for scrollback replay.
// The in-progress partialLine is NOT included (matches TS outputBuffer.join("")).
func (r *Ring) Snapshot() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.joinLocked(nil)
}

// Replay returns the complete terminal byte stream retained for a newly
// attached viewer, including the current non-newline-terminated fragment.
// Full-screen TUIs commonly repaint with cursor-control sequences and carriage
// returns instead of newlines, so excluding partialLine can otherwise replay an
// empty screen even though the process has already drawn its UI.
func (r *Ring) Replay() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.joinLocked(r.partialLocked())
}

// joinLocked returns the stored lines followed by tail, in one allocation.
func (r *Ring) joinLocked(tail []byte) []byte {
	out := make([]byte, 0, r.size+len(tail))
	for _, line := range r.lines {
		out = append(out, line...)
	}
	return append(out, tail...)
}

// Tail returns the last n stored lines joined as a string.
// Mirrors the MSG_GET_OUTPUT_REQ handler: start = max(0, len-lines).
// n <= 0 returns "".
func (r *Ring) Tail(n int) string {
	r.mu.Lock()
	defer r.mu.Unlock()

	if n <= 0 {
		return ""
	}
	start := len(r.lines) - n
	if start < 0 {
		start = 0
	}
	return strings.Join(r.lines[start:], "")
}
