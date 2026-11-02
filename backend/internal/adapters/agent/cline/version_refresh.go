package cline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// SupportsTerminalVersionRefresh declares unattended exact-conversation resume.
func (*Plugin) SupportsTerminalVersionRefresh() bool { return true }

// NativeConversationExists verifies the CLI's durable root session, not the
// SDK conversation/task ID in older hooks. Cline --id silently starts fresh
// when its saved history is missing, so check before stopping the old process.
func (*Plugin) NativeConversationExists(ctx context.Context, ref ports.SessionRef, nativeID string, env map[string]string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if nativeID == "" || nativeID == "." || nativeID == ".." || strings.ContainsAny(nativeID, "/\\:\x00") {
		return false, nil
	}
	home := env["HOME"]
	if runtime.GOOS == "windows" {
		home = env["USERPROFILE"]
	}
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return false, err
		}
	}
	dir := filepath.Join(home, ".cline", "data", "sessions", nativeID)
	var manifest struct {
		SessionID string `json:"session_id"`
		Cwd       string `json:"cwd"`
	}
	if err := readClineHistoryJSON(filepath.Join(dir, nativeID+".json"), &manifest); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	if manifest.SessionID != nativeID || manifest.Cwd == "" || ref.WorkspacePath == "" {
		return false, nil
	}
	savedDir, savedErr := os.Stat(manifest.Cwd)
	workspace, workspaceErr := os.Stat(ref.WorkspacePath)
	if savedErr != nil || workspaceErr != nil {
		return false, errors.Join(savedErr, workspaceErr)
	}
	if !os.SameFile(savedDir, workspace) {
		return false, nil
	}
	var history struct {
		SessionID string            `json:"sessionId"`
		Messages  []json.RawMessage `json:"messages"`
	}
	if err := readClineHistoryJSON(filepath.Join(dir, nativeID+".messages.json"), &history); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	return history.SessionID == nativeID && len(history.Messages) > 0, nil
}

func readClineHistoryJSON(path string, value any) error {
	file, err := os.Open(path) //nolint:gosec // nativeID is a validated filename, rooted in the launch home
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	const maxHistoryBytes = 16 << 20
	data, err := io.ReadAll(io.LimitReader(file, maxHistoryBytes+1))
	if err != nil {
		return err
	}
	if len(data) > maxHistoryBytes {
		return errors.New("cline native history exceeds verification limit")
	}
	if err := json.Unmarshal(data, value); err != nil {
		return fmt.Errorf("invalid Cline native history: %w", err)
	}
	return nil
}

var _ ports.AgentInterfaceHandoffHistoryProbe = (*Plugin)(nil)
