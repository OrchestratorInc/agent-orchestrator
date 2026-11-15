package muse

import (
	"regexp"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

var museTerminalEscape = regexp.MustCompile(`\x1b(?:\[[\x30-\x3f]*[\x20-\x2f]*[\x40-\x7e]|\][^\x07]*(?:\x07|\x1b\\))`)

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

func (p *Plugin) ContinuouslyDetectTerminalActivity() bool { return true }

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
