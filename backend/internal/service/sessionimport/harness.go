package sessionimport

import (
	"context"
	"os"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// harnessImporter keeps a provider's layout and parser in its own file.
// Registration adds one map entry here, its importedHarnesses hooks in
// session_manager, and one IMPORTABLE_AGENTS entry in the UI; the harnesses
// Scan lists itself are dispatched in service.go and transcript.go instead.
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
	discoverWithProjects func(context.Context, string, []string) ([]sourceFiles, error)
	readWithProjects     func(context.Context, string, []string, ...string) (transcript, error)
}

// Keep entries immutable after package initialization: scans read them in parallel.
var harnessImporters = map[domain.AgentHarness]harnessImporter{
	domain.HarnessAider: {discoverRegistered: discoverAiderRegistered, read: readAiderTranscript, mode: domain.SessionModeTUI},

	domain.HarnessCline:  {root: clineRoot, discover: discoverCline, read: readClineTranscript},
	domain.HarnessCursor: {root: cursorRoot, discover: discoverCursor, read: readCursorTranscript},
	domain.HarnessGrok:   {root: grokRoot, discover: discoverGrok, read: readGrokTranscript},
	domain.HarnessGemini: {root: geminiRoot, discoverWithProjects: discoverGeminiWithProjects, readWithProjects: readGeminiTranscriptWithProjects},
}

func readTranscriptForProjects(ctx context.Context, harness domain.AgentHarness, root string, projects []string, paths ...string) (t transcript, err error) {
	if read := harnessImporters[harness].readWithProjects; read != nil {
		t, err = read(ctx, root, projects, paths...)
	} else {
		t, err = readTranscript(ctx, harness, root, paths...)
	}
	if t.last.IsZero() {
		if info, statErr := os.Stat(t.path); statErr == nil {
			t.last = info.ModTime()
		}
	}
	t.keepRecentMessages()
	return t, err
}

// importTitle keeps list titles compact without cutting UTF-8 characters.
func importTitle(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	if runes := []rune(text); len(runes) > 100 {
		return string(runes[:100]) + "…"
	}
	return text
}
