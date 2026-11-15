package muse

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/binaryutil"
)

func (p *Plugin) ResolveBinary(ctx context.Context) (string, error) {
	return p.museBinary(ctx)
}

func (p *Plugin) ResolveBinaryPresence(ctx context.Context) (string, error) {
	return binaryutil.ResolveBinary(ctx, museBinarySpec)
}
