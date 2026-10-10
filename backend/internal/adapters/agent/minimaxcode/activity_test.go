package minimaxcode

import (
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"testing"
)

const footer = "  workspace │ Full access │ ✦ glm-5.3-flash · Thinking On\n"
const composer = "    ─────────────────────\n  ›  Ask Mcode to do anything\n    ─────────────────────\n\n" + footer

func TestTerminalActivity(t *testing.T) {
	p := New()
	for _, tc := range []struct {
		name, text string
		state      domain.ActivityState
		empty      bool
	}{
		{"settled", "Message · Enter send · Ctrl+J newline    Tip: help\n" + composer, domain.ActivityIdle, true},
		{"active empty composer", "Loading 2s · Alt+Enter queue · Enter steer · Esc stop\n" + composer, domain.ActivityActive, false},
		{"cancelled draft", "Stopped · message restored to the Composer.\n───\n› cancelled prompt\n" + footer, domain.ActivityIdle, false},
		{"typed draft", "Prompt · Enter send · Ctrl+J newline\n───\n› user draft\n" + footer, "", false},
		{"loading", "Message · Enter send · Ctrl+J newline\n───\n› Ask Mcode to do anything\n───\nworkspace │ loading\n", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state, ok := p.DetectTerminalActivity(tc.text)
			if state != tc.state || ok != (tc.state != "") {
				t.Fatalf("state=%q,%v want %q", state, ok, tc.state)
			}
			if got := p.ComposerIsEmpty(tc.text); got != tc.empty {
				t.Fatalf("empty=%v want %v", got, tc.empty)
			}
		})
	}
}
func TestAuthPresenceIsNotAuthorization(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{`{"providers":[{"active":true,"enabled":true,"hasApiKey":true,"status":{"state":"available"}}]}`, "configured"},
		{`{"providers":[{"active":false,"enabled":true,"hasApiKey":true}]}`, "unknown"},
		{`not-json`, "unknown"},
	} {
		if got := providerAuthStatus([]byte(tc.input)); string(got) != tc.want {
			t.Fatalf("status=%q want %q", got, tc.want)
		}
	}
}
