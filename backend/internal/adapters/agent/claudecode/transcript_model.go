package claudecode

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/nativeconfig"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// transcriptTailBytes bounds how much of a transcript is read to find the
// latest assistant message. Claude appends one JSON object per line, so the
// last assistant entry sits near the end even in a long conversation.
const transcriptTailBytes = 256 << 10

// LastAssistantModel returns the model that produced the most recent assistant
// message in a Claude Code session transcript. Claude records the concrete
// model the API answered with, so this is how AO learns what an alias such as
// "opus" resolved to in the installed CLI. It returns "" with no error when
// the transcript does not exist yet or holds no assistant message.
func LastAssistantModel(ctx context.Context, env map[string]string, sessionID string) (string, error) {
	configDir, err := nativeconfig.Resolve(env, claudeConfigDirEnv, ".claude")
	if err != nil {
		return "", fmt.Errorf("claude-code: resolve config dir: %w", err)
	}
	path, ok, err := (&Plugin{}).LocateTranscript(ctx, ports.NativeSessionRef{
		NativeSessionID: sessionID, ConfigDir: configDir,
	})
	if err != nil || !ok {
		return "", err
	}
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("claude-code: open transcript: %w", err)
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return "", fmt.Errorf("claude-code: stat transcript: %w", err)
	}
	offset := info.Size() - transcriptTailBytes
	if offset < 0 {
		offset = 0
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return "", fmt.Errorf("claude-code: read transcript: %w", err)
	}
	tail, err := io.ReadAll(file)
	if err != nil {
		return "", fmt.Errorf("claude-code: read transcript: %w", err)
	}
	return lastAssistantModelFromJSONL(tail), nil
}

func lastAssistantModelFromJSONL(data []byte) string {
	lines := bytes.Split(data, []byte{'\n'})
	for i := len(lines) - 1; i >= 0; i-- {
		line := bytes.TrimSpace(lines[i])
		if len(line) == 0 || line[0] != '{' {
			// Also skips a line cut in half by the tail offset.
			continue
		}
		var entry struct {
			Type    string `json:"type"`
			Message struct {
				Model string `json:"model"`
			} `json:"message"`
		}
		if json.Unmarshal(line, &entry) != nil || entry.Type != "assistant" {
			continue
		}
		model := strings.TrimSpace(entry.Message.Model)
		// Claude writes "<synthetic>" for messages it generated locally, such as
		// API error notices; those were not answered by any model.
		if model == "" || strings.HasPrefix(model, "<") {
			continue
		}
		return model
	}
	return ""
}

// TypedPrompt shows a slash command as typed and drops its local output.
func TypedPrompt(text string) (string, bool) {
	trimmed := strings.TrimSpace(text)
	if strings.HasPrefix(trimmed, "<local-command-stdout>") || strings.HasPrefix(trimmed, "<local-command-stderr>") {
		return "", true
	}
	between := func(start, end string) string {
		_, rest, ok := strings.Cut(trimmed, start)
		value, _, _ := strings.Cut(rest, end)
		if !ok {
			return ""
		}
		return strings.TrimSpace(value)
	}
	// Claude writes a command record that starts with its tags; a prompt that
	// merely quotes them is still a prompt.
	if !strings.HasPrefix(trimmed, "<command-name>") && !strings.HasPrefix(trimmed, "<command-message>") {
		return text, false
	}
	name := between("<command-name>", "</command-name>")
	if name == "" {
		return text, false
	}
	if args := between("<command-args>", "</command-args>"); args != "" {
		name += " " + args
	}
	return name, true
}
