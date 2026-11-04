package cline

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func importedClineIdentity(ref ports.SessionRef) (string, string, error) {
	source := ref.Metadata[ports.MetadataKeyImportSourceNativeID]
	id := ref.Metadata[ports.MetadataKeyImportResumeNativeID]
	if id == "" {
		id = source
	}
	root := ref.Metadata[ports.MetadataKeyImportConfigDir]
	if !clineNativeID.MatchString(source) || !clineNativeID.MatchString(id) || ref.Metadata[ports.MetadataKeyAgentSessionID] != id || !filepath.IsAbs(root) || !filepath.IsAbs(ref.WorkspacePath) {
		return "", "", ports.ErrNativeHistoryUnavailable
	}
	return id, filepath.Clean(root), nil
}

func readImportedClineJSON(ctx context.Context, root *os.Root, path string, value any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	info, err := root.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > 16<<20 {
		return errors.New("cline imported history is not a bounded regular file")
	}
	f, err := root.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, (16<<20)+1))
	if err != nil {
		return err
	}
	if len(data) > 16<<20 {
		return errors.New("cline imported history exceeds read budget")
	}
	if err := json.Unmarshal(data, value); err != nil {
		return err
	}
	return ctx.Err()
}

func importedClineConversationExists(ctx context.Context, ref ports.SessionRef, nativeID string, env map[string]string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	id, configDir, err := importedClineIdentity(ref)
	if err != nil || nativeID != id {
		return false, ports.ErrNativeHistoryUnavailable
	}
	// Metadata names the selected import root. An explicit launch override
	// must agree, so validation cannot succeed for a different account/store.
	_, dataSet := env["CLINE_DATA_DIR"]
	_, configSet := env["CLINE_DIR"]
	if dataSet || configSet {
		resolved, err := (&Plugin{}).NativeSessionConfigDir(ctx, env)
		if err != nil {
			return false, err
		}
		selected, selectedErr := os.Stat(configDir)
		actual, actualErr := os.Stat(resolved)
		if selectedErr != nil || actualErr != nil || !os.SameFile(selected, actual) {
			return false, errors.Join(ports.ErrNativeHistoryUnavailable, selectedErr, actualErr)
		}
	}
	workspace, err := os.Stat(ref.WorkspacePath)
	if err != nil || !workspace.IsDir() {
		return false, errors.Join(ports.ErrNativeHistoryUnavailable, err)
	}
	root, err := os.OpenRoot(configDir)
	if err != nil {
		return false, errors.Join(ports.ErrNativeHistoryUnavailable, err)
	}
	defer func() { _ = root.Close() }()
	dir := filepath.Join("sessions", id)
	var manifest struct {
		ID  string `json:"session_id"`
		CWD string `json:"cwd"`
	}
	if err := readImportedClineJSON(ctx, root, filepath.Join(dir, id+".json"), &manifest); err != nil {
		return false, errors.Join(ports.ErrNativeHistoryUnavailable, err)
	}
	if manifest.ID != id || !filepath.IsAbs(manifest.CWD) {
		return false, ports.ErrNativeHistoryUnavailable
	}
	var history struct {
		ID       string            `json:"sessionId"`
		Messages []json.RawMessage `json:"messages"`
	}
	if err := readImportedClineJSON(ctx, root, filepath.Join(dir, id+".messages.json"), &history); err != nil {
		return false, errors.Join(ports.ErrNativeHistoryUnavailable, err)
	}
	if history.ID != id || len(history.Messages) == 0 {
		return false, ports.ErrNativeHistoryUnavailable
	}
	// The saved cwd is provenance. Cline's interactive loader reads by id,
	// then starts that same id with current config; argv pins --cwd below.
	return true, nil
}

func (p *Plugin) importedRestoreCommand(ctx context.Context, cfg ports.RestoreConfig) ([]string, string, bool, error) {
	id, _, err := importedClineIdentity(cfg.Session)
	if err != nil {
		return nil, "", false, err
	}
	if exists, err := importedClineConversationExists(ctx, cfg.Session, id, nil); err != nil || !exists {
		return nil, "", false, errors.Join(ports.ErrNativeHistoryUnavailable, err)
	}
	binary, err := p.clineBinary(ctx)
	if err != nil {
		return nil, "", false, err
	}
	cmd := []string{binary}
	appendApprovalFlags(&cmd, cfg.Permissions)
	appendModelFlag(&cmd, cfg.Config)
	if cfg.SystemPrompt != "" {
		cmd = append(cmd, "-s", cfg.SystemPrompt)
	}
	cmd = append(cmd, "--cwd", cfg.Session.WorkspacePath, "--id", id)
	return cmd, id, true, nil
}
