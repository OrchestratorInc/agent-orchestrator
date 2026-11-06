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

// ringEntry is one storage chunk of the retained stream: an entry is not
// necessarily a logical line. A repaint line longer than MaxRingLineBytes is
// split into several continuation entries (endsLine false) followed by its
// terminator; a line that fits is a single entry. endsLine reports whether the
// entry completes a logical line.
type ringEntry struct {
	text     string
	endsLine bool
}

// Ring is a bounded rolling buffer of terminal output, ANSI codes preserved.
// It mirrors the appendOutput state machine from pty-host.ts.
// Concurrent Append and Snapshot/Tail calls are safe.
type Ring struct {
	mu sync.Mutex
	// lines holds storage chunks, not logical lines: Tail and Snapshot
	// reassemble logical lines by endsLine, while Replay concatenates every
	// chunk in order.
	lines       []ringEntry
	partialLine string
	bytes       int // len(partialLine) + sum of len(entry.text) for entry in lines
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
		r.lines = append(r.lines, ringEntry{text: line + "\n", endsLine: true})
	}
	// The buffer's content grows by exactly the appended bytes; the split above
	// only moves bytes between partialLine and lines.
	r.bytes += len(raw)
	// A full-screen TUI can repaint for hours without a newline, so the partial
	// line is not covered by MaxOutputLines. Split it into MaxRingLineBytes
	// entries so trimLocked's byte accounting can evict it; entries concatenate
	// in order, so the joined stream is unchanged. The chunks are continuations:
	// the line stays incomplete until a newline (or FlushPartial) terminates it.
	for len(r.partialLine) >= MaxRingLineBytes {
		r.lines = append(r.lines, ringEntry{text: r.partialLine[:MaxRingLineBytes]})
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
		r.bytes -= len(r.lines[drop].text)
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

// joinEntryTexts concatenates the texts of entries, in order, into one string.
func joinEntryTexts(entries []ringEntry) string {
	var b strings.Builder
	for _, e := range entries {
		b.WriteString(e.text)
	}
	return b.String()
}

// FlushPartial pushes any in-progress partial line as a final entry.
// Called on PTY exit to mirror the pty-host.ts onExit handler.
// The partial line might be empty while the line's last chunk is stored: the
// line ended exactly on the MaxRingLineBytes boundary, so the trailing
// continuation entry becomes the terminator instead. A second call is a no-op.
func (r *Ring) FlushPartial() {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.partialLine != "" {
		r.lines = append(r.lines, ringEntry{text: r.partialLine, endsLine: true})
		r.partialLine = ""
	} else if n := len(r.lines); n > 0 && !r.lines[n-1].endsLine {
		r.lines[n-1].endsLine = true
	}
	r.trimLocked()
}

// Snapshot returns all completed logical lines concatenated as raw bytes for
// scrollback replay: every entry up to and including the last terminator. The
// in-progress line is NOT included, including its stored chunks (matches TS
// outputBuffer.join("")).
func (r *Ring) Snapshot() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()

	end := 0
	for i := len(r.lines) - 1; i >= 0; i-- {
		if r.lines[i].endsLine {
			end = i + 1
			break
		}
	}
	return []byte(joinEntryTexts(r.lines[:end]))
}

// Replay returns the complete terminal byte stream retained for a newly
// attached viewer, including the current non-newline-terminated fragment.
// Full-screen TUIs commonly repaint with cursor-control sequences and carriage
// returns instead of newlines, so excluding partialLine can otherwise replay an
// empty screen even though the process has already drawn its UI.
// Entries are storage chunks, so joining every one plus partialLine yields the
// byte-identical retained stream.
func (r *Ring) Replay() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()

	return []byte(joinEntryTexts(r.lines) + r.partialLine)
}

// Tail returns the last n completed logical lines joined as a string.
// Mirrors the MSG_GET_OUTPUT_REQ handler: start = max(0, len-lines), but n
// counts logical lines, not storage chunks, so a chunked (longer than
// MaxRingLineBytes) line is reassembled from its continuation entries and
// terminator before being counted. The in-progress line is excluded.
// n <= 0 returns "".
//
// A byte-cap eviction can drop the head of the oldest retained line, in which
// case Tail returns that line's retained suffix.
func (r *Ring) Tail(n int) string {
	r.mu.Lock()
	defer r.mu.Unlock()

	if n <= 0 {
		return ""
	}
	// end is the index after the last terminator; everything behind it belongs
	// to the still in-progress line, which Tail excludes.
	end := len(r.lines)
	for end > 0 && !r.lines[end-1].endsLine {
		end--
	}
	// Walk back and stop one terminator past the oldest requested line.
	start := 0
	count := 0
	for i := end - 1; i >= 0; i-- {
		if r.lines[i].endsLine {
			count++
			if count == n+1 {
				start = i + 1
				break
			}
		}
	}
	return joinEntryTexts(r.lines[start:end])
}
