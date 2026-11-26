package backgroundtasks

import (
	"regexp"
	"strconv"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// One read of a task's output returns at most this much of its tail.
const (
	MaxOutputBytes = 64 << 10
	MaxOutputLines = 200
)

// Cursor-forward (CSI n C). Windows ConPTY draws blanks this way, often next
// to a color change, so it becomes blanks instead of being stripped. After a
// carriage return it skips cells a partial repaint left unchanged.
var cursorForward = regexp.MustCompile(`(?:\x1b\[|\x{9b})(\d*)C`)

// maxCursorForward bounds the blanks one cursor-forward can add; one read
// adds at most MaxOutputBytes in all.
const maxCursorForward = 256

// skipCell stands for a cursor-forward cell until lines are resolved. Raw NULs
// are removed first, so it never comes from the output itself.
const skipCell = '\x00'

// Terminal escapes, 7-bit and 8-bit: CSI (colors, cursor moves); OSC, DCS
// and the other string controls, ending at their terminator or the line end
// so an unterminated one cannot swallow later lines; character-set
// designations such as ESC(B; then the remaining two-byte forms such as ESC7
// and ESC=. Removed before control characters so no parameters leak.
var terminalEscape = regexp.MustCompile(`(?:\x1b\[|\x{9b})[\x30-\x3f]*[\x20-\x2f]*[\x40-\x7e]` +
	`|(?:\x1b[\]PX^_]|[\x{9d}\x{90}\x{98}\x{9e}\x{9f}])[^\x07\x1b\n\x{9c}]*(?:\x07|\x1b\\|\x{9c})?` +
	`|\x1b[\x20-\x2f]+[\x30-\x7e]` +
	`|\x1b[\x30-\x7e]`)

// TailOutput turns raw terminal output into its readable tail: escapes and
// control characters removed, a carriage-return redraw reduced to what the
// terminal finally showed, then the last MaxOutputLines lines within
// MaxOutputBytes. dropped says the caller already left out older output.
func TailOutput(raw string, dropped bool) ports.ChatBackgroundTaskOutput {
	// Cut to the last lines first so the escape pass skips discarded output.
	if strings.Count(raw, "\n") > MaxOutputLines+1 {
		trimmed := strings.TrimRight(raw, "\r\n")
		cut := len(trimmed)
		for n := 0; n <= MaxOutputLines && cut > 0; n++ {
			if cut = strings.LastIndexByte(trimmed[:cut], '\n'); cut < 0 {
				break
			}
		}
		if cut > 0 {
			raw, dropped = trimmed[cut+1:], true
		}
	}
	if !utf8.ValidString(raw) {
		raw = strings.ToValidUTF8(raw, "�")
	}
	budget := MaxOutputBytes
	text := cursorForward.ReplaceAllStringFunc(strings.ReplaceAll(raw, "\x00", ""), func(seq string) string {
		n, _ := strconv.Atoi(cursorForward.FindStringSubmatch(seq)[1])
		n = min(max(n, 1), maxCursorForward, budget)
		budget -= n
		return strings.Repeat(string(skipCell), n)
	})
	text = terminalEscape.ReplaceAllString(text, "")
	text = strings.TrimRight(text, "\r\n")
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		line = strings.ReplaceAll(redraw(strings.TrimRight(line, "\r")), string(skipCell), " ")
		lines[i] = strings.Map(dropInvisible, domain.SanitizeControlChars(line))
	}
	if len(lines) > MaxOutputLines {
		lines = lines[len(lines)-MaxOutputLines:]
		dropped = true
	}
	last := len(lines) - 1
	if len(lines[last]) > MaxOutputBytes {
		// A newest line over the budget keeps its end rather than emptying the read.
		cut := len(lines[last]) - MaxOutputBytes
		for cut < len(lines[last]) && !utf8.RuneStart(lines[last][cut]) {
			cut++
		}
		lines, dropped = []string{lines[last][cut:]}, true
	}
	size := 0
	for i := len(lines) - 1; i >= 0; i-- {
		size += len(lines[i])
		if i < len(lines)-1 {
			size++ // The newline after this line.
		}
		if size > MaxOutputBytes {
			lines = lines[i+1:]
			dropped = true
			break
		}
	}
	return ports.ChatBackgroundTaskOutput{Text: strings.Join(lines, "\n"), Truncated: dropped}
}

// redraw reduces a line rewritten with \r to what the terminal finally showed.
// A progress bar redraws it whole, so the last drawing wins. A drawing that
// starts by moving the cursor forward is a partial repaint: it writes over
// the cells it reaches and leaves the skipped ones as they were.
func redraw(line string) string {
	if !strings.ContainsRune(line, '\r') {
		return line
	}
	var cells []rune
	for _, drawing := range strings.Split(line, "\r") {
		if !strings.HasPrefix(drawing, string(skipCell)) {
			cells = []rune(drawing)
			continue
		}
		for at, r := range []rune(drawing) {
			switch {
			case at >= len(cells):
				cells = append(cells, r)
			case r != skipCell:
				cells[at] = r
			}
		}
	}
	return string(cells)
}

// dropInvisible removes format characters (bidi controls, zero-width spaces,
// tag characters and the like), which make text display differently from what
// it is, as the command line does. Joiners stay, since emoji and some scripts
// need them.
func dropInvisible(r rune) rune {
	if r != '\u200c' && r != '\u200d' && unicode.Is(unicode.Cf, r) {
		return -1
	}
	return r
}

// outputBufferKeys bounds how many unpinned commands keep output at once.
// Output is buffered before a provider says a command runs in the background,
// so foreground commands pass through too; the least recently written goes
// first. Pinned commands are never evicted.
const outputBufferKeys = 16

// evictedKeys bounds how many evicted commands are remembered, so a later read
// can say their output was dropped instead of reporting none.
const evictedKeys = 64

// OutputBuffer keeps the latest raw output of recent commands, by provider
// item. Its zero value is ready.
type OutputBuffer struct {
	// MaxDeltas, when set, is how many deltas the provider streams for one
	// command before it stops. A command that reaches it reads as stale.
	// Best effort: deltas are counted from when this buffer first saw the
	// command, so a late attach or an evicted command may never read stale,
	// and one that ends at exactly MaxDeltas without a final output may.
	MaxDeltas int

	mu      sync.Mutex
	entries map[string]*outputEntry
	pinned  map[string]bool
	evicted []string
	clock   uint64
}

type outputEntry struct {
	data []byte
	// dropped: earlier output is missing. midLine: data was cut by size, so
	// it may start part way through a line.
	dropped, midLine bool
	deltas           int
	written          uint64
}

// Append adds streamed output for one item, keeping only a bounded tail.
func (b *OutputBuffer) Append(key, delta string) {
	if key == "" || delta == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	entry := b.entry(key)
	entry.deltas++
	if len(delta) > MaxOutputBytes {
		// Copy only the delta's tail into a bounded array, so neither the
		// delta nor a grown buffer stays alive.
		entry.data = append(make([]byte, 0, 2*MaxOutputBytes), delta[len(delta)-MaxOutputBytes:]...)
		entry.dropped, entry.midLine = true, true
		return
	}
	entry.data = append(entry.data, delta...)
	// Trim in batches so a chatty process does not copy on every line. The
	// tail moves to the front of the same array, so later appends do not regrow.
	if len(entry.data) > 2*MaxOutputBytes {
		entry.data = entry.data[:copy(entry.data, entry.data[len(entry.data)-MaxOutputBytes:])]
		entry.dropped, entry.midLine = true, true
	}
}

// MarkDropped records that an item's earlier output was missed, for a
// command first seen part way through.
func (b *OutputBuffer) MarkDropped(key string) {
	if key == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.entry(key).dropped = true
}

// Replace sets an item's whole output, as reported once the command ends.
// It fills whatever the stream missed.
func (b *OutputBuffer) Replace(key, output string) {
	if key == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	entry := b.entry(key)
	entry.dropped, entry.midLine, entry.deltas = false, false, 0
	if len(output) > MaxOutputBytes {
		// Copy only the tail, so the whole aggregated output is not kept alive.
		output, entry.dropped, entry.midLine = output[len(output)-MaxOutputBytes:], true, true
	}
	entry.data = []byte(output)
}

// Pin keeps these items' output however many other commands write, and
// releases items pinned before. Callers pin the commands still listed.
func (b *OutputBuffer) Pin(keys []string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.pinned = make(map[string]bool, len(keys))
	for _, key := range keys {
		b.pinned[key] = true
	}
}

// Read returns an item's readable tail; ok is false when nothing was kept.
// Output that was evicted reads as empty and truncated.
func (b *OutputBuffer) Read(key string) (ports.ChatBackgroundTaskOutput, bool) {
	b.mu.Lock()
	entry := b.entries[key]
	if entry == nil {
		evicted := false
		for _, k := range b.evicted {
			evicted = evicted || k == key
		}
		b.mu.Unlock()
		return ports.ChatBackgroundTaskOutput{Truncated: evicted}, evicted
	}
	raw, dropped, midLine := string(entry.data), entry.dropped, entry.midLine
	stale := b.MaxDeltas > 0 && entry.deltas >= b.MaxDeltas
	b.mu.Unlock()
	if len(raw) > MaxOutputBytes {
		raw, dropped, midLine = raw[len(raw)-MaxOutputBytes:], true, true
	}
	if midLine {
		// The cut can land mid-line; that partial first line is not output,
		// unless no complete line follows it.
		if at := strings.IndexByte(strings.TrimRight(raw, "\r\n"), '\n'); at >= 0 {
			raw = raw[at+1:]
		}
	}
	out := TailOutput(raw, dropped)
	out.Stale = stale
	return out, true
}

// entry returns key's entry, creating it and evicting as needed. Caller holds mu.
func (b *OutputBuffer) entry(key string) *outputEntry {
	if b.entries == nil {
		b.entries = make(map[string]*outputEntry)
	}
	b.clock++
	entry := b.entries[key]
	if entry == nil {
		unpinned := 0
		for k := range b.entries {
			if !b.pinned[k] {
				unpinned++
			}
		}
		if unpinned >= outputBufferKeys {
			b.evictOldest()
		}
		entry = &outputEntry{}
		for i, k := range b.evicted {
			if k == key {
				// Output is arriving again for an evicted command; its start is gone.
				b.evicted = append(b.evicted[:i], b.evicted[i+1:]...)
				entry.dropped = true
				break
			}
		}
		b.entries[key] = entry
	}
	entry.written = b.clock
	return entry
}

// Caller holds mu.
func (b *OutputBuffer) evictOldest() {
	oldest, at := "", uint64(0)
	for key, entry := range b.entries {
		if !b.pinned[key] && (oldest == "" || entry.written < at) {
			oldest, at = key, entry.written
		}
	}
	if oldest == "" {
		return
	}
	delete(b.entries, oldest)
	b.evicted = append(b.evicted, oldest)
	if len(b.evicted) > evictedKeys {
		b.evicted = b.evicted[len(b.evicted)-evictedKeys:]
	}
}
