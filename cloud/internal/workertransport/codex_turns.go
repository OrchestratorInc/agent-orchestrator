package workertransport

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// newCodexRolloutTail follows the newest rollout of the terminal's Codex under
// a CODEX_HOME: the conversation it resumed (conversationID, if any; its
// rollout is the terminal's whatever surface started it), or any rollout an
// interactive Codex writes. A conversation started in Chat (source vscode) and
// resumed in the terminal is the terminal's now; Chat's app-server shares the
// CODEX_HOME but never runs beside the terminal agent. Exec, MCP and subagent
// rollouts are never the terminal's own turn. Call it before the agent starts.
func newCodexRolloutTail(codexHome, conversationID string, started time.Time) *transcriptTail {
	conversationID = strings.TrimSpace(conversationID)
	owns := func(path, name string) (bool, bool) {
		// Codex names a rollout rollout-<time>-<conversation id>.jsonl.
		if conversationID != "" && strings.HasSuffix(strings.TrimSuffix(name, ".jsonl"), "-"+conversationID) {
			return true, true
		}
		return codexTerminalRollout(path)
	}
	return newTranscriptTail(filepath.Join(codexHome, "sessions"), 0, started, owns, codexTurnRecord)
}

// codexTerminalRollout reads a rollout's session_meta source. known is false
// while Codex has not finished writing that first line.
func codexTerminalRollout(path string) (terminal, known bool) {
	file, err := os.Open(path)
	if err != nil {
		return false, false
	}
	defer file.Close()
	reader := bufio.NewReaderSize(file, 64<<10)
	line, err := reader.ReadSlice('\n')
	if err == io.EOF {
		return false, false
	}
	if err != nil && len(line) == 0 {
		return false, false
	}
	var record struct {
		Type    string `json:"type"`
		Payload struct {
			Source json.RawMessage `json:"source"`
		} `json:"payload"`
	}
	if json.Unmarshal(line, &record) != nil || record.Type != "session_meta" {
		// A meta line longer than the buffer still belongs to some rollout;
		// only a readable non-interactive source excludes it.
		return err == bufio.ErrBufferFull, true
	}
	var source string
	if json.Unmarshal(record.Payload.Source, &source) == nil {
		return source != "exec" && source != "mcp", true
	}
	// Subagent and other structured sources are not the terminal's own turn.
	return len(record.Payload.Source) == 0, true
}

// codexTurnRecord classifies a rollout record. A turn that ended on an error
// (a task_complete carrying one) or that the user interrupted (turn_aborted,
// reason interrupted) is a turn end; a successful turn end is skipped because
// its Stop hook already reported it. The turn ended at the record's own
// timestamp, not when the watcher read it up to a poll later; a record without
// one is taken to have just ended.
func codexTurnRecord(line []byte) (turnEnd, turnRecordKind) {
	var record struct {
		Timestamp string `json:"timestamp"`
		Type      string `json:"type"`
		Payload   struct {
			Type   string           `json:"type"`
			Reason string           `json:"reason"`
			Error  *json.RawMessage `json:"error"`
		} `json:"payload"`
	}
	if json.Unmarshal(line, &record) != nil || record.Type != "event_msg" {
		return turnEnd{}, turnRecordOther
	}
	at, err := time.Parse(time.RFC3339Nano, record.Timestamp)
	if err != nil {
		at = time.Now()
	}
	switch record.Payload.Type {
	case "task_started", "turn_started":
		return turnEnd{}, turnRecordStart
	case "turn_aborted":
		// Esc. Other reasons (a turn replaced by a newer one, a review ending)
		// leave the agent working or are reported by their own hooks.
		if record.Payload.Reason != "interrupted" {
			return turnEnd{}, turnRecordOther
		}
		return turnEnd{at: at, interrupted: true}, turnRecordEnd
	case "task_complete", "turn_complete":
	default:
		return turnEnd{}, turnRecordOther
	}
	if record.Payload.Error == nil || bytes.Equal(bytes.TrimSpace(*record.Payload.Error), []byte("null")) {
		return turnEnd{}, turnRecordOther
	}
	var failure struct {
		Message string `json:"message"`
	}
	_ = json.Unmarshal(*record.Payload.Error, &failure)
	return turnEnd{message: failure.Message, at: at}, turnRecordEnd
}
