package openhands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hooksjson"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hookutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	hooksDirName      = ".openhands"
	hooksFileName     = "hooks.json"
	hookCommandPrefix = "ao hooks openhands "
	hookTimeout       = 30
)

var managedHooks = []hooksjson.HookSpec{
	{Event: "SessionStart", Command: hookCommandPrefix + "session-start"},
	{Event: "UserPromptSubmit", Command: hookCommandPrefix + "user-prompt-submit"},
	{Event: "Stop", Command: hookCommandPrefix + "stop"},
}

var hooks = hooksjson.Manager{
	Label:         adapterID,
	CommandPrefix: hookCommandPrefix,
	Timeout:       hookTimeout,
	Path:          hooksPath,
	Managed:       managedHooks,
}

func hooksPath(workspacePath string) string {
	return filepath.Join(workspacePath, hooksDirName, hooksFileName)
}

var hookEvents = []string{"PreToolUse", "PostToolUse", "UserPromptSubmit", "SessionStart", "SessionEnd", "Stop"}

func toSnake(pascal string) string {
	var b strings.Builder
	for i, r := range pascal {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				b.WriteByte('_')
			}
			r += 'a' - 'A'
		}
		b.WriteRune(r)
	}
	return b.String()
}

func (p *Plugin) GetAgentHooks(ctx context.Context, cfg ports.WorkspaceHookConfig) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(cfg.WorkspacePath) != "" {
		if err := normalizeHooksFile(hooksPath(cfg.WorkspacePath)); err != nil {
			return fmt.Errorf("%s.GetAgentHooks: %w", adapterID, err)
		}
	}
	return hooks.Install(ctx, cfg.WorkspacePath)
}

func (p *Plugin) UninstallHooks(ctx context.Context, workspacePath string) error {
	return hooks.Uninstall(ctx, workspacePath)
}

func (p *Plugin) AreHooksInstalled(ctx context.Context, workspacePath string) (bool, error) {
	return hooks.AreInstalled(ctx, workspacePath)
}

func normalizeHooksFile(path string) error {
	data, err := os.ReadFile(path) //nolint:gosec // path built from caller-owned workspace dir
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	if strings.TrimSpace(string(data)) == "" {
		return nil
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}

	events := top
	wrapped := false
	if raw, ok := top["hooks"]; ok {
		wrapped = true
		events = map[string]json.RawMessage{}
		if err := json.Unmarshal(raw, &events); err != nil {
			return fmt.Errorf("parse hooks in %s: %w", path, err)
		}
	}

	wrappedEvents := map[string]json.RawMessage{}
	changed := !wrapped
	for _, pascal := range hookEvents {
		var groups []json.RawMessage
		found := false
		for _, key := range []string{pascal, toSnake(pascal)} {
			raw, ok := events[key]
			if !ok {
				continue
			}
			found = true
			delete(events, key)
			if key != pascal {
				changed = true
			}
			var more []json.RawMessage
			if err := json.Unmarshal(raw, &more); err != nil {
				return fmt.Errorf("parse %s in %s: %w", key, path, err)
			}
			groups = append(groups, more...)
		}
		if !found {
			continue
		}
		raw, err := json.Marshal(groups)
		if err != nil {
			return fmt.Errorf("encode %s: %w", pascal, err)
		}
		wrappedEvents[pascal] = raw
	}
	if !changed || len(wrappedEvents) == 0 {
		return nil
	}

	out := top
	if wrapped {
		for key, raw := range events {
			wrappedEvents[key] = raw
		}
	}
	eventsJSON, err := json.Marshal(wrappedEvents)
	if err != nil {
		return fmt.Errorf("encode hooks: %w", err)
	}
	out["hooks"] = eventsJSON
	encoded, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}
	encoded = append(encoded, '\n')
	if err := hookutil.AtomicWriteFile(path, encoded, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
