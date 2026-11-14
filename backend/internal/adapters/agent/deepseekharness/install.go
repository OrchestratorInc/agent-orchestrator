package deepseekharness

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var _ ports.AgentBinaryResolver = (*Plugin)(nil)

func (p *Plugin) ResolveBinary(ctx context.Context) (string, error) {
	return p.deepseekBinary(ctx)
}
