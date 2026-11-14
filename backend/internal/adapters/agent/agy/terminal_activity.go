package agy

import (
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/terminalui"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var _ ports.TerminalSurfaceInspector = (*Plugin)(nil)

const agyBottomScan = 14

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
