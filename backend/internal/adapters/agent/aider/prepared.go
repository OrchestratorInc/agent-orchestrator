package aider

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/importcopy"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const aiderPreparationFile = ".aider.chat.history.md.ao-import.json"

type aiderPreparation struct {
	Source      string `json:"source"`
	ID          string `json:"id"`
	CWD         string `json:"cwd"`
	Fingerprint string `json:"fingerprint"`
}

func aiderPreparationFor(ctx context.Context, r ports.ImportedSessionRelocation) (aiderPreparation, error) {
	if err := ctx.Err(); err != nil {
		return aiderPreparation{}, err
	}
	cwd, err := filepath.EvalSymlinks(r.LaunchCWD)
	if err != nil {
		return aiderPreparation{}, err
	}
	return aiderPreparation{Source: filepath.Clean(r.TranscriptPath), ID: r.NativeSessionID, CWD: cwd, Fingerprint: r.SourceFingerprint}, nil
}

// A proven copy can outlive its original even when checkpoint persistence failed.
// Only the private workspace is read; source identity comes from the selection.
func recoverAiderPreparation(ctx context.Context, root *os.Root, r ports.ImportedSessionRelocation) (bool, error) {
	if r.SourceFingerprint == "" {
		return false, nil
	}
	raw, err := readAiderFile(ctx, root, aiderPreparationFile, 16<<10)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	want, err := aiderPreparationFor(ctx, r)
	if err != nil {
		return false, err
	}
	var got aiderPreparation
	if json.Unmarshal(raw, &got) != nil || got != want {
		return false, errors.New("aider private preparation belongs to a different selection")
	}
	marker, err := readAiderFile(ctx, root, aiderHistoryMarker, 128)
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(string(marker)) != r.NativeSessionID {
		return false, errors.New("aider private preparation identity changed")
	}
	data, err := aiderHistory(ctx, root)
	if err != nil {
		return false, err
	}
	if NativeHistoryFingerprint(data) != r.SourceFingerprint {
		return false, errors.New("aider private preparation history changed")
	}
	return true, nil
}

func writeAiderPreparation(ctx context.Context, root *os.Root, r ports.ImportedSessionRelocation) error {
	if r.SourceFingerprint == "" {
		return nil
	}
	want, err := aiderPreparationFor(ctx, r)
	if err != nil {
		return err
	}
	data, err := json.Marshal(want)
	if err != nil {
		return err
	}
	if len(data) > 16<<10 {
		return errors.New("aider private preparation exceeds read budget")
	}
	if err := importcopy.WriteOnce(ctx, root, aiderPreparationFile, data); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	verified, err := recoverAiderPreparation(ctx, root, r)
	if err != nil {
		return err
	}
	if !verified {
		return errors.New("aider private preparation was not published")
	}
	return nil
}
