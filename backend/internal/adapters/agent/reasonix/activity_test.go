package reasonix

import (
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestReasonixActivityRequiresNativeMainSession(t *testing.T) {
	tests := []struct {
		name, event, payload string
		want                 domain.ActivityState
		ok                   bool
	}{
		{"prompt", "user-prompt-submit", `{"event":"UserPromptSubmit","sessionId":"4a98d817b5b31aaea8c4c33331ba3a34","prompt":"hello","turn":1}`, domain.ActivityActive, true},
		{"pre tool", "pre-tool-use", `{"event":"PreToolUse","sessionId":"native-1","toolName":"bash","toolArgs":{"command":"secret"}}`, domain.ActivityActive, true},
		{"post tool", "post-tool-use", `{"event":"PostToolUse","sessionId":"native-1","toolName":"bash","toolResult":"private"}`, domain.ActivityActive, true},
		{"successful stop", "stop", `{"event":"Stop","sessionId":"native-1","turn":1,"lastAssistantText":"private"}`, domain.ActivityWaitingInput, true},
		{"failed stop", "stop", `{"event":"Stop","sessionId":"native-1","error":"failed"}`, "", false},
		{"interrupted stop", "stop", `{"event":"Stop","sessionId":"native-1","isInterrupt":true}`, "", false},
		{"start metadata only", "session-start", `{"event":"SessionStart","sessionId":"native-1","source":"startup"}`, "", false},
		{"permission without correlation", "permission-request", `{"event":"PermissionRequest","sessionId":"native-1","toolName":"bash","subject":"secret"}`, "", false},
		{"rotation not process death", "session-end", `{"event":"SessionEnd","sessionId":"native-1","reason":"clear"}`, "", false},
		{"subagent", "stop", `{"event":"Stop","sessionId":"subagent:task-123"}`, "", false},
		{"planner", "stop", `{"event":"Stop","sessionId":"native-1:planner"}`, "", false},
		{"fallback child", "stop", `{"event":"Stop","sessionId":"subagent"}`, "", false},
		{"mismatched event", "stop", `{"event":"PreToolUse","sessionId":"native-1"}`, "", false},
		{"missing event", "stop", `{"sessionId":"native-1"}`, "", false},
		{"missing identity", "stop", `{"event":"Stop"}`, "", false},
		{"malformed", "stop", `{`, "", false},
		{"null", "stop", `null`, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := DeriveActivityState(tt.event, []byte(tt.payload))
			if got != tt.want || ok != tt.ok {
				t.Fatalf("got (%q,%v), want (%q,%v)", got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestReasonixSessionIdentity(t *testing.T) {
	for _, id := range []string{"4a98d817b5b31aaea8c4c33331ba3a34", "native-1", "20260926-150405.123456789-model"} {
		if !ValidSessionID(id) {
			t.Errorf("rejected %q", id)
		}
		if got := HookSessionID([]byte(`{"sessionId":"` + id + `"}`)); got != id {
			t.Errorf("identity=%q, want %q", got, id)
		}
	}
	for _, id := range []string{"", "../escape", "-option", ".hidden", "trailing.", "CON", "com1.txt", "root:planner", "subagent:task", "subagent", "planner", " spaces ", "tab\tvalue", strings.Repeat("x", 256)} {
		if ValidSessionID(id) {
			t.Errorf("accepted %q", id)
		}
	}
	for _, payload := range []string{`{"session_id":"native-1"}`, `{"sessionId":15}`, `{"sessionId":"subagent:task"}`, `{"sessionId":" native-1 "}`, `{`} {
		if got := HookSessionID([]byte(payload)); got != "" {
			t.Errorf("identity=%q for %s", got, payload)
		}
	}
}
