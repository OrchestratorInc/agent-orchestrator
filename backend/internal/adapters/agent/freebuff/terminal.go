package freebuff

import (
	"context"
	"strings"
	"time"
	"unicode"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/terminalui"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Freebuff's composer placeholder names the run state: "Enter a coding task"
// when idle, "Add to the current task" while a run is in flight (Enter steers
// the running task). During a run the status bar row shows "thinking..." or
// "working..." with the "■ Esc" stop button on the same row.
const (
	idlePlaceholder   = "Enter a coding task"
	activePlaceholder = "Add to the current task"
	stopButton        = "■ Esc"
)

var runningStatus = []string{"thinking...", "working..."}

// tailLines covers Freebuff's composer, status bar and footer chrome.
const tailLines = 12

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
	return composerPlaceholder(screenTail(output)) == idlePlaceholder
}

// ContinuouslyDetectTerminalActivity samples Freebuff's screen every tick: it
// has no hooks, so the terminal is the only activity signal.
func (*Plugin) ContinuouslyDetectTerminalActivity() bool { return true }

// DetectTerminalActivity reads only Freebuff's current chrome: the status-bar
// row and the newest composer placeholder. Marker text inside the transcript or
// a human draft never counts.
func (*Plugin) DetectTerminalActivity(output string) (domain.ActivityState, bool) {
	lines := screenTail(output)
	if statusBarRunning(lines) {
		return domain.ActivityActive, true
	}
	switch composerPlaceholder(lines) {
	case activePlaceholder:
		return domain.ActivityActive, true
	case idlePlaceholder:
		return domain.ActivityIdle, true
	}
	return "", false
}

// PrimesAfterStartSubmit asks AO for a lone Enter into the empty composer
// before the first turn. Freebuff ignores an empty submit, and until it has
// seen a Return key it treats a bare line feed as Enter on Linux, which would
// otherwise split a multi-line turn into several messages.
func (*Plugin) PrimesAfterStartSubmit() bool { return true }

// AfterStartSubmitState reads whether the typed first turn left the composer.
// Freebuff's submit handler can read stale input when Enter lands right after
// a large burst of typed text, leaving the whole turn as a draft.
func (p *Plugin) AfterStartSubmitState(output, prompt string) ports.AfterStartSubmitState {
	if _, known := p.DetectTerminalActivity(output); known {
		// Running, or the composer is empty again: the turn was consumed.
		return ports.AfterStartSubmitted
	}
	fragment := promptTailFragment(prompt)
	if fragment != "" && strings.Contains(compact(strings.Join(screenTail(output), "")), fragment) {
		return ports.AfterStartDraftPending
	}
	return ports.AfterStartSubmitUnknown
}

func screenTail(output string) []string {
	lines := strings.Split(strings.TrimSpace(terminalui.PlainTerminalText(output)), "\n")
	if len(lines) > tailLines {
		lines = lines[len(lines)-tailLines:]
	}
	return lines
}

// composerPlaceholder returns the placeholder of the bottom-most composer row,
// or "" when the composer holds text. A row counts only when the placeholder
// starts it after the composer's border and prompt glyphs, so transcript
// sentences that merely mention the words are ignored.
func composerPlaceholder(lines []string) string {
	for i := len(lines) - 1; i >= 0; i-- {
		content := chromeContent(lines[i])
		for _, placeholder := range []string{idlePlaceholder, activePlaceholder} {
			if strings.HasPrefix(content, placeholder) {
				return placeholder
			}
		}
	}
	return ""
}

// statusBarRunning matches the status-bar row itself: a running indicator
// that starts the row together with the stop button rendered beside it.
func statusBarRunning(lines []string) bool {
	for _, line := range lines {
		if !strings.Contains(line, stopButton) {
			continue
		}
		content := chromeContent(line)
		for _, status := range runningStatus {
			if strings.HasPrefix(content, status) {
				return true
			}
		}
	}
	return false
}

// chromeContent strips the leading border, prompt glyph and padding that
// Freebuff draws around its composer and status bar.
func chromeContent(line string) string {
	return strings.TrimLeftFunc(line, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

// promptTailFragment is the end of the turn's last line, which sits beside the
// cursor in a composer still holding the draft.
func promptTailFragment(prompt string) string {
	lines := strings.Split(strings.TrimSpace(prompt), "\n")
	runes := []rune(compact(lines[len(lines)-1]))
	if len(runes) > 24 {
		runes = runes[len(runes)-24:]
	}
	return string(runes)
}

// compact drops whitespace and box-drawing characters, so a draft wrapped
// across composer rows still matches the text AO typed.
func compact(text string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || (r >= 0x2500 && r <= 0x259F) {
			return -1
		}
		return r
	}, text)
}
