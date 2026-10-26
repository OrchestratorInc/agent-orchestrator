package claudecode

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"path/filepath"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hookutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var _ ports.TranscriptTurnFailureDetector = (*Plugin)(nil)

// claudeTurnTailWindow bounds how much of a transcript's end is read to find
// its newest user record. An interrupt marker is small and written last, so
// it always sits in this window when it is the newest record that matters.
const claudeTurnTailWindow = 256 << 10

// claudeInterruptMarkers are the user messages Claude Code records when Esc
// interrupts a turn (verified against Claude Code 2.1.287 and 2.1.302).
var claudeInterruptMarkers = []string{
	"[Request interrupted by user]",
	"[Request interrupted by user for tool use]",
}

// LastTurnFailure reports an Esc interrupt of the newest Claude Code turn.
// Claude fires no hook when Esc interrupts a turn, whether it is streaming a
// reply, running a tool or waiting at a permission prompt: not Stop, not
// StopFailure, not the interrupted tool's PostToolUse. The session stayed
// active (or blocked) until the stale-activity observer screen-proved it idle.
// Claude does record the interrupt as a main-conversation user message holding
// only a marker. A turn that ended on an API error is reported by the
// StopFailure hook, so only interrupts are reported here.
//
// Only the newest main-conversation user record counts: a prompt or tool
// result after the marker means a later turn is running.
func (p *Plugin) LastTurnFailure(ctx context.Context, transcriptPath string) (ports.TurnFailure, bool, error) {
	if err := ctx.Err(); err != nil {
		return ports.TurnFailure{}, false, err
	}
	path := strings.TrimSpace(transcriptPath)
	// The path comes from a hook payload. Read only what looks like a Claude
	// conversation transcript so the daemon never parses an unrelated file.
	if !filepath.IsAbs(path) || filepath.Ext(path) != ".jsonl" {
		return ports.TurnFailure{}, false, nil
	}
	tail, err := hookutil.ReadJSONLTail(path, claudeTurnTailWindow)
	if errors.Is(err, fs.ErrNotExist) {
		return ports.TurnFailure{}, false, nil
	}
	if err != nil {
		return ports.TurnFailure{}, false, err
	}
	failure, ok := lastClaudeInterrupt(tail)
	return failure, ok, nil
}

// lastClaudeInterrupt walks the records newest first and stops at the first
// main-conversation user record.
func lastClaudeInterrupt(records []byte) (ports.TurnFailure, bool) {
	for len(records) > 0 {
		records = bytes.TrimRight(records, "\n")
		start := bytes.LastIndexByte(records, '\n') + 1
		line := records[start:]
		records = records[:start]
		interrupt, user := claudeUserRecord(line)
		if user {
			return interrupt, !interrupt.At.IsZero()
		}
	}
	return ports.TurnFailure{}, false
}

// claudeUserRecord classifies one transcript record. user is true for a
// main-conversation user record; interrupt.At is set only when it is an
// interrupt marker with a readable timestamp. A marker without one is not
// reported: dating it now could make an old interrupt look newer than the
// current prompt and idle a turn that is still running.
func claudeUserRecord(line []byte) (interrupt ports.TurnFailure, user bool) {
	// Assistant output, tool progress and bookkeeping records make up most of
	// a transcript; skip them before decoding.
	if !bytes.Contains(line, []byte(`"user"`)) {
		return ports.TurnFailure{}, false
	}
	var record struct {
		Type        string `json:"type"`
		IsSidechain bool   `json:"isSidechain"`
		Timestamp   string `json:"timestamp"`
		Message     struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(line, &record) != nil || record.Type != "user" || record.IsSidechain {
		return ports.TurnFailure{}, false
	}
	if !IsInterruptMarker(record.Message.Content) {
		return ports.TurnFailure{}, true
	}
	at, err := time.Parse(time.RFC3339Nano, record.Timestamp)
	if err != nil {
		return ports.TurnFailure{}, true
	}
	return ports.TurnFailure{At: at, Interrupted: true}, true
}

// IsInterruptMarker reports whether a user message's content is exactly an
// interrupt marker. Claude Code writes the marker as a one-element text
// block array; a typed prompt is recorded as a plain string, so a prompt that
// is exactly the marker text is not an interrupt. The marker's
// interruptedMessageId cannot be required: Claude sets it only when Esc
// cancelled a known API message (2.1.302 omits it before the first response).
func IsInterruptMarker(content json.RawMessage) bool {
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(content, &blocks) != nil || len(blocks) != 1 || blocks[0].Type != "text" {
		return false
	}
	for _, marker := range claudeInterruptMarkers {
		if blocks[0].Text == marker {
			return true
		}
	}
	return false
}
