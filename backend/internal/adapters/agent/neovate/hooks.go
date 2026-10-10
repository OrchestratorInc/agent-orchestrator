package neovate

import (
	"bufio"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hookutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const hookSentinel = "agent-orchestrator: managed neovate plugin"

//go:embed assets/ao-plugin.mjs
var pluginSource string

func pluginPath(workspace, session string) string {
	sum := sha256.Sum256([]byte(session))
	return filepath.Join(workspace, ".neovate", "ao", hex.EncodeToString(sum[:16])+".mjs")
}

// GetAgentHooks installs a dedicated plugin, without changing native user config.
func (p *Plugin) GetAgentHooks(ctx context.Context, cfg ports.WorkspaceHookConfig) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if cfg.WorkspacePath == "" || cfg.SessionID == "" {
		return errors.New("neovate: workspace and AO session ID are required")
	}
	prompt := cfg.SystemPrompt
	if prompt == "" && cfg.SystemPromptFile != "" {
		data, err := os.ReadFile(cfg.SystemPromptFile) //nolint:gosec // AO-owned prompt path
		if err != nil {
			return err
		}
		prompt = string(data)
	}
	workspace, err := filepath.Abs(cfg.WorkspacePath)
	if err != nil {
		return err
	}
	path := pluginPath(workspace, cfg.SessionID)
	config, err := json.Marshal(map[string]string{"workspace": workspace, "prompt": prompt, "marker": path + ".session.json"})
	if err != nil {
		return err
	}
	body := strings.Replace(pluginSource, "__AO_CONFIG__", string(config), 1)
	if existing, err := os.ReadFile(path); err == nil && !strings.HasPrefix(string(existing), "// "+hookSentinel) { //nolint:gosec // AO workspace path
		return fmt.Errorf("neovate: refusing to overwrite non-AO plugin %s", path)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	if err := hookutil.EnsureWorkspaceGitignore(filepath.Dir(path), filepath.Base(path), filepath.Base(path)+".session.json", filepath.Base(path)+".session.json.tmp", "*.mjs", "*.mjs.session.json", "*.mjs.session.json.tmp", ".ao-tmp-*"); err != nil {
		return err
	}
	return hookutil.AtomicWriteFile(path, []byte(body), 0o600)
}

// AreHooksInstalled reports whether an AO plugin is present in this workspace.
func (p *Plugin) AreHooksInstalled(ctx context.Context, workspace string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	paths, err := filepath.Glob(filepath.Join(workspace, ".neovate", "ao", "*.mjs"))
	if err != nil {
		return false, err
	}
	for _, path := range paths {
		data, err := os.ReadFile(path) //nolint:gosec // AO workspace path
		if err != nil {
			return false, err
		}
		if strings.HasPrefix(string(data), "// "+hookSentinel) {
			return true, nil
		}
	}
	return false, nil
}

// UninstallHooks removes only files belonging to AO's plugin.
func (p *Plugin) UninstallHooks(ctx context.Context, workspace string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	paths, err := filepath.Glob(filepath.Join(workspace, ".neovate", "ao", "*.mjs"))
	if err != nil {
		return err
	}
	for _, path := range paths {
		data, err := os.ReadFile(path) //nolint:gosec // AO workspace path
		if err != nil {
			return err
		}
		if !strings.HasPrefix(string(data), "// "+hookSentinel) {
			continue
		}
		for _, suffix := range []string{"", ".session.json", ".session.json.tmp"} {
			if err := os.Remove(path + suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
	}
	return nil
}

type sessionMarker struct {
	NativeID   string `json:"nativeID"`
	Workspace  string `json:"workspace"`
	Transcript string `json:"transcript"`
}

func validateRestore(workspace, session, nativeID string, mode ports.PermissionMode) error {
	if !validNativeID(nativeID) {
		return errors.New("missing or invalid native session ID")
	}
	data, err := os.ReadFile(pluginPath(workspace, session) + ".session.json") //nolint:gosec // session-owned marker
	if err != nil {
		return fmt.Errorf("read native identity marker: %w", err)
	}
	var marker sessionMarker
	if err := json.Unmarshal(data, &marker); err != nil {
		return fmt.Errorf("invalid native identity marker: %w", err)
	}
	abs, err := filepath.Abs(workspace)
	if err != nil {
		return err
	}
	if marker.NativeID != nativeID || filepath.Clean(marker.Workspace) != abs || !filepath.IsAbs(marker.Transcript) {
		return errors.New("native session does not belong to this workspace")
	}
	return validateTranscript(marker.Transcript, nativeID, mode)
}

func validateTranscript(path, nativeID string, mode ports.PermissionMode) error {
	f, err := os.Open(path) //nolint:gosec // captured native transcript path
	if err != nil {
		return fmt.Errorf("open native transcript: %w", err)
	}
	defer func() { _ = f.Close() }()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	userMessages := 0
	for scanner.Scan() {
		if strings.TrimSpace(scanner.Text()) == "" {
			continue
		}
		var entry struct {
			Type      string                  `json:"type"`
			SessionID string                  `json:"sessionId"`
			Role      string                  `json:"role"`
			UUID      string                  `json:"uuid"`
			Content   json.RawMessage         `json:"content"`
			Config    *nativePermissionPolicy `json:"config"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			return fmt.Errorf("corrupt native transcript: %w", err)
		}
		if entry.Type == "config" {
			if entry.Config == nil {
				return errors.New("native transcript has invalid session configuration")
			}
			if err := validatePermissionPolicy(*entry.Config, mode); err != nil {
				return err
			}
		}
		if entry.Type != "message" {
			continue
		}
		if entry.SessionID != nativeID || entry.UUID == "" || !validTranscriptRole(entry.Role) || !validTranscriptContent(entry.Content) {
			return errors.New("native transcript has a foreign or invalid message")
		}
		if entry.Role == "user" {
			userMessages++
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if userMessages == 0 {
		return errors.New("native transcript contains no conversation")
	}
	return nil
}

func validTranscriptRole(role string) bool {
	switch role {
	case "system", "user", "assistant", "tool":
		return true
	}
	return false
}

func validTranscriptContent(raw json.RawMessage) bool {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return false
	}
	switch value.(type) {
	case string, []any:
		return true
	}
	return false
}

// Native saved grants are evaluated in addition to argv approval mode. A strict
// AO restore must not inherit broader approval from an earlier interactive run.
type nativePermissionPolicy struct {
	ApprovalMode  string   `json:"approvalMode"`
	ApprovalTools []string `json:"approvalTools"`
}

func validatePermissionPolicy(saved nativePermissionPolicy, requested ports.PermissionMode) error {
	mode := ports.NormalizePermissionMode(requested)
	if mode == ports.PermissionModeBypassPermissions {
		return nil
	}
	if len(saved.ApprovalTools) != 0 {
		return errors.New("saved native tool approvals exceed the requested permission mode; revoke them in Neovate before restoring")
	}
	if mode == ports.PermissionModeDefault && saved.ApprovalMode == "autoEdit" {
		return errors.New("saved native autoEdit approval exceeds default permissions; reset it in Neovate before restoring")
	}
	return nil
}
