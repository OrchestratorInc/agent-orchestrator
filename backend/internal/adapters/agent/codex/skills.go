package codex

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/domain"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/ports"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/skillbridge"
)

var _ ports.AgentSkillSource = (*Plugin)(nil)

// SkillRoots is the folder of bridged skills to give a chat session's
// app-server with skills/extraRoots/set, which lasts as long as that process.
func SkillRoots(dataDir, sessionID string) []string {
	dir := skillbridge.Attached(dataDir, string(domain.HarnessCodex), sessionID)
	if dir == "" {
		return nil
	}
	return []string{filepath.Join(dir, filepath.FromSlash(skillbridge.AgentsLayout))}
}

// SkillSupport reports where Codex finds the user's skills. The terminal CLI
// has no way to add a folder of skills to one session.
func (p *Plugin) SkillSupport(_ context.Context, request ports.AgentSkillRequest) ports.AgentSkillSupport {
	var support ports.AgentSkillSupport
	if request.Chat {
		support.Layout = skillbridge.AgentsLayout
	}
	home, _ := os.UserHomeDir()
	codexHome := strings.TrimSpace(request.Env[codexHomeEnv])
	if codexHome == "" {
		codexHome = codexHomeDir()
	}
	if codexHome != "" {
		support.Native = append(support.Native, filepath.Join(codexHome, "skills"))
	}
	if home != "" {
		support.Native = append(support.Native, filepath.Join(home, ".agents", "skills"))
	}
	return support
}
