package cortexacp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const maxInstructionBytes = 64 << 10

func validateInstructions(prompt string, enabled bool) error {
	if prompt == "" {
		return nil
	}
	if !enabled {
		return fmt.Errorf("%w: Cortex ACP additive standing instructions are not qualified", ports.ErrChatDriverIncompatible)
	}
	if (runtime.GOOS != "linux" && runtime.GOOS != "darwin") || len(prompt) > maxInstructionBytes || !utf8.ValidString(prompt) || strings.ContainsRune(prompt, 0) {
		return fmt.Errorf("%w: Cortex instruction candidate requires bounded UTF-8 context and POSIX hooks", ports.ErrChatDriverIncompatible)
	}
	return nil
}

// instructionPlugin creates launch-owned data, never modifies a native/user or
// project plugin, and never interpolates instructions into a shell command.
// UserPromptSubmit is used because native SessionStart alone does not establish
// per-prompt context delivery on ACP. This supported plugin route is experimental
// until authenticated fresh/restore consumption is qualified on the stock CLI.
// https://docs.snowflake.com/en/user-guide/cortex-code/extensibility#hooks
// https://docs.snowflake.com/en/user-guide/cortex-code/cortex-code-plugins
func instructionPlugin(ctx context.Context, dataDir, prompt string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := validateInstructions(prompt, true); err != nil {
		return "", err
	}
	// A fresh private directory on every cold launch avoids overwriting a surviving
	// host's context. Shared persistenthost never invokes Configure on live adoption.
	dir, err := os.MkdirTemp(dataDir, ".cortex-acp-instructions-")
	if err != nil {
		return "", fmt.Errorf("create Cortex instruction plugin: %w", err)
	}
	complete := false
	defer func() {
		if !complete {
			_ = os.RemoveAll(dir)
		}
	}()
	manifestDir := filepath.Join(dir, ".cortex-plugin")
	if err := os.Mkdir(manifestDir, 0700); err != nil {
		return "", err
	}
	output, err := json.Marshal(map[string]any{
		"hookSpecificOutput": map[string]string{"hookEventName": "UserPromptSubmit", "additionalContext": prompt},
	})
	if err != nil {
		return "", err
	}
	// Absolute POSIX paths cannot be options. Shell quoting applies only to the
	// generated data path; quotes, substitutions and newlines in context are JSON.
	command := "/bin/cat " + "'" + strings.ReplaceAll(filepath.Join(dir, "context.json"), "'", "'\"'\"'") + "'"
	manifest, err := json.Marshal(map[string]any{
		"name":        "ao-private-context-" + strings.TrimPrefix(filepath.Base(dir), ".cortex-acp-instructions-"),
		"version":     "0.1.0",
		"description": "AO qualification-only additive per-prompt context",
		"hooks": map[string]any{"UserPromptSubmit": []any{map[string]any{
			"hooks": []any{map[string]any{"type": "command", "command": command, "timeout": 5}},
		}}},
	})
	if err != nil {
		return "", err
	}
	for _, file := range []struct {
		name string
		data []byte
	}{
		{filepath.Join(dir, "context.json"), output},
		{filepath.Join(manifestDir, "plugin.json"), manifest},
	} {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		f, err := os.OpenFile(file.name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return "", err
		}
		_, writeErr := f.Write(file.data)
		closeErr := f.Close()
		if writeErr != nil {
			return "", writeErr
		}
		if closeErr != nil {
			return "", closeErr
		}
	}
	complete = true
	return dir, nil
}
