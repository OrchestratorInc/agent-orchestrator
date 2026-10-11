package mastracode

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	_ "modernc.org/sqlite" // native, read-only local Mastra history

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var nativeIDPattern = regexp.MustCompile(`^[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12}$`)

func openHistory(state nativeState) (*sql.DB, error) {
	if _, err := os.Stat(state.Database); err != nil {
		return nil, fmt.Errorf("required native history: %w", err)
	}
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(state.Database)}
	return sql.Open("sqlite", u.String()+"?mode=ro&_pragma=busy_timeout(1000)")
}

func refuseExistingWorkspaceHistory(ctx context.Context, workspace string) error {
	state, err := readState(workspace)
	if err != nil {
		return err
	}
	if _, err := os.Stat(state.Database); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	db, err := openHistory(state)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	var count int
	probe, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	// A not-yet-initialized native DB is fresh. Existing malformed schemas fail
	// closed rather than silently turning an initial task into a native resume.
	var tables int
	if err := db.QueryRowContext(probe, `SELECT count(*) FROM sqlite_master WHERE type='table' AND name='mastra_threads'`).Scan(&tables); err != nil {
		return err
	}
	if tables == 0 {
		return nil
	}
	if err := db.QueryRowContext(probe, `SELECT count(*) FROM mastra_threads WHERE json_extract(metadata, '$.projectPath')=?`, workspace).Scan(&count); err != nil {
		return err
	}
	if count != 0 {
		return errors.New("mastra-code already has history for this workspace; fresh CLI launch would auto-resume and skip the initial task")
	}
	return nil
}

func validateHistory(ctx context.Context, workspace, id, model string, mode ports.PermissionMode) error {
	if !nativeIDPattern.MatchString(id) {
		return errors.New("mastra-code restore requires a complete native thread UUID; refusing fresh fallback")
	}
	state, err := readState(workspace)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(filepath.Join(state.Profile, "locks", id+".lock"))
	if err == nil {
		pid, parseErr := strconv.Atoi(strings.TrimSpace(string(data)))
		if parseErr != nil || pid <= 0 {
			return errors.New("invalid Mastra thread lock; refusing resume")
		}
		process, findErr := os.FindProcess(pid)
		if findErr != nil {
			return findErr
		}
		signalErr := process.Signal(syscall.Signal(0))
		_ = process.Release()
		if signalErr == nil || errors.Is(signalErr, syscall.EPERM) {
			return errors.New("mastra-code requested thread is locked by a live process")
		}
		if !errors.Is(signalErr, syscall.ESRCH) && !errors.Is(signalErr, os.ErrProcessDone) {
			return fmt.Errorf("cannot establish native lock ownership: %w", signalErr)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	db, err := openHistory(state)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	probe, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var path, savedModel sql.NullString
	var messages, yolo, escape int
	// Native metadata can be SQLite JSONB or legacy JSON text. SQLite's JSON
	// functions decode both; treating the persisted blob as JSON text is wrong.
	err = db.QueryRowContext(probe, `SELECT json_extract(metadata,'$.projectPath'), json_extract(metadata,'$.currentModelId'), COALESCE(json_extract(metadata,'$.yolo'),0), COALESCE(json_extract(metadata,'$.escapeAsCancel'),1), (SELECT count(*) FROM mastra_messages WHERE thread_id=mastra_threads.id) FROM mastra_threads WHERE id=?`, id).Scan(&path, &savedModel, &yolo, &escape, &messages)
	if err != nil {
		return fmt.Errorf("missing or unreadable native Mastra history: %w", err)
	}
	if !path.Valid || filepath.Clean(path.String) != filepath.Clean(workspace) || messages == 0 {
		return errors.New("mastra-code history must belong to this exact workspace and contain real messages")
	}
	if yolo != 0 && ports.NormalizePermissionMode(mode) != ports.PermissionModeBypassPermissions {
		return errors.New("saved Mastra thread has yolo enabled; explicit bypass is required")
	}
	if escape == 0 {
		return errors.New("saved Mastra thread disables Escape cancellation; enable it natively before AO restore")
	}
	if model != "" && (!savedModel.Valid || model != savedModel.String) {
		return errors.New("mastra-code resume model override is not qualified; retain the native saved model")
	}
	return nil
}

// GetRestoreCommand rejects missing, empty, foreign and locked native history.
// Mastra's own CLI can otherwise fall back to an unrelated fresh thread.
func (p *Plugin) GetRestoreCommand(ctx context.Context, cfg ports.RestoreConfig) ([]string, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if err := validatePolicy(cfg.Permissions, cfg.AllowedTools, cfg.DisallowedTools); err != nil {
		return nil, false, err
	}
	if err := validateTask(cfg.Prompt); err != nil {
		return nil, false, err
	}
	id := strings.TrimSpace(cfg.Session.Metadata[ports.MetadataKeyAgentSessionID])
	if err := validateHistory(ctx, cfg.Session.WorkspacePath, id, strings.TrimSpace(cfg.Config.Model), cfg.Permissions); err != nil {
		return nil, false, err
	}
	binary, err := p.ResolveBinary(ctx)
	if err != nil {
		return nil, false, err
	}
	cmd := []string{binary, "resume", id}
	if cfg.Prompt != "" {
		cmd = append(cmd, "--tui-prompt", cfg.Prompt)
	}
	return cmd, true, nil
}

func receiptPath(dataDir, session, generation string) string {
	return filepath.Join(dataDir, adapterID, "launches", session, generation+".json")
}

// VerifyNativeRestore checks an actual current-launch native callback and the
// initialized current composer, before AO publishes a restored generation.
func (*Plugin) VerifyNativeRestore(ctx context.Context, cfg ports.RestoreConfig, generation, output string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	data, err := os.ReadFile(receiptPath(cfg.DataDir, cfg.Session.ID, generation))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var receipt struct {
		NativeID   string `json:"nativeId"`
		Cwd        string `json:"cwd"`
		Generation string `json:"generation"`
	}
	if err := json.Unmarshal(data, &receipt); err != nil {
		return false, err
	}
	if receipt.Generation != generation || filepath.Clean(receipt.Cwd) != filepath.Clean(cfg.Session.WorkspacePath) {
		return false, errors.New("mastra-code restore witness is stale or belongs to another workspace")
	}
	if receipt.NativeID != cfg.Session.Metadata[ports.MetadataKeyAgentSessionID] {
		return false, errors.New("mastra-code resumed a different native thread; refusing fallback")
	}
	return (&Plugin{}).ComposerIsEmpty(output), nil
}
