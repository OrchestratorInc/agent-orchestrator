package stakpak

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const maxInstructionBytes = 128 * 1024

// freshArgs prepares only source-backed argv, not permission admission. Keep
// this private until native qualification supplies the missing launch gates.
// Contract: cli/src/main.rs Cli positional prompt and --workdir; interactive
// system message insertion at mode_interactive.rs:525 followed by native
// TaskBoardContextHook prepending its embedded base prompt.
// Native main.rs trims file content before insertion; validation checks the
// original file against AO text, not byte-for-byte model consumption.
func freshArgs(ctx context.Context, cfg ports.LaunchConfig) ([]string, error) {
	if err := ctx.Err(); err != nil { return nil, err }
	if cfg.Config != (ports.AgentConfig{}) || cfg.Permissions != "" ||
		len(cfg.AllowedTools) != 0 || len(cfg.DisallowedTools) != 0 || cfg.NativeSessionID != "" {
		return nil, errors.New("stakpak preparation does not support model, permission, tool or native identity overrides")
	}
	if !filepath.IsAbs(cfg.WorkspacePath) || strings.ContainsRune(cfg.WorkspacePath, 0) {
		return nil, errors.New("stakpak requires an absolute workspace")
	}
	if !utf8.ValidString(cfg.Prompt) || strings.ContainsRune(cfg.Prompt, 0) {
		return nil, errors.New("stakpak prompt must be UTF-8 without NUL")
	}
	args := []string{"--workdir", cfg.WorkspacePath}
	if cfg.SystemPrompt != "" || cfg.SystemPromptFile != "" {
		if err := validateInstructions(ctx, cfg.SystemPromptFile, cfg.SystemPrompt); err != nil { return nil, err }
		args = append(args, "--system-prompt-file", cfg.SystemPromptFile)
	}
	// The option terminator also prevents a task equal to a native subcommand
	// from selecting that subcommand. Native Clap behavior still needs live proof.
	if cfg.Prompt != "" { args = append(args, "--", cfg.Prompt) }
	return args, nil
}

func validateInstructions(ctx context.Context, path, expected string) error {
	if err := ctx.Err(); err != nil { return err }
	if !filepath.IsAbs(path) || strings.ContainsRune(path, 0) {
		return errors.New("stakpak instructions require an absolute file path")
	}
	info, err := os.Lstat(path)
	if err != nil { return errors.New("stakpak instruction file is unavailable") }
	if !info.Mode().IsRegular() || info.Size() > maxInstructionBytes {
		return errors.New("stakpak instruction file must be a bounded regular file")
	}
	file, err := os.Open(path)
	if err != nil { return errors.New("stakpak instruction file is unreadable") }
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		return errors.New("stakpak instruction file changed during validation")
	}
	content, err := io.ReadAll(io.LimitReader(file, maxInstructionBytes+1))
	if err != nil || len(content) > maxInstructionBytes || !utf8.Valid(content) ||
		strings.ContainsRune(string(content), 0) || strings.TrimSpace(string(content)) == "" {
		return errors.New("stakpak instruction file must contain bounded nonempty UTF-8 text without NUL")
	}
	if expected != "" && string(content) != expected {
		return errors.New("stakpak instruction file does not match standing instructions")
	}
	return ctx.Err()
}
