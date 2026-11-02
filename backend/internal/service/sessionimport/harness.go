package sessionimport

import (
	"context"

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
}

// Keep entries immutable after package initialization: scans read them in parallel.
var harnessImporters = map[domain.AgentHarness]harnessImporter{
	domain.HarnessAutohand: {root: autohandRoot, discover: discoverAutohand, read: readAutohandTranscript},
}
