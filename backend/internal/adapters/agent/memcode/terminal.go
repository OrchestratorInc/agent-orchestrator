package memcode

import (
	"context"
	"regexp"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/terminalui"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func (*Plugin) PromptReadinessHints(ctx context.Context, _ ports.LaunchConfig) (ports.PromptReadinessHints, error) {
	return ports.PromptReadinessHints{Timeout: 30 * time.Second, PollInterval: 100 * time.Millisecond, Lines: 100, Patterns: []string{"→  Ask memcode…   ·   $ = shell"}}, ctx.Err()
}
func (*Plugin) ComposerIsEmpty(output string) bool       { return emptyComposer(output) }
func (*Plugin) ContinuouslyDetectTerminalActivity() bool { return true }

var thinkingLine = regexp.MustCompile(`^[⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏] Thinking… \([^\n]*esc to interrupt\)$`)

func (*Plugin) DetectTerminalActivity(output string) (domain.ActivityState, bool) {
	if decisionCard(output) {
		return domain.ActivityBlocked, true
	}
	if emptyComposer(output) {
		return domain.ActivityIdle, true
	}
	text := strings.TrimSpace(terminalui.PlainTerminalText(output))
	lines := strings.Split(text, "\n")
	if len(lines) > 16 {
		lines = lines[len(lines)-16:]
	}
	// Native indicators must be in current chrome, never historical transcript.
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if thinkingLine.MatchString(line) {
			return domain.ActivityActive, true
		}
	}
	return "", false
}

func decisionCard(output string) bool {
	text := strings.TrimSpace(terminalui.PlainTerminalText(output))
	lines := strings.Split(text, "\n")
	if len(lines) > 64 {
		lines = lines[len(lines)-64:]
	}
	text = strings.Join(lines, "\n")
	footer := -1
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "memcode · ") {
			footer = i
		}
	}
	if footer < 0 {
		return false
	}
	lines = lines[:footer]
	text = strings.Join(lines, "\n")
	if strings.Contains(text, "○ idle") || thinkingLine.MatchString(strings.TrimSpace(text)) {
		return false
	}
	if strings.Contains(text, "↑↓ select · Enter · or type your own answer · Esc to skip") || strings.Contains(text, "↑↓ select · Enter · type to revise · Esc cancel") {
		return true
	}
	return strings.Contains(text, "Do you want to proceed?") && (strings.Contains(text, "❯ 1. Yes") || strings.Contains(text, "❯ 1. Execute"))
}
