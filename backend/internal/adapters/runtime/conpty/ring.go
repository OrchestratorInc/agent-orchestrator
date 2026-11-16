package conpty

import (
	"bytes"
	"strings"
	"sync"
)

// MaxOutputLines is the rolling line-buffer cap, matching MAX_OUTPUT_LINES in pty-host.ts.
const MaxOutputLines = 1000

// Full-screen TUIs repaint with cursor moves and carriage returns, so their
// output never reaches a newline and piles up in the partial line. Past
// partialTrimBytes everything before the latest full-screen clear is dropped:
// replay paints the same screen without it. maxPartialBytes is the hard cap for
// programs that never clear; it keeps the newest half, cut at an escape so
// replay does not start inside a sequence. Modes negotiated in the dropped
// part are re-sent by modeTracker.Restore on attach.
const (
	partialTrimBytes = 64 << 10
	maxPartialBytes  = 4 << 20
)

// screenClears end everything drawn before them: erase display and full reset.
var screenClears = [][]byte{[]byte("\x1b[2J"), []byte("\x1bc")}

// Ring is a bounded rolling buffer of terminal output lines, ANSI codes preserved.
// It mirrors the appendOutput state machine from pty-host.ts.
// Concurrent Append and Snapshot/Tail calls are safe.
type Ring struct {
	mu    sync.Mutex
	lines []string // each entry is "line\n" (or bare text on FlushPartial)
	// partial is the in-progress line. It grows by append, so a stream with no
	// newline costs O(bytes) instead of re-copying the whole line per chunk.
	partial []byte
	// lastClear is the offset in partial of the newest screen clear, or -1.
	lastClear int
}

// NewRing returns an empty Ring.
func NewRing() *Ring {
	return &Ring{lastClear: -1}
}

// Append mirrors appendOutput from pty-host.ts: prepend the current partial
// line, split on newlines, store completed lines with "\n" re-appended, keep
// the remainder as the new partial line, then trim to MaxOutputLines.
func (r *Ring) Append(raw []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for {
		i := bytes.IndexByte(raw, '\n')
		if i < 0 {
			break
		}
		r.lines = append(r.lines, string(r.partial)+string(raw[:i+1]))
		r.partial, r.lastClear = r.partial[:0], -1
		raw = raw[i+1:]
	}
	if len(raw) > 0 {
		// Rescan a few bytes before the new data so a clear split across
		// chunks is still found.
		from := max(0, len(r.partial)-3)
		r.partial = append(r.partial, raw...)
		for _, clear := range screenClears {
			if j := bytes.LastIndex(r.partial[from:], clear); j >= 0 && from+j > r.lastClear {
				r.lastClear = from + j
			}
		}
		r.trimPartial()
	}
	if len(r.lines) > MaxOutputLines {
		// ponytail: slice off the head; ceiling: O(n) copy on every trim cycle.
		// Upgrade path: circular buffer if trim rate is very high.
		r.lines = r.lines[len(r.lines)-MaxOutputLines:]
	}
}

func (r *Ring) trimPartial() {
	if len(r.partial) > partialTrimBytes && r.lastClear > 0 {
		r.partial = append(r.partial[:0], r.partial[r.lastClear:]...)
		r.lastClear = 0
	}
	if len(r.partial) <= maxPartialBytes {
		return
	}
	cut := len(r.partial) - maxPartialBytes/2
	if esc := bytes.IndexByte(r.partial[cut:], 0x1b); esc >= 0 {
		cut += esc
	}
	r.partial = append(r.partial[:0], r.partial[cut:]...)
	r.lastClear = -1
	for _, clear := range screenClears {
		r.lastClear = max(r.lastClear, bytes.LastIndex(r.partial, clear))
	}
}

// FlushPartial pushes any in-progress partial line as a final entry.
// Called on PTY exit to mirror the pty-host.ts onExit handler.
func (r *Ring) FlushPartial() {
	r.mu.Lock()
	defer r.mu.Unlock()

	if len(r.partial) == 0 {
		return
	}
	r.lines = append(r.lines, string(r.partial))
	r.partial, r.lastClear = r.partial[:0], -1
}

// Snapshot returns all stored lines concatenated as raw bytes for scrollback replay.
// The in-progress partial line is NOT included (matches TS outputBuffer.join("")).
func (r *Ring) Snapshot() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()

	return []byte(strings.Join(r.lines, ""))
}

// Replay returns the complete terminal byte stream retained for a newly
// attached viewer, including the current non-newline-terminated fragment.
// Full-screen TUIs commonly repaint with cursor-control sequences and carriage
// returns instead of newlines, so excluding the partial line can otherwise replay an
// empty screen even though the process has already drawn its UI.
func (r *Ring) Replay() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()

	out := make([]byte, 0, r.linesBytes()+len(r.partial))
	for _, line := range r.lines {
		out = append(out, line...)
	}
	return append(out, r.partial...)
}

func (r *Ring) linesBytes() int {
	n := 0
	for _, line := range r.lines {
		n += len(line)
	}
	return n
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
