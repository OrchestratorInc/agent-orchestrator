package codex

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

const codexTurnTailWindow = 256 << 10

var codexTurnBoundaryTypes = [][]byte{
	[]byte(`"task_started"`), []byte(`"turn_started"`), []byte(`"turn_aborted"`),
	[]byte(`"task_complete"`), []byte(`"turn_complete"`),
}

func (p *Plugin) LastTurnFailure(ctx context.Context, transcriptPath string) (ports.TurnFailure, bool, error) {
	if err := ctx.Err(); err != nil {
		return ports.TurnFailure{}, false, err
	}
	path := strings.TrimSpace(transcriptPath)
	name := filepath.Base(path)
	if !filepath.IsAbs(path) || !strings.HasPrefix(name, "rollout-") || !strings.HasSuffix(name, ".jsonl") {
		return ports.TurnFailure{}, false, nil
	}
	tail, err := hookutil.ReadJSONLTail(path, codexTurnTailWindow)
	if errors.Is(err, fs.ErrNotExist) {
		return ports.TurnFailure{}, false, nil
	}
	if err != nil {
		return ports.TurnFailure{}, false, err
	}
	failure, ok := lastCodexTurnFailure(tail)
	return failure, ok, nil
}

func lastCodexTurnFailure(records []byte) (ports.TurnFailure, bool) {
	for len(records) > 0 {
		records = bytes.TrimRight(records, "\n")
		start := bytes.LastIndexByte(records, '\n') + 1
		line := records[start:]
		records = records[:start]
		failure, boundary, failed := codexTurnBoundary(line)
		if boundary {
			return failure, failed
		}
	}
	return ports.TurnFailure{}, false
}

func codexTurnBoundary(line []byte) (failure ports.TurnFailure, boundary, failed bool) {
	if !bytes.Contains(line, []byte(`"event_msg"`)) || !containsCodexTurnBoundaryType(line) {
		return ports.TurnFailure{}, false, false
	}
	var record struct {
		Timestamp string `json:"timestamp"`
		Type      string `json:"type"`
		Payload   struct {
			Type   string          `json:"type"`
			Reason string          `json:"reason"`
			Error  json.RawMessage `json:"error"`
		} `json:"payload"`
	}
	if json.Unmarshal(line, &record) != nil || record.Type != "event_msg" {
		return ports.TurnFailure{}, false, false
	}
	switch record.Payload.Type {
	case "task_started", "turn_started":
		return ports.TurnFailure{}, true, false
	case "turn_aborted":
		if record.Payload.Reason != "interrupted" {
			return ports.TurnFailure{}, true, false
		}
		at, err := time.Parse(time.RFC3339Nano, record.Timestamp)
		if err != nil {
			return ports.TurnFailure{}, true, false
		}
		return ports.TurnFailure{At: at, Interrupted: true}, true, true
	case "task_complete", "turn_complete":
	default:
		return ports.TurnFailure{}, false, false
	}
	errorJSON := bytes.TrimSpace(record.Payload.Error)
	if len(errorJSON) == 0 || bytes.Equal(errorJSON, []byte("null")) {
		return ports.TurnFailure{}, true, false
	}
	var detail struct {
		Message string `json:"message"`
	}
	_ = json.Unmarshal(errorJSON, &detail)
	at, err := time.Parse(time.RFC3339Nano, record.Timestamp)
	if err != nil {
		return ports.TurnFailure{}, true, false
	}
	return ports.TurnFailure{At: at, Message: detail.Message}, true, true
}

func containsCodexTurnBoundaryType(line []byte) bool {
	for _, kind := range codexTurnBoundaryTypes {
		if bytes.Contains(line, kind) {
			return true
		}
	}
	return false
}
