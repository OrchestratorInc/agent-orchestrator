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

const claudeTurnTailWindow = 256 << 10

var claudeInterruptMarkers = []string{
	"[Request interrupted by user]",
	"[Request interrupted by user for tool use]",
}

func (p *Plugin) LastTurnFailure(ctx context.Context, transcriptPath string) (ports.TurnFailure, bool, error) {
	if err := ctx.Err(); err != nil {
		return ports.TurnFailure{}, false, err
	}
	path := strings.TrimSpace(transcriptPath)
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

func claudeUserRecord(line []byte) (interrupt ports.TurnFailure, user bool) {
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
