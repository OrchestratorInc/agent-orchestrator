package claudecode

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/skillbridge"
)

var _ ports.AgentSkillSource = (*Plugin)(nil)

// SkillDirectories is the folder of bridged skills to add to a chat session:
// Claude Code loads <dir>/.claude/skills from an ACP additional directory, for
// that session only.
func SkillDirectories(dataDir, sessionID string) []string {
	if dir := skillbridge.Attached(dataDir, string(domain.HarnessClaudeCode), sessionID); dir != "" {
		return []string{dir}
	}
	return nil
}

// SkillSupport reports where Claude Code finds the user's skills. The terminal
// CLI asks whether to trust a directory given with --add-dir, with "No, exit" as
// the default, and only an entry in the user's own config answers for it. So a
// terminal session is pointed at a list of skills instead.
func (p *Plugin) SkillSupport(_ context.Context, request ports.AgentSkillRequest) ports.AgentSkillSupport {
	var support ports.AgentSkillSupport
	if request.Chat {
		support.Layout = skillbridge.ClaudeLayout
	}
	config := strings.TrimSpace(request.Env["CLAUDE_CONFIG_DIR"])
	if config == "" {
		config = strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR"))
	}
	if config == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return support
		}
		config = filepath.Join(home, ".claude")
	}
	support.Native = []string{filepath.Join(config, "skills")}
	return support
}
