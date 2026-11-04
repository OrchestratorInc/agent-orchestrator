package sessionimport

import (
	"context"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// harnessImporter keeps each additional provider's layout and parser in its own
// file. Registration adds one map entry here and one IMPORTABLE_AGENTS entry in
// the UI. The callbacks must only read provider state, confine file reads with
// os.Root (or open SQLite mode=ro), and return the exact CLI resume identity.
// A nil nativeID uses sourceFiles.key, which discovery can set to the native ID.
type harnessImporter struct {
	root     func(home string) string
	discover func(context.Context, string) ([]sourceFiles, error)
	read     func(context.Context, string, ...string) (transcript, error)
	nativeID func(sourceFiles) string
	// discoverRegistered receives only explicitly registered project/repository
	// paths. Repo-local providers leave root nil and return a root contained in
	// one supplied path; they must not search parents or unrelated repositories.
	discoverRegistered func(context.Context, []string) ([]sourceFiles, error)
	// mode can force Terminal import when a provider's Chat driver cannot load
	// the native format being imported. Empty preserves capability selection.
	mode domain.SessionMode
	// Global stores can also need registered paths to resolve older metadata.
	discoverWithProjects func(context.Context, string, []string) ([]sourceFiles, error)
	readWithProjects     func(context.Context, string, []string, ...string) (transcript, error)
}

// Keep entries immutable after package initialization: scans read them in parallel.
var harnessImporters = map[domain.AgentHarness]harnessImporter{
	domain.HarnessAider: {discoverRegistered: discoverAiderRegistered, read: readAiderTranscript, mode: domain.SessionModeTUI},

	domain.HarnessAuggie: {root: auggieRoot, discover: discoverAuggie, read: readAuggieTranscript, mode: domain.SessionModeTUI},
	domain.HarnessDevin:  {root: devinRoot, discover: discoverDevin, read: readDevinTranscript},
	domain.HarnessCline:  {root: clineRoot, discover: discoverCline, read: readClineTranscript},
	domain.HarnessCursor: {root: cursorRoot, discover: discoverCursor, read: readCursorTranscript, mode: domain.SessionModeTUI},
	domain.HarnessGrok:   {root: grokRoot, discover: discoverGrok, read: readGrokTranscript},
	domain.HarnessGemini: {root: geminiRoot, discover: discoverGemini, read: readGeminiTranscript, discoverWithProjects: discoverGeminiWithProjects, readWithProjects: readGeminiTranscriptWithProjects},
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
