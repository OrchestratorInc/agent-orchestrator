package kimchiacp

import (
	"context"
	"fmt"
	"strings"

	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"

	"github.com/aoagents/agent-orchestrator/backend/internal/agentversion"
)

// minimumKimchiVersion is the oldest Kimchi build known to support ACP mode.
// Kimchi 0.0.7 added the --mode acp flag and the minimal ACP server.
const minimumKimchiVersion = "0.0.7"

// versionProbe checks that the resolved Kimchi binary is at least
// minimumKimchiVersion. It runs `kimchi --version`, parses the semver
// triplet, and returns an error if the binary is too old or the version
// output is unparseable.
func versionProbe(ctx context.Context, bin string) error {
	output, err := aoprocess.CommandContext(ctx, bin, "--version").CombinedOutput()
	if err != nil {
		return fmt.Errorf("read Kimchi version: %w", err)
	}
	installed, ok := agentversion.Find(string(output))
	if !ok {
		return fmt.Errorf("unrecognized Kimchi version %q (Orchestrator requires %s or newer)",
			strings.TrimSpace(string(output)), minimumKimchiVersion)
	}
	minimum := agentversion.MustParse(minimumKimchiVersion)
	if installed.Less(minimum) {
		return fmt.Errorf("kimchi %s is older than Orchestrator's tested minimum %s",
			installed, minimumKimchiVersion)
	}
	return nil
}
