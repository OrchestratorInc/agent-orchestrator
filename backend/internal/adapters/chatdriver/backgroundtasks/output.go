package backgroundtasks

import (
	"regexp"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// One read of a task's output returns at most this much of its tail.
const (
	MaxOutputBytes = 64 << 10
	MaxOutputLines = 200
)

// Terminal escapes: CSI (colors, cursor moves), OSC (titles, links) and the
// two-byte forms. Removed before control characters so no parameters leak.
var terminalEscape = regexp.MustCompile(`\x1b(?:\[[\x30-\x3f]*[\x20-\x2f]*[\x40-\x7e]|\][^\x07\x1b]*(?:\x07|\x1b\\)?|[\x40-\x5a\x5c-\x5f])`)

// TailOutput turns raw terminal output into its readable tail: escapes and
// control characters removed, a carriage-return redraw reduced to what the
// terminal finally showed, then the last MaxOutputLines lines within
// MaxOutputBytes. dropped says the caller already left out older output.
func TailOutput(raw string, dropped bool) ports.ChatBackgroundTaskOutput {
	if !utf8.ValidString(raw) {
		raw = strings.ToValidUTF8(raw, "�")
	}
	text := terminalEscape.ReplaceAllString(raw, "")
	text = strings.TrimRight(text, "\r\n")
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		line = strings.TrimRight(line, "\r")
		// A progress bar rewrites its line with \r; keep the last drawing.
		if at := strings.LastIndexByte(line, '\r'); at >= 0 {
			line = line[at+1:]
		}
		lines[i] = domain.SanitizeControlChars(line)
	}
	if len(lines) > MaxOutputLines {
		lines = lines[len(lines)-MaxOutputLines:]
		dropped = true
	}
	size := 0
	for i := len(lines) - 1; i >= 0; i-- {
		size += len(lines[i]) + 1
		if size > MaxOutputBytes {
			lines = lines[i+1:]
			dropped = true
			break
		}
	}
	return ports.ChatBackgroundTaskOutput{Text: strings.Join(lines, "\n"), Truncated: dropped}
}

// outputBufferKeys bounds how many commands keep output at once. Output is
// buffered before a provider says a command runs in the background, so
// foreground commands pass through too; the least recently written goes first.
const outputBufferKeys = 16

// OutputBuffer keeps the latest raw output of recent commands, by provider
// item. Its zero value is ready.
type OutputBuffer struct {
	mu      sync.Mutex
	entries map[string]*outputEntry
	clock   uint64
}

type outputEntry struct {
	data    []byte
	dropped bool
	written uint64
}

// Append adds streamed output for one item, keeping only a bounded tail.
func (b *OutputBuffer) Append(key, delta string) {
	if key == "" || delta == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.entries == nil {
		b.entries = make(map[string]*outputEntry)
	}
	b.clock++
	entry := b.entries[key]
	if entry == nil {
		if len(b.entries) >= outputBufferKeys {
			b.evictOldest()
		}
		entry = &outputEntry{}
		b.entries[key] = entry
	}
	entry.written = b.clock
	entry.data = append(entry.data, delta...)
	// Trim in batches so a chatty process does not copy on every line.
	if len(entry.data) > 2*MaxOutputBytes {
		entry.data = append([]byte(nil), entry.data[len(entry.data)-MaxOutputBytes:]...)
		entry.dropped = true
	}
}

// Read returns an item's readable tail; ok is false when nothing was kept.
func (b *OutputBuffer) Read(key string) (ports.ChatBackgroundTaskOutput, bool) {
	b.mu.Lock()
	entry := b.entries[key]
	if entry == nil {
		b.mu.Unlock()
		return ports.ChatBackgroundTaskOutput{}, false
	}
	raw, dropped := string(entry.data), entry.dropped
	b.mu.Unlock()
	if len(raw) > MaxOutputBytes {
		raw, dropped = raw[len(raw)-MaxOutputBytes:], true
	}
	if dropped {
		// The cut can land mid-line; that partial first line is not output.
		if at := strings.IndexByte(raw, '\n'); at >= 0 {
			raw = raw[at+1:]
		}
	}
	return TailOutput(raw, dropped), true
}

// Caller holds mu.
func (b *OutputBuffer) evictOldest() {
	oldest, at := "", uint64(0)
	for key, entry := range b.entries {
		if oldest == "" || entry.written < at {
			oldest, at = key, entry.written
		}
	}
	delete(b.entries, oldest)
}
