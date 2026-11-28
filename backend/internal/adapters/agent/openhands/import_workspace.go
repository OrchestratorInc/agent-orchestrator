package openhands

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// validateImportedOpenHandsWorkspace checks the effective launch environment.
// CLI 283c6ac locations.py:26-31 gives OPENHANDS_WORK_DIR precedence over cwd;
// it must not redirect a native imported conversation to its old workspace.
func validateImportedOpenHandsWorkspace(ctx context.Context, cfg ports.WorkspaceHookConfig) error {
	if cfg.ImportConfigDir == "" {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	value, present := cfg.Env["OPENHANDS_WORK_DIR"]
	if !present {
		value, present = os.LookupEnv("OPENHANDS_WORK_DIR")
	}
	if !present {
		return nil
	}
	if !filepath.IsAbs(value) || !filepath.IsAbs(cfg.WorkspacePath) {
		return errors.New("openhands imported workdir must be the absolute AO worktree")
	}
	actual, err := filepath.EvalSymlinks(value)
	if err != nil {
		return errors.New("openhands imported workdir is unavailable")
	}
	expected, err := filepath.EvalSymlinks(cfg.WorkspacePath)
	if err != nil {
		return errors.New("openhands AO worktree is unavailable")
	}
	if actual != expected {
		return errors.New("openhands imported workdir differs from the AO worktree")
	}
	return nil
}
