package hooksjson

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hookutil"
)

// hooksFileMaxBytes bounds reading an agent-writable settings file.
const hooksFileMaxBytes = 16 << 20

// The workspace is agent-writable, so every hooks file access goes through an
// os.Root opened on it (the trust boundary); the shared Lstat-per-component,
// remove-the-squatter, temp+rename discipline lives in hookutil/rootfs.go.
// A symlink or other non-regular entry in place of the file is treated as
// absent and replaced on write; a symlink (or file) in place of an AO-owned
// parent directory is removed, never followed, and recreated as a real dir.

// openWorkspaceRoot opens workspacePath as a root and returns the hooks file's
// path relative to it. A hooks path outside the workspace is refused.
func openWorkspaceRoot(workspacePath, hooksPath string) (*os.Root, string, error) {
	rel, err := filepath.Rel(workspacePath, hooksPath)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return nil, "", fmt.Errorf("hooks path %s is outside workspace %s", hooksPath, workspacePath)
	}
	root, err := os.OpenRoot(workspacePath)
	if err != nil {
		return nil, "", err
	}
	return root, rel, nil
}

func readRootFile(root *os.Root, rel string) ([]byte, bool, error) {
	return hookutil.ReadRootFile(root, rel, hooksFileMaxBytes)
}

func writeRootFile(root *os.Root, rel string, data []byte) error {
	return hookutil.WriteRootFile(root, rel, data)
}
