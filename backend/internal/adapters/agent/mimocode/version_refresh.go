package mimocode

import (
	"context"
	"maps"
	"runtime"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/adapters/agent/agentbase"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/ports"
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
	return agentbase.NativeJSONSessionExists(ctx, binary, []string{"export", nativeID}, ref, nativeID, exportProbeEnv(env, runtime.GOOS == "windows"))
}

// exportProbeEnv copies the caller env with Claude import forced off, dropping
// differently-cased spellings of that key where env keys are case-insensitive.
func exportProbeEnv(env map[string]string, caseInsensitive bool) map[string]string {
	probeEnv := make(map[string]string, len(env)+1)
	maps.Copy(probeEnv, env)
	agentbase.ApplyEnvOverrides(probeEnv, map[string]string{disableClaudeImportEnv: "1"}, caseInsensitive)
	return probeEnv
}

var _ ports.AgentInterfaceHandoffHistoryProbe = (*Plugin)(nil)
