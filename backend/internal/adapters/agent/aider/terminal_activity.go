package aider

import (
	"regexp"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

var (
	aiderTerminalEscape = regexp.MustCompile(`\x1b(?:\[[\x30-\x3f]*[\x20-\x2f]*[\x40-\x7e]|\][^\x07]*(?:\x07|\x1b\\))`)
	aiderPromptLine     = regexp.MustCompile(`^(?:(?:[a-z][a-z0-9-]*)(?: multi)?|multi)?>(?:[ \t](.*))?$`)
)

func (p *Plugin) ContinuouslyDetectTerminalActivity() bool { return true }

func (p *Plugin) ComposerIsEmpty(output string) bool {
	lines := aiderTerminalLines(output)
	for i := len(lines) - 1; i >= 0; i-- {
		match := aiderPromptLine.FindStringSubmatch(lines[i])
		if match != nil {
			return len(match) < 2 || strings.TrimSpace(match[1]) == ""
		}
	}
	return false
}

func (p *Plugin) EmptyComposerProvesWaitingInputReady() bool { return true }

func (p *Plugin) DetectTerminalActivity(output string) (domain.ActivityState, bool) {
	lines := aiderTerminalLines(output)
	start := len(lines) - 40
	if start < 0 {
		start = 0
	}
	for i := len(lines) - 1; i >= start; i-- {
		match := aiderPromptLine.FindStringSubmatch(lines[i])
		if match == nil {
			continue
		}
		if len(match) < 2 || strings.TrimSpace(match[1]) == "" {
			return "", false
		}
		return domain.ActivityActive, true
	}
	return "", false
}

func aiderTerminalLines(output string) []string {
	plain := aiderTerminalEscape.ReplaceAllString(strings.ReplaceAll(output, "\r", "\n"), "")
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
