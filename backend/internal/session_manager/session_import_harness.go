package sessionmanager

import (
	"context"
	"path/filepath"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type importedHarnessHooks struct {
	configEnvKey          string
	configValue           func(string) string
	ownedElsewhere        func(context.Context, domain.SessionRecord) bool
	adoptedOwnedElsewhere func(context.Context, domain.SessionRecord) bool
}

var importedHarnesses = map[domain.AgentHarness]importedHarnessHooks{
	domain.HarnessClaudeCode: {configEnvKey: "CLAUDE_CONFIG_DIR"},
	domain.HarnessCline:      {configEnvKey: "CLINE_DATA_DIR", ownedElsewhere: clineImportOwnedElsewhere, adoptedOwnedElsewhere: clineImportOwnedElsewhere},
	domain.HarnessCodex:      {configEnvKey: "CODEX_HOME"},
	domain.HarnessCopilot:    {configEnvKey: "COPILOT_HOME"},
	domain.HarnessCursor:     {configEnvKey: "CURSOR_DATA_DIR", adoptedOwnedElsewhere: cursorAdoptedImportOwnedElsewhere},
	domain.HarnessGemini:     {configEnvKey: "GEMINI_CLI_HOME", configValue: filepath.Dir},
	domain.HarnessGrok:       {configEnvKey: "GROK_HOME", ownedElsewhere: grokImportOwnedElsewhere, adoptedOwnedElsewhere: grokImportOwnedElsewhere},
	domain.HarnessKimchi:     {configEnvKey: "KIMCHI_CODING_AGENT_DIR"},
	domain.HarnessOMP:        {configEnvKey: "PI_CODING_AGENT_DIR"},
	domain.HarnessOpenCode:   {configEnvKey: "OPENCODE_DATA_DIR"},
	domain.HarnessPi:         {configEnvKey: "PI_CODING_AGENT_DIR"},
	domain.HarnessQwen:       {configEnvKey: "QWEN_HOME"},
}

var importedProcessNames = []string{"claude", "codex", "gemini", "node", "copilot", "omp", "aider", "cline", "cursor-agent", "grok"}
