package conpty

import (
	"bytes"
	"strings"
	"sync"
	"unicode/utf8"
)

// MaxOutputLines is the rolling line-buffer cap, matching MAX_OUTPUT_LINES in pty-host.ts.
const MaxOutputLines = 1000

// MaxPartialLineBytes bounds retained redraw output from TUIs that rarely emit newlines.
const MaxPartialLineBytes = 256 << 10

// Ring is a bounded rolling buffer of terminal output lines, ANSI codes preserved.
// It mirrors the appendOutput state machine from pty-host.ts.
// Concurrent Append and Snapshot/Tail calls are safe.
type Ring struct {
	mu          sync.Mutex
	lines       []string // each entry is "line\n" (or bare text on FlushPartial)
	partialLine string
}

// NewRing returns an empty Ring.
func NewRing() *Ring {
	return &Ring{}
}

// Append mirrors appendOutput from pty-host.ts: prepend the current partialLine,
// split on newlines, store completed lines with "\n" re-appended, keep the last
// element as the new partialLine, then trim to MaxOutputLines.
func (r *Ring) Append(raw []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()

	partial := r.partialLine
	truncated := false
	if bytes.IndexByte(raw, '\n') < 0 && len(partial)+len(raw) > MaxPartialLineBytes {
		// Only this tail can survive. Discard the excess before concatenating
		// so repeated redraws copy and scan at most the cap, not their history.
		truncated = true
		if len(raw) >= MaxPartialLineBytes {
			partial = ""
			raw = raw[len(raw)-MaxPartialLineBytes:]
		} else {
			partial = partial[len(partial)+len(raw)-MaxPartialLineBytes:]
		}
	}
	text := partial + string(raw)
	parts := strings.Split(text, "\n")
	// The last element is either "" (text ended with \n) or an incomplete line.
	r.partialLine = parts[len(parts)-1]
	if len(r.partialLine) > MaxPartialLineBytes {
		r.partialLine = r.partialLine[len(r.partialLine)-MaxPartialLineBytes:]
		truncated = true
	}
	if truncated {
		start := 0
		for start < len(r.partialLine) && !utf8.RuneStart(r.partialLine[start]) {
			start++
		}
		// Prefer a repaint/control boundary near the cut rather than replaying
		// the middle of an escape sequence. The search stays bounded to 4 KiB.
		end := min(start+(4<<10), len(r.partialLine))
		if i := strings.IndexAny(r.partialLine[start:end], "\r\x1b"); i >= 0 {
			start += i
		}
		// A suffix of a large newline-containing chunk must not retain that
		// chunk's entire backing allocation.
		r.partialLine = strings.Clone(r.partialLine[start:])
	}
	for _, line := range parts[:len(parts)-1] {
		r.lines = append(r.lines, line+"\n")
	}
	if len(r.lines) > MaxOutputLines {
		// ponytail: slice off the head; ceiling: O(n) copy on every trim cycle.
		// Upgrade path: circular buffer if trim rate is very high.
		r.lines = r.lines[len(r.lines)-MaxOutputLines:]
	}
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
