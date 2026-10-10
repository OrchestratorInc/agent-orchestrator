package local

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	processutil "github.com/aoagents/agent-orchestrator/backend/internal/process"
	"github.com/aoagents/agent-orchestrator/backend/internal/skillassets"
)

func prepareTarget(ctx context.Context, frontend, commit, root string) error {
	dir := filepath.Join(root, "setup")
	if err := os.Mkdir(dir, 0o700); err != nil {
		return err
	}
	for _, name := range []string{"prepare.py", "prepare.cjs", "bootstrap.cjs"} {
		data, err := skillassets.TestingScript(name)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			return err
		}
	}
	cmd := processutil.CommandContext(ctx, "python3", filepath.Join(dir, "prepare.py"), "--repository", filepath.Dir(frontend), "--commit", commit)
	cmd.Env = strippedEnv(os.Environ())
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("prepare target: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func buildOwnedDaemon(ctx context.Context, frontend, executable string) error {
	if err := os.Mkdir(filepath.Dir(executable), 0o700); err != nil {
		return err
	}
	cmd := processutil.CommandContext(ctx, "go", "build", "-p", "2", "-o", executable, "./cmd/ao")
	cmd.Dir, cmd.Env = filepath.Join(filepath.Dir(frontend), "backend"), strippedEnv(os.Environ())
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("build owned daemon: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func (a *Adapter) prepareOwnedDaemon(ctx context.Context, s *launch) error {
	data, err := os.ReadFile(filepath.Join(s.frontend, ".vite", "testing-target.json"))
	if err != nil {
		return err
	}
	var manifest struct {
		Preflight json.RawMessage `json:"preflight"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return err
	}
	if len(manifest.Preflight) == 0 || string(manifest.Preflight) == "null" {
		return fmt.Errorf("unsupported_revision: prepared checkout has no checked runtime facts")
	}
	s.preflight = manifest.Preflight
	return a.ops.build(ctx, s.frontend, s.daemon)
}

func daemonCommand(executable string) string {
	return "exec '" + strings.ReplaceAll(executable, "'", "'\"'\"'") + "' daemon"
}
