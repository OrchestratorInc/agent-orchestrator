package opencodev2

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/opencode"
)

// DataHome returns the XDG_DATA_HOME OpenCode 2 is launched with after
// preserving any pre-isolation OpenCode store. Its database and sessions live
// under <data home>/opencode-v2-home/opencode.
func DataHome(ctx context.Context) (string, error) { return opencode.PrepareV2DataHome(ctx) }
