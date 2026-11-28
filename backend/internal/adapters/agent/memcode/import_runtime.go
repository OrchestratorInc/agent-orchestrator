package memcode

import (
	"errors"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/terminalui"
)

func memcodeRestoreCrash(output string) error {
	lines := strings.Split(terminalui.PlainTerminalText(output), "\n")
	start := 0
	for i, line := range lines {
		if strings.TrimSpace(line) == "○ idle" {
			start = i + 1
		}
	}
	for _, line := range lines[start:] {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "panic:") || strings.HasPrefix(line, "fatal error:") ||
			strings.HasPrefix(line, "github.com/memcode-ai/vaxis/") ||
			strings.HasPrefix(line, "github.com/memcode-ai/memcode/internal/vxui.") {
			return errors.New("memcode native terminal crashed during restore")
		}
	}
	return nil
}
