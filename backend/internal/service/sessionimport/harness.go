package sessionimport

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// harnessImporter keeps a provider's layout and parser in its own file.
// Registration adds one map entry here, its importedHarnesses hooks in
// session_manager, and one IMPORTABLE_AGENTS entry in the UI.
// The callbacks must only read provider state, confine file reads with os.Root
// (or open SQLite mode=ro), and return the exact CLI resume identity.
// Discovery sets sourceFiles.key to the native ID.
type harnessImporter struct {
	root     func(home string) string
	discover func(context.Context, string) ([]sourceFiles, error)
	read     func(context.Context, string, ...string) (transcript, error)
	// discoverRegistered receives only explicitly registered project/repository
	// paths. Repo-local providers leave root nil and return a root contained in
	// one supplied path; they must not search parents or unrelated repositories.
	discoverRegistered func(context.Context, []string) ([]sourceFiles, error)
	// mode can force Terminal import when a provider's Chat driver cannot load
	// the native format being imported. Empty preserves capability selection.
	mode domain.SessionMode
	// Global stores can also need registered paths to resolve older metadata.
	readWithProjects func(context.Context, string, []string, ...string) (transcript, error)
	// A source that is not an agent sets as to the agent its chats continue
	// on, and adopt writes a selected chat where that agent reads it.
	as    domain.AgentHarness
	adopt func(context.Context, transcript) (transcript, error)
}

// Keep entries immutable after package initialization: scans read them in parallel.
var harnessImporters = map[domain.AgentHarness]harnessImporter{
	domain.HarnessAider:      {discoverRegistered: discoverAiderRegistered, read: readAiderTranscript, mode: domain.SessionModeTUI},
	domain.HarnessClaudeCode: {root: claudeRoot, discover: walkSources(domain.HarnessClaudeCode, "projects", jsonlKey), read: readClaudeTranscript},
	// Archived Codex threads are not resumable without unarchiving them in Codex.
	domain.HarnessCodex:    {root: codexRoot, discover: walkSources(domain.HarnessCodex, "sessions", codexKey), read: readCodexTranscript},
	domain.HarnessCopilot:  {root: copilotRoot, discover: discoverCopilot, read: readCopilotTranscript},
	domain.HarnessKimchi:   {root: kimchiRoot, discover: walkSources(domain.HarnessKimchi, "sessions", jsonlKey), read: piFamilyReader(domain.HarnessKimchi)},
	domain.HarnessOMP:      {root: ompRoot, discover: walkSources(domain.HarnessOMP, "sessions", jsonlKey), read: piFamilyReader(domain.HarnessOMP)},
	domain.HarnessOpenCode: {root: openCodeRoot, discover: discoverOpenCode, read: readOpenCodeTranscript},
	domain.HarnessPi:       {root: piRoot, discover: walkSources(domain.HarnessPi, "sessions", jsonlKey), read: piFamilyReader(domain.HarnessPi)},
	domain.HarnessQwen:     {root: qwenRoot, discover: walkSources(domain.HarnessQwen, "projects", qwenKey), read: readQwenTranscript},
	domain.HarnessCline:    {root: clineRoot, discover: discoverCline, read: readClineTranscript},
	domain.HarnessCursor:   {root: cursorRoot, discover: discoverCursor, read: readCursorTranscript},
	domain.HarnessGrok:     {root: grokRoot, discover: discoverGrok, read: readGrokTranscript},
	domain.HarnessGemini:   {root: geminiRoot, discover: discoverGemini, readWithProjects: readGeminiTranscriptWithProjects},
	conductorSource:        {root: conductorRoot, discover: discoverConductor, read: readConductorTranscript, as: domain.HarnessClaudeCode, adopt: writeConductorTranscript},
}

func claudeRoot(home string) string {
	return envOr("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
}
func codexRoot(home string) string { return envOr("CODEX_HOME", filepath.Join(home, ".codex")) }
func piRoot(home string) string {
	return envOr("PI_CODING_AGENT_DIR", filepath.Join(home, ".pi", "agent"))
}
func qwenRoot(home string) string { return envOr(qwenHomeEnv, filepath.Join(home, ".qwen")) }

// Kimchi 1.24.3's entry point ignores config-root overrides and always uses
// homedir/.config/kimchi/harness. Only offer the root its runtime can load.
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

// importTitle keeps list titles compact without cutting UTF-8 characters.
func importTitle(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	if runes := []rune(text); len(runes) > 100 {
		return string(runes[:100]) + "…"
	}
	return text
}
