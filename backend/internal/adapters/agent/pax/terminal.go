package pax

import (
	"context"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/terminalui"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Pax's live editor is a box without side borders at the bottom of the
// screen (pydantic_ai_pax/ui/prompt/live_prompt.py):
//
//	─ Working ⠋ 12s · Esc to interrupt ─────────   title rule; all `─` when idle
//	❯ draft text                                   the editor, `❯` on its first row
//	  draft continuation, completion popup rows
//	─────────────────────────────────────────────   bottom rule
//	model · workspace · context …                  optional pinned row, then the toolbar
//
// A question the model or Pax asks (an `ask_user` question, a tool approval in
// `auto` or `read-only` mode, the first-run folder trust question) replaces the
// editor with a menu whose hint row ends in "Esc decline" or "Ctrl+C decline".
// A tool approval's choices are "Approve", "Approve for this session" and "Deny".
const (
	editorMarker     = "❯"
	ruleRune         = '─'
	workingTitle     = "─ Working "
	statusWorking    = "> Working "
	statusInterrupt  = "Ctrl+C to interrupt"
	questionDecline  = "Esc decline"
	questionCtrlC    = "Ctrl+C decline"
	approvalChoice   = "Approve for this session"
	trustQuestion    = "Do you trust the files in this folder?"
	editorAfterARule = "─\n" + editorMarker
)

// bottomScan bounds how far up from the last non-blank row the current chrome
// is looked for, so transcript text cannot satisfy a check. The editor box is
// at most a third of the screen; the menu rows sit above their hint row.
const bottomScan = 16

var (
	_ ports.AgentPromptReadinessProvider       = (*Plugin)(nil)
	_ ports.ContinuousTerminalActivityDetector = (*Plugin)(nil)
	_ ports.TerminalActivityOptionsProvider    = (*Plugin)(nil)
	_ ports.EmptyComposerDetector              = (*Plugin)(nil)
)

// PromptReadinessHints holds the first task until the editor box is drawn: a
// rule directly above the `❯` row, which a shell prompt does not produce.
// Pax starts Python and loads its plugins first, and keys typed before its
// editor owns the terminal are lost. The folder trust question, asked once
// per repository, holds delivery until the user answers it.
func (*Plugin) PromptReadinessHints(ctx context.Context, _ ports.LaunchConfig) (ports.PromptReadinessHints, error) {
	return ports.PromptReadinessHints{
		Timeout:          60 * time.Second,
		PollInterval:     200 * time.Millisecond,
		Lines:            80,
		RenderedScreen:   true,
		Patterns:         []string{editorAfterARule},
		BlockingPatterns: []string{trustQuestion},
	}, ctx.Err()
}

// ContinuouslyDetectTerminalActivity reports that the screen is the source of
// every transition: AO installs no hooks for Pax.
func (*Plugin) ContinuouslyDetectTerminalActivity() bool { return true }

// RequiresRenderedActivity asks for the rendered screen: Pax repaints its
// reserved rows in place, so they never form whole lines in the raw stream.
func (*Plugin) RequiresRenderedActivity() bool { return true }

// TerminalActivityOptions declares a tool approval as a native decision.
func (*Plugin) TerminalActivityOptions() ports.TerminalActivityOptions {
	return ports.TerminalActivityOptions{SupportsBlocked: true}
}

// ComposerIsEmpty reports an idle editor that holds no draft.
func (*Plugin) ComposerIsEmpty(output string) bool {
	box, ok := editorBox(bottomLines(output))
	return ok && !box.working && box.empty
}

// DetectTerminalActivity reads the current chrome. A typed draft, an open
// popup or an unrecognized frame is unknown.
func (*Plugin) DetectTerminalActivity(output string) (domain.ActivityState, bool) {
	lines := bottomLines(output)
	if question, approval := questionMenu(lines); question {
		if approval {
			return domain.ActivityBlocked, true
		}
		return domain.ActivityWaitingInput, true
	}
	if statusBox(lines) {
		return domain.ActivityActive, true
	}
	box, ok := editorBox(lines)
	switch {
	case !ok:
		return "", false
	case box.working:
		return domain.ActivityActive, true
	case box.empty:
		return domain.ActivityIdle, true
	}
	return "", false
}

// bottomLines returns the last bottomScan rows up to the last non-blank one,
// ANSI stripped and right-trimmed.
func bottomLines(output string) []string {
	lines := terminalui.PlainTerminalLines(strings.ReplaceAll(output, "\r\n", "\n"))
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " \t")
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) > bottomScan {
		lines = lines[len(lines)-bottomScan:]
	}
	return lines
}

type editorState struct {
	working bool
	empty   bool
}

// editorBox finds the editor closest to the bottom: a title rule, the `❯`
// row, any continuation rows, and a bottom rule with at most two rows (the
// pinned row and the toolbar) under it.
func editorBox(lines []string) (editorState, bool) {
	for i := len(lines) - 1; i >= 1; i-- {
		if !strings.HasPrefix(lines[i], editorMarker) {
			continue
		}
		title := lines[i-1]
		if !isRule(title) && !strings.HasPrefix(title, workingTitle) {
			return editorState{}, false
		}
		bottom := -1
		for j := i + 1; j < len(lines); j++ {
			if isRule(lines[j]) {
				bottom = j
				break
			}
		}
		if bottom < 0 || len(lines)-1-bottom > 2 {
			return editorState{}, false
		}
		draft := strings.TrimSpace(strings.TrimPrefix(lines[i], editorMarker)) == "" && bottom == i+1
		return editorState{working: strings.HasPrefix(title, workingTitle), empty: draft}, true
	}
	return editorState{}, false
}

// isRule reports a row drawn only with `─`.
func isRule(line string) bool {
	if line == "" {
		return false
	}
	for _, r := range line {
		if r != ruleRune {
			return false
		}
	}
	return true
}

// questionMenu reports a question menu whose hint row is one of the last
// rows, and whether it is a tool approval.
func questionMenu(lines []string) (question, approval bool) {
	hint := -1
	for i := len(lines) - 1; i >= 0 && i >= len(lines)-3; i-- {
		if strings.HasSuffix(lines[i], questionDecline) || strings.HasSuffix(lines[i], questionCtrlC) {
			hint = i
			break
		}
	}
	if hint < 0 {
		return false, false
	}
	for _, line := range lines[:hint] {
		if strings.Contains(line, approvalChoice) {
			return true, true
		}
	}
	return true, false
}

// statusBox reports the boxed "> Working … Ctrl+C to interrupt" row Pax
// draws at the bottom while a turn runs with the editor closed.
func statusBox(lines []string) bool {
	for i := len(lines) - 1; i >= 0 && i >= len(lines)-3; i-- {
		row := strings.Trim(lines[i], "│ ")
		if strings.HasPrefix(row, statusWorking) && strings.Contains(row, statusInterrupt) {
			return true
		}
	}
	return false
}
