package agy

import (
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Frames captured from Antigravity CLI 1.3.4 (rule width shortened).
var (
	agyTestRule   = strings.Repeat("─", 60)
	agyIdleFrame  = strings.Join([]string{" ┃ Verbosity now defaults to medium", " ┃ esc to dismiss", agyTestRule, ">", agyTestRule, "? for shortcuts                       Gemini 3.8 Flash · medium"}, "\n")
	agyDraftFrame = strings.Join([]string{agyTestRule, "> Use your shell tool to run exactly: rm victim.txt", agyTestRule, "                                      Gemini 3.8 Flash · medium"}, "\n")
	agyWorking    = strings.Join([]string{"⡿  Generating...", agyTestRule, ">", agyTestRule, "esc to cancel                         Gemini 3.8 Flash · medium"}, "\n")
	agyApproval   = strings.Join([]string{agyTestRule, "Requesting permission for:", "   rm victim.txt", "Run this command?", "> 1. Yes, run command", "  4. No, cancel",
		"  ↑/↓ Navigate · tab Amend · ctrl+g edit/expand command", "esc to cancel                         Gemini 3.8 Flash · medium"}, "\n")
	agyTrust = strings.Join([]string{"Do you trust the contents of this project?", "> Yes, I trust this folder", "  No, exit", "  ↑/↓ Navigate · enter Confirm"}, "\n")
)

func TestInspectTerminalSurface(t *testing.T) {
	p := New()
	styledIdle := "\x1b[90m" + agyTestRule + "\n\x1b[94m>\x1b[39m\n\x1b[90m" + agyTestRule + "\n? for shortcuts\x1b[39m      \x1b[2mGemini 3.8 Flash · medium"
	for name, tc := range map[string]struct {
		frame    string
		work     ports.TerminalSurfaceWorkState
		composer ports.TerminalComposerState
	}{
		"idle":     {agyIdleFrame, ports.TerminalSurfaceWorkIdle, ports.TerminalComposerEmpty},
		"styled":   {styledIdle, ports.TerminalSurfaceWorkIdle, ports.TerminalComposerEmpty},
		"draft":    {agyDraftFrame, ports.TerminalSurfaceWorkUnknown, ports.TerminalComposerDraft},
		"working":  {agyWorking, ports.TerminalSurfaceWorkActive, ports.TerminalComposerEmpty},
		"approval": {agyApproval, ports.TerminalSurfaceWorkWaitingInput, ports.TerminalComposerUnknown},
		"trust":    {agyTrust, ports.TerminalSurfaceWorkWaitingInput, ports.TerminalComposerUnknown},
		"raw":      {"\n\n\n\n", ports.TerminalSurfaceWorkUnknown, ports.TerminalComposerUnknown},
	} {
		got := p.InspectTerminalSurface(tc.frame)
		if got.Work != tc.work || got.Composer != tc.composer {
			t.Errorf("%s: work=%v composer=%v, want work=%v composer=%v", name, got.Work, got.Composer, tc.work, tc.composer)
		}
	}
}

// agyFrame is an Antigravity CLI 1.5 screen: a notice, the composer between
// its rules, then the footer.
func agyFrame(composer, footer string) string {
	return strings.Join([]string{" ┃ Auto is now the default agent mode", " ┃ esc to dismiss", agyTestRule, composer, agyTestRule, footer}, "\n")
}

// TestInspectTerminalSurfaceReadsAModePlaceholderAsEmpty: Antigravity CLI 1.5
// shows its agent mode's hint in an empty composer (captured from 1.5.18). It
// is an idle prompt, so account moves and interface switches can drain to it.
// A draft that only mentions a mode stays a draft.
func TestInspectTerminalSurfaceReadsAModePlaceholderAsEmpty(t *testing.T) {
	p := New()
	idleFooter := "? for shortcuts                auto · Gemini 3.8 Flash · medium"
	for _, hint := range []string{
		"Auto mode: edits & safe commands auto-approved (shift+tab to cycle)",
		"Plan mode: research & plan only (shift+tab to cycle)",
		"Accept-edits mode: file edits auto-approved (shift+tab to cycle)",
		"Best-of-N mode: parallel arms, /model to pick models (shift+tab to cycle)",
	} {
		got := p.InspectTerminalSurface(agyFrame("> "+hint, idleFooter))
		if got.Work != ports.TerminalSurfaceWorkIdle || got.Composer != ports.TerminalComposerEmpty {
			t.Errorf("%q: work=%v composer=%v, want idle with an empty composer", hint, got.Work, got.Composer)
		}
	}
	draft := p.InspectTerminalSurface(agyFrame("> Plan mode: write the migration first", "                Gemini 3.8 Flash · medium"))
	if draft.Composer != ports.TerminalComposerDraft {
		t.Errorf("a draft mentioning a mode read as %v, want a draft", draft.Composer)
	}
}

// Message delivery feeds TerminalActivityDetector the raw output stream, which
// never shows Antigravity's composer. Declaring it would stall delivery.
func TestPluginDoesNotDeclareTheRawOutputDetector(t *testing.T) {
	var plugin any = New()
	if _, ok := plugin.(ports.TerminalActivityDetector); ok {
		t.Error("Antigravity must not implement TerminalActivityDetector")
	}
}
