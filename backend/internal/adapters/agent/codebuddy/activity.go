package codebuddy

import (
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/terminalui"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func DeriveActivityState(event string, _ []byte) (domain.ActivityState, bool) {
	switch event {
	case "user-prompt-submit", "pre-tool-use", "post-tool-use", "post-tool-use-failure":
		return domain.ActivityActive, true
	case "permission-request":
		return domain.ActivityWaitingInput, true
	case "stop":
		return domain.ActivityIdle, true
	default:
		// SessionStart proves native identity, not initialized input readiness.
		// Conversation-end is not proof that the native process exited.
		return "", false
	}
}

func (*Plugin) ContinuouslyDetectTerminalActivity() bool { return true }

func (p *Plugin) ValidateTerminalMessage(output, message string) error {
	if err := validateTask(message); err != nil {
		return err
	}
	observation := p.InspectTerminalSurface(output)
	if observation.Work != ports.TerminalSurfaceWorkIdle || observation.Composer != ports.TerminalComposerEmpty {
		return errors.Join(ports.ErrTerminalMessageNotReady, errors.New("codebuddy requires an initialized empty composer; resolve native decisions or your draft in the terminal"))
	}
	return nil
}

func (p *Plugin) ComposerIsEmpty(output string) bool {
	o := p.InspectTerminalSurface(output)
	return o.Work == ports.TerminalSurfaceWorkIdle && o.Composer == ports.TerminalComposerEmpty
}

func (p *Plugin) DetectTerminalActivity(output string) (domain.ActivityState, bool) {
	switch p.InspectTerminalSurface(output).Work {
	case ports.TerminalSurfaceWorkActive:
		return domain.ActivityActive, true
	case ports.TerminalSurfaceWorkIdle:
		return domain.ActivityIdle, true
	case ports.TerminalSurfaceWorkWaitingInput:
		return domain.ActivityWaitingInput, true
	default:
		return "", false
	}
}

// Interpret only the caller's current rendered viewport. A raw ring tail can
// retain overwritten spinner/composer frames and must never authorize input.
func (*Plugin) InspectTerminalSurface(output string) ports.TerminalSurfaceObservation {
	output = strings.TrimRight(output, "\r\n ")
	lines := terminalui.PlainTerminalLines(output)
	start := max(0, len(lines)-24)
	recent := strings.Join(lines[start:], "\n")
	for _, cue := range []string{"Do you want to", "Would you like to", "Select login method:", "Enter to confirm", "Enter to login", "No, and tell CodeBuddy", "Allow this", "Do you trust the files", "Enter to select"} {
		if strings.Contains(recent, cue) {
			return ports.TerminalSurfaceObservation{Work: ports.TerminalSurfaceWorkWaitingInput}
		}
	}
	composer := terminalui.LastBorderedPromptComposerState(output, ">")
	o := ports.TerminalSurfaceObservation{}
	switch composer {
	case terminalui.ComposerEmpty:
		o.Composer = ports.TerminalComposerEmpty
	case terminalui.ComposerDraft:
		o.Composer = ports.TerminalComposerDraft
	default:
		return o
	}
	// Require real footer chrome and an adjacent current framed composer.
	prompt := -1
	for i := len(lines) - 1; i >= start; i-- {
		if strings.HasPrefix(strings.TrimSpace(lines[i]), ">") {
			prompt = i
			break
		}
	}
	if prompt < 0 {
		return ports.TerminalSurfaceObservation{}
	}
	footer := strings.Join(lines[prompt+1:], "\n")
	if !strings.Contains(footer, "← for agents") && !strings.Contains(footer, "? for shortcuts") {
		return ports.TerminalSurfaceObservation{}
	}
	upper := prompt - 1
	for upper >= 0 && strings.TrimSpace(lines[upper]) == "" {
		upper--
	}
	if upper < 0 || !horizontalRule(lines[upper]) {
		return ports.TerminalSurfaceObservation{}
	}
	for i := upper - 1; i >= max(0, upper-5); i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" || strings.HasPrefix(line, "└ Tip:") {
			continue
		}
		first, _ := utf8.DecodeRuneInString(line)
		if strings.ContainsRune("✶✸✹✺✷", first) && strings.Contains(line, "…") && strings.Contains(line, "(") {
			o.Work = ports.TerminalSurfaceWorkActive
			return o
		}
		break
	}
	o.Work = ports.TerminalSurfaceWorkIdle
	return o
}

func horizontalRule(line string) bool {
	line = strings.TrimSpace(line)
	return utf8.RuneCountInString(line) >= 8 && strings.Trim(line, "─━") == ""
}
