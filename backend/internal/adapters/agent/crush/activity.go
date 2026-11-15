package crush

import (
	"regexp"
	"slices"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

var crushTerminalEscape = regexp.MustCompile(`\x1b(?:\[[\x30-\x3f]*[\x20-\x2f]*[\x40-\x7e]|\][^\x07]*(?:\x07|\x1b\\))`)

var crushReadyPlaceholders = []string{
	"ready!",
	"ready...",
	"ready?",
	"ready for instructions",
}

var crushWorkingPlaceholders = []string{
	"working!",
	"working...",
	"brrrrr...",
	"prrrrrrrr...",
	"processing...",
	"thinking...",
}

const crushPermissionDialogLookahead = 24

const crushQuestionLookahead = 4

func DeriveActivityState(event string, _ []byte) (domain.ActivityState, bool) {
	return "", false
}

func (p *Plugin) ContinuouslyDetectTerminalActivity() bool { return false }

func (p *Plugin) DetectTerminalActivity(output string) (domain.ActivityState, bool) {
	lines := crushTerminalLines(output)
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
		switch {
		case crushLineLooksWaitingInput(recent, i):
			return domain.ActivityWaitingInput, true
		case crushLineLooksActive(line):
			return domain.ActivityActive, true
		case crushLineLooksIdle(line):
			return domain.ActivityIdle, true
		}
	}
	return "", false
}

func crushLineLooksWaitingInput(lines []string, index int) bool {
	line := strings.ToLower(lines[index])
	if crushLineLooksQuestionDialog(lines, index) {
		return true
	}
	if !strings.Contains(line, "permission required") {
		return false
	}
	end := min(index+crushPermissionDialogLookahead, len(lines)-1)
	hasAllow := false
	hasDeny := false
	for _, nearby := range lines[index+1 : end+1] {
		nearby = strings.ToLower(nearby)
		button := strings.Trim(nearby, " │")
		if strings.HasPrefix(button, "allow") {
			hasAllow = true
		}
		if strings.HasPrefix(button, "deny") {
			hasDeny = true
		}
	}
	return hasAllow && hasDeny
}

func crushLineLooksQuestionDialog(lines []string, index int) bool {
	text := crushDialogLineText(lines[index])
	if !strings.HasPrefix(text, "? ") || strings.TrimSpace(strings.TrimPrefix(text, "?")) == "" {
		return false
	}
	end := min(index+crushQuestionLookahead, len(lines)-1)
	for _, nearby := range lines[index+1 : end+1] {
		if crushQuestionAnswerRow(nearby) {
			return true
		}
	}
	return false
}

func crushQuestionAnswerRow(line string) bool {
	line = crushDialogLineText(line)
	if strings.HasPrefix(line, "┃ ") || strings.HasPrefix(line, "> ") || strings.HasPrefix(line, "❯ ") ||
		strings.HasPrefix(line, "› ") || strings.HasPrefix(line, "○ ") ||
		strings.HasPrefix(line, "◯ ") {
		return true
	}
	return len(line) >= 3 && line[0] >= '1' && line[0] <= '9' && line[1] == '.' && line[2] == ' '
}

func (p *Plugin) ContinuouslyDetectTerminalActivityWhileWaiting() bool { return true }

func crushDialogLineText(line string) string {
	return strings.Trim(line, " │")
}

func crushLineLooksActive(line string) bool {
	if strings.Contains(line, "esc to interrupt") || strings.Contains(line, "ctrl+c to interrupt") {
		return true
	}
	if strings.Contains(line, "agent is busy, please wait") ||
		strings.Contains(line, "agent is working, please wait") {
		return true
	}
	return crushPromptLineHasPlaceholder(line, crushWorkingPlaceholders)
}

func crushLineLooksIdle(line string) bool {
	return crushPromptLineHasPlaceholder(line, crushReadyPlaceholders)
}

func crushPromptLineHasPlaceholder(line string, placeholders []string) bool {
	line = strings.TrimSpace(line)
	for _, marker := range []string{">", "y"} {
		if after, ok := strings.CutPrefix(line, marker); ok {
			text := strings.TrimSpace(after)
			return slices.Contains(placeholders, text)
		}
	}
	return false
}

func crushTerminalLines(output string) []string {
	plain := crushTerminalEscape.ReplaceAllString(strings.ReplaceAll(output, "\r", "\n"), "")
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
