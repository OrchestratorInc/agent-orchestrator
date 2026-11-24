package ompacp

import (
	"context"
	"fmt"
	"strings"

	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"

	"github.com/aoagents/agent-orchestrator/backend/internal/agentversion"
)

// minimumOMPVersion is the first tagged OMP release that contains the native
// `omp acp` command. Older builds exposed a different RPC mode whose session,
// approval, and replay semantics are not treated as interchangeable.
const minimumOMPVersion = "15.0.0"

func versionProbe(ctx context.Context, bin string) error {
	output, err := aoprocess.CommandContext(ctx, bin, "--version").CombinedOutput()
	if err != nil {
		return fmt.Errorf("read OMP version: %w", err)
	}
	return validateVersionOutput(string(output))
}

func validateVersionOutput(output string) error {
	installed, ok := agentversion.Find(output)
	if !ok {
		return fmt.Errorf("unrecognized OMP version %q (Orchestrator requires %s or newer)",
			strings.TrimSpace(output), minimumOMPVersion)
	}
	minimum := agentversion.MustParse(minimumOMPVersion)
	if installed.Less(minimum) {
		return fmt.Errorf("OMP %s is older than Orchestrator's tested minimum %s",
			installed, minimumOMPVersion)
	}
	return nil
}
