package freebuff

import (
	"context"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/terminalui"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Freebuff's composer placeholder names the run state: "Enter a coding task"
// when idle, "Add to the current task" while a run is in flight (Enter steers
// the running task). The status bar shows "thinking..." or "working..." next to
// a "■ Esc" stop button during a run.
const (
	idlePlaceholder   = "Enter a coding task"
	activePlaceholder = "Add to the current task"
)

var activeStatus = []string{"thinking...", "working...", "■ Esc", "high demand — in line"}

// PromptReadinessHints waits for the idle composer before typing the first turn.
// Freebuff may hold a new session in a model queue first, so the wait is long.
func (*Plugin) PromptReadinessHints(ctx context.Context, _ ports.LaunchConfig) (ports.PromptReadinessHints, error) {
	return ports.PromptReadinessHints{
		InitialDelay: 500 * time.Millisecond,
		Timeout:      60 * time.Second,
		PollInterval: 200 * time.Millisecond,
		Lines:        60,
		Patterns:     []string{idlePlaceholder},
	}, ctx.Err()
}

// ComposerIsEmpty reports whether Freebuff's composer shows its idle placeholder.
func (*Plugin) ComposerIsEmpty(output string) bool {
	return tailContains(output, 12, idlePlaceholder)
}

// ContinuouslyDetectTerminalActivity samples Freebuff's screen every tick: it
// has no hooks, so the terminal is the only activity signal.
func (*Plugin) ContinuouslyDetectTerminalActivity() bool { return true }

// DetectTerminalActivity reads Freebuff's current composer and status bar.
func (*Plugin) DetectTerminalActivity(output string) (domain.ActivityState, bool) {
	if tailContains(output, 12, activePlaceholder) {
		return domain.ActivityActive, true
	}
	for _, marker := range activeStatus {
		if tailContains(output, 12, marker) {
			return domain.ActivityActive, true
		}
	}
	if tailContains(output, 12, idlePlaceholder) {
		return domain.ActivityIdle, true
	}
	return "", false
}

// tailContains looks only at the bottom of the screen, where Freebuff draws its
// composer and status bar, so transcript text never counts as current state.
func tailContains(output string, lines int, marker string) bool {
	all := strings.Split(strings.TrimSpace(terminalui.PlainTerminalText(output)), "\n")
	if len(all) > lines {
		all = all[len(all)-lines:]
	}
	for _, line := range all {
		if strings.Contains(line, marker) {
			return true
		}
	}
	return false
}
