package claudecode

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Records written by Claude Code 2.1.287 (fields AO does not read trimmed):
// a prompt, Esc while the reply streamed, and Esc while a Bash tool ran or
// waited at its permission prompt (the rejected tool result, then the marker).
const (
	transcriptPrompt      = `{"parentUuid":null,"isSidechain":false,"type":"user","message":{"role":"user","content":"SLOW please"},"uuid":"c777b312-76d9-4292-b9d2-b81f4db98933","timestamp":"2026-10-19T04:32:29.535Z","sessionId":"511e68be-f634-4b26-b7c7-7f4d258144c6","version":"2.1.287"}`
	transcriptAssistant   = `{"parentUuid":"c777b312-76d9-4292-b9d2-b81f4db98933","isSidechain":false,"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"tick 0 tick 1 "}]},"timestamp":"2026-10-19T04:32:34.621Z"}`
	transcriptInterrupted = `{"parentUuid":"b5d9c73e-c819-4980-a46c-334b77a7cab1","isSidechain":false,"type":"user","message":{"role":"user","content":[{"type":"text","text":"[Request interrupted by user]"}]},"uuid":"140d42fd-27d5-4865-9f20-9ae37b260d48","timestamp":"2026-10-19T04:32:34.624Z","interruptedMessageId":"msg_1792384349623","sessionId":"511e68be-f634-4b26-b7c7-7f4d258144c6","version":"2.1.287"}`
	transcriptRejected    = `{"parentUuid":"77e5d0c8-074e-4243-a7fc-5869fed37198","isSidechain":false,"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"The user doesn't want to proceed with this tool use.","is_error":true,"tool_use_id":"toolu_1792384368818"}]},"timestamp":"2026-10-19T04:33:02.854Z","toolUseResult":"User rejected tool use"}`
	transcriptToolStopped = `{"parentUuid":"4b4c6e72-bddc-48d9-9c82-3e26e0a8ff09","isSidechain":false,"type":"user","message":{"role":"user","content":[{"type":"text","text":"[Request interrupted by user for tool use]"}]},"uuid":"65bdc24b-8d9d-4610-849b-a8ff5cc6dc9a","timestamp":"2026-10-19T04:33:02.854Z","sessionId":"511e68be-f634-4b26-b7c7-7f4d258144c6","version":"2.1.287"}`
	transcriptLastPrompt  = `{"type":"last-prompt","lastPrompt":"TOOL now","sessionId":"511e68be-f634-4b26-b7c7-7f4d258144c6"}`
	transcriptMode        = `{"type":"permission-mode","permissionMode":"default","sessionId":"511e68be-f634-4b26-b7c7-7f4d258144c6"}`
)

func writeTranscript(t *testing.T, records ...string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "projects", "-home-me-repo")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "511e68be-f634-4b26-b7c7-7f4d258144c6.jsonl")
	content := ""
	if len(records) > 0 {
		content = strings.Join(records, "\n") + "\n"
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLastTurnFailureReportsAnEscInterrupt(t *testing.T) {
	cases := map[string]struct {
		records []string
		at      time.Time
	}{
		"while streaming": {
			[]string{transcriptPrompt, transcriptAssistant, transcriptInterrupted, transcriptLastPrompt, transcriptMode},
			time.Date(2026, 10, 19, 4, 32, 34, 624_000_000, time.UTC),
		},
		"while a tool ran or awaited permission": {
			[]string{transcriptPrompt, transcriptAssistant, transcriptRejected, transcriptToolStopped, transcriptLastPrompt},
			time.Date(2026, 10, 19, 4, 33, 2, 854_000_000, time.UTC),
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			failure, ok, err := New().LastTurnFailure(context.Background(), writeTranscript(t, tc.records...))
			if err != nil || !ok || !failure.Interrupted {
				t.Fatalf("LastTurnFailure = %#v ok %v err %v, want the interrupt", failure, ok, err)
			}
			if !failure.At.Equal(tc.at) {
				t.Fatalf("interrupt At = %v, want the marker's own timestamp %v", failure.At, tc.at)
			}
		})
	}
}

func TestLastTurnFailureIgnoresTurnsThatWereNotInterrupted(t *testing.T) {
	cases := map[string][]string{
		"turn running":              {transcriptPrompt, transcriptAssistant},
		"interrupt, then a prompt":  {transcriptPrompt, transcriptInterrupted, transcriptPrompt, transcriptAssistant},
		"tool result after a reply": {transcriptPrompt, transcriptInterrupted, transcriptRejected},
		"marker quoted in a prompt": {strings.Replace(transcriptPrompt, `"SLOW please"`, `"what does [Request interrupted by user] mean?"`, 1)},
		// A typed prompt is recorded as a string, however it reads.
		"prompt that is the marker": {transcriptPrompt, transcriptInterrupted,
			strings.Replace(transcriptPrompt, `"SLOW please"`, `"[Request interrupted by user]"`, 1)},
		"prompt that is the tool use marker": {transcriptPrompt, transcriptInterrupted,
			strings.Replace(transcriptPrompt, `"SLOW please"`, `"[Request interrupted by user for tool use]"`, 1)},
		"marker beside another block": {transcriptPrompt, strings.Replace(transcriptInterrupted, `"text":"[Request interrupted by user]"}]`,
			`"text":"[Request interrupted by user]"},{"type":"text","text":"more"}]`, 1)},
		// A subagent's interrupt is not the conversation's turn end.
		"subagent interrupt": {transcriptPrompt, strings.Replace(transcriptInterrupted, `"isSidechain":false`, `"isSidechain":true`, 1)},
		"marker without a timestamp": {transcriptPrompt,
			strings.Replace(transcriptInterrupted, `"timestamp":"2026-10-19T04:32:34.624Z",`, ``, 1)},
		"no user records": {transcriptLastPrompt},
		"empty":           nil,
	}
	for name, records := range cases {
		t.Run(name, func(t *testing.T) {
			if failure, ok, err := New().LastTurnFailure(context.Background(), writeTranscript(t, records...)); ok || err != nil {
				t.Fatalf("LastTurnFailure = %#v ok %v err %v, want none", failure, ok, err)
			}
		})
	}
}

func TestLastTurnFailureReadsOnlyTranscripts(t *testing.T) {
	dir := t.TempDir()
	other := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(other, []byte(transcriptInterrupted+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{other, "relative.jsonl", filepath.Join(dir, "missing.jsonl"), dir, ""} {
		if _, ok, err := New().LastTurnFailure(context.Background(), path); ok || err != nil {
			t.Fatalf("LastTurnFailure(%q) = ok %v err %v, want none", path, ok, err)
		}
	}
}
