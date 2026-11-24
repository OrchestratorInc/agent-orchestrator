package qwenacp

import (
	"context"
	"fmt"
	"strings"

	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"

	"github.com/aoagents/agent-orchestrator/backend/internal/agentversion"
)

// minimumQwenVersion is the oldest Qwen Code build whose `--approval-mode`
// accepts `auto`, the mode AO selects for an unset session permission.
// `--acp` itself landed in 0.15.0, but 0.15.x rejects `auto` at launch.
const minimumQwenVersion = "0.16.0"

func versionProbe(ctx context.Context, bin string) error {
	output, err := aoprocess.CommandContext(ctx, bin, "--version").CombinedOutput()
	if err != nil {
		return fmt.Errorf("read Qwen Code version: %w", err)
	}
	return validateVersionOutput(string(output))
}

func validateVersionOutput(output string) error {
	installed, ok := agentversion.Find(output)
	if !ok {
		return fmt.Errorf("unrecognized Qwen Code version %q (Orchestrator requires %s or newer)",
			strings.TrimSpace(output), minimumQwenVersion)
	}
	minimum := agentversion.MustParse(minimumQwenVersion)
	if installed.Less(minimum) {
		return fmt.Errorf("installed Qwen Code %s is older than Orchestrator's tested minimum %s",
			installed, minimumQwenVersion)
	}
	return nil
}
