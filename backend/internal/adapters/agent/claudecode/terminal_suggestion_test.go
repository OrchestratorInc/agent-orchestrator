package claudecode

import (
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Claude Code 2.1.314 draws its placeholder or prompt suggestion as
// reverse(first letter) + dim(rest) while it believes its terminal has focus,
// and all dim otherwise. The reversed letter is Claude's own cursor, not typed
// text. On 2026-10-30 four sends to an idle Claude session were refused as
// "composer holds a draft" while it showed a suggestion.
func claudeSuggestionScreen(prompt string) string {
	rule := strings.Repeat("─", 100)
	return "⏺ placeholder reply\n\n" + rule + "\n" + prompt + "\n" + rule +
		"\n⚠ status warning\nuser@host project | model | ctx 19%" +
		"\n-- INSERT -- ⏵⏵ auto mode on (shift+tab to cycle)"
}

func TestClaudeSuggestionUnderCursorReadsEmpty(t *testing.T) {
	for _, tt := range []struct {
		name, prompt string
		want         ports.TerminalComposerState
	}{
		{"suggestion under the cursor", "❯ \x1b[7mg\x1b[27m\x1b[2mo TD 2s fix\x1b[22m", ports.TerminalComposerEmpty},
		{"suggestion without the cursor", "❯ \x1b[2mgo TD 2s fix\x1b[22m", ports.TerminalComposerEmpty},
		{"draft under the cursor", "❯ \x1b[7mg\x1b[27mo TD 2s fix", ports.TerminalComposerDraft},
	} {
		t.Run(tt.name, func(t *testing.T) {
			plugin := New()
			screen := claudeSuggestionScreen(tt.prompt)
			observation := plugin.InspectTerminalSurface(screen)
			if observation.Work != ports.TerminalSurfaceWorkIdle || observation.Composer != tt.want {
				t.Fatalf("surface = work %v composer %v, want idle and %v", observation.Work, observation.Composer, tt.want)
			}
			if got, want := plugin.ComposerIsEmpty(screen), tt.want == ports.TerminalComposerEmpty; got != want {
				t.Fatalf("ComposerIsEmpty = %v, want %v", got, want)
			}
		})
	}
}
