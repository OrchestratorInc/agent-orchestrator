package sessionmanager

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	claudeagent "github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/claudecode"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// A send to an idle Claude session that shows a prompt suggestion under
// Claude's own cursor (reversed first letter, dim rest) must be delivered, and
// a typed draft under the same cursor must still be refused. This runs the
// real Claude inspector behind the send composer check.
func TestSend_ClaudeSuggestionUnderCursorDelivers(t *testing.T) {
	rule := strings.Repeat("─", 100)
	screen := func(prompt string) string {
		return "⏺ placeholder reply\n\n" + rule + "\n" + prompt + "\n" + rule +
			"\nuser@host project | model | ctx 19%\n-- INSERT -- ⏵⏵ auto mode on (shift+tab to cycle)"
	}
	for _, tt := range []struct {
		name, prompt string
		deliver      bool
	}{
		{"suggestion under the cursor", "❯ \x1b[7mg\x1b[27m\x1b[2mo TD 2s fix\x1b[22m", true},
		{"draft under the cursor", "❯ \x1b[7mg\x1b[27mo TD 2s fix", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			st := newFakeStore()
			st.sessions["s1"] = pastStartupGate(domain.SessionRecord{
				ID: "s1", Harness: domain.HarnessClaudeCode, Mode: domain.SessionModeTUI,
				Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: time.Now()},
				Metadata: domain.SessionMetadata{RuntimeHandleID: "h1"},
			})
			msg := &fakeMessenger{}
			m := newSendTestManager(t, claudeagent.New(), msg, st)
			frame := screen(tt.prompt)
			m.runtime = &transitionRuntime{fakeRuntime: &fakeRuntime{}, outputForCall: func(int) string { return frame }}
			m.interfaceTransition = fastComposerProbe

			err := m.Send(context.Background(), "s1", "run the tests", nil)
			if tt.deliver {
				if err != nil || len(msg.msgs) != 1 {
					t.Fatalf("Send = %v with %d message(s), want delivered once", err, len(msg.msgs))
				}
				return
			}
			if !errors.Is(err, ErrComposerBusy) || len(msg.msgs) != 0 {
				t.Fatalf("Send = %v with %d message(s), want ErrComposerBusy and nothing written", err, len(msg.msgs))
			}
		})
	}
}
