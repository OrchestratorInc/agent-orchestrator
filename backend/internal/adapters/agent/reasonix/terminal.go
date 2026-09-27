package reasonix

import (
	"context"
	"regexp"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/terminalui"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// PromptReadinessHints prevents input from falling through into startup or
// permission dialogs when the expected composer is not visible.
func (*Plugin) PromptReadinessHints(ctx context.Context, _ ports.LaunchConfig) (ports.PromptReadinessHints, error) {
	if err := ctx.Err(); err != nil {
		return ports.PromptReadinessHints{}, err
	}
	return ports.PromptReadinessHints{RequireReady: true, Patterns: []string{"❯"}, PollInterval: 200 * time.Millisecond, Timeout: 15 * time.Second, Lines: 30}, nil
}

// DetectTerminalActivity recognizes the empty bordered composer and the native
// mode-switch footer. Modal footers replace these shortcuts; a working spinner
// keeps an otherwise empty composer from being mistaken for idle.
func (*Plugin) DetectTerminalActivity(output string) (domain.ActivityState, bool) {
	lines := terminalui.PlainTerminalLines(output)
	start := max(0, len(lines)-12)
	for i := len(lines) - 1; i >= start; i-- {
		line := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(line, "❯") {
			continue
		}
		if line != "❯" || i == 0 || !composerBorder(lines[i-1]) || i+2 >= len(lines) || !composerBorder(lines[i+1]) {
			return "", false
		}
		footer := strings.TrimSpace(lines[i+2])
		if !readyFooterState.MatchString(footer) && !strings.HasPrefix(footer, "YOLO · ") {
			return "", false
		}
		for _, row := range lines[max(start, i-4):i] {
			for _, r := range row {
				if r >= 0x2800 && r <= 0x28ff {
					return "", false
				}
			}
		}
		shortcuts := strings.Contains(footer, "Shift+Tab")
		gitRowAllowed := false
		dataBand := false
		for _, row := range lines[i+3:] {
			row = strings.TrimSpace(row)
			if row == "" {
				continue
			}
			if !dataBand && composerBorder(row) {
				dataBand, gitRowAllowed = true, true
				continue
			}
			if !dataBand && (strings.HasPrefix(row, "Shift+Tab ") || strings.HasPrefix(row, "Ctrl+Y ")) {
				shortcuts = shortcuts || strings.HasPrefix(row, "Shift+Tab ")
				continue
			}
			if !footerData(row) && !(gitRowAllowed && gitFooterIdentity.MatchString(row)) {
				return "", false
			}
			gitRowAllowed = false
		}
		if !shortcuts {
			return "", false
		}
		return domain.ActivityIdle, true
	}
	return "", false
}

// The native footer wraps interaction shortcuts before a separate data band.
// Git owns only the first data row (repo@branch), followed by named metrics.
var (
	readyFooterState  = regexp.MustCompile(`(?:^| · )(?:ready|就绪|就緒)(?: · |$|\s{2,})`)
	gitFooterIdentity = regexp.MustCompile(`^[^@\r\n]+@\S+(?:  .*)?$`)
)

func composerBorder(line string) bool {
	line = strings.TrimSpace(line)
	return len(line) >= 3 && strings.Trim(line, "─━- ") == ""
}

func footerData(line string) bool {
	for _, prefix := range []string{"MODEL ", "EFFORT ", "PRESET ", "CTX ", "CACHE ", "COMPACT ", "JOBS ", "BAL ", "COST ",
		"模型 ", "强度 ", "預設 ", "预设 ", "上下文 ", "缓存 ", "压缩 ", "任务 ", "余额 ", "费用 ",
		"強度 ", "快取 ", "壓縮 ", "任務 ", "餘額 ", "費用 "} {
		if strings.HasPrefix(line, prefix) {
			return true
		}
	}
	return false
}
