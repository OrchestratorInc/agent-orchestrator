package sessionmanager

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/testenv"
)

// TestMain also resolves the temp root once so t.TempDir() fixtures are
// canonical. On macOS the default TMPDIR is under the /var -> /private/var
// symlink, and import fixtures compare paths the code canonicalizes.
func TestMain(m *testing.M) {
	testenv.ClearSession()
	if dir, err := filepath.EvalSymlinks(os.TempDir()); err == nil {
		_ = os.Setenv("TMPDIR", dir)
	}
	os.Exit(m.Run())
}
