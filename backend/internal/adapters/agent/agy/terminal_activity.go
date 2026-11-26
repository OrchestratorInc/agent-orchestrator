package agy

import (
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/terminalui"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Antigravity deliberately implements only TerminalSurfaceInspector, which AO
// feeds the rendered screen. TerminalActivityDetector receives the raw output
// stream, where Antigravity's cursor-positioned redraws never form a composer,
// so declaring it would stall message delivery to an Antigravity terminal.
var _ ports.TerminalSurfaceInspector = (*Plugin)(nil)

// agyBottomScan bounds how far up from the bottom dialogs are looked for.
const agyBottomScan = 14

// InspectTerminalSurface reads Antigravity's rendered screen. A terminal
// resumed by a Chat to Terminal switch has no fresh Stop hook until its next
// turn, so an interface switch proves idle from the screen. Every frame that is
// not positively recognized is unknown.
//
// Antigravity CLI 1.3.4, in tmux and in AO's own terminal, draws a ">" composer
// between two "─" rules, then a footer:
//
//	idle      "? for shortcuts"
//	working   "esc to cancel", with a spinner and "Generating..." above
//	draft     "> <text>" with a footer holding only the model
//	dialogs   approvals and folder trust list choices above "↑/↓ Navigate"
func (p *Plugin) InspectTerminalSurface(output string) ports.TerminalSurfaceObservation {
	lines := terminalui.PlainTerminalLines(strings.ReplaceAll(output, "\r\n", "\n"))
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	var observation ports.TerminalSurfaceObservation
	start := max(0, len(lines)-agyBottomScan)
	for _, line := range lines[start:] {
		if strings.Contains(line, "↑/↓ Navigate") {
			observation.Work = ports.TerminalSurfaceWorkWaitingInput
			return observation
		}
	}
	if len(lines) < 4 {
		return observation
	}
	n := len(lines)
	observation.Composer = agyComposer(lines[n-4], lines[n-3], lines[n-2])
	if observation.Composer == ports.TerminalComposerUnknown {
		return ports.TerminalSurfaceObservation{}
	}
	footer := lines[n-1]
	switch {
	case strings.Contains(footer, "esc to cancel"):
		observation.Work = ports.TerminalSurfaceWorkActive
	case strings.Contains(footer, "? for shortcuts") && observation.Composer == ports.TerminalComposerEmpty:
		observation.Work = ports.TerminalSurfaceWorkIdle
	}
	return observation
}

// agyComposer reads the three composer rows: a rule, "> [draft]", a rule.
func agyComposer(top, middle, bottom string) ports.TerminalComposerState {
	if !agyRule(top) || !agyRule(bottom) {
		return ports.TerminalComposerUnknown
	}
	rest, ok := strings.CutPrefix(strings.TrimSpace(middle), ">")
	if !ok {
		return ports.TerminalComposerUnknown
	}
	if strings.TrimSpace(rest) == "" {
		return ports.TerminalComposerEmpty
	}
	return ports.TerminalComposerDraft
}

func agyRule(line string) bool {
	line = strings.TrimSpace(line)
	if len([]rune(line)) < 8 {
		return false
	}
	for _, r := range line {
		if r != '─' {
			return false
		}
	}
	return true
}
