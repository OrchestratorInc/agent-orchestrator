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
