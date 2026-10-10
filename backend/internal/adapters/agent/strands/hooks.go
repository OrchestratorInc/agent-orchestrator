package strands

import (
	"context"
	_ "embed"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hookutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

//go:embed assets/ao-activity.mjs
var hookSource []byte

func hookPath(dataDir string) string {
	return filepath.Join(dataDir, "agents", "strands", "ao-activity.mjs")
}

// GetAgentHooks installs a private external plugin; no workspace or user
// configuration file is modified.
func (p *Plugin) GetAgentHooks(ctx context.Context, cfg ports.WorkspaceHookConfig) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !filepath.IsAbs(cfg.DataDir) {
		return errors.New("strands: absolute AO data directory is required")
	}
	path := hookPath(cfg.DataDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return hookutil.AtomicWriteFile(path, hookSource, 0o600)
}

// EmitsSubmitActivity reports the native BeforeInvocationEvent callback.
func (p *Plugin) EmitsSubmitActivity() bool { return true }

// DeriveActivityState maps only native root-invocation lifecycle events.
func DeriveActivityState(event string, _ []byte) (domain.ActivityState, bool) {
	switch event {
	case "active":
		return domain.ActivityActive, true
	case "session-start", "stop":
		return domain.ActivityIdle, true
	default:
		return "", false
	}
}

// ComposerIsEmpty recognizes the native empty editor and initialized footer.
// Invocation completion is established by the plugin, never by this editor:
// Strands also leaves it available for queueing during an active turn.
func (p *Plugin) ComposerIsEmpty(output string) bool {
	lines := strings.Split(strings.ReplaceAll(output, "\r", ""), "\n")
	footer := -1
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			footer = i
			break
		}
	}
	if footer < 0 || !strings.HasSuffix(strings.TrimSpace(lines[footer]), "/settings") {
		return false
	}
	for i := footer - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		return line == "▌Enter to send • Ctrl+J for newline • / for commands" || line == "Enter to send • Ctrl+J for newline • / for commands"
	}
	return false
}
