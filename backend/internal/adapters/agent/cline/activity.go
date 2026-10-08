package cline

import (
	"regexp"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

var clineTerminalEscape = regexp.MustCompile(`\x1b(?:\[[\x30-\x3f]*[\x20-\x2f]*[\x40-\x7e]|\][^\x07]*(?:\x07|\x1b\\))`)

// DeriveActivityState maps a Cline hook sub-command onto an AO activity
// state. The bool is false when the event carries no activity signal.
//
// PreToolUse arrives as pre-tool-use (active): since upstream cline/cline#7446
// it fires AFTER the user approves — or with no prompt at all under
// auto-approve — so it proves work, not a pending decision. The legacy
// permission-request sub-command (hook scripts installed before the remap)
// maps to active for the same reason: no other Cline event emits it, and the
// old mapping pinned every tool invocation to sticky waiting_input.
// Genuine approval dialogs carry no hook of their own and are observed via
// DetectTerminalActivity instead.
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

// ContinuouslyDetectTerminalActivity opts Cline into terminal reconciliation
// on every observer tick. Completion hooks provide the primary transition, but
// the idle composer is the fallback when Cline omits or loses a callback.
func (p *Plugin) ContinuouslyDetectTerminalActivity() bool { return true }

// DetectTerminalActivity recognizes authoritative current-state markers in
// Cline's TUI. The newest marker wins so retained transcript text cannot
// override the current composer or generation indicator. An approval dialog
// (Approve tool call? + [y] approve / [n] deny) reports waiting_input: Cline
// fires no hook while the dialog is on screen, so the terminal is the only
// observation path for a genuinely pending decision.
func (p *Plugin) DetectTerminalActivity(output string) (domain.ActivityState, bool) {
	lines := clineTerminalLines(output)
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
		if clineApprovalAt(recent, i) {
			return domain.ActivityWaitingInput, true
		}
		if strings.Contains(line, "thinking... (esc to cancel)") {
			return domain.ActivityActive, true
		}
		if clineEmptyComposer(recent[i]) && clineModeFooterAfter(recent, i) {
			return domain.ActivityIdle, true
		}
	}
	return "", false
}

// clineApprovalAt reports whether the line at idx is part of a live approval
// dialog. It requires both sides of the dialog affordance (an approve marker
// and a deny marker within a small window, anchored on a dialog line) — not
// the "Auto-approve all disabled" footer alone, which also shows during
// normal work in manual-approval mode — so transcript mentions of approval
// never match.
func clineApprovalAt(lines []string, idx int) bool {
	line := strings.ToLower(lines[idx])
	if strings.Contains(line, "approve tool call") {
		return true
	}
	anchor := strings.Contains(line, "[y]") || strings.Contains(line, "[n]") ||
		strings.Contains(line, "approv") || strings.Contains(line, "deny")
	if !anchor {
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
		nearby := strings.ToLower(lines[i])
		if strings.Contains(nearby, "approv") || strings.Contains(nearby, "[y]") {
			approve = true
		}
		if strings.Contains(nearby, "deny") || strings.Contains(nearby, "[n]") {
			deny = true
		}
	}
	return approve && deny
}

func clineEmptyComposer(line string) bool {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "❯") {
		return false
	}
	prompt := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(line, "❯")))
	return prompt == "ask anything..." || prompt == "plan something..."
}

func clineModeFooterAfter(lines []string, composer int) bool {
	for _, line := range lines[composer+1:] {
		line = strings.ToLower(line)
		if strings.Contains(line, "plan") && strings.Contains(line, "act") && strings.Contains(line, "(tab)") {
			return true
		}
	}
	return false
}

func clineTerminalLines(output string) []string {
	plain := clineTerminalEscape.ReplaceAllString(strings.ReplaceAll(output, "\r", "\n"), "")
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
