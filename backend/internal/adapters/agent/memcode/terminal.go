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

// PromptReadinessHints implements the native TUI adapter contract.
func (*Plugin) PromptReadinessHints(ctx context.Context, _ ports.LaunchConfig) (ports.PromptReadinessHints, error) {
	return ports.PromptReadinessHints{Timeout: 30 * time.Second, PollInterval: 100 * time.Millisecond, Lines: 100, Patterns: []string{"→  Ask memcode…   ·   $ = shell"}}, ctx.Err()
}

// ComposerIsEmpty implements the native TUI adapter contract.
func (*Plugin) ComposerIsEmpty(output string) bool { return emptyComposer(output) }

// ContinuouslyDetectTerminalActivity implements the native TUI adapter contract.
func (*Plugin) ContinuouslyDetectTerminalActivity() bool { return true }

var approvalOption = regexp.MustCompile(`(?m)^\s*(?:❯\s*)?1\. (?:Yes|Execute)(?:\s|$)`)

var thinkingLine = regexp.MustCompile(`^[⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏] Thinking… \([^\n]*esc to interrupt\)$`)

// DetectTerminalActivity implements the native TUI adapter contract.
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
	// Only indicators after the newest decision cue can settle that card.
	// Earlier status lines belong to the historical transcript.
	approval := strings.LastIndex(text, "Do you want to proceed?")
	question := strings.LastIndex(text, "↑↓ select · Enter · or type your own answer · Esc to skip")
	plan := strings.LastIndex(text, "↑↓ select · Enter · type to revise · Esc cancel")
	cue := max(approval, question, plan)
	if cue < 0 {
		return false
	}
	current := text[cue:]
	for _, line := range strings.Split(current, "\n") {
		line = strings.TrimSpace(line)
		if line == "○ idle" || thinkingLine.MatchString(line) {
			return false
		}
	}
	if cue == question || cue == plan {
		return true
	}
	return approvalOption.MatchString(current)
}
