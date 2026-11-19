package memcode

import (
	"errors"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/terminalui"
)

func prepareMemcodeRuntimeEnv(env map[string]string) {
	// For the canonical endpoint Memcode leaves the OpenAI SDK base unset.
	// The SDK then honors OPENAI_BASE_URL inherited from the user's shell,
	// independently of Memcode's chosen credential/endpoint. Pin only this
	// process's SDK default; explicit Memcode custom endpoints still select
	// their own transport/base. Never change or copy provider credentials.
	env["OPENAI_BASE_URL"] = "https://api.openai.com/v1"
}

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
