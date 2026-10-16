package copilot

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const copilotStopLine = `{"type":"hook.start","data":{"hookType":"agentStop","input":{"stopReason":"end_turn","timestamp":1791704010286}}}`

func shortenCopilotTranscriptWait(t *testing.T) {
	t.Helper()
	wait, poll := copilotTranscriptWait, copilotTranscriptPoll
	copilotTranscriptWait, copilotTranscriptPoll = 200*time.Millisecond, 5*time.Millisecond
	t.Cleanup(func() { copilotTranscriptWait, copilotTranscriptPoll = wait, poll })
}

func writeCopilotTranscript(t *testing.T, lines ...string) (path string, payload []byte) {
	t.Helper()
	path = filepath.Join(t.TempDir(), copilotTranscriptName)
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(map[string]any{"transcriptPath": path, "stopReason": "end_turn", "timestamp": 1791704010286})
	if err != nil {
		t.Fatal(err)
	}
	return path, payload
}

func TestFinalAssistantMessageReadsTheTurnThatJustEnded(t *testing.T) {
	shortenCopilotTranscriptWait(t)
	_, payload := writeCopilotTranscript(t,
		`{"type":"user.message","data":{"content":"first"}}`,
		`{"type":"assistant.message","data":{"content":"old answer"}}`,
		`{"type":"hook.start","data":{"hookType":"agentStop","input":{"timestamp":1791704000000}}}`,
		`{"type":"user.message","data":{"content":"second"}}`,
		`{"type":"tool.execution_start","data":{"input":"a tool's input is not an object"}}`,
		`{"type":"assistant.message","data":{"content":"","toolRequests":[{"name":"view"}]}}`,
		`{"type":"assistant.message","data":{"content":"  new answer\n"}}`,
		`{"type":"assistant.turn_end","data":{"turnId":"1"}}`,
		copilotStopLine,
		`{"type":"user.message","data":{"content":"queued behind the stop"}}`,
		`{"type":"assistant.message","data":{"content":"a later turn's answer"}}`,
	)
	if got := FinalAssistantMessage(payload); got != "new answer" {
		t.Fatalf("FinalAssistantMessage = %q, want %q", got, "new answer")
	}
}

// Copilot flushes its log in batches, so the turn's last lines usually land
// after the stop hook has started.
func TestFinalAssistantMessageWaitsForTheLogToCatchUp(t *testing.T) {
	shortenCopilotTranscriptWait(t)
	path, payload := writeCopilotTranscript(t,
		`{"type":"user.message","data":{"content":"first"}}`,
		`{"type":"assistant.message","data":{"content":"old answer"}}`,
		`{"type":"user.message","data":{"content":"second"}}`,
	)
	go func() {
		time.Sleep(40 * time.Millisecond)
		file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			return
		}
		defer file.Close()
		_, _ = file.WriteString(`{"type":"assistant.message","data":{"content":"late answer"}}` + "\n" + copilotStopLine + "\n")
	}()
	if got := FinalAssistantMessage(payload); got != "late answer" {
		t.Fatalf("FinalAssistantMessage = %q, want %q", got, "late answer")
	}
}

func TestFinalAssistantMessageIgnoresEarlierTurns(t *testing.T) {
	shortenCopilotTranscriptWait(t)
	_, payload := writeCopilotTranscript(t,
		`{"type":"user.message","data":{"content":"first"}}`,
		`{"type":"assistant.message","data":{"content":"old answer"}}`,
		`{"type":"user.message","data":{"content":"interrupted"}}`,
		`{"type":"assistant.turn_end","data":{"turnId":"1"}}`,
		copilotStopLine,
	)
	if got := FinalAssistantMessage(payload); got != "" {
		t.Fatalf("FinalAssistantMessage = %q, want nothing for a turn without an answer", got)
	}
}

// A log that never records the hook call still yields the turn's answer once
// the wait is over.
func TestFinalAssistantMessageFallsBackWithoutTheStopRecord(t *testing.T) {
	shortenCopilotTranscriptWait(t)
	_, payload := writeCopilotTranscript(t,
		`{"type":"user.message","data":{"content":"only"}}`,
		`{"type":"assistant.message","data":{"content":"answer"}}`,
	)
	if got := FinalAssistantMessage(payload); got != "answer" {
		t.Fatalf("FinalAssistantMessage = %q, want %q", got, "answer")
	}
}

func TestFinalAssistantMessageRejectsOtherFiles(t *testing.T) {
	shortenCopilotTranscriptWait(t)
	path := filepath.Join(t.TempDir(), "notes.jsonl")
	if err := os.WriteFile(path, []byte(`{"type":"assistant.message","data":{"content":"secret"}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	otherFile, err := json.Marshal(map[string]string{"transcriptPath": path})
	if err != nil {
		t.Fatal(err)
	}
	for _, payload := range []string{string(otherFile), `{"transcriptPath":"events.jsonl"}`, `{}`, `not json`} {
		started := time.Now()
		if got := FinalAssistantMessage([]byte(payload)); got != "" {
			t.Fatalf("FinalAssistantMessage(%s) = %q, want nothing", payload, got)
		}
		if time.Since(started) > copilotTranscriptWait/2 {
			t.Fatalf("FinalAssistantMessage(%s) waited for a file it must not read", payload)
		}
	}
}
