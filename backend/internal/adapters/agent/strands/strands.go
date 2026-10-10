// Package strands integrates the Strands CLI terminal interface.
package strands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/agentbase"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/binaryutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hookutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Plugin launches the installed Strands CLI without changing its user profile.
type Plugin struct {
	agentbase.Base
	binaryMu       sync.Mutex
	resolvedBinary string
}

var _ ports.Agent = (*Plugin)(nil)
var _ ports.AgentBinaryResolver = (*Plugin)(nil)
var _ ports.AgentBinaryResolutionInvalidator = (*Plugin)(nil)

// New constructs the Strands adapter.
func New() *Plugin { return &Plugin{} }

// Manifest describes the terminal interface.
func (p *Plugin) Manifest() adapters.Manifest {
	return adapters.Manifest{ID: "strands", Name: "Strands", Description: "Run Strands CLI terminal sessions.", Version: "0.0.1", Capabilities: []adapters.Capability{adapters.CapabilityAgent}}
}

// GetConfigSpec exposes the native free-form model flag, not an invented catalog.
func (p *Plugin) GetConfigSpec(ctx context.Context) (ports.ConfigSpec, error) {
	return agentbase.ModelConfigSpec(ctx, "Native provider/model override. Leave empty to preserve the configured model; model modules do not accept this override.")
}

var safeID = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,127}$`)

// GetLaunchCommand starts a distinct durable conversation and records only its
// correlation metadata under AO data. The visible task is separate from the
// provider's additive domain-instruction channel.
func (p *Plugin) GetLaunchCommand(ctx context.Context, cfg ports.LaunchConfig) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !safeID.MatchString(cfg.SessionID) {
		return nil, errors.New("strands: invalid AO session id")
	}
	id := cfg.NativeSessionID
	if id == "" {
		id = uuid.NewString()
	}
	if !safeID.MatchString(id) {
		return nil, errors.New("strands: invalid native session id")
	}
	cmd, err := p.command(ctx, cfg.DataDir, id, cfg.Permissions, cfg.Config, cfg.SystemPrompt, cfg.SystemPromptFile, cfg.AllowedTools, cfg.DisallowedTools)
	if err != nil {
		return nil, err
	}
	if !filepath.IsAbs(cfg.WorkspacePath) {
		return nil, errors.New("strands: absolute workspace path is required")
	}
	body, err := json.Marshal(sessionLocator{NativeSessionID: id, Workspace: filepath.Clean(cfg.WorkspacePath)})
	if err != nil {
		return nil, err
	}
	path := locatorPath(cfg.DataDir, cfg.SessionID)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if err := hookutil.AtomicWriteFile(path, body, 0o600); err != nil {
		return nil, err
	}
	if cfg.Prompt != "" {
		cmd = append(cmd, "--prompt", cfg.Prompt)
	}
	return cmd, nil
}

// GetRestoreCommand refuses absent native history: --session-id otherwise
// silently creates a new Strands conversation for a missing id.
func (p *Plugin) GetRestoreCommand(ctx context.Context, cfg ports.RestoreConfig) ([]string, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	id := cfg.Session.Metadata[ports.MetadataKeyAgentSessionID]
	if !safeID.MatchString(id) {
		return nil, false, errors.New("strands: exact native session id is required")
	}
	loc, err := readLocator(ctx, cfg.DataDir, cfg.Session.ID)
	if err != nil {
		return nil, false, err
	}
	if loc.NativeSessionID != id || loc.Workspace != filepath.Clean(cfg.Session.WorkspacePath) {
		return nil, false, errors.New("strands: native session or workspace does not match its launch")
	}
	if err := validateCheckpoint(ctx, cfg.DataDir, id); err != nil {
		return nil, false, err
	}
	cmd, err := p.command(ctx, cfg.DataDir, id, cfg.Permissions, cfg.Config, cfg.SystemPrompt, cfg.SystemPromptFile, cfg.AllowedTools, cfg.DisallowedTools)
	if err != nil {
		return nil, false, err
	}
	if cfg.Prompt != "" {
		cmd = append(cmd, "--prompt", cfg.Prompt)
	}
	return cmd, true, nil
}

func (p *Plugin) command(ctx context.Context, dataDir, id string, mode ports.PermissionMode, config ports.AgentConfig, inline, file string, allow, deny []string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !filepath.IsAbs(dataDir) {
		return nil, errors.New("strands: absolute AO data directory is required")
	}
	if len(allow) > 0 || len(deny) > 0 {
		return nil, errors.New("strands: per-launch tool restrictions are unsupported")
	}
	native, err := readNativeConfig(ctx)
	if err != nil {
		return nil, err
	}
	if err := native.validate(mode, config.Model); err != nil {
		return nil, err
	}
	prompt := inline
	if prompt == "" && file != "" {
		data, err := os.ReadFile(file)
		if err != nil {
			return nil, fmt.Errorf("strands: read private instructions: %w", err)
		}
		prompt = string(data)
	} //nolint:gosec // AO-owned instruction path
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	binary, err := p.ResolveBinary(ctx)
	if err != nil {
		return nil, err
	}
	cmd := []string{binary, "--session", "on", "--session-id", id, "--set", "session.dir=" + sessionDir(dataDir)}
	if err := p.GetAgentHooks(ctx, ports.WorkspaceHookConfig{DataDir: dataDir}); err != nil {
		return nil, err
	}
	plugin, err := json.Marshal(map[string]string{"kind": "plugin", "module": hookPath(dataDir)})
	if err != nil {
		return nil, err
	}
	guard, err := json.Marshal(map[string]string{"kind": "plugin", "module": hookPath(dataDir), "export": "shellGuard"})
	if err != nil {
		return nil, err
	}
	plugins := append([]json.RawMessage{guard}, native.Profile.Plugins...)
	plugins = append(plugins, json.RawMessage(plugin))
	pluginJSON, err := json.Marshal(plugins)
	if err != nil {
		return nil, err
	}
	cmd = append(cmd, "--set", "plugins="+string(pluginJSON), "--set", "backgroundTasks=false")
	// AO supports foreground invocations only: native fire-and-forget tools
	// can outlive AfterInvocation and would falsely mark the session settled.
	agentbase.AppendModelFlag(&cmd, config, "--model")
	// --instructions replaces profile.instructions, but the harness appends the
	// result to its built-in contract. Merge the saved domain string ourselves.
	if prompt != "" {
		if native.Profile.Instructions != "" {
			prompt = native.Profile.Instructions + "\n\n" + prompt
		}
		cmd = append(cmd, "--instructions", prompt)
	}
	return cmd, nil
}

type sessionLocator struct {
	NativeSessionID string `json:"nativeSessionId"`
	Workspace       string `json:"workspace"`
}

func sessionDir(dataDir string) string {
	return filepath.Join(dataDir, "agents", "strands", "sessions")
}
func locatorPath(dataDir, sessionID string) string {
	return filepath.Join(dataDir, "agents", "strands", "launches", sessionID+".json")
}
func checkpointPath(dataDir, id string) string {
	return filepath.Join(sessionDir(dataDir), id, "scopes", "agent", "agent", "snapshots", "snapshot_latest.json")
}

func readLocator(ctx context.Context, dataDir, sessionID string) (sessionLocator, error) {
	var loc sessionLocator
	if err := ctx.Err(); err != nil {
		return loc, err
	}
	if !filepath.IsAbs(dataDir) || !safeID.MatchString(sessionID) {
		return loc, errors.New("strands: invalid session locator")
	}
	data, err := os.ReadFile(locatorPath(dataDir, sessionID))
	if err != nil {
		return loc, fmt.Errorf("strands: read launch identity: %w", err)
	} //nolint:gosec // validated AO-owned path
	if err := json.Unmarshal(data, &loc); err != nil {
		return loc, errors.New("strands: malformed launch identity")
	}
	if !safeID.MatchString(loc.NativeSessionID) || !filepath.IsAbs(loc.Workspace) {
		return loc, errors.New("strands: invalid launch identity")
	}
	return loc, nil
}

func validateCheckpoint(ctx context.Context, dataDir, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	if !filepath.IsAbs(dataDir) || !safeID.MatchString(id) {
		return errors.New("strands: invalid native checkpoint path")
	}
	path := checkpointPath(dataDir, id)
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("strands: native history is unavailable: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() > 64<<20 {
		return errors.New("strands: native checkpoint is not a bounded regular file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	} //nolint:gosec // validated AO-owned path
	var checkpoint struct {
		Scope         string `json:"scope"`
		SchemaVersion string `json:"schemaVersion"`
		Data          struct {
			Messages []struct {
				Role    string                       `json:"role"`
				Content []map[string]json.RawMessage `json:"content"`
			} `json:"messages"`
		} `json:"data"`
	}
	if json.Unmarshal(data, &checkpoint) != nil || checkpoint.Scope != "agent" || checkpoint.SchemaVersion != "1.0" || len(checkpoint.Data.Messages) == 0 {
		return errors.New("strands: native checkpoint is malformed or unsupported")
	}
	// Validate the persisted message envelope without reimplementing the SDK's
	// evolving content union. Empty/null history cannot establish continuation.
	for _, message := range checkpoint.Data.Messages {
		if (message.Role != "user" && message.Role != "assistant") || len(message.Content) == 0 {
			return errors.New("strands: native checkpoint has an invalid message envelope")
		}
		for _, block := range message.Content {
			if len(block) == 0 {
				return errors.New("strands: native checkpoint has an empty content block")
			}
			if raw, ok := block["text"]; ok {
				var text string
				if string(raw) == "null" || json.Unmarshal(raw, &text) != nil {
					return errors.New("strands: native checkpoint has invalid text content")
				}
			}
		}
	}
	return nil
}

// SessionInfo publishes identity only after Strands has persisted native history.
func (p *Plugin) SessionInfo(ctx context.Context, ref ports.SessionRef) (ports.SessionInfo, bool, error) {
	if err := ctx.Err(); err != nil {
		return ports.SessionInfo{}, false, err
	}
	loc, err := readLocator(ctx, ref.DataDir, ref.ID)
	if errors.Is(err, os.ErrNotExist) {
		return ports.SessionInfo{}, false, nil
	}
	if err != nil {
		return ports.SessionInfo{}, false, err
	}
	if loc.Workspace != filepath.Clean(ref.WorkspacePath) {
		return ports.SessionInfo{}, false, errors.New("strands: workspace does not match native launch")
	}
	if err := validateCheckpoint(ctx, ref.DataDir, loc.NativeSessionID); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ports.SessionInfo{}, false, nil
		}
		return ports.SessionInfo{}, false, err
	}
	return ports.SessionInfo{AgentSessionID: loc.NativeSessionID}, true, nil
}

var binarySpec = binaryutil.BinarySpec{Label: "strands", Names: []string{"strands"}, WinNames: []string{"strands.cmd", "strands.exe", "strands"}, UnixPaths: []string{"/usr/local/bin/strands", "/opt/homebrew/bin/strands"}, UnixHomePaths: binaryutil.NodeManagedUnixHomePaths("strands"), NodeManaged: true, WinPaths: []binaryutil.WinPath{{Base: binaryutil.WinAppData, Parts: []string{"npm", "strands.cmd"}}}}

// ResolveBinary locates the separately installed official Strands CLI.
func (p *Plugin) ResolveBinary(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	p.binaryMu.Lock()
	defer p.binaryMu.Unlock()
	if p.resolvedBinary != "" {
		return p.resolvedBinary, nil
	}
	path, err := binaryutil.ResolveBinary(ctx, binarySpec)
	if err != nil {
		return "", err
	}
	if err := checkVersion(ctx, path); err != nil {
		return "", err
	}
	p.resolvedBinary = path
	return path, nil
}

// InvalidateBinaryResolution rechecks installation on the next operation.
func (p *Plugin) InvalidateBinaryResolution() {
	p.binaryMu.Lock()
	p.resolvedBinary = ""
	p.binaryMu.Unlock()
}

type nativeConfig struct {
	AgentProject string `json:"agentProject"`
	Profile      struct {
		Instructions       string            `json:"instructions"`
		Model              string            `json:"model"`
		ModelModule        json.RawMessage   `json:"modelModule"`
		Plugins            []json.RawMessage `json:"plugins"`
		Tools              []json.RawMessage `json:"tools"`
		Sandbox            json.RawMessage   `json:"sandbox"`
		BuiltinTools       json.RawMessage   `json:"builtinTools"`
		AgentConfig        json.RawMessage   `json:"agentConfig"`
		AgentConfigModules json.RawMessage   `json:"agentConfigModules"`
	} `json:"profile"`
	Permissions struct {
		Mode  string   `json:"mode"`
		Allow []string `json:"allow"`
	} `json:"permissions"`
}

func readNativeConfig(ctx context.Context) (nativeConfig, error) {
	var cfg nativeConfig
	if err := ctx.Err(); err != nil {
		return cfg, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return cfg, err
	}
	data, err := os.ReadFile(filepath.Join(home, ".strands", "cli", "config.json"))
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	} //nolint:gosec // native public configuration; never return contents
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, errors.New("strands: invalid native CLI configuration")
	}
	return cfg, nil
}
func (cfg nativeConfig) validate(mode ports.PermissionMode, model string) error {
	if len(cfg.Profile.AgentConfig) > 0 && string(cfg.Profile.AgentConfig) != "null" && string(cfg.Profile.AgentConfig) != "{}" || len(cfg.Profile.AgentConfigModules) > 0 && string(cfg.Profile.AgentConfigModules) != "null" && string(cfg.Profile.AgentConfigModules) != "[]" && string(cfg.Profile.AgentConfigModules) != "{}" {
		return errors.New("strands: custom agent configuration is unsupported by AO instructions and native identity")
	}
	if len(cfg.Profile.Tools) > 0 || len(cfg.Profile.Sandbox) > 0 && string(cfg.Profile.Sandbox) != "null" {
		return errors.New("strands: custom tools and sandboxes are unsupported by AO cancellation")
	}
	var builtins map[string]json.RawMessage
	if json.Unmarshal(cfg.Profile.BuiltinTools, &builtins) == nil {
		if shell := builtins["shell"]; len(shell) > 0 && string(shell) != "true" && string(shell) != "false" {
			return errors.New("strands: custom built-in shell options are unsupported by AO cancellation")
		}
	}
	if cfg.AgentProject != "" {
		return errors.New("strands: authored agent projects are not supported by the terminal adapter")
	}
	if strings.TrimSpace(model) != "" && len(cfg.Profile.ModelModule) > 0 && string(cfg.Profile.ModelModule) != "null" {
		return errors.New("strands: configured model module takes precedence over --model; leave model override empty")
	}
	switch mode {
	case "", ports.PermissionModeDefault:
		if cfg.Permissions.Mode != "" && cfg.Permissions.Mode != "default" || len(cfg.Permissions.Allow) > 0 {
			return errors.New("strands: Ask Permissions requires native default mode with no always-allowed tools")
		}
	case ports.PermissionModeBypassPermissions:
		if cfg.Permissions.Mode != "bypassPermissions" {
			return errors.New("strands: bypass requires explicit native bypassPermissions configuration; AO does not modify it")
		}
	default:
		return errors.New("strands: this permission mode has no native per-launch equivalent")
	}
	return nil
}
