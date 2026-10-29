package stakpak

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hookutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// The Stakpak TUI has no permission flag. It reads per-workspace tool
// policies from .stakpak/session/auto_approve.json, relative to its cwd, and
// merges them over its defaults. AO writes that file for accept-edits, auto
// and bypass, and marks it as AO's so a later manual launch removes it again.
//
// Stakpak always prompts for shell tools (run_command and friends) whatever
// the file says, so auto and bypass approve every other tool and the shell
// commands still ask in the terminal.
const (
	policyDir     = ".stakpak/session"
	policyFile    = "auto_approve.json"
	policyMarker  = "ao-permission-mode"
	policyAuto    = "Auto"
	policyPrompt  = "Prompt"
	policyIgnored = "*\n"
)

// editTools are the file-changing tools accept-edits approves.
var editTools = []string{"create", "str_replace", "remove"}

// otherTools are approved too in auto and bypass. Shell tools are listed so
// the file states the intent, even though Stakpak re-prompts for them.
var otherTools = []string{
	"generate_code", "dynamic_subagent_task", "cancel_task",
	"run_command", "run_command_task", "run_remote_command", "run_remote_command_task",
}

type autoApproveConfig struct {
	Enabled         bool              `json:"enabled"`
	DefaultPolicy   string            `json:"default_policy"`
	Tools           map[string]string `json:"tools"`
	CommandPatterns commandPatterns   `json:"command_patterns"`
}

type commandPatterns struct {
	SafeReadonly         []string `json:"safe_readonly"`
	SensitiveDestructive []string `json:"sensitive_destructive"`
	InteractiveRequired  []string `json:"interactive_required"`
}

func policyFor(mode ports.PermissionMode) (autoApproveConfig, bool) {
	config := autoApproveConfig{
		Enabled:       true,
		DefaultPolicy: policyPrompt,
		Tools:         map[string]string{},
		CommandPatterns: commandPatterns{
			SafeReadonly: []string{}, SensitiveDestructive: []string{}, InteractiveRequired: []string{},
		},
	}
	switch ports.NormalizePermissionMode(mode) {
	case ports.PermissionModeAcceptEdits:
		for _, tool := range editTools {
			config.Tools[tool] = policyAuto
		}
	case ports.PermissionModeAuto, ports.PermissionModeBypassPermissions:
		config.DefaultPolicy = policyAuto
		for _, tool := range append(append([]string(nil), editTools...), otherTools...) {
			config.Tools[tool] = policyAuto
		}
	default:
		return autoApproveConfig{}, false
	}
	return config, true
}

// PreLaunch applies AO's permission mode before every fresh launch and
// restore. A policy file the user wrote themselves is left alone.
func (*Plugin) PreLaunch(ctx context.Context, cfg ports.LaunchConfig) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(cfg.WorkspacePath) == "" {
		return nil
	}
	return applyPermissionMode(cfg.WorkspacePath, cfg.Permissions)
}

func applyPermissionMode(workspace string, mode ports.PermissionMode) error {
	dir := filepath.Join(workspace, filepath.FromSlash(policyDir))
	path := filepath.Join(dir, policyFile)
	marker := filepath.Join(dir, policyMarker)
	_, markerErr := os.Stat(marker)
	ours := markerErr == nil
	_, fileErr := os.Stat(path)
	userOwned := fileErr == nil && !ours

	config, apply := policyFor(mode)
	if !apply {
		if !ours {
			return nil
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("stakpak: remove AO tool policy: %w", err)
		}
		if err := os.Remove(marker); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("stakpak: remove AO tool policy marker: %w", err)
		}
		return nil
	}
	if userOwned {
		return nil
	}
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("stakpak: create %s: %w", policyDir, err)
	}
	// Keep the session footprint out of git even when Stakpak's own
	// .gitignore update is turned off.
	ignore := filepath.Join(dir, ".gitignore")
	if _, err := os.Stat(ignore); errors.Is(err, os.ErrNotExist) {
		if err := hookutil.AtomicWriteFile(ignore, []byte(policyIgnored), 0o600); err != nil {
			return fmt.Errorf("stakpak: write %s: %w", ignore, err)
		}
	}
	if err := hookutil.AtomicWriteFile(path, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("stakpak: write tool policy: %w", err)
	}
	if err := hookutil.AtomicWriteFile(marker, []byte(string(ports.NormalizePermissionMode(mode))+"\n"), 0o600); err != nil {
		return fmt.Errorf("stakpak: write tool policy marker: %w", err)
	}
	return nil
}
