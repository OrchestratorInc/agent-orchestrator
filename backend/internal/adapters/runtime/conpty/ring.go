package conpty

import (
	"strings"
	"sync"
)

// MaxOutputLines is the rolling line-buffer cap, matching MAX_OUTPUT_LINES in pty-host.ts.
const MaxOutputLines = 1000

// Byte bounds. A line count alone does not bound the retained stream: a
// full-screen TUI repaints with cursor control and few newlines, so each
// "line" grows into megabytes and a 1000-line replay can reach tens of MB.
// MaxRingBytes bounds what a viewer replays and a probe reads;
// MaxRingLineBytes splits such a repaint into entries that eviction can drop;
// MaxTailBytes caps one GetOutput/GetStyledOutput response.
const (
	MaxRingBytes     = 4 << 20
	MaxRingLineBytes = 64 << 10
	MaxTailBytes     = 128 << 10
)

// Ring is a bounded rolling buffer of terminal output lines, ANSI codes preserved.
// It mirrors the appendOutput state machine from pty-host.ts.
// Concurrent Append and Snapshot/Tail calls are safe.
type Ring struct {
	mu          sync.Mutex
	lines       []string // each entry is "line\n" (or bare text on FlushPartial)
	partialLine string
	bytes       int // len(partialLine) + sum of len(line) for line in lines
}

// NewRing returns an empty Ring.
func NewRing() *Ring {
	return &Ring{}
}

// Append mirrors appendOutput from pty-host.ts: prepend the current partialLine,
// split on newlines, store completed lines with "\n" re-appended, keep the last
// element as the new partialLine, then trim to the byte and line caps.
func (r *Ring) Append(raw []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()

	text := r.partialLine + string(raw)
	parts := strings.Split(text, "\n")
	// The last element is either "" (text ended with \n) or an incomplete line.
	r.partialLine = parts[len(parts)-1]
	for _, line := range parts[:len(parts)-1] {
		r.lines = append(r.lines, line+"\n")
	}
	// The buffer's content grows by exactly the appended bytes; the split above
	// only moves bytes between partialLine and lines.
	r.bytes += len(raw)
	// A full-screen TUI can repaint for hours without a newline, so the partial
	// line is not covered by MaxOutputLines. Split it into MaxRingLineBytes
	// entries so trimLocked's byte accounting can evict it; entries concatenate
	// in order, so the joined stream is unchanged.
	for len(r.partialLine) >= MaxRingLineBytes {
		r.lines = append(r.lines, r.partialLine[:MaxRingLineBytes])
		r.partialLine = r.partialLine[MaxRingLineBytes:]
	}
	r.trimLocked()
}

// trimLocked drops the oldest entries until the ring is within both caps. The
// newest bytes are the ones a viewer or probe came for, so eviction is always
// from the head.
func (r *Ring) trimLocked() {
	if len(r.lines) <= MaxOutputLines && r.bytes <= MaxRingBytes {
		return
	}
	drop := 0
	for drop < len(r.lines) && (len(r.lines)-drop > MaxOutputLines || r.bytes > MaxRingBytes) {
		r.bytes -= len(r.lines[drop])
		drop++
	}
	// ponytail: slice off the head; ceiling: O(n) copy on every trim cycle.
	// Upgrade path: circular buffer if trim rate is very high.
	r.lines = r.lines[drop:]
}

// capTailBytes returns the last MaxTailBytes bytes of b, so one GetOutput or
// GetStyledOutput response cannot carry a full scrollback. The cut can land
// mid-rune or mid-escape, which only affects the leading glyph of the reply.
func capTailBytes(b []byte) []byte {
	if len(b) > MaxTailBytes {
		return b[len(b)-MaxTailBytes:]
	}
	return b
}

// FlushPartial pushes any in-progress partial line as a final entry.
// Called on PTY exit to mirror the pty-host.ts onExit handler.
func (r *Ring) FlushPartial() {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.partialLine == "" {
		return
	}
	r.lines = append(r.lines, r.partialLine)
	r.partialLine = ""
	r.trimLocked()
}

// Snapshot returns all stored lines concatenated as raw bytes for scrollback replay.
// The in-progress partialLine is NOT included (matches TS outputBuffer.join("")).
func (r *Ring) Snapshot() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()

	return []byte(strings.Join(r.lines, ""))
}

// Replay returns the complete terminal byte stream retained for a newly
// attached viewer, including the current non-newline-terminated fragment.
// Full-screen TUIs commonly repaint with cursor-control sequences and carriage
// returns instead of newlines, so excluding partialLine can otherwise replay an
// empty screen even though the process has already drawn its UI.
func (r *Ring) Replay() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()

	return []byte(strings.Join(r.lines, "") + r.partialLine)
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
