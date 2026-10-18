package worker

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/backend/pkg/contract"
)

func TestActivityEventFromHookCarriesConversationFacts(t *testing.T) {
	event, ok := ActivityEventFromHook("codex", "user-prompt-submit", []byte(`{
		"session_id":"native-1",
		"prompt":"from the terminal"
	}`))
	if !ok {
		t.Fatal("hook event was not accepted")
	}
	if event.AgentSessionID != "native-1" || event.LatestUserPrompt != "from the terminal" {
		t.Fatalf("event = %+v, want native identity and prompt", event)
	}
}

func TestActivityEventFromHookCarriesAssistantFactOnStop(t *testing.T) {
	event, ok := ActivityEventFromHook("codex", "stop", []byte(`{
		"sessionId":"native-1",
		"lastAssistantMessage":"reply from the terminal"
	}`))
	if !ok {
		t.Fatal("hook event was not accepted")
	}
	if event.LatestAssistantUpdate != "reply from the terminal" {
		t.Fatalf("assistant update = %q", event.LatestAssistantUpdate)
	}
}

// Claude Code's Stop hook names the answer last_assistant_message. Reading only
// the camelCase spelling left every terminal answer out of the cloud chat while
// the prompt, read from "prompt", still arrived.
func TestActivityEventFromHookCarriesClaudeStopAnswer(t *testing.T) {
	event, ok := ActivityEventFromHook("claude-code", "stop", []byte(`{
		"session_id":"native-1",
		"transcript_path":"/home/ao/.claude/projects/x/native-1.jsonl",
		"hook_event_name":"Stop",
		"stop_hook_active":false,
		"last_assistant_message":"391 PINEAPPLE"
	}`))
	if !ok {
		t.Fatal("hook event was not accepted")
	}
	if event.LatestAssistantUpdate != "391 PINEAPPLE" {
		t.Fatalf("assistant update = %q, want the Stop hook's answer", event.LatestAssistantUpdate)
	}
}

func TestActivityEventFromHookIgnoresSubagentAnswer(t *testing.T) {
	event, ok := ActivityEventFromHook("claude-code", "stop", []byte(`{
		"session_id":"native-1",
		"agent_id":"subagent-1",
		"last_assistant_message":"from a subagent"
	}`))
	if !ok {
		t.Fatal("hook event was not accepted")
	}
	if event.LatestAssistantUpdate != "" {
		t.Fatalf("assistant update = %q, want a subagent's reply left out", event.LatestAssistantUpdate)
	}
}

func TestActivityEventFromHookKeepsTheStartOfALongAnswer(t *testing.T) {
	answer := strings.Repeat("if a < b && c > d {\n\treturn \"é\"\n}\n", 2000)
	payload, err := json.Marshal(map[string]string{"session_id": "native-1", "last_assistant_message": answer})
	if err != nil {
		t.Fatal(err)
	}
	event, ok := ActivityEventFromHook("claude-code", "stop", payload)
	if !ok {
		t.Fatal("hook event was not accepted")
	}
	if event.LatestAssistantUpdate == "" || !strings.HasPrefix(answer, event.LatestAssistantUpdate) {
		t.Fatalf("assistant update has %d bytes, want a non-empty prefix of the answer", len(event.LatestAssistantUpdate))
	}
	if !utf8.ValidString(event.LatestAssistantUpdate) {
		t.Fatal("assistant update was cut inside a character")
	}
	if !ValidActivityEvent(event) {
		t.Fatal("bounded event is not valid")
	}
	encoded, err := json.Marshal(event.LatestAssistantUpdate)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded)-2 > 16<<10 {
		t.Fatalf("escaped answer is %d bytes, want at most %d", len(encoded)-2, 16<<10)
	}
}

func TestActivityEventFromHookOpenCode(t *testing.T) {
	// session-start lands the native session id used for --session restore; it
	// carries no activity state, so it flows purely on the identity path.
	start, ok := ActivityEventFromHook("opencode", "session-start", []byte(`{"session_id":"ses_abc"}`))
	if !ok || start.AgentSessionID != "ses_abc" || start.State != "" {
		t.Fatalf("session-start = (%+v, %v), want identity ses_abc with empty state", start, ok)
	}
	if !ValidActivityEvent(start) {
		t.Fatal("opencode session-start should be valid")
	}

	cases := []struct {
		event string
		state contract.ActivityState
	}{
		{"user-prompt-submit", contract.ActivityActive},
		{"active", contract.ActivityActive},
		{"permission-blocked", contract.ActivityWaitingInput},
		{"stop", contract.ActivityIdle},
	}
	for _, tc := range cases {
		event, ok := ActivityEventFromHook("opencode", tc.event, []byte(`{"session_id":"ses_abc"}`))
		if !ok {
			t.Fatalf("%s not accepted", tc.event)
		}
		if event.State != tc.state {
			t.Errorf("%s -> state %q, want %q", tc.event, event.State, tc.state)
		}
		if !ValidActivityEvent(event) {
			t.Errorf("%s should be a valid opencode activity event", tc.event)
		}
	}
}

func TestValidActivityEventAcceptsInteractiveSourceMarker(t *testing.T) {
	if !ValidActivityEvent(ActivityEvent{
		Harness:         "codex",
		Event:           "stop",
		State:           "idle",
		SourceInterface: "tui",
	}) {
		t.Fatal("interactive TUI source marker should be valid")
	}
}

func TestValidActivityEventRejectsUnknownSourceMarker(t *testing.T) {
	if ValidActivityEvent(ActivityEvent{
		Harness:         "codex",
		Event:           "stop",
		State:           "idle",
		SourceInterface: "chat",
	}) {
		t.Fatal("unknown source marker should be rejected")
	}
}

func TestCodexInterruptReportsIdle(t *testing.T) {
	event, ok := ActivityEventFromHook("codex", "interrupt", []byte(`{"turn_id":"turn-1"}`))
	if !ok || event.State != contract.ActivityIdle || !ValidActivityEvent(event) {
		t.Fatalf("Codex interrupt event = %+v, reported=%t; want valid idle activity", event, ok)
	}
}
