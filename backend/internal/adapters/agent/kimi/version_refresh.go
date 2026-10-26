package kimi

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/agentbase"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// SupportsTerminalVersionRefresh declares unattended exact-conversation resume.
// Restore selects the captured native conversation and preserves session options.
func (*Plugin) SupportsTerminalVersionRefresh() bool { return true }

// NativeConversationExists requires readable, exact local history before an
// unattended restart. An unknown ID must never become a fresh conversation.
func (p *Plugin) NativeConversationExists(ctx context.Context, ref ports.SessionRef, nativeID string, env map[string]string) (bool, error) {
	binary, err := p.kimiBinary(ctx)
	if err != nil {
		return false, err
	}
	return agentbase.NativeJSONSessionExists(ctx, binary, []string{"session", "list", "--all", "--json"}, ref, nativeID, env)
}

var _ ports.AgentInterfaceHandoffHistoryProbe = (*Plugin)(nil)
