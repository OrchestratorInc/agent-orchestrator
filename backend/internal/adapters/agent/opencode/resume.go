package opencode

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/agentbase"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// NativeConversationExists checks local history because a failed v1 TUI resume
// navigates home while remaining alive. A fresh composer is not migration success.
func (*Plugin) NativeConversationExists(ctx context.Context, ref ports.SessionRef, nativeID string, env map[string]string) (bool, error) {
	binary, err := ResolveBinaryForMajor(ctx, 1)
	if err != nil {
		return false, err
	}
	return agentbase.NativeJSONSessionExists(ctx, binary, []string{"export", nativeID}, ref, nativeID, env)
}

var _ ports.AgentInterfaceHandoffHistoryProbe = (*Plugin)(nil)
