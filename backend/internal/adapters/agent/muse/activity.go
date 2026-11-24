package muse

import (
	"regexp"
	"strings"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/domain"
)

var museTerminalEscape = regexp.MustCompile(`\x1b(?:\[[\x30-\x3f]*[\x20-\x2f]*[\x40-\x7e]|\][^\x07]*(?:\x07|\x1b\\))`)

// DeriveActivityState maps Muse's AO hook callbacks onto activity states.
func DeriveActivityState(event string, _ []byte) (domain.ActivityState, bool) {
	switch event {
	case "user-prompt-submit":
		return domain.ActivityActive, true
	case "permission-request":
		return domain.ActivityBlocked, true
	case "stop":
		return domain.ActivityIdle, true
	case "session-start":
		return "", false
	default:
		return "", false
	}
}

// ContinuouslyDetectTerminalActivity opts Muse into terminal reconciliation
// on every observer tick. Unlike hook-driven agents, Muse's structured input
// picker emits no lifecycle callback, and its answer likewise emits no prompt
// callback, so both pause and resume must be observed from the TUI.
func (p *Plugin) ContinuouslyDetectTerminalActivity() bool { return true }

// DetectTerminalActivity recognizes authoritative states in Meta Muse's TUI.
// The newest authoritative marker wins so picker and generation text retained
// in scrollback cannot override the current TUI state.
func (p *Plugin) DetectTerminalActivity(output string) (domain.ActivityState, bool) {
	lines := museTerminalLines(output)
	if len(lines) == 0 {
		return "", false
	}
	start := len(lines) - 30
	if start < 0 {
		start = 0
	}
	recent := lines[start:]

	for i := len(recent) - 1; i >= 0; i-- {
		line := strings.ToLower(recent[i])
		if strings.HasPrefix(line, "◆ request user input") {
			return domain.ActivityWaitingInput, true
		}
		if strings.Contains(line, "· esc to interrupt") && !strings.Contains(line, "enter to select") {
			return domain.ActivityActive, true
		}
		if strings.Contains(line, "enter to select") && strings.Contains(line, "optional note") &&
			strings.Contains(line, "esc to interrupt") {
			return domain.ActivityWaitingInput, true
		}
	}

	hasComposer := false
	hasFooter := false
	for _, line := range recent {
		if line == "⟩" {
			hasComposer = true
		}
		if strings.Contains(line, " · ") && strings.Contains(line, "muse-") {
			hasFooter = true
		}
	}
	if hasComposer && hasFooter {
		return domain.ActivityIdle, true
	}
	return "", false
}

func museTerminalLines(output string) []string {
	plain := museTerminalEscape.ReplaceAllString(strings.ReplaceAll(output, "\r", "\n"), "")
	raw := strings.Split(plain, "\n")
	lines := raw[:0]
	for _, line := range raw {
		line = strings.TrimSpace(line)
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}
