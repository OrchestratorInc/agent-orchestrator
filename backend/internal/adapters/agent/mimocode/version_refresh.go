package mimocode

import (
	"context"
	"maps"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/agentbase"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// SupportsTerminalVersionRefresh declares unattended exact-conversation resume.
// Restore selects the captured native conversation and preserves session options.
func (*Plugin) SupportsTerminalVersionRefresh() bool { return true }

// NativeConversationExists requires readable, exact local history before an
// unattended restart. An unknown ID must never become a fresh conversation.
func (p *Plugin) NativeConversationExists(ctx context.Context, ref ports.SessionRef, nativeID string, env map[string]string) (bool, error) {
	binary, err := p.mimoBinary(ctx)
	if err != nil {
		return false, err
	}
	probeEnv := make(map[string]string, len(env)+1)
	maps.Copy(probeEnv, env)
	probeEnv[disableClaudeImportEnv] = "1"
	return agentbase.NativeJSONSessionExists(ctx, binary, []string{"export", nativeID}, ref, nativeID, probeEnv)
}

var _ ports.AgentInterfaceHandoffHistoryProbe = (*Plugin)(nil)
