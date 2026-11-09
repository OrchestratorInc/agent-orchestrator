package zcode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hooksjson"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// ValidateProtocolHookBudget refuses configured competing prompt hooks: their
// runtime output cannot be bounded without executing user commands. Native's
// aggregate 24,000-character cap must not silently truncate AO instructions.
func ValidateProtocolHookBudget(ctx context.Context, cfg ports.WorkspaceHookConfig) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	if value := cfg.Env["HOME"]; value != "" {
		home = value
	}
	paths := []string{filepath.Join(home, ".zcode", "cli", "config.json")}
	for current := cfg.WorkspacePath; current != ""; current = filepath.Dir(current) {
		paths = append(paths, filepath.Join(current, "zcode.json"), filepath.Join(current, ".zcode", "config.json"))
		if _, err := os.Stat(filepath.Join(current, ".git")); err == nil {
			break
		}
		if filepath.Dir(current) == current {
			break
		}
	}
	managed := filepath.Clean(filepath.Join(cfg.WorkspacePath, ".zcode", "config.json"))
	for _, path := range paths {
		file, err := os.Open(path) //nolint:gosec // native documented config discovery, read-only.
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		data, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
		_ = file.Close()
		if err != nil {
			return err
		}
		if len(data) > 1<<20 {
			return fmt.Errorf("zcode: config exceeds supported inspection bound")
		}
		var config struct {
			Hooks *struct {
				MaxOutputBytes *int                                `json:"maxOutputBytes"`
				Enabled        *bool                               `json:"enabled"`
				Events         map[string][]hooksjson.MatcherGroup `json:"events"`
			} `json:"hooks"`
		}
		if err := json.Unmarshal(data, &config); err != nil {
			return fmt.Errorf("zcode: cannot preserve malformed native hook configuration: %w", err)
		}
		if config.Hooks == nil {
			continue
		}
		if config.Hooks.Enabled != nil && !*config.Hooks.Enabled {
			return fmt.Errorf("zcode: native hooks are explicitly disabled; AO cannot override this choice")
		}
		if config.Hooks.MaxOutputBytes != nil && *config.Hooks.MaxOutputBytes < nativeHookOutputBytes {
			return fmt.Errorf("zcode: configured native hook output limit is below the supported %d-byte budget", nativeHookOutputBytes)
		}
		for _, group := range config.Hooks.Events["UserPromptSubmit"] {
			for _, hook := range group.Hooks {
				if filepath.Clean(path) == managed && hook.Command == hookCommandPrefix+"user-prompt-submit" {
					continue
				}
				return fmt.Errorf("zcode: another UserPromptSubmit hook has unbounded context output; preserve it and use another harness")
			}
		}
	}
	return nil
}
