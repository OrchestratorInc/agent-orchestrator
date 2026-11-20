package primeagent

import (
	"context"
	"errors"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// importedRestoreCommand resumes the exact identity checkpointed by native
// preparation. There is no interactive initializer or terminal input: the
// provider already created its fork before AO launched the runtime.
func (p *Plugin) importedRestoreCommand(ctx context.Context, cfg ports.RestoreConfig, id string) ([]string, bool, error) {
	metadata := cfg.Session.Metadata
	root := metadata[ports.MetadataKeyImportConfigDir]
	if root == "" || id == metadata[ports.MetadataKeyImportSourceNativeID] || id != metadata[ports.MetadataKeyImportResumeNativeID] || id != strings.TrimSpace(id) {
		return nil, false, errors.New("prime import has no exact prepared native identity")
	}
	path, found, err := p.LocateTranscript(ctx, ports.NativeSessionRef{NativeSessionID: id, ConfigDir: root})
	if err != nil {
		return nil, false, err
	}
	if !found {
		return nil, false, errors.New("prepared prime native history is unavailable")
	}
	workspace, err := filepath.EvalSymlinks(cfg.Session.WorkspacePath)
	if err != nil {
		return nil, false, err
	}
	history, err := readPrimeNativeHistory(ctx, root, path, 0)
	if err != nil {
		return nil, false, err
	}
	if !primeSamePath(history.cwd, workspace) {
		return nil, false, errors.New("prepared prime history belongs to another workspace")
	}
	argv, err := p.baseCommand(ctx, cfg.DataDir, cfg.SystemPrompt, cfg.SystemPromptFile, cfg.Config)
	if err != nil {
		return nil, false, err
	}
	if err := p.requireDaemonSocket(ctx, argv[0]); err != nil {
		return nil, false, err
	}
	argv, err = primeCommandSessionDir(argv, root)
	if err != nil {
		return nil, false, err
	}
	return primeEndpointCommand(append(argv, "--cwd", workspace, "--resume", path), history.provider, cfg.Env), true, nil
}

// Reapply only an explicit launch override after shell startup. Without one,
// the native profile and login shell retain ownership of endpoint routing.
func primeEndpointCommand(argv []string, provider string, overrides map[string]string) []string {
	value, explicit := overrides["OPENAI_BASE_URL"]
	if !strings.EqualFold(strings.TrimSpace(provider), "openai") || !explicit {
		return argv
	}
	if value == "" {
		return append([]string{"env", "-u", "OPENAI_BASE_URL"}, argv...)
	}
	return append([]string{"env", "OPENAI_BASE_URL=" + value}, argv...)
}
