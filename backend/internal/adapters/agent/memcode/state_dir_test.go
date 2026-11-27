package memcode

import (
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hookutil"
)

// transcript (restore.go) reads .memcode/sessions/<id>/messages.json, a
// directory AO's .gitignore lists. Scratch teardown keeps a folder holding a
// transcript only while hookutil registers that directory as restorable.
func TestTranscriptDirectoryIsRestorableState(t *testing.T) {
	if !hookutil.IsRestorableStateDir(filepath.Join(".memcode", "sessions")) {
		t.Fatal(".memcode/sessions holds memcode transcripts but is not registered as restorable state")
	}
}
