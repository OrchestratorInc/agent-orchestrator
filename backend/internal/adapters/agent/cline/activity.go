package cline

import (
	"regexp"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

var clineTerminalEscape = regexp.MustCompile(`\x1b(?:\[[\x30-\x3f]*[\x20-\x2f]*[\x40-\x7e]|\][^\x07]*(?:\x07|\x1b\\))`)

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

func (p *Plugin) ContinuouslyDetectTerminalActivity() bool { return true }

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

func clineApprovalAt(lines []string, idx int) bool {
	lo := idx - clineApprovalWindow
	if lo < 0 {
		lo = 0
	}
	hi := idx + clineApprovalWindow
	if hi >= len(lines) {
		hi = len(lines) - 1
	}
	var prompt, approve, deny bool
	for i := lo; i <= hi; i++ {
		line := strings.ToLower(strings.TrimSpace(lines[i]))
		if clineApprovalPrompt(line) {
			prompt = true
		}
		if strings.Contains(line, "[y] approve") {
			approve = true
		}
		if strings.Contains(line, "[n] deny") {
			deny = true
		}
	}
	return prompt && approve && deny
}

const clineApprovalWindow = 3

func clineApprovalPrompt(line string) bool {
	return strings.Contains(line, "approve") && strings.HasSuffix(line, "?")
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
