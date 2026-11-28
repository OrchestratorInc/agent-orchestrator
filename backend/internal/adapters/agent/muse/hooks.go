package muse

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/adapters/agent/hooksjson"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/adapters/agent/hookutil"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/ports"
)

const (
	museManagedHooksEnvVar = "TBH_MANAGED_HOOKS_PATH"
	museHookCommandPrefix  = "ao hooks muse "
	aoRunFileEnvVar        = "AO_RUN_FILE"
)

type museHooksFile struct {
	Hooks map[string][]hooksjson.MatcherGroup `json:"hooks"`
}

func museManagedHooks(cfg ports.WorkspaceHookConfig) map[string][]hooksjson.MatcherGroup {
	// Muse runs background subagents after the main turn has stopped. Their
	// tool hooks share this managed configuration but do not emit a matching
	// Stop, so installing PreToolUse/PostToolUse can incorrectly reactivate an
	// otherwise idle AO session. Track the top-level turn lifecycle instead;
	// structured input is a runtime-native TUI control and is detected from its
	// terminal marker rather than a tool hook.
	return map[string][]hooksjson.MatcherGroup{
		"SessionStart":      {museHookGroup(cfg, "session-start")},
		"UserPromptSubmit":  {museHookGroup(cfg, "user-prompt-submit")},
		"PermissionRequest": {museHookGroup(cfg, "permission-request")},
		"Stop":              {museHookGroup(cfg, "stop")},
	}
}

func museHookGroup(cfg ports.WorkspaceHookConfig, event string) hooksjson.MatcherGroup {
	return hooksjson.MatcherGroup{Hooks: []hooksjson.HookEntry{{
		Type:    "command",
		Command: museHookCommand(cfg, event),
		Timeout: 5,
	}}}
}

// Muse sanitizes AO_* variables from hook subprocesses. Put the non-sensitive
// callback route directly in AO's per-session hook command so the callback can
// identify its AO session and, in dev mode, the exact daemon that launched it.
func museHookCommand(cfg ports.WorkspaceHookConfig, event string) string {
	assignments := []string{
		"AO_SESSION_ID=" + museShellQuote(strings.TrimSpace(cfg.SessionID)),
		"AO_DATA_DIR=" + museShellQuote(strings.TrimSpace(cfg.DataDir)),
	}
	if runFile := strings.TrimSpace(os.Getenv(aoRunFileEnvVar)); runFile != "" {
		assignments = append(assignments, aoRunFileEnvVar+"="+museShellQuote(runFile))
	}
	return "env " + strings.Join(assignments, " ") + " " + museHookCommandPrefix + event
}

func museShellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

// GetAgentHooks writes Muse's managed-hook configuration under AO's data
// directory. Muse receives its path through a process-local environment
// variable, so this does not create or modify any file in the project.
func (p *Plugin) GetAgentHooks(ctx context.Context, cfg ports.WorkspaceHookConfig) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	path, err := museManagedHooksPath(cfg.DataDir, cfg.SessionID)
	if err != nil {
		return fmt.Errorf("muse.GetAgentHooks: %w", err)
	}
	data, err := json.MarshalIndent(museHooksFile{Hooks: museManagedHooks(cfg)}, "", "  ")
	if err != nil {
		return fmt.Errorf("muse.GetAgentHooks: marshal hooks: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("muse.GetAgentHooks: create hooks directory: %w", err)
	}
	if err := hookutil.AtomicWriteFile(path, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("muse.GetAgentHooks: write hooks: %w", err)
	}
	return nil
}

// CleanupWorkspace removes the AO-owned managed-hook file. The method name is
// part of the shared agent lifecycle; Muse's project workspace stays untouched.
func (p *Plugin) CleanupWorkspace(ctx context.Context, cfg ports.WorkspaceHookConfig) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	path, err := museManagedHooksPath(cfg.DataDir, cfg.SessionID)
	if err != nil {
		return fmt.Errorf("muse.CleanupWorkspace: %w", err)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("muse.CleanupWorkspace: remove hooks: %w", err)
	}
	return nil
}

func museManagedHooksPath(dataDir, sessionID string) (string, error) {
	dataDir = strings.TrimSpace(dataDir)
	if dataDir == "" {
		return "", errors.New("DataDir is required")
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return "", errors.New("SessionID is required")
	}
	if sessionID == "." || filepath.Base(sessionID) != sessionID {
		return "", errors.New("SessionID must be a single path component")
	}
	return filepath.Join(dataDir, "agent-hooks", adapterID, sessionID+".json"), nil
}

func museSystemPromptText(inline, file string) (string, error) {
	if strings.TrimSpace(inline) != "" {
		return strings.TrimRight(inline, "\n"), nil
	}
	if strings.TrimSpace(file) == "" {
		return "", nil
	}
	data, err := os.ReadFile(file) //nolint:gosec // path is AO-owned launch config
	if err != nil {
		return "", fmt.Errorf("read system prompt file: %w", err)
	}
	return strings.TrimRight(string(data), "\n"), nil
}
