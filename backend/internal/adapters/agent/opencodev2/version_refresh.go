package opencodev2

import (
	"context"
	"maps"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/agentbase"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func (*Plugin) SupportsTerminalVersionRefresh() bool { return true }

func (p *Plugin) NativeConversationExists(ctx context.Context, ref ports.SessionRef, nativeID string, env map[string]string) (bool, error) {
	binary, err := p.ResolveBinary(ctx)
	if err != nil {
		return false, err
	}
	home, err := DataHome(ctx)
	if err != nil {
		return false, err
	}
	probeEnv := maps.Clone(env)
	if probeEnv == nil {
		probeEnv = map[string]string{}
	}
	probeEnv["XDG_DATA_HOME"] = home
	return agentbase.NativeJSONSessionExists(ctx, binary, []string{"session", "export", nativeID, "--standalone"}, ref, nativeID, probeEnv)
}

var _ ports.AgentInterfaceHandoffHistoryProbe = (*Plugin)(nil)
