package primeagent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// maxTranscriptTail bounds the read per session per poll. A prompt older than
// this only loses its text: the session still reports working and idle.
const maxTranscriptTail = 256 << 10

var _ ports.NativeSessionObserver = (*Plugin)(nil)

// ObserveNativeSessions stands in for the extension on prime-agent 0.10, which
// can't load one. Prime reports its live sessions itself; the newest messages
// come from each session's transcript. It reports nothing for a binary that
// still accepts the extension, so those sessions keep their hooks.
func (p *Plugin) ObserveNativeSessions(ctx context.Context, dataDir string) ([]ports.NativeSessionSnapshot, bool, error) {
	binary, err := p.primeAgentBinary(ctx)
	if err != nil {
		if errors.Is(err, ports.ErrAgentBinaryNotFound) {
			return nil, false, nil
		}
		return nil, false, err
	}
	if p.acceptsExtension(ctx, binary) {
		return nil, false, nil
	}
	sessions, err := sessionDir(dataDir)
	if err != nil {
		return nil, false, err
	}
	output, err := p.executePrimeCommand(ctx, dataDir, "list", "--json")
	if err != nil {
		return nil, false, fmt.Errorf("prime-agent: list live sessions: %w: %s", err, boundedPrimeOutput(output))
	}
	var response primeListResponse
	if err := json.Unmarshal(output, &response); err != nil {
		return nil, false, fmt.Errorf("prime-agent: decode session list: %w", err)
	}
	snapshots := make([]ports.NativeSessionSnapshot, 0, len(response.Sessions))
	for _, session := range response.Sessions {
		snapshot, ok := nativeSnapshot(session, sessions)
		if !ok {
			continue
		}
		snapshot.Prompt, snapshot.Reply = readLatestMessages(snapshot.TranscriptPath)
		snapshots = append(snapshots, snapshot)
	}
	return snapshots, true, nil
}

// nativeSnapshot skips subagents, which share their parent's cwd. The transcript
// path is built from the id, so a value from Prime's output never picks the file.
func nativeSnapshot(session primeSessionSummary, sessions string) (ports.NativeSessionSnapshot, bool) {
	id := strings.TrimSpace(session.SessionID)
	cwd := strings.TrimSpace(session.CWD)
	if id == "" || cwd == "" || id == "." || id == ".." || strings.ContainsAny(id, `/\`) {
		return ports.NativeSessionSnapshot{}, false
	}
	if session.RLMDepth != 0 || (session.RuntimeKind != "" && session.RuntimeKind != "top-level") {
		return ports.NativeSessionSnapshot{}, false
	}
	var working bool
	switch session.Activity {
	case "working":
		working = true
	case "idle":
	default:
		return ports.NativeSessionSnapshot{}, false
	}
	started, _ := time.Parse(time.RFC3339Nano, session.Created)
	return ports.NativeSessionSnapshot{
		WorkspacePath:  cwd,
		AgentSessionID: id,
		TranscriptPath: filepath.Join(sessions, id+".jsonl"),
		StartedAt:      started,
		Working:        working,
	}, true
}

// readLatestMessages returns the newest user and assistant messages. A transcript
// that can't be read reports none: Prime's own working or idle state still holds.
func readLatestMessages(path string) (prompt, reply ports.NativeMessage) {
	tail, err := readTail(path, maxTranscriptTail)
	if err != nil {
		return prompt, reply
	}
	for _, line := range bytes.Split(tail, []byte{'\n'}) {
		var entry struct {
			Type      string `json:"type"`
			Timestamp string `json:"timestamp"`
			Message   struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if len(line) == 0 || json.Unmarshal(line, &entry) != nil || entry.Type != "message" {
			continue
		}
		at, err := time.Parse(time.RFC3339Nano, entry.Timestamp)
		if err != nil {
			continue
		}
		message := ports.NativeMessage{Text: messageText(entry.Message.Content), At: at}
		switch entry.Message.Role {
		case "user":
			prompt = message
		case "assistant":
			reply = message
		}
	}
	return prompt, reply
}

// readTail returns the last limit bytes of path without a leading partial line.
func readTail(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path) //nolint:gosec // built from AO's session directory and a validated id
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	offset := max(info.Size()-limit, 0)
	data, err := io.ReadAll(io.NewSectionReader(file, offset, limit))
	if err != nil {
		return nil, err
	}
	if offset > 0 {
		if newline := bytes.IndexByte(data, '\n'); newline >= 0 {
			return data[newline+1:], nil
		}
		return nil, nil
	}
	return data, nil
}

func messageText(content json.RawMessage) string {
	var text string
	if json.Unmarshal(content, &text) == nil {
		return text
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(content, &parts) != nil {
		return ""
	}
	texts := make([]string, 0, len(parts))
	for _, part := range parts {
		if part.Type == "text" && part.Text != "" {
			texts = append(texts, part.Text)
		}
	}
	return strings.Join(texts, "\n")
}
