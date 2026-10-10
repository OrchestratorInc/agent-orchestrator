package minimaxcode

import (
	"regexp"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// DeriveActivityState maps native hooks. SessionEnd is intentionally omitted:
// MiniMax uses it for an idle timeout, not process exit.
func DeriveActivityState(event string, _ []byte) (domain.ActivityState, bool) {
	switch event {
	case "session-start", "user-prompt-submit", "pre-tool-use", "post-tool-use", "post-tool-use-failure":
		return domain.ActivityActive, true
	case "permission-blocked":
		return domain.ActivityBlocked, true
	case "stop":
		return domain.ActivityIdle, true
	default:
		return "", false
	}
}

var ansiRE = regexp.MustCompile(`\x1b\[[\x30-\x3f]*[\x20-\x2f]*[\x40-\x7e]|\x1b\][^\x07]*(?:\x07|\x1b\\)`)
var footerRE = regexp.MustCompile(`(?m)^.*(?:│|\|).*(?:Ask|Auto|Full access).*(?:│|\|).*✦ [^\n]+\s*$`)
var emptyRE = regexp.MustCompile(`(?s)Message · Enter send · Ctrl\+J newline[^\n]*\n\s*─+\s*\n\s*›\s+Ask Mcode to do anything\s*\n\s*─+\s*\n\s*[^\n]+(?:│|\|)[^\n]+✦ [^\n]+\s*$`)

func terminalTail(output string) string {
	clean := strings.ReplaceAll(ansiRE.ReplaceAllString(output, ""), "\r", "")
	lines := strings.Split(strings.TrimSpace(clean), "\n")
	if len(lines) > 16 {
		lines = lines[len(lines)-16:]
	}
	return strings.Join(lines, "\n")
}

// ContinuouslyDetectTerminalActivity enables native cancellation observation.
func (p *Plugin) ContinuouslyDetectTerminalActivity() bool { return true }

// DetectTerminalActivity observes cancellation without inventing a missing Stop
// hook. The interrupted draft is settled but is not an empty composer.
func (p *Plugin) DetectTerminalActivity(output string) (domain.ActivityState, bool) {
	tail := terminalTail(output)
	if !footerRE.MatchString(tail) {
		return "", false
	}
	if strings.Contains(tail, "Esc stop") || strings.Contains(tail, "Stopping response") {
		return domain.ActivityActive, true
	}
	if strings.Contains(tail, "Stopped · message restored to the Composer.") {
		return domain.ActivityIdle, true
	}
	if emptyRE.MatchString(tail) {
		return domain.ActivityIdle, true
	}
	return "", false
}

// ComposerIsEmpty requires the initialized, draft-free native composer.
func (p *Plugin) ComposerIsEmpty(output string) bool {
	return emptyRE.MatchString(terminalTail(output))
}
