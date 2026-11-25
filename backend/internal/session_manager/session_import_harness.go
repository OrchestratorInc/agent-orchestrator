package sessionmanager

import (
	"context"
	"path/filepath"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// importedHarnessHooks lets a harness add ownership and config-home handling
// without changing the shared restore flow. Each provider implements callbacks
// in session_import_<harness>.go and registers one map entry here. Config env
// values name ConfigDir unless configValue translates a provider home override.
type importedHarnessHooks struct {
	configEnvKey          string
	configValue           func(string) string
	ownedElsewhere        func(context.Context, domain.SessionRecord) bool
	adoptedOwnedElsewhere func(context.Context, domain.SessionRecord) bool
}

var importedHarnesses = map[domain.AgentHarness]importedHarnessHooks{
	domain.HarnessPrimeAgent: {configEnvKey: "PRIME_AGENT_SESSION_DIR"},

	domain.HarnessDroid:      {configEnvKey: "FACTORY_HOME_OVERRIDE", configValue: filepath.Dir},
	domain.HarnessKimi:       {configEnvKey: "KIMI_CODE_HOME"},
	domain.HarnessAgy:        {configEnvKey: "GEMINI_HOME"},
	domain.HarnessDeepSeek:   {configEnvKey: "DSH_HOME"},
	domain.HarnessClaudeCode: {configEnvKey: "CLAUDE_CONFIG_DIR"},
	domain.HarnessCline:      {configEnvKey: "CLINE_DATA_DIR", ownedElsewhere: clineImportOwnedElsewhere, adoptedOwnedElsewhere: clineImportOwnedElsewhere},
	domain.HarnessCodewhale:  {configEnvKey: "CODEWHALE_HOME", ownedElsewhere: codewhaleImportOwnedElsewhere, adoptedOwnedElsewhere: codewhaleAdoptedImportOwnedElsewhere},
	domain.HarnessCodex:      {configEnvKey: "CODEX_HOME"},
	domain.HarnessCopilot:    {configEnvKey: "COPILOT_HOME"},
	domain.HarnessCrush:      {configEnvKey: "CRUSH_GLOBAL_DATA", ownedElsewhere: crushImportOwnedElsewhere, adoptedOwnedElsewhere: crushImportOwnedElsewhere},
	domain.HarnessCursor:     {configEnvKey: "CURSOR_DATA_DIR", adoptedOwnedElsewhere: cursorAdoptedImportOwnedElsewhere},
	domain.HarnessFX:         {configEnvKey: "FX_HOME", ownedElsewhere: fxImportOwnedElsewhere, adoptedOwnedElsewhere: fxImportOwnedElsewhere},
	domain.HarnessGemini:     {configEnvKey: "GEMINI_CLI_HOME", configValue: filepath.Dir},
	domain.HarnessGrok:       {configEnvKey: "GROK_HOME", ownedElsewhere: grokImportOwnedElsewhere, adoptedOwnedElsewhere: grokImportOwnedElsewhere},
	domain.HarnessKilocode:   {configEnvKey: "XDG_DATA_HOME", configValue: kiloCodeImportDataHome, ownedElsewhere: kiloCodeImportOwnedElsewhere},
	domain.HarnessKimchi:     {configEnvKey: "KIMCHI_CODING_AGENT_DIR"},
	domain.HarnessOMP:        {configEnvKey: "PI_CODING_AGENT_DIR"},
	domain.HarnessMemcode:    {},
	domain.HarnessOpenCode:   {configEnvKey: "OPENCODE_DATA_DIR"},
	domain.HarnessOpenHands:  {configEnvKey: "OPENHANDS_CONVERSATIONS_DIR", ownedElsewhere: openHandsImportOwnedElsewhere, adoptedOwnedElsewhere: openHandsImportOwnedElsewhere},
	domain.HarnessPi:         {configEnvKey: "PI_CODING_AGENT_DIR"},
	domain.HarnessQwen:       {configEnvKey: "QWEN_HOME"},
}

// writableTranscriptOwner also covers providers without a session registry.
// Node-based CLIs normally appear as their binary name or node in lsof output.
// This is best-effort evidence, not a substitute for provider writer locks.
var importedProcessNames = []string{"claude", "codex", "gemini", "node", "copilot", "omp", "aider", "cline", "cursor-agent", "grok", "prime-agent", "codewhale", "crush", "memcode", "fx", "openhands", "droid", "agy"}
