package openinterpreter

import (
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/terminalui"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// DetectTerminalActivity derives state from the current composer or modal.
func (p *Plugin) DetectTerminalActivity(output string) (domain.ActivityState, bool) {
	switch p.InspectTerminalSurface(output).Work {
	case ports.TerminalSurfaceWorkIdle:
		return domain.ActivityIdle, true
	case ports.TerminalSurfaceWorkActive:
		return domain.ActivityActive, true
	case ports.TerminalSurfaceWorkWaitingInput:
		return domain.ActivityWaitingInput, true
	default:
		return "", false
	}
}

// ComposerIsEmpty is conservative when current native chrome is absent.
func (p *Plugin) ComposerIsEmpty(output string) bool {
	return p.InspectTerminalSurface(output).Composer == ports.TerminalComposerEmpty
}

// InspectTerminalSurface follows Open Interpreter's Codex-derived composer
// boundary. The instructional footer's queue hint wins over a visible prompt;
// permission and question footers never imply an ordinary ready composer.
func (p *Plugin) InspectTerminalSurface(output string) ports.TerminalSurfaceObservation {
	var observation ports.TerminalSurfaceObservation
	raw := strings.Split(strings.ReplaceAll(output, "\r", "\n"), "\n")
	start := len(raw) - 18
	if start < 0 {
		start = 0
	}
	prompt := -1
	for i := len(raw) - 1; i >= start; i-- {
		line := strings.TrimSpace(terminalui.PlainTerminalText(raw[i]))
		if strings.HasPrefix(line, "›") {
			prompt = i
			break
		}
	}
	if prompt < 0 {
		return observation
	}
	foot := -1
	running := false
	for i := prompt + 1; i < len(raw); i++ {
		line := strings.ToLower(strings.TrimSpace(terminalui.PlainTerminalText(raw[i])))
		if strings.Contains(line, "enter to confirm") || strings.Contains(line, "enter to select") || strings.Contains(line, "enter to submit answer") || strings.Contains(line, "enter to submit all") || strings.Contains(line, "esc to go back") || strings.Contains(line, "enter confirm · esc skip") {
			observation.Work = ports.TerminalSurfaceWorkWaitingInput
			return observation
		}
		if strings.Contains(line, "tab to queue") {
			foot = i
			running = true
			break
		}
		if strings.HasPrefix(line, "? for shortcuts") || strings.HasPrefix(line, "← for agents · ? for shortcuts") {
			foot = i
			break
		}
	}
	if foot < 0 {
		return observation
	}
	// Native queued-input chrome can sit between the status and composer.
	// Skip only that identified block, then inspect the nearest nonblank row.
	statusBoundary := prompt
	for i := prompt - 1; i >= start; i-- {
		line := strings.TrimSpace(terminalui.PlainTerminalText(raw[i]))
		if line == "• Queued follow-up inputs" {
			statusBoundary = i
			break
		}
		if strings.HasPrefix(line, "›") {
			break
		}
	}
	for i := statusBoundary - 1; i >= start; i-- {
		line := strings.TrimSpace(terminalui.PlainTerminalText(raw[i]))
		if line == "" {
			continue
		}
		if strings.Contains(strings.ToLower(line), "esc to interrupt") {
			running = true
		}
		break
	}
	state := terminalui.LastPromptComposerState(strings.Join(raw[prompt:foot], "\n"), "›", "Ask Open Interpreter to do anything", "Ask Interpreter to do anything", "Ask Codex to do anything")
	switch state {
	case terminalui.ComposerEmpty:
		observation.Composer = ports.TerminalComposerEmpty
	case terminalui.ComposerDraft:
		observation.Composer = ports.TerminalComposerDraft
	}
	observation.Work = ports.TerminalSurfaceWorkIdle
	if running {
		observation.Work = ports.TerminalSurfaceWorkActive
	}
	return observation
}
