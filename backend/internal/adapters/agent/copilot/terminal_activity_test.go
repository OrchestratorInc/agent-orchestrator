package copilot

import (
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Frames captured from Copilot CLI 1.0.93 in a 120-column terminal.
var (
	fixtureRule = strings.Repeat("─", 120)
	fixtureIdle = strings.Join([]string{
		" ● Selected custom agent: ao-probe",
		"",
		" /private/tmp/ws          Session: 0.56 AIC reported (partial)",
		fixtureRule,
		"❯",
		fixtureRule,
		" ← open sidebar · Interactive · Allow All · / commands · ? help · tab next tab     ao-probe · Auto",
		"",
	}, "\n")
	fixtureWorking = strings.Join([]string{
		" /private/tmp/ws          Session: 0.56 AIC reported (partial)",
		fixtureRule,
		"❯",
		fixtureRule,
		" ◉ Working esc edit prompt                                ao-probe · Auto → gpt-6-luna",
	}, "\n")
	fixtureDraft = strings.Join([]string{
		fixtureRule,
		"❯ Write a 300 word essay about rivers",
		fixtureRule,
		" Interactive · Allow All · @ files · # issues                    ao-probe · Auto",
	}, "\n")
	fixtureApproval = strings.Join([]string{
		" $ Shell Remove victim.txt                                  19s",
		"╭────────────────────────────────────────╮",
		"│ Do you want to run this command?       │",
		"│ ❯ 1. Yes                               │",
		"│   3. No, and tell Copilot what to do differently (Esc to stop) │",
		"│ ↑/↓ to navigate · enter to select · esc to cancel │",
		"╰────────────────────────────────────────╯",
	}, "\n")
)

// Idle footers Copilot truncates in 80- and 45-column panes.
var fixtureNarrowIdle = []string{
	strings.Join([]string{strings.Repeat("─", 80), "❯", strings.Repeat("─", 80),
		" ← open sidebar · Interactive · Allow All · / commands          ao-probe · Auto"}, "\n"),
	strings.Join([]string{strings.Repeat("─", 45), "❯", strings.Repeat("─", 45),
		" Interactive · Allow All · / ao-probe · Auto"}, "\n"),
}

// Block-style frames as Copilot CLI 1.0.96 renders them in AO's own terminal,
// taken from GetStyledOutput of a live pty-host (width shortened).
var (
	blockTop     = "\x1b[38;2;129;139;152m╻\x1b[38;2;20;27;34m" + strings.Repeat("▄", 60) + "\x1b[m"
	blockBottom  = "\x1b[38;2;129;139;152m╹\x1b[38;2;20;27;34m" + strings.Repeat("▀", 60) + "\x1b[m"
	blockFooter  = " \x1b[38;2;145;152;161;1m←\x1b[22m open sidebar\x1b[m \x1b[38;2;145;152;161m· \x1b[mInteractive · Manual Approval \x1b[38;2;145;152;161m· \x1b[1m/\x1b[22m commands · \x1b[1m?\x1b[22m help\x1b[m      ao-personal-portfolio-1 · Auto"
	fixtureBlock = strings.Join([]string{
		" \x1b[38;2;91;156;255m●\x1b[m \x1b[38;2;190;190;190mSelected custom agent: ao-personal-portfolio-1\x1b[m        \x1b[38;2;240;246;252m┃\x1b[m",
		"",
		" \x1b[38;2;145;152;161m~/.ao/dev/.../personal_por-orchestrator\x1b[m     \x1b[38;2;145;152;161mSession: 1.29 AIC used\x1b[m",
		blockTop,
		"\x1b[38;2;129;139;152m┃\x1b[39;48;2;20;27;34m" + strings.Repeat(" ", 60) + "\x1b[m",
		blockBottom,
		blockFooter,
	}, "\n")
	fixtureBlockDraft   = strings.Join([]string{blockTop, "\x1b[38;2;129;139;152m┃\x1b[m explain the repo", blockBottom, blockFooter}, "\n")
	fixtureBlockWorking = strings.Join([]string{blockTop, "\x1b[38;2;129;139;152m┃\x1b[m", blockBottom, " ◉ Working esc edit prompt      ao · Auto"}, "\n")
)

func TestInspectTerminalSurface(t *testing.T) {
	p := New()
	for name, tc := range map[string]struct {
		frame    string
		work     ports.TerminalSurfaceWorkState
		composer ports.TerminalComposerState
	}{
		"rules idle":     {fixtureIdle, ports.TerminalSurfaceWorkIdle, ports.TerminalComposerEmpty},
		"rules working":  {fixtureWorking, ports.TerminalSurfaceWorkActive, ports.TerminalComposerEmpty},
		"rules draft":    {fixtureDraft, ports.TerminalSurfaceWorkUnknown, ports.TerminalComposerDraft},
		"blocks idle":    {fixtureBlock, ports.TerminalSurfaceWorkIdle, ports.TerminalComposerEmpty},
		"blocks draft":   {fixtureBlockDraft, ports.TerminalSurfaceWorkUnknown, ports.TerminalComposerDraft},
		"blocks working": {fixtureBlockWorking, ports.TerminalSurfaceWorkActive, ports.TerminalComposerEmpty},
		"approval":       {fixtureApproval, ports.TerminalSurfaceWorkWaitingInput, ports.TerminalComposerUnknown},
		"raw ring bytes": {"\x1b[148C┃\r\r\n\x1b[148C┃\r\r\n", ports.TerminalSurfaceWorkUnknown, ports.TerminalComposerUnknown},
		"empty":          {"", ports.TerminalSurfaceWorkUnknown, ports.TerminalComposerUnknown},
	} {
		got := p.InspectTerminalSurface(tc.frame)
		if got.Work != tc.work || got.Composer != tc.composer {
			t.Errorf("%s: work=%v composer=%v, want work=%v composer=%v", name, got.Work, got.Composer, tc.work, tc.composer)
		}
	}
}

func TestInspectTerminalSurfaceNarrowIdle(t *testing.T) {
	p := New()
	for _, frame := range fixtureNarrowIdle {
		if got := p.InspectTerminalSurface(frame); got.Work != ports.TerminalSurfaceWorkIdle {
			t.Errorf("narrow idle frame: work=%v, want idle:\n%s", got.Work, frame)
		}
	}
}

// Message delivery and the activity observer feed these capabilities the raw
// output stream, which never shows Copilot's composer. Declaring them would
// stall delivery to a Copilot terminal.
func TestPluginDoesNotDeclareRawOutputDetectors(t *testing.T) {
	var plugin any = New()
	if _, ok := plugin.(ports.TerminalActivityDetector); ok {
		t.Error("Copilot must not implement TerminalActivityDetector")
	}
	if _, ok := plugin.(ports.EmptyComposerDetector); ok {
		t.Error("Copilot must not implement EmptyComposerDetector")
	}
}
