package copilot

import (
	"strings"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/terminalui"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var _ ports.TerminalSurfaceInspector = (*Plugin)(nil)

const copilotFooterScan = 14

func (p *Plugin) InspectTerminalSurface(output string) ports.TerminalSurfaceObservation {
	lines := terminalui.PlainTerminalLines(strings.ReplaceAll(output, "\r\n", "\n"))
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	var observation ports.TerminalSurfaceObservation
	start := len(lines) - copilotFooterScan
	if start < 0 {
		start = 0
	}
	for _, line := range lines[start:] {
		if strings.Contains(line, "↑/↓ to navigate") || strings.Contains(line, "enter to select") {
			observation.Work = ports.TerminalSurfaceWorkWaitingInput
			return observation
		}
	}
	if len(lines) < 4 {
		return observation
	}
	n := len(lines)
	footer := strings.TrimSpace(lines[n-1])
	observation.Composer = copilotComposer(lines[n-4], lines[n-3], lines[n-2])
	if observation.Composer == ports.TerminalComposerUnknown || footer == "" {
		return ports.TerminalSurfaceObservation{}
	}
	switch {
	case copilotBusyFooter(footer):
		observation.Work = ports.TerminalSurfaceWorkActive
	case observation.Composer == ports.TerminalComposerEmpty:
		observation.Work = ports.TerminalSurfaceWorkIdle
	}
	return observation
}

func copilotComposer(top, middle, bottom string) ports.TerminalComposerState {
	top, middle, bottom = strings.TrimSpace(top), strings.TrimSpace(middle), strings.TrimSpace(bottom)
	var marker string
	switch {
	case copilotEdge(top, "", '─') && copilotEdge(bottom, "", '─'):
		marker = "❯"
	case copilotEdge(top, "╻", '▄') && copilotEdge(bottom, "╹", '▀'):
		marker = "┃"
	default:
		return ports.TerminalComposerUnknown
	}
	rest, ok := strings.CutPrefix(middle, marker)
	if !ok {
		return ports.TerminalComposerUnknown
	}
	if strings.TrimSpace(rest) == "" {
		return ports.TerminalComposerEmpty
	}
	return ports.TerminalComposerDraft
}

func copilotEdge(line, corner string, fill rune) bool {
	rest, ok := strings.CutPrefix(line, corner)
	if !ok || utf8.RuneCountInString(rest) < 8 {
		return false
	}
	for _, r := range rest {
		if r != fill {
			return false
		}
	}
	return true
}

func copilotBusyFooter(footer string) bool {
	if strings.Contains(footer, "Working") || strings.Contains(footer, "esc edit prompt") {
		return true
	}
	first, _ := utf8.DecodeRuneInString(footer)
	return strings.ContainsRune("◉◎○●◌◍◐◑◒◓", first)
}
