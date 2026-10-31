package conpty

import (
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/claudecode"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Styled output from this runtime is the host emulator's re-encoding of each
// cell, not Claude's own bytes. A Claude Code suggestion under Claude's cursor
// (reversed first letter, dim rest) must still read as an empty composer after
// that re-encoding, and a typed draft under the same cursor must not.
func TestRenderedSurfaceKeepsClaudeSuggestionUnderCursorEmpty(t *testing.T) {
	rule := strings.Repeat("─", 100)
	for _, tt := range []struct {
		name, prompt string
		want         ports.TerminalComposerState
	}{
		{"suggestion under the cursor", "❯ \x1b[7mg\x1b[27m\x1b[2mo TD 2s fix\x1b[22m", ports.TerminalComposerEmpty},
		{"draft under the cursor", "❯ \x1b[7mg\x1b[27mo TD 2s fix", ports.TerminalComposerDraft},
	} {
		t.Run(tt.name, func(t *testing.T) {
			surface := newRenderedSurface(100, 12)
			surface.Write([]byte("\x1b[2J\x1b[H⏺ placeholder reply\r\n\r\n" + rule + "\r\n" + tt.prompt + "\r\n" + rule +
				"\r\nuser@host project | model | ctx 19%\r\n-- INSERT -- ⏵⏵ auto mode on (shift+tab to cycle)"))
			screen := surface.Tail(200)
			if got := claudecode.New().InspectTerminalSurface(screen).Composer; got != tt.want {
				for _, row := range strings.Split(screen, "\n") {
					if strings.Contains(row, "TD 2s") {
						t.Logf("rendered prompt row: %q", row)
					}
				}
				t.Fatalf("composer = %v, want %v", got, tt.want)
			}
		})
	}
}
