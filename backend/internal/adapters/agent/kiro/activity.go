package kiro

import (
	"regexp"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

var kiroTerminalEscape = regexp.MustCompile(`\x1b(?:\[[\x30-\x3f]*[\x20-\x2f]*[\x40-\x7e]|\][^\x07]*(?:\x07|\x1b\\))`)

// DeriveActivityState maps a Kiro hook sub-command onto an AO activity state.
// The bool is false when the event carries no activity signal.
//
// PreToolUse arrives as pre-tool-use (active): it fires for every tool
// invocation including auto-approved/trusted tools, so it proves work, not a
// pending decision. The legacy permission-request sub-command (agent files
// written before the remap) maps to active for the same reason: no other Kiro
// event emits it, and the old mapping pinned every tool invocation to sticky
// waiting_input. Genuine approval dialogs carry no hook of their own and are
// observed via DetectTerminalActivity instead.
func DeriveActivityState(event string, _ []byte) (domain.ActivityState, bool) {
	switch event {
	case "session-start", "user-prompt-submit":
		return domain.ActivityActive, true
	case "pre-tool-use", "post-tool-use", "post-tool-use-failure", "permission-resolved":
		return domain.ActivityActive, true
	case "permission-request":
		return domain.ActivityActive, true
	case "stop":
		return domain.ActivityIdle, true
	default:
		return "", false
	}
}

// ContinuouslyDetectTerminalActivity opts Kiro into terminal reconciliation
// on every observer tick. Completion hooks provide the primary transition,
// but the TUI approval bar is the only observation path for a genuinely
// pending permission decision.
func (p *Plugin) ContinuouslyDetectTerminalActivity() bool { return true }

// DetectTerminalActivity recognizes authoritative current-state markers in
// Kiro's TUI. The newest marker wins so retained transcript text cannot
// override the live approval bar or generation indicator.
//
//   - Approval: TUI approval bar above the input (Yes / Trust / No options)
//     or the classic `[y/n/t]` prompt. Kiro fires no hook while the dialog is
//     on screen, so the terminal is the only observation path for a genuinely
//     pending decision.
//   - Active: busy/generation indicator (esc to interrupt/cancel).
//   - Idle: empty composer placeholder ("ask a question or describe a task").
//
// Approval requires actual dialog controls (see kiroApprovalAt), not bare
// "trust"/"approval"/"no" words, so status lines like "No approval is
// required." or "shell not trusted" never match. An answered classic prompt
// (the choice echoed after `[y/n/t]:`) proves the decision was already made,
// so scanning stops there instead of matching older retained UI.
func (p *Plugin) DetectTerminalActivity(output string) (domain.ActivityState, bool) {
	lines := kiroTerminalLines(output)
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
		if kiroClassicPromptAt(line) && !kiroClassicPromptLive(recent, i) {
			// The choice was already echoed (on the prompt line or a later
			// answer line): the decision was made, so anything older is
			// history, never a live dialog — stop instead of matching
			// stale UI.
			return "", false
		}
		if kiroApprovalAt(recent, i) {
			return domain.ActivityWaitingInput, true
		}
		if strings.Contains(line, "esc to interrupt") || strings.Contains(line, "esc to cancel") {
			return domain.ActivityActive, true
		}
		if strings.Contains(line, "ask a question or describe a task") {
			return domain.ActivityIdle, true
		}
	}
	return "", false
}

// kiroApprovalHeaders are decision-announcing phrases. Bare "approval" or
// "trust" alone never counts: status lines like "No approval is required."
// or "shell not trusted" carry no dialog.
var kiroApprovalHeaders = []string{
	"requires approval",
	"awaiting approval",
	"approval required",
	"needs approval",
	"approve tool call",
	"allow this action",
}

// kiroMaxOptionLen bounds dialog option labels. Real options are short
// ("Yes, single permission", "Trust, always allow in this session", "No");
// transcript prose never qualifies.
const kiroMaxOptionLen = 48

// kiroApprovalAt reports whether the line at idx is part of a live approval
// dialog. Two shapes count:
//
//   - Classic: a line carrying the `[y/n/t]` prompt marker (answered prompts
//     are excluded by the caller).
//   - TUI bar: an approval header line plus an approve option and a deny
//     option on nearby lines. Options must be short control labels, so only
//     a full approve+deny pair around a real header counts.
func kiroApprovalAt(lines []string, idx int) bool {
	line := strings.ToLower(lines[idx])
	if kiroClassicPromptAt(line) {
		return kiroClassicPromptLive(lines, idx)
	}
	if !kiroApprovalHeader(line) {
		return false
	}
	lo := idx - 3
	if lo < 0 {
		lo = 0
	}
	hi := idx + 3
	if hi >= len(lines) {
		hi = len(lines) - 1
	}
	var approve, deny bool
	for i := lo; i <= hi; i++ {
		if i == idx {
			continue
		}
		nearby := strings.ToLower(lines[i])
		if kiroApproveOption(nearby) {
			approve = true
		}
		if kiroDenyOption(nearby) {
			deny = true
		}
	}
	return approve && deny
}

// kiroApprovalHeader reports whether a line announces a pending approval
// decision (as opposed to merely mentioning approval or trust).
func kiroApprovalHeader(line string) bool {
	for _, header := range kiroApprovalHeaders {
		if strings.Contains(line, header) {
			return true
		}
	}
	return false
}

// kiroOptionText strips selection/box-drawing markers from a dialog option
// line so short labels like "❯ Yes, single permission" compare cleanly.
func kiroOptionText(line string) string {
	return strings.Trim(strings.TrimSpace(line), "❯›>•-*│┃ ")
}

// kiroApproveOption reports whether a line is a short approve-side dialog
// control (yes/approve/allow/trust option or [y] key hint).
func kiroApproveOption(line string) bool {
	text := kiroOptionText(line)
	if text == "" || len(text) > kiroMaxOptionLen {
		return false
	}
	return strings.Contains(text, "[y]") || strings.Contains(text, "yes") ||
		strings.Contains(text, "approv") || strings.Contains(text, "allow") ||
		strings.Contains(text, "trust")
}

// kiroDenyOption reports whether a line is a short deny-side dialog control
// (a standalone "No" with an optional key hint like "No (Tab to edit)", a
// deny option, or an [n] key hint). A bare "no" prefix inside a longer
// sentence never counts.
func kiroDenyOption(line string) bool {
	text := kiroOptionText(line)
	if text == "" || len(text) > kiroMaxOptionLen {
		return false
	}
	if strings.Contains(text, "[n]") || strings.Contains(text, "deny") {
		return true
	}
	if rest, ok := strings.CutPrefix(text, "no"); ok {
		rest = strings.TrimSpace(rest)
		return rest == "" || (strings.HasPrefix(rest, "(") && strings.HasSuffix(rest, ")"))
	}
	return false
}

// kiroClassicPromptAt reports whether a line carries the classic `[y/n/t]`
// approval prompt marker.
func kiroClassicPromptAt(line string) bool {
	return strings.Contains(line, "[y/n/t]") || strings.Contains(line, "y/n")
}

// kiroClassicPromptLive reports whether the classic prompt at idx is still
// awaiting an answer: no echoed choice on the marker line itself and none on
// the following lines. The classic prompt prints newlines before reading the
// answer, so the choice often lands on its own later line ("[ao] > y").
func kiroClassicPromptLive(lines []string, idx int) bool {
	if kiroClassicPromptAnswered(strings.ToLower(lines[idx])) {
		return false
	}
	end := idx + 4
	if end > len(lines) {
		end = len(lines)
	}
	for _, later := range lines[idx+1 : end] {
		if kiroAnswerEcho(strings.ToLower(later)) {
			return false
		}
	}
	return true
}

// kiroAnswerEcho reports whether a line is a bare y/n/t answer echo: short,
// with prompt chrome stripped, exactly one choice letter remains. Guards the
// lookahead so prose never reads as an answer.
func kiroAnswerEcho(line string) bool {
	text := strings.TrimSpace(line)
	if text == "" || len(text) > 12 {
		return false
	}
	fields := strings.Fields(text)
	last := strings.Trim(fields[len(fields)-1], ":.,;!?")
	if last != "y" && last != "n" && last != "t" {
		return false
	}
	return len(fields) == 1 || strings.Contains(text, ">")
}

// kiroClassicPromptAnswered reports whether a classic `[y/n/t]` prompt line
// already echoes the user's answer ("[y/n/t]: y"). The decision is made, so
// the prompt is history, not a live dialog.
func kiroClassicPromptAnswered(line string) bool {
	if idx := strings.Index(line, "[y/n/t]"); idx >= 0 {
		return strings.Trim(line[idx+len("[y/n/t]"):], ":.- \t") != ""
	}
	if idx := strings.Index(line, "y/n"); idx >= 0 {
		return strings.Trim(line[idx+len("y/n"):], ":.- \t") != ""
	}
	return false
}

func kiroTerminalLines(output string) []string {
	plain := kiroTerminalEscape.ReplaceAllString(strings.ReplaceAll(output, "\r", "\n"), "")
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
