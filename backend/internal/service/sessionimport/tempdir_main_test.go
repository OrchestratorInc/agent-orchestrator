package sessionimport

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMain resolves the temp root once so t.TempDir() fixtures are canonical.
// On macOS the default TMPDIR is under the /var -> /private/var symlink, and
// import fixtures compare paths the code canonicalizes.
func TestMain(m *testing.M) {
	if dir, err := filepath.EvalSymlinks(os.TempDir()); err == nil {
		_ = os.Setenv("TMPDIR", dir)
	}
	os.Exit(m.Run())
}
