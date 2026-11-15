package acp

import (
	"fmt"
	"os"
	"path/filepath"
)

func SystemPromptFile(cfg LaunchConfig, agent string) (string, error) {
	dir, err := filepath.Abs(filepath.Join(cfg.DataDir, "prompts", string(cfg.SessionID), agent))
	if err != nil {
		return "", fmt.Errorf("%s system prompt dir: %w", agent, err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create %s system prompt dir: %w", agent, err)
	}
	path := filepath.Join(dir, "system-prompt.md")
	if err := os.WriteFile(path, []byte(cfg.SystemPrompt), 0o600); err != nil {
		return "", fmt.Errorf("write %s system prompt: %w", agent, err)
	}
	return path, nil
}
