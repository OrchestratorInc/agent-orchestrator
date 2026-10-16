package copilot

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	copilotTranscriptName = "events.jsonl"
	// One turn's events sit at the end of the log. A model snapshot line can
	// run to a few hundred kilobytes, so the window is generous.
	copilotTranscriptTailBytes = 4 << 20
)

// Copilot writes its event log in batches, roughly every 100ms, so the answer
// is normally not on disk yet when the stop hook starts. Variables so tests do
// not have to wait.
var (
	copilotTranscriptWait = 1500 * time.Millisecond
	copilotTranscriptPoll = 40 * time.Millisecond
)

type copilotTranscriptEvent struct {
	Type string `json:"type"`
	Data struct {
		Content  string          `json:"content"`
		HookType string          `json:"hookType"`
		Input    json.RawMessage `json:"input"`
	} `json:"data"`
}

// FinalAssistantMessage returns the answer that closed the turn an agentStop
// hook reports. Copilot's hook input names the session's event log instead of
// carrying the text, so the answer is read from there: the newest
// assistant.message of the turn this stop ended. A turn that ended without one
// yields nothing rather than an earlier turn's answer.
//
// The log records the hook call itself, with the same timestamp the hook
// receives. Once that line is on disk everything before it is too, so the read
// waits for it, up to copilotTranscriptWait, and then reads whatever is there.
func FinalAssistantMessage(payload []byte) string {
	var input struct {
		TranscriptPath string  `json:"transcriptPath"`
		Timestamp      float64 `json:"timestamp"`
	}
	if json.Unmarshal(payload, &input) != nil {
		return ""
	}
	path := strings.TrimSpace(input.TranscriptPath)
	if !filepath.IsAbs(path) || filepath.Base(path) != copilotTranscriptName {
		return ""
	}
	deadline := time.Now().Add(copilotTranscriptWait)
	for {
		text, settled := readCopilotFinalMessage(path, input.Timestamp)
		if settled || !time.Now().Before(deadline) {
			return text
		}
		time.Sleep(copilotTranscriptPoll)
	}
}

// readCopilotFinalMessage reads the answer of the turn the stop at
// stopTimestamp ended. settled reports whether the log already holds that
// stop's own hook record; until it does the text may belong to an earlier turn.
func readCopilotFinalMessage(path string, stopTimestamp float64) (text string, settled bool) {
	lines := copilotTranscriptTail(path)
	end := len(lines)
	for index := len(lines) - 1; index >= 0 && stopTimestamp != 0; index-- {
		if !bytes.Contains(lines[index], []byte(`"agentStop"`)) {
			continue
		}
		var event copilotTranscriptEvent
		var hookInput struct {
			Timestamp float64 `json:"timestamp"`
		}
		if json.Unmarshal(lines[index], &event) != nil || event.Type != "hook.start" || event.Data.HookType != "agentStop" ||
			json.Unmarshal(event.Data.Input, &hookInput) != nil || hookInput.Timestamp != stopTimestamp {
			continue
		}
		end, settled = index, true
		break
	}
	for index := end - 1; index >= 0; index-- {
		line := lines[index]
		if !bytes.Contains(line, []byte(`"assistant.message"`)) && !bytes.Contains(line, []byte(`"user.message"`)) {
			continue
		}
		var event copilotTranscriptEvent
		if json.Unmarshal(line, &event) != nil {
			continue
		}
		switch event.Type {
		case "user.message":
			return "", settled
		case "assistant.message":
			if content := strings.TrimSpace(event.Data.Content); content != "" {
				return content, settled
			}
		}
	}
	return "", settled
}

func copilotTranscriptTail(path string) [][]byte {
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return nil
	}
	offset := max(info.Size()-copilotTranscriptTailBytes, 0)
	tail, err := io.ReadAll(io.NewSectionReader(file, offset, info.Size()-offset))
	if err != nil {
		return nil
	}
	lines := bytes.Split(tail, []byte("\n"))
	if offset > 0 {
		// The window starts mid-line.
		lines = lines[1:]
	}
	return lines
}
