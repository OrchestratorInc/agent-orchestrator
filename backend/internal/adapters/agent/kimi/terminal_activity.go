package kimi

import (
	"strings"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/terminalui"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var (
	_ ports.TerminalActivityDetector        = (*Plugin)(nil)
	_ ports.TerminalActivityOptionsProvider = (*Plugin)(nil)
)

// kimiSpinnerFrames are Kimi Code's activity spinners: braille while the model
// streams, thinks or compacts, and moon phases while it waits on the provider
// or a tool runs (src/tui/constant/rendering.ts).
const kimiSpinnerFrames = "⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏🌑🌒🌓🌔🌕🌖🌗🌘"

// kimiFooterLines bounds the rows under the composer: the model and directory
// row, and the context-usage row.
const kimiFooterLines = 3

// kimiActivityScanLines bounds how far above the composer a live spinner is
// looked for. Kimi draws its activity row right above the composer, but an
// AgentSwarm call moves the spinner into the swarm's block in the transcript.
const kimiActivityScanLines = 40

// kimiApprovalScanLines bounds the rows from the bottom searched for an
// approval panel's key hints: the hint row, a rule, and the footer.
const kimiApprovalScanLines = 6

// TerminalActivityOptions asks for the rendered screen. Kimi Code redraws
// changed rows in place, so the PTY host's raw output never holds a whole
// composer; the rendered viewport does on every runtime.
func (*Plugin) TerminalActivityOptions() ports.TerminalActivityOptions {
	return ports.TerminalActivityOptions{PreferStyledOutput: true}
}

// DetectTerminalActivity reads Kimi Code's TUI. Kimi's activity is hook-driven,
// and a turn whose Stop or StopFailure hook never reaches AO (a daemon restart,
// a hook timeout) leaves the session active. This capability opts Kimi into
// the activity observer's stale-active reconciliation.
//
// Kimi Code 2.4 keeps its composer on screen while a turn runs, so the
// composer alone proves nothing (captured from Kimi Code 2.4.1 in tmux):
//
//	idle      ● Done.
//	          ╭──────────╮
//	          │ >        │
//	          ╰──────────╯
//	           model  ~/project
//	                      context: 1% (163/128k)
//	working     ⠧ Working… · Tip: …       (braille: streaming, thinking)
//	            🌒 · Tip: …               (moon: waiting, running a tool)
//	          ╭──────────╮ …
//	approval  ▶ 1. Approve once … ↑/↓ select · 1/2/3/4 choose · ↵ confirm
//	          (the panel replaces the composer)
//
// A composer at the bottom with no spinner above it is idle, a spinner is
// active, and an approval panel is waiting_input. Anything else, such as the
// folder-trust dialog at startup, is not authoritative.
func (*Plugin) DetectTerminalActivity(output string) (domain.ActivityState, bool) {
	lines := terminalui.PlainTerminalLines(strings.ReplaceAll(output, "\r\n", "\n"))
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	top, ok := kimiComposerTop(lines)
	if !ok {
		if kimiApprovalPanel(lines) {
			return domain.ActivityWaitingInput, true
		}
		return "", false
	}
	for _, line := range lines[max(0, top-kimiActivityScanLines):top] {
		if kimiActivityLine(line) {
			return domain.ActivityActive, true
		}
	}
	return domain.ActivityIdle, true
}

// kimiComposerTop finds the composer box closing just above the footer and
// returns the index of its top border. The first row inside must be the
// "│ >" prompt; a draft may wrap onto more rows.
func kimiComposerTop(lines []string) (int, bool) {
	bottom := -1
	for i := len(lines) - 1; i >= 0 && i >= len(lines)-1-kimiFooterLines; i-- {
		if kimiBorder(lines[i], "╰", "╯") {
			bottom = i
			break
		}
	}
	if bottom < 1 {
		return 0, false
	}
	i := bottom - 1
	for i >= 0 && strings.HasPrefix(strings.TrimSpace(lines[i]), "│") {
		i--
	}
	if i < 0 || i == bottom-1 || !kimiBorder(lines[i], "╭", "╮") {
		return 0, false
	}
	if !strings.HasPrefix(strings.TrimSpace(lines[i+1]), "│ >") {
		return 0, false
	}
	return i, true
}

func kimiBorder(line, left, right string) bool {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, left) || !strings.HasSuffix(line, right) {
		return false
	}
	inner := strings.TrimSuffix(strings.TrimPrefix(line, left), right)
	return inner != "" && strings.Trim(inner, "─") == ""
}

// kimiActivityLine reports a row led by a spinner frame, or the compaction
// status, which blinks a bullet instead of spinning.
func kimiActivityLine(line string) bool {
	line = strings.TrimSpace(line)
	r, _ := utf8.DecodeRuneInString(line)
	if r != utf8.RuneError && strings.ContainsRune(kimiSpinnerFrames, r) {
		return true
	}
	return strings.HasPrefix(strings.TrimPrefix(line, "● "), "Compacting context…")
}

// kimiApprovalPanel reports the tool approval panel's key hints near the
// bottom of the screen.
func kimiApprovalPanel(lines []string) bool {
	for _, line := range lines[max(0, len(lines)-kimiApprovalScanLines):] {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "↑/↓ select") && strings.HasSuffix(line, "confirm") {
			return true
		}
	}
	return false
}
