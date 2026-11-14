package opencode

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/agentbase"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func (*Plugin) NativeConversationExists(ctx context.Context, ref ports.SessionRef, nativeID string, env map[string]string) (bool, error) {
	binary, err := ResolveBinaryForMajor(ctx, 1)
	if err != nil {
		return false, err
	}
	return agentbase.NativeJSONSessionExists(ctx, binary, []string{"export", nativeID}, ref, nativeID, env)
}

var _ ports.AgentInterfaceHandoffHistoryProbe = (*Plugin)(nil)
