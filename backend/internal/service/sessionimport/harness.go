package sessionimport

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type harnessImporter struct {
	root               func(home string) string
	discover           func(context.Context, string) ([]sourceFiles, error)
	read               func(context.Context, string, ...string) (transcript, error)
	discoverRegistered func(context.Context, []string) ([]sourceFiles, error)
	mode               domain.SessionMode
	readWithProjects   func(context.Context, string, []string, ...string) (transcript, error)
	as                 domain.AgentHarness
	adopt              func(context.Context, transcript) (transcript, error)
}

var harnessImporters = map[domain.AgentHarness]harnessImporter{
	domain.HarnessAider:      {discoverRegistered: discoverAiderRegistered, read: readAiderTranscript, mode: domain.SessionModeTUI},
	domain.HarnessClaudeCode: {root: claudeRoot, discover: walkSources(domain.HarnessClaudeCode, "projects", jsonlKey), read: readClaudeTranscript},
	domain.HarnessCodex:      {root: codexRoot, discover: walkSources(domain.HarnessCodex, "sessions", codexKey), read: readCodexTranscript},
	domain.HarnessCopilot:    {root: copilotRoot, discover: discoverCopilot, read: readCopilotTranscript},
	domain.HarnessKimchi:     {root: kimchiRoot, discover: walkSources(domain.HarnessKimchi, "sessions", jsonlKey), read: piFamilyReader(domain.HarnessKimchi)},
	domain.HarnessOMP:        {root: ompRoot, discover: walkSources(domain.HarnessOMP, "sessions", jsonlKey), read: piFamilyReader(domain.HarnessOMP)},
	domain.HarnessOpenCode:   {root: openCodeRoot, discover: discoverOpenCode, read: readOpenCodeTranscript},
	domain.HarnessPi:         {root: piRoot, discover: walkSources(domain.HarnessPi, "sessions", jsonlKey), read: piFamilyReader(domain.HarnessPi)},
	domain.HarnessQwen:       {root: qwenRoot, discover: walkSources(domain.HarnessQwen, "projects", qwenKey), read: readQwenTranscript},
	domain.HarnessCline:      {root: clineRoot, discover: discoverCline, read: readClineTranscript},
	domain.HarnessCursor:     {root: cursorRoot, discover: discoverCursor, read: readCursorTranscript},
	domain.HarnessGrok:       {root: grokRoot, discover: discoverGrok, read: readGrokTranscript},
	domain.HarnessGemini:     {root: geminiRoot, discover: discoverGemini, readWithProjects: readGeminiTranscriptWithProjects},
	conductorSource:          {root: conductorRoot, discover: discoverConductor, read: readConductorTranscript, as: domain.HarnessClaudeCode, adopt: writeConductorTranscript},
}

func claudeRoot(home string) string {
	return envOr("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
}
func codexRoot(home string) string { return envOr("CODEX_HOME", filepath.Join(home, ".codex")) }
func piRoot(home string) string {
	return envOr("PI_CODING_AGENT_DIR", filepath.Join(home, ".pi", "agent"))
}
func qwenRoot(home string) string { return envOr(qwenHomeEnv, filepath.Join(home, ".qwen")) }

func kimchiRoot(home string) string { return filepath.Join(home, ".config", "kimchi", "harness") }

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func readTranscriptForProjects(ctx context.Context, harness domain.AgentHarness, root string, projects []string, paths ...string) (transcript, error) {
	if read := harnessImporters[harness].readWithProjects; read != nil {
		return read(ctx, root, projects, paths...)
	}
	return readTranscript(ctx, harness, root, paths...)
}

func importTitle(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	if runes := []rune(text); len(runes) > 100 {
		return string(runes[:100]) + "…"
	}
	return text
}
