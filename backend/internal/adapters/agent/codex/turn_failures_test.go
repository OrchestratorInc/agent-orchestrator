package codex

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Rollout records in the shape Codex 0.150 persists (event_msg payloads with
// serde names task_started / task_complete / turn_aborted).
const (
	rolloutMeta        = `{"timestamp":"2026-10-18T10:00:00.000Z","type":"session_meta","payload":{"id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","source":"cli"}}`
	rolloutTaskStarted = `{"timestamp":"2026-10-18T10:00:01.000Z","type":"event_msg","payload":{"type":"task_started","turn_id":"1"}}`
	rolloutUserMessage = `{"timestamp":"2026-10-18T10:00:01.100Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}}`
	rolloutErrorEvent  = `{"timestamp":"2026-10-18T10:00:02.000Z","type":"event_msg","payload":{"type":"error","message":"The 'gpt-5.6-sol' model is not supported when using Codex with a ChatGPT account."}}`
	rolloutFailed      = `{"timestamp":"2026-10-18T10:00:02.250Z","type":"event_msg","payload":{"type":"task_complete","turn_id":"1","last_agent_message":null,"error":{"message":"The 'gpt-5.6-sol' model is not supported when using Codex with a ChatGPT account.","codex_error_info":"bad_request"}}}`
	rolloutSucceeded   = `{"timestamp":"2026-10-18T10:00:05.000Z","type":"event_msg","payload":{"type":"task_complete","turn_id":"2","last_agent_message":"done"}}`
	rolloutNullError   = `{"timestamp":"2026-10-18T10:00:05.000Z","type":"event_msg","payload":{"type":"task_complete","turn_id":"2","last_agent_message":"done","error":null}}`
	rolloutNextStarted = `{"timestamp":"2026-10-18T10:00:09.000Z","type":"event_msg","payload":{"type":"task_started","turn_id":"3"}}`
	rolloutAborted     = `{"timestamp":"2026-10-18T10:00:09.500Z","type":"event_msg","payload":{"type":"turn_aborted","turn_id":"3","reason":"interrupted"}}`
	rolloutTokenCount  = `{"timestamp":"2026-10-18T10:00:02.300Z","type":"event_msg","payload":{"type":"token_count","info":null}}`
)

func writeRollout(t *testing.T, content string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "sessions", "2026", "10", "18")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "rollout-2026-10-18T10-00-00-0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b.jsonl")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func lines(records ...string) string {
	return strings.Join(records, "\n") + "\n"
}

func TestLastTurnFailureReportsAFailedTurn(t *testing.T) {
	path := writeRollout(t, lines(rolloutMeta, rolloutTaskStarted, rolloutUserMessage, rolloutErrorEvent, rolloutFailed, rolloutTokenCount))
	failure, ok, err := New().LastTurnFailure(context.Background(), path)
	if err != nil || !ok {
		t.Fatalf("LastTurnFailure = ok %v err %v, want a failure", ok, err)
	}
	if want := time.Date(2026, 10, 18, 10, 0, 2, 250_000_000, time.UTC); !failure.At.Equal(want) {
		t.Fatalf("failure.At = %v, want the record's own timestamp %v", failure.At, want)
	}
	if !strings.Contains(failure.Message, "not supported") {
		t.Fatalf("failure.Message = %q", failure.Message)
	}
}

// Esc fires no Stop hook (only Codex 0.150+'s Interrupt hook, which AO does
// not install); the rollout records the aborted turn.
func TestLastTurnFailureReportsAnInterruptedTurn(t *testing.T) {
	path := writeRollout(t, lines(rolloutMeta, rolloutTaskStarted, rolloutFailed, rolloutNextStarted, rolloutAborted, rolloutTokenCount))
	failure, ok, err := New().LastTurnFailure(context.Background(), path)
	if err != nil || !ok || !failure.Interrupted {
		t.Fatalf("LastTurnFailure = %#v ok %v err %v, want the interrupt", failure, ok, err)
	}
	if want := time.Date(2026, 10, 18, 10, 0, 9, 500_000_000, time.UTC); !failure.At.Equal(want) {
		t.Fatalf("interrupt At = %v, want the record's own timestamp %v", failure.At, want)
	}
}

func TestLastTurnFailureIgnoresTurnsThatDidNotFail(t *testing.T) {
	cases := map[string]string{
		// A successful turn is already reported by Codex's Stop hook.
		"successful turn":               lines(rolloutMeta, rolloutTaskStarted, rolloutSucceeded),
		"explicit null error":           lines(rolloutMeta, rolloutTaskStarted, rolloutNullError),
		"failure then a success":        lines(rolloutMeta, rolloutTaskStarted, rolloutFailed, rolloutTaskStarted, rolloutSucceeded),
		"old failure, turn now running": lines(rolloutMeta, rolloutTaskStarted, rolloutFailed, rolloutNextStarted, rolloutUserMessage),
		"interrupted, then a new turn":  lines(rolloutMeta, rolloutNextStarted, rolloutAborted, rolloutTaskStarted),
		// A turn replaced by a newer one is not a turn the user stopped.
		"replaced turn": lines(rolloutMeta, rolloutTaskStarted, rolloutFailed, rolloutNextStarted,
			strings.Replace(rolloutAborted, `"interrupted"`, `"replaced"`, 1)),
		"interrupt without a readable timestamp": lines(rolloutMeta, rolloutNextStarted,
			strings.Replace(rolloutAborted, `"timestamp":"2026-10-18T10:00:09.500Z",`, ``, 1)),
		"turn still running": lines(rolloutMeta, rolloutTaskStarted, rolloutUserMessage),
		"no turns":           lines(rolloutMeta),
		"empty":              "",
		// Codex has not finished writing the record: it is not read yet.
		"unfinished failure record": lines(rolloutMeta, rolloutTaskStarted) + rolloutFailed,
		// Without its own time a failure cannot be ordered against the current
		// prompt, so it is not reported, and the older failure behind it is
		// not reached either.
		"failure without a readable timestamp": lines(rolloutMeta, rolloutFailed, rolloutTaskStarted,
			strings.Replace(rolloutFailed, `"timestamp":"2026-10-18T10:00:02.250Z"`, `"timestamp":"not a time"`, 1)),
		"failure without a timestamp": lines(rolloutMeta, rolloutTaskStarted,
			strings.Replace(rolloutFailed, `"timestamp":"2026-10-18T10:00:02.250Z",`, ``, 1)),
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			path := writeRollout(t, content)
			if failure, ok, err := New().LastTurnFailure(context.Background(), path); ok || err != nil {
				t.Fatalf("LastTurnFailure = %#v ok %v err %v, want none", failure, ok, err)
			}
		})
	}
}

// A large tool output earlier in the turn must not hide the failure, and a
// record cut by the tail window's start must not be parsed as a fragment.
func TestLastTurnFailureReadsOnlyTheRolloutTail(t *testing.T) {
	huge := `{"timestamp":"2026-10-18T10:00:01.500Z","type":"response_item","payload":{"type":"function_call_output","output":"` +
		strings.Repeat("x", codexTurnTailWindow+1024) + `"}}`
	path := writeRollout(t, lines(rolloutMeta, rolloutTaskStarted, huge, rolloutErrorEvent, rolloutFailed))
	if _, ok, err := New().LastTurnFailure(context.Background(), path); !ok || err != nil {
		t.Fatalf("failure after a large record: ok %v err %v", ok, err)
	}
	// The failure itself sits beyond the window: only the huge record's end
	// is in reach, which is no turn boundary.
	path = writeRollout(t, lines(rolloutMeta, rolloutFailed, huge))
	if _, ok, err := New().LastTurnFailure(context.Background(), path); ok || err != nil {
		t.Fatalf("failure outside the tail window: ok %v err %v, want none", ok, err)
	}
}

func TestLastTurnFailureReadsOnlyCodexRollouts(t *testing.T) {
	dir := t.TempDir()
	other := filepath.Join(dir, "notes.jsonl")
	if err := os.WriteFile(other, []byte(lines(rolloutFailed)), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{other, "rollout-relative.jsonl", filepath.Join(dir, "rollout-missing.jsonl"), ""} {
		if _, ok, err := New().LastTurnFailure(context.Background(), path); ok || err != nil {
			t.Fatalf("LastTurnFailure(%q) = ok %v err %v, want none", path, ok, err)
		}
	}
}

// Event records of other types are skipped before decoding, even when they
// name a turn (turn_id) or are not valid JSON, and do not hide the boundary
// before them.
func TestLastTurnFailureSkipsOtherEventRecords(t *testing.T) {
	agentMessage := `{"timestamp":"2026-10-18T10:00:02.400Z","type":"event_msg","payload":{"type":"agent_message","turn_id":"1","message":"hi"}}`
	garbled := `{"timestamp":"2026-10-18T10:00:02.500Z","type":"event_msg","payload":{"type":"agent_reasoning"`
	path := writeRollout(t, lines(rolloutMeta, rolloutTaskStarted, rolloutFailed, rolloutTokenCount, agentMessage, garbled))
	if _, ok, err := New().LastTurnFailure(context.Background(), path); !ok || err != nil {
		t.Fatalf("LastTurnFailure = ok %v err %v, want the failure", ok, err)
	}
	for _, line := range []string{rolloutTokenCount, agentMessage, garbled, rolloutUserMessage} {
		if containsCodexTurnBoundaryType([]byte(line)) {
			t.Fatalf("pre-filter kept a non-boundary record: %s", line)
		}
	}
	for _, line := range []string{rolloutTaskStarted, rolloutFailed, rolloutSucceeded, rolloutNextStarted, rolloutAborted} {
		if !containsCodexTurnBoundaryType([]byte(line)) {
			t.Fatalf("pre-filter dropped a boundary record: %s", line)
		}
	}
}
