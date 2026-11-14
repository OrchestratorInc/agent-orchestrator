package kiro

import (
	"regexp"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

var kiroTerminalEscape = regexp.MustCompile(`\x1b(?:\[[\x30-\x3f]*[\x20-\x2f]*[\x40-\x7e]|\][^\x07]*(?:\x07|\x1b\\))`)

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

var kiroApprovalHeaders = []string{
	"requires approval",
	"awaiting approval",
	"approval required",
	"needs approval",
	"approve tool call",
	"allow this action",
}

const kiroMaxOptionLen = 48

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

func kiroApprovalHeader(line string) bool {
	for _, header := range kiroApprovalHeaders {
		if strings.Contains(line, header) {
			return true
		}
	}
	return false
}

func kiroOptionText(line string) string {
	return strings.Trim(strings.TrimSpace(line), "❯›>•-*│┃ ")
}

func kiroApproveOption(line string) bool {
	text := kiroOptionText(line)
	if text == "" || len(text) > kiroMaxOptionLen {
		return false
	}
	return strings.Contains(text, "[y]") || strings.Contains(text, "yes") ||
		strings.Contains(text, "approv") || strings.Contains(text, "allow") ||
		strings.Contains(text, "trust")
}

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

func kiroClassicPromptAt(line string) bool {
	return strings.Contains(line, "[y/n/t]") || strings.Contains(line, "y/n")
}

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
