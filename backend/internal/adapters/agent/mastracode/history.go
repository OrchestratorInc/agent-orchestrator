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
	"slices"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite" // native, read-only local Mastra history

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/processalive"
)

var nativeIDPattern = regexp.MustCompile(`^[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12}$`)

func openHistory(state nativeState) (*sql.DB, error) {
	if _, err := os.Stat(state.Database); err != nil {
		return nil, fmt.Errorf("required native history: %w", err)
	}
	u := url.URL{Path: state.Database}
	return sql.Open("sqlite", "file:"+u.EscapedPath()+"?mode=ro&_pragma=busy_timeout(1000)")
}

func validateHistory(ctx context.Context, workspace, id string, mode ports.PermissionMode, state nativeState) error {
	if !nativeIDPattern.MatchString(id) {
		return errors.New("mastra-code restore requires a complete native thread UUID; refusing fresh fallback")
	}
	data, err := os.ReadFile(filepath.Join(state.Profile, "locks", id+".lock"))
	if err == nil {
		pid, parseErr := strconv.Atoi(strings.TrimSpace(string(data)))
		if parseErr != nil || pid <= 0 {
			return errors.New("invalid Mastra thread lock; refusing resume")
		}
		// Match native PID-file semantics using AO's cross-platform probe.
		if processalive.Alive(pid) {
			return errors.New("mastra-code requested thread is locked by a live process")
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
	var path sql.NullString
	var messages, yolo, escape int
	// Native metadata can be SQLite JSONB or legacy JSON text. SQLite's JSON
	// functions decode both; treating the persisted blob as JSON text is wrong.
	err = db.QueryRowContext(probe, `SELECT json_extract(metadata,'$.projectPath'), COALESCE(json_extract(metadata,'$.yolo'),0), COALESCE(json_extract(metadata,'$.escapeAsCancel'),1), (SELECT count(*) FROM mastra_messages WHERE thread_id=mastra_threads.id) FROM mastra_threads WHERE id=?`, id).Scan(&path, &yolo, &escape, &messages)
	if err != nil {
		return fmt.Errorf("missing or unreadable native Mastra history: %w", err)
	}
	if !path.Valid || !sameWorkspace(path.String, workspace) || messages == 0 {
		return errors.New("mastra-code history must belong to this exact workspace and contain real messages")
	}
	if yolo != 0 && ports.NormalizePermissionMode(mode) != ports.PermissionModeBypassPermissions {
		return errors.New("saved Mastra thread has yolo enabled; explicit bypass is required")
	}
	if escape == 0 {
		return errors.New("saved Mastra thread disables Escape cancellation; enable it natively before AO restore")
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
	state, err := readState(cfg.Session.WorkspacePath)
	if err != nil {
		return nil, false, err
	}
	current := state.Database
	var historyErr error
	for _, database := range append([]string{current}, state.PreviousDatabases...) {
		candidate := state
		candidate.Database = database
		historyErr = validateHistory(ctx, cfg.Session.WorkspacePath, id, cfg.Permissions, candidate)
		if historyErr == nil {
			state.Database = database
			break
		}
	}
	if historyErr != nil {
		return nil, false, historyErr
	}
	if !slices.Contains(state.PreviousDatabases, current) {
		state.PreviousDatabases = append(state.PreviousDatabases, current)
	}
	binary, err := p.ResolveBinary(ctx)
	if err != nil {
		return nil, false, err
	}
	state.LaunchModel = ""
	if err := writeNativeState(cfg.DataDir, cfg.Session.ID, cfg.Session.WorkspacePath, state); err != nil {
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
	if receipt.Generation != generation || !sameWorkspace(receipt.Cwd, cfg.Session.WorkspacePath) {
		return false, errors.New("mastra-code restore witness is stale or belongs to another workspace")
	}
	if receipt.NativeID != cfg.Session.Metadata[ports.MetadataKeyAgentSessionID] {
		return false, errors.New("mastra-code resumed a different native thread; refusing fallback")
	}
	return (&Plugin{}).ComposerIsEmpty(output), nil
}

func sameWorkspace(a, b string) bool {
	if resolved, err := filepath.EvalSymlinks(a); err == nil {
		a = resolved
	}
	if resolved, err := filepath.EvalSymlinks(b); err == nil {
		b = resolved
	}
	return filepath.Clean(a) == filepath.Clean(b)
}
