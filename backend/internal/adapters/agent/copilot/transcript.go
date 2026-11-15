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
	copilotTranscriptName      = "events.jsonl"
	copilotTranscriptTailBytes = 4 << 20
)

var (
	copilotTranscriptWait = 400 * time.Millisecond
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
	text, size := "", int64(-1)
	for {
		if info, err := os.Stat(path); err == nil && info.Size() != size {
			size = info.Size()
			var settled bool
			if text, settled = readCopilotFinalMessage(path, input.Timestamp); settled {
				return text
			}
		}
		if !time.Now().Before(deadline) {
			return text
		}
		time.Sleep(copilotTranscriptPoll)
	}
}

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
		lines = lines[1:]
	}
	return lines
}
