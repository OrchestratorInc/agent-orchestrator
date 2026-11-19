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
	// baseCommand puts the session directory immediately after its first flag.
	argv[2] = root
	return primeEndpointCommand(append(argv, "--cwd", workspace, "--resume", path), history.provider), true, nil
}

// Apply after the login shell has run, so an unrelated shell export cannot
// redirect the official OpenAI provider or its credential to another endpoint.
func primeEndpointCommand(argv []string, provider string) []string {
	if strings.EqualFold(provider, "openai") {
		return append([]string{"env", "OPENAI_BASE_URL=https://api.openai.com/v1"}, argv...)
	}
	return argv
}
