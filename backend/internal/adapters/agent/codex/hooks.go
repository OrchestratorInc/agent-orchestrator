package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hookutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/agentlaunch"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/pkg/agentruntime"
)

const (
	codexHooksDirName  = ".codex"
	codexHooksFileName = "hooks.json"

	codexHookCommandPrefix = "ao hooks codex "
	codexHookTimeout       = 5
)

type codexHookFile struct {
	Hooks map[string][]codexMatcherGroup `json:"hooks"`
}

type codexMatcherGroup struct {
	Matcher *string          `json:"matcher,omitempty"`
	Hooks   []codexHookEntry `json:"hooks"`
}

type codexHookEntry struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	Timeout int    `json:"timeout,omitempty"`
}

type codexHookSpec struct {
	Event   string
	Command string
	Matcher string
}

var codexManagedHooks = []codexHookSpec{
	{Event: "SessionStart", Command: codexHookCommandPrefix + "session-start"},
	{Event: "UserPromptSubmit", Command: codexHookCommandPrefix + "user-prompt-submit"},
	{Event: "PermissionRequest", Command: codexHookCommandPrefix + "permission-request"},
	{Event: "PostToolUse", Command: codexHookCommandPrefix + "post-tool-use", Matcher: "^(spawn_agent|collaborationspawn_agent)$"},
	{Event: "SubagentStart", Command: codexHookCommandPrefix + "subagent-start"},
	{Event: "SubagentStop", Command: codexHookCommandPrefix + "subagent-stop"},
	{Event: "Stop", Command: codexHookCommandPrefix + "stop"},
}

func appendSessionHookFlags(cmd *[]string) error {
	executable, err := agentlaunch.Executable()
	if err != nil {
		return fmt.Errorf("resolve Orchestrator hook executable: %w", err)
	}
	if !filepath.IsAbs(executable) {
		executable, err = filepath.Abs(executable)
		if err != nil {
			return fmt.Errorf("make Orchestrator hook executable absolute: %w", err)
		}
	}
	appendSessionHookFlagsForExecutable(cmd, executable)
	return nil
}

func appendSessionHookFlagsForExecutable(cmd *[]string, executable string) {
	prefix := shellQuoteHookExecutable(executable) + " hooks codex "
	for _, spec := range codexManagedHooks {
		action := strings.TrimPrefix(spec.Command, codexHookCommandPrefix)
		matcher := ""
		if spec.Matcher != "" {
			matcher = "matcher=" + codexTOMLBasicString(spec.Matcher) + ","
		}
		flag := fmt.Sprintf(`hooks.%s=[{%shooks=[{type="command",command=%s,timeout=%d}]}]`,
			spec.Event, matcher, codexTOMLBasicString(prefix+action), codexHookTimeout)
		*cmd = append(*cmd, "-c", flag)
	}
}

func shellQuoteHookExecutable(executable string) string {
	if runtime.GOOS == "windows" {
		return `& "` + executable + `"`
	}
	return `'` + strings.ReplaceAll(executable, `'`, `'"'"'`) + `'`
}

func appendWorkspaceTrustFlag(cmd *[]string, workspacePath string) {
	*cmd = append(*cmd, agentruntime.CodexWorkspaceTrustArgs(workspacePath)...)
}

func codexTOMLConfigString(s string) string {
	if !containsTOMLControl(s) && !strings.Contains(s, "'") {
		return codexTOMLLiteralString(s)
	}
	return codexTOMLBasicString(s)
}

func codexTOMLLiteralString(s string) string {
	return "'" + s + "'"
}

func codexTOMLBasicString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '\\':
			b.WriteString(`\\`)
		case r == '"':
			b.WriteString(`\"`)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\u%04X`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

func containsTOMLControl(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

func (p *Plugin) GetAgentHooks(ctx context.Context, cfg ports.WorkspaceHookConfig) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(cfg.WorkspacePath) == "" {
		return errors.New("codex.GetAgentHooks: WorkspacePath is required")
	}
	if err := removeLegacyWorkspaceHooks(cfg.WorkspacePath); err != nil {
		return fmt.Errorf("codex.GetAgentHooks: %w", err)
	}
	return nil
}

func (p *Plugin) UninstallHooks(ctx context.Context, workspacePath string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(workspacePath) == "" {
		return errors.New("codex.UninstallHooks: workspacePath is required")
	}
	if err := removeLegacyWorkspaceHooks(workspacePath); err != nil {
		return fmt.Errorf("codex.UninstallHooks: %w", err)
	}
	return nil
}

func removeLegacyWorkspaceHooks(workspacePath string) error {
	hooksPath := codexHooksPath(workspacePath)
	if _, err := os.Stat(hooksPath); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	topLevel, rawHooks, err := readCodexHooks(hooksPath)
	if err != nil {
		return err
	}

	changed := false
	for event, raw := range rawHooks {
		var groups []codexMatcherGroup
		if err := json.Unmarshal(raw, &groups); err != nil {
			return fmt.Errorf("parse %s hooks: %w", event, err)
		}
		kept := removeCodexManagedHooks(groups)
		if countCodexHooks(kept) == countCodexHooks(groups) {
			continue
		}
		changed = true
		if len(kept) == 0 {
			delete(rawHooks, event)
			continue
		}
		data, err := json.Marshal(kept)
		if err != nil {
			return fmt.Errorf("encode %s hooks: %w", event, err)
		}
		rawHooks[event] = data
	}
	if !changed {
		return nil
	}
	return writeCodexHooks(hooksPath, topLevel, rawHooks)
}

func (p *Plugin) AreHooksInstalled(ctx context.Context, workspacePath string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if strings.TrimSpace(workspacePath) == "" {
		return false, errors.New("codex.AreHooksInstalled: workspacePath is required")
	}

	hooksPath := codexHooksPath(workspacePath)
	if _, err := os.Stat(hooksPath); errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	_, rawHooks, err := readCodexHooks(hooksPath)
	if err != nil {
		return false, fmt.Errorf("codex.AreHooksInstalled: %w", err)
	}

	for event, raw := range rawHooks {
		var groups []codexMatcherGroup
		if err := json.Unmarshal(raw, &groups); err != nil {
			return false, fmt.Errorf("codex.AreHooksInstalled: parse %s hooks: %w", event, err)
		}
		for _, group := range groups {
			for _, hook := range group.Hooks {
				if isCodexManagedHook(hook.Command) {
					return true, nil
				}
			}
		}
	}
	return false, nil
}

func codexHooksPath(workspacePath string) string {
	return filepath.Join(workspacePath, codexHooksDirName, codexHooksFileName)
}

func readCodexHooks(hooksPath string) (topLevel, rawHooks map[string]json.RawMessage, err error) {
	topLevel = map[string]json.RawMessage{}
	rawHooks = map[string]json.RawMessage{}

	data, err := os.ReadFile(hooksPath) //nolint:gosec // path built from caller-owned workspace dir
	if errors.Is(err, os.ErrNotExist) {
		return topLevel, rawHooks, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("read %s: %w", hooksPath, err)
	}
	if strings.TrimSpace(string(data)) == "" {
		return topLevel, rawHooks, nil
	}
	if err := json.Unmarshal(data, &topLevel); err != nil {
		return nil, nil, fmt.Errorf("parse %s: %w", hooksPath, err)
	}
	if hooksRaw, ok := topLevel["hooks"]; ok {
		if err := json.Unmarshal(hooksRaw, &rawHooks); err != nil {
			return nil, nil, fmt.Errorf("parse hooks in %s: %w", hooksPath, err)
		}
	}
	return topLevel, rawHooks, nil
}

func writeCodexHooks(hooksPath string, topLevel, rawHooks map[string]json.RawMessage) error {
	if len(rawHooks) == 0 {
		delete(topLevel, "hooks")
	} else {
		hooksJSON, err := json.Marshal(rawHooks)
		if err != nil {
			return fmt.Errorf("encode hooks: %w", err)
		}
		topLevel["hooks"] = hooksJSON
	}

	if err := os.MkdirAll(filepath.Dir(hooksPath), 0o750); err != nil {
		return fmt.Errorf("create hook dir: %w", err)
	}
	data, err := json.MarshalIndent(topLevel, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", hooksPath, err)
	}
	data = append(data, '\n')
	if err := hookutil.AtomicWriteFile(hooksPath, data, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", hooksPath, err)
	}
	return nil
}

func isCodexManagedHook(command string) bool {
	return strings.HasPrefix(command, codexHookCommandPrefix)
}

func countCodexHooks(groups []codexMatcherGroup) int {
	total := 0
	for _, group := range groups {
		total += len(group.Hooks)
	}
	return total
}

func removeCodexManagedHooks(groups []codexMatcherGroup) []codexMatcherGroup {
	result := make([]codexMatcherGroup, 0, len(groups))
	for _, group := range groups {
		kept := make([]codexHookEntry, 0, len(group.Hooks))
		for _, hook := range group.Hooks {
			if !isCodexManagedHook(hook.Command) {
				kept = append(kept, hook)
			}
		}
		if len(kept) > 0 {
			group.Hooks = kept
			result = append(result, group)
		}
	}
	return result
}
