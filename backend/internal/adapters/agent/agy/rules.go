package agy

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hookutil"
)

const (
	agyRulesDirName      = "rules"
	agyStandingRuleName  = "ao-agent-orchestrator.md"
	agyStandingRuleMark  = "agent-orchestrator: managed agy standing instructions"
	agyStandingRuleFront = "---\ntrigger: always_on\n---\n\n"
)

// installStandingRule writes AO's standing instructions as an always-on AGY
// workspace rule. AGY has no launch-time system-prompt flag, and its
// SessionStart hook output is not injected into the model. The one channel it
// consumes on both a fresh start and `--conversation` resume is
// .agents/rules/*.md with `trigger: always_on`; rules without that trigger are
// ignored, and a plain AGENTS.md would collide with the project's own.
//
// An empty prompt writes nothing. A non-AO file at the generated path is
// refused rather than overwritten, so a launch cannot silently run with the
// wrong rule.
func installStandingRule(workspacePath, inline, file string) error {
	prompt, err := standingRuleText(inline, file)
	if err != nil {
		return err
	}
	if prompt == "" {
		return nil
	}

	rulePath := agyStandingRulePath(workspacePath)
	if data, err := os.ReadFile(rulePath); err == nil { //nolint:gosec // path built from caller-owned workspace dir
		if !strings.Contains(string(data), agyStandingRuleMark) {
			return fmt.Errorf("refusing to overwrite non-AO file at %s", rulePath)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read %s: %w", rulePath, err)
	}

	if err := os.MkdirAll(filepath.Dir(rulePath), 0o750); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(rulePath), err)
	}
	content := agyStandingRuleFront + "<!-- " + agyStandingRuleMark + " -->\n\n" + prompt + "\n"
	if err := hookutil.AtomicWriteFile(rulePath, []byte(content), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", rulePath, err)
	}
	if err := hookutil.EnsureWorkspaceGitignore(filepath.Dir(rulePath), agyStandingRuleName); err != nil {
		return fmt.Errorf("gitignore: %w", err)
	}
	return nil
}

// removeStandingRule deletes the generated rule only when it carries AO's
// marker. A missing file is a no-op.
func removeStandingRule(workspacePath string) error {
	rulePath := agyStandingRulePath(workspacePath)
	managed, err := isAOManagedRule(rulePath)
	if err != nil {
		return err
	}
	if !managed {
		return nil
	}
	if err := os.Remove(rulePath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove %s: %w", rulePath, err)
	}
	return nil
}

func agyStandingRulePath(workspacePath string) string {
	return filepath.Join(workspacePath, agyHooksDirName, agyRulesDirName, agyStandingRuleName)
}

func standingRuleText(inline, file string) (string, error) {
	if strings.TrimSpace(file) != "" {
		data, err := os.ReadFile(file) //nolint:gosec // path is AO-owned launch config
		if err != nil {
			return "", fmt.Errorf("read system prompt file: %w", err)
		}
		return strings.TrimSpace(string(data)), nil
	}
	return strings.TrimSpace(inline), nil
}

func isAOManagedRule(path string) (bool, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path built from caller-owned workspace dir
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read %s: %w", path, err)
	}
	return strings.Contains(string(data), agyStandingRuleMark), nil
}
