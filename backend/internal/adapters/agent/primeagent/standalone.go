package primeagent

import (
	"context"
	"os"
	"path/filepath"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hookutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// PrepareStandaloneWorkspace satisfies the normal launch command's required
// extension file without registering daemon lifecycle callbacks. The private
// artifact transport owns completion and cancellation for this launch.
func (p *Plugin) PrepareStandaloneWorkspace(ctx context.Context, cfg ports.WorkspaceHookConfig) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	path, err := extensionPath(cfg.DataDir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return hookutil.AtomicWriteFile(path, []byte("export default function standaloneDiagnostics() {}\n"), 0o600)
}
