package mastracode

import (
	"errors"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/terminalui"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// ValidateTerminalMessage refuses pending native decisions and user drafts.
func (p *Plugin) ValidateTerminalMessage(output, message string) error {
	if err := validateTask(message); err != nil {
		return err
	}
	if !p.ComposerIsEmpty(output) {
		return errors.New("Mastra requires an initialized empty composer; resolve native decisions or submit/clear your draft in the terminal")
	}
	return nil
}

// DeriveActivityState maps native callbacks without inferring process exit from
// conversation-end or an interrupt acknowledgement.
func DeriveActivityState(event string, _ []byte) (domain.ActivityState, bool) {
	switch event {
	case "session-start", "stop":
		return domain.ActivityIdle, true
	case "user-prompt-submit", "agent-start", "permission-result":
		return domain.ActivityActive, true
	case "permission-request":
		return domain.ActivityWaitingInput, true
	default:
		return "", false
	}
}

// ExitDetectionMode delegates CLI exit ownership to AO's existing supervisor.
func (*Plugin) ExitDetectionMode() ports.AgentExitDetectionMode {
	return ports.AgentExitDetectionSupervisor
}

// ContinuouslyDetectTerminalActivity samples current rendered native chrome.
func (*Plugin) ContinuouslyDetectTerminalActivity() bool { return true }

func pendingDecision(output string) bool {
	for _, marker := range []string{"Tool Approval Required", "Allow?", "Grant sandbox access", "Enter to select", "Enter select", "Enter to confirm", "Enter to submit", "Enter to add", "Esc to skip", "Esc reject", "Thread not found:"} {
		if strings.Contains(output, marker) {
			return true
		}
	}
	return false
}

// ComposerIsEmpty requires the native framed composer and model footer. It
// refuses native decisions and ordinary draft content, including wrapped rows.
func (*Plugin) ComposerIsEmpty(output string) bool {
	plain := terminalui.PlainTerminalText(output)
	if pendingDecision(plain) {
		return false
	}
	lines := strings.Split(plain, "\n")
	footer := -1
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.HasPrefix(strings.TrimSpace(lines[i]), "▐") && strings.Contains(lines[i], "▌") {
			footer = i
			break
		}
	}
	if footer < 3 {
		return false
	}
	bottom := strings.TrimSpace(lines[footer-1])
	if !strings.HasPrefix(bottom, "╰─") || !strings.HasSuffix(bottom, "╯") {
		return false
	}
	for i := footer - 2; i >= 0 && i >= footer-12; i-- {
		line := strings.TrimSpace(lines[i])
		if strings.HasPrefix(line, "╭─") {
			return false
		}
		if !strings.HasPrefix(line, "│") || !strings.HasSuffix(line, "│") {
			return false
		}
		content := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(line, "│"), "│"))
		if strings.HasPrefix(content, "›") {
			return strings.TrimSpace(strings.TrimPrefix(content, "›")) == "" && i > 0 && strings.HasPrefix(strings.TrimSpace(lines[i-1]), "╭─")
		}
		if content != "" {
			return false
		}
	}
	return false
}

// DetectTerminalActivity never treats an old ring tail as current UI evidence.
func (p *Plugin) DetectTerminalActivity(output string) (domain.ActivityState, bool) {
	plain := terminalui.PlainTerminalText(output)
	if pendingDecision(plain) {
		return domain.ActivityWaitingInput, true
	}
	if p.ComposerIsEmpty(output) {
		return domain.ActivityIdle, true
	}
	lines := strings.Split(plain, "\n")
	start := max(0, len(lines)-8)
	for _, line := range lines[start:] {
		if strings.HasPrefix(strings.TrimSpace(line), "│ •") {
			return domain.ActivityActive, true
		}
	}
	return "", false
}
