package opencodev2

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/opencode"
)

func DataHome(ctx context.Context) (string, error) { return opencode.PrepareV2DataHome(ctx) }
