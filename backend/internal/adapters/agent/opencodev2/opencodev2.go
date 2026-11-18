// Package opencodev2 implements the official OpenCode 2 TUI contract. The
// executable name is still opencode; the registry identity is opencode-v2.
package opencodev2

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/agentbase"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/opencode"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

// Plugin is the OpenCode 2 TUI agent adapter.
type Plugin struct {
	agentbase.Base
	passwordMu             sync.Mutex
	pendingSharedPasswords map[string]string
}

// New returns an OpenCode 2 adapter.
func New() *Plugin { return &Plugin{} }

var _ adapters.Adapter = (*Plugin)(nil)
var _ ports.Agent = (*Plugin)(nil)
var _ ports.AgentAuthChecker = (*Plugin)(nil)
var _ ports.AgentBinaryResolver = (*Plugin)(nil)
var _ ports.AgentRuntimeLaunchEnv = (*Plugin)(nil)
var _ ports.SemanticMessageAcceptanceSignaler = (*Plugin)(nil)

// Manifest describes the adapter.
func (p *Plugin) Manifest() adapters.Manifest {
	return adapters.Manifest{ID: string(domain.HarnessOpenCodeV2), Name: "OpenCode 2", Description: "Run OpenCode 2 worker sessions.", Version: "0.0.1", Capabilities: []adapters.Capability{adapters.CapabilityAgent}}
}

// EmitsSemanticMessageAcceptance is false: OpenCode 2's prompt hook runs before
// durable prompt admission, so no hook can confirm a prompt was accepted.
func (p *Plugin) EmitsSemanticMessageAcceptance() bool { return false }

// ResolveBinary resolves the shared opencode executable and requires the v2
// command contract before catalog, authentication, install verification, or a
// session can use it.
func (p *Plugin) ResolveBinary(ctx context.Context) (string, error) {
	return opencode.ResolveBinaryForMajor(ctx, 2)
}

// GetConfigSpec returns the model override spec.
func (p *Plugin) GetConfigSpec(ctx context.Context) (ports.ConfigSpec, error) {
	return agentbase.ModelConfigSpec(ctx, "Model override for the OpenCode 2 session agent.")
}

// GetLaunchCommand builds the OpenCode 2 launch argv.
func (p *Plugin) GetLaunchCommand(ctx context.Context, cfg ports.LaunchConfig) ([]string, error) {
	binary, err := opencode.ResolveBinaryForMajor(ctx, 2)
	if err != nil {
		return nil, err
	}
	return p.command(ctx, binary, cfg, "")
}

// GetRestoreCommand builds the OpenCode 2 restore argv.
func (p *Plugin) GetRestoreCommand(ctx context.Context, cfg ports.RestoreConfig) ([]string, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	nativeID := strings.TrimSpace(cfg.Session.Metadata[ports.MetadataKeyAgentSessionID])
	if nativeID == "" {
		return nil, false, nil
	}
	binary, err := opencode.ResolveBinaryForMajor(ctx, 2)
	if err != nil {
		return nil, false, err
	}
	cmd, err := p.command(ctx, binary, ports.LaunchConfig{Config: cfg.Config, DataDir: cfg.DataDir, WorkspacePath: cfg.Session.WorkspacePath, SessionID: cfg.Session.ID, Permissions: cfg.Permissions, Prompt: cfg.Prompt, SystemPrompt: cfg.SystemPrompt, SystemPromptFile: cfg.SystemPromptFile, Env: cfg.Env}, nativeID)
	return cmd, err == nil, err
}

// errProviderEnvDrift marks a launch whose environment differs from the daemon
// environment. The shared server inherits the daemon environment, so such a
// launch keeps the private-server path where cfg.Env reaches the server.
var errProviderEnvDrift = errors.New("opencode-v2: launch environment differs from the daemon environment")

// errSharedServerEnvUnknown marks a launch that did not supply the environment
// it will run with. Sharing is only safe when every override is known.
var errSharedServerEnvUnknown = errors.New("opencode-v2: launch environment is required for a shared server")

// errSharedServerEnvConfig marks a launch whose config comes from the daemon
// environment. The shared server starts with those variables removed and a
// client does not forward them, so only the private path sees them.
var errSharedServerEnvConfig = errors.New("opencode-v2: daemon OpenCode config needs a private server")

// sharedServerEnvConfig reports whether the daemon supplies OpenCode config
// through variables the shared server strips. The private path overrides
// OPENCODE_CONFIG_CONTENT only when it has content of its own, so an empty
// launch would otherwise inherit a config the shared path cannot reproduce.
func sharedServerEnvConfig(content string) bool {
	if content == "" && os.Getenv("OPENCODE_CONFIG_CONTENT") != "" {
		return true
	}
	for _, name := range []string{"OPENCODE_CONFIG", "OPENCODE_CONFIG_DIR"} {
		if os.Getenv(name) != "" {
			return true
		}
	}
	return false
}

// projectConfigOwner names the session whose agent the worktree config selects,
// or "" when the file is absent, unreadable as JSON, or not AO-generated. Only
// AO's own documents (default_agent with the ao- prefix) identify a session.
func projectConfigOwner(workspacePath string) (string, error) {
	data, err := os.ReadFile(filepath.Join(workspacePath, ".opencode", projectConfigFileName))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("opencode-v2: read project config: %w", err)
	}
	var config struct {
		DefaultAgent string `json:"default_agent"`
	}
	// A config another tool wrote is not AO's concern: an unparsable document
	// has no AO agent name, so the workspace counts as not owned by a session.
	if err := json.Unmarshal(data, &config); err != nil {
		return "", nil //nolint:nilerr // unparsable config has no AO agent, not a failure
	}
	if !strings.HasPrefix(config.DefaultAgent, "ao-") {
		return "", nil
	}
	return config.DefaultAgent, nil
}

// defaultAgentConfig is the minimal document that keeps a private launch on
// its own AO agent when the worktree config belongs to another session.
func defaultAgentConfig(sessionID string) (string, error) {
	name := aoAgentName(sessionID)
	data, err := json.Marshal(map[string]any{
		"agents":        map[string]any{name: map[string]any{"mode": "primary"}},
		"default_agent": name,
	})
	if err != nil {
		return "", fmt.Errorf("opencode-v2: encode default agent config: %w", err)
	}
	return string(data), nil
}

// The v2 root TUI has no --model/--agent flags. AO normally attaches the TUI
// to one shared OpenCode 2 server and selects the AO agent through the
// worktree's project config. A launch that cannot use the shared server keeps
// the private-server path and passes the same config inline instead.
// --prompt=<value> keeps even leading-dash prompts a single string value in
// the Effect CLI.
func (p *Plugin) command(ctx context.Context, binary string, cfg ports.LaunchConfig, nativeID string) ([]string, error) {
	prompt := cfg.SystemPrompt
	if prompt == "" && cfg.SystemPromptFile != "" {
		data, err := os.ReadFile(cfg.SystemPromptFile)
		if err != nil {
			return nil, fmt.Errorf("opencode-v2: read system prompt: %w", err)
		}
		prompt = string(data)
	}
	content, err := prepareConfigContent(os.Getenv("OPENCODE_CONFIG_CONTENT"), cfg.SessionID, prompt, cfg.Config.Model, cfg.Permissions)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	dataHome, err := DataHome(ctx)
	if err != nil {
		return nil, fmt.Errorf("opencode-v2: prepare data home: %w", err)
	}
	envArgs := []string{"XDG_DATA_HOME=" + dataHome}
	var server sharedServer
	var sharedErr error
	if cfg.Env == nil {
		sharedErr = errSharedServerEnvUnknown
	} else if sharedServerEnvDrift(cfg.Env) {
		sharedErr = errProviderEnvDrift
	} else if sharedServerEnvConfig(content) {
		sharedErr = errSharedServerEnvConfig
	} else {
		server, sharedErr = prepareSharedServer(ctx, binary, cfg, dataHome, content)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// A running server owns the data home. Starting a private server beside it
	// would make both write the same OpenCode state, so these errors surface
	// instead of falling back.
	if errors.Is(sharedErr, errSharedServerUnavailable) || errors.Is(sharedErr, errSharedServerConfig) {
		return nil, sharedErr
	}
	var cmd []string
	if sharedErr == nil {
		// The password reaches the TUI through the runtime environment, not
		// argv: process listings and terminal pane commands must not expose it.
		p.rememberSharedPassword(cfg.SessionID, server.password)
		cmd = append([]string{"env"}, envArgs...)
		cmd = append(cmd, binary, "--server", server.url)
	} else {
		p.forgetSharedPassword(cfg.SessionID)
		// OpenCode 2 ignores inline config when a client attaches to a shared
		// server, so a launch that cannot use one keeps the private-server path.
		injected := content
		if injected == "" {
			// A project config generated for another session would apply that
			// session's agent, model, prompt, and permissions here. Pass this
			// session's own minimal config inline instead of inheriting it.
			owner, err := projectConfigOwner(cfg.WorkspacePath)
			if err != nil {
				return nil, err
			}
			if owner != "" && owner != aoAgentName(cfg.SessionID) {
				injected, err = defaultAgentConfig(cfg.SessionID)
				if err != nil {
					return nil, err
				}
			}
		}
		if injected != "" {
			envArgs = append(envArgs, "OPENCODE_CONFIG_CONTENT="+injected)
		}
		cmd = append([]string{"env"}, envArgs...)
		cmd = append(cmd, binary, "--standalone")
	}
	switch ports.NormalizePermissionMode(cfg.Permissions) {
	case ports.PermissionModeAuto:
		cmd = append(cmd, "--auto")
	case ports.PermissionModeBypassPermissions:
		cmd = append(cmd, "--dangerously-skip-permissions")
	}
	if nativeID != "" {
		cmd = append(cmd, "--session", nativeID)
	}
	if cfg.Prompt != "" {
		cmd = append(cmd, "--prompt="+cfg.Prompt)
	}
	return cmd, nil
}

// rememberSharedPassword records the password for the next supervised launch
// of sessionID. AugmentRuntimeLaunchEnv moves it into the runtime environment.
func (p *Plugin) rememberSharedPassword(sessionID, password string) {
	if sessionID == "" {
		return
	}
	p.passwordMu.Lock()
	defer p.passwordMu.Unlock()
	if p.pendingSharedPasswords == nil {
		p.pendingSharedPasswords = map[string]string{}
	}
	p.pendingSharedPasswords[sessionID] = password
}

func (p *Plugin) forgetSharedPassword(sessionID string) {
	if sessionID == "" {
		return
	}
	p.passwordMu.Lock()
	defer p.passwordMu.Unlock()
	delete(p.pendingSharedPasswords, sessionID)
}

// AugmentRuntimeLaunchEnv supplies the shared server's password without putting
// it in argv. A private launch has no recorded password and keeps the caller's
// environment untouched.
func (p *Plugin) AugmentRuntimeLaunchEnv(env map[string]string, _ string, sessionID domain.SessionID, _ string) {
	key := string(sessionID)
	if key == "" {
		return
	}
	p.passwordMu.Lock()
	password := p.pendingSharedPasswords[key]
	delete(p.pendingSharedPasswords, key)
	p.passwordMu.Unlock()
	if password != "" {
		env["OPENCODE_PASSWORD"] = password
	}
}

type permissionRule struct {
	Action   string `json:"action"`
	Resource string `json:"resource"`
	Effect   string `json:"effect"`
}

// Only AO's selected agent and default_agent are owned here. User config files,
// credentials, other agents, provider settings, and top-level permission rules
// remain intact. Native v2 applies inline config after project config and
// appends agent rules after global rules (last matching permission wins).
func prepareConfigContent(existing, sessionID, prompt, model string, mode ports.PermissionMode) (string, error) {
	model = strings.TrimSpace(model)
	mode = ports.NormalizePermissionMode(mode)
	if prompt == "" && model == "" && mode != ports.PermissionModeAcceptEdits && mode != ports.PermissionModeBypassPermissions {
		return "", nil
	}
	config := map[string]json.RawMessage{}
	if strings.TrimSpace(existing) != "" {
		if err := json.Unmarshal([]byte(existing), &config); err != nil || config == nil {
			return "", fmt.Errorf("opencode-v2: OPENCODE_CONFIG_CONTENT must be a JSON object")
		}
	}
	agents := map[string]json.RawMessage{}
	if raw, ok := config["agents"]; ok {
		if err := json.Unmarshal(raw, &agents); err != nil || agents == nil {
			return "", fmt.Errorf("opencode-v2: OPENCODE_CONFIG_CONTENT agents must be an object")
		}
	}
	name := aoAgentName(sessionID)
	agent := map[string]any{}
	if raw, ok := agents[name]; ok {
		if err := json.Unmarshal(raw, &agent); err != nil || agent == nil {
			return "", fmt.Errorf("opencode-v2: OPENCODE_CONFIG_CONTENT agent %q must be an object", name)
		}
	}
	// agents.build.permissions is left untouched: inline config replaces arrays on
	// merge, so writing it would clear file-defined build-agent rules.
	agent["mode"] = "primary"
	if prompt != "" {
		agent["system"] = prompt
	}
	delete(agent, "model")
	if model != "" {
		agent["model"] = model
	}
	delete(agent, "permissions")
	switch mode {
	case ports.PermissionModeAcceptEdits:
		agent["permissions"] = []permissionRule{{Action: "edit", Resource: "*", Effect: "allow"}}
	case ports.PermissionModeBypassPermissions:
		agent["permissions"] = []permissionRule{{Action: "*", Resource: "*", Effect: "allow"}}
	}
	raw, err := json.Marshal(agent)
	if err != nil {
		return "", err
	}
	agents[name] = raw
	config["agents"], err = json.Marshal(agents)
	if err != nil {
		return "", err
	}
	config["default_agent"], err = json.Marshal(name)
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(config)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// PrepareACPConfigContent merges AO's standing instructions and permission
// boundary into OpenCode 2's built-in build agent. OpenCode's ACP bridge may
// snapshot its built-in modes before custom agents finish loading, so AO keeps
// the provider-visible mode stable and answers its permission requests itself.
// User-owned providers, top-level permission rules, and unrelated agents remain
// untouched.
func PrepareACPConfigContent(existing, systemPrompt string, _ ports.PermissionMode) (string, error) {
	config := map[string]json.RawMessage{}
	if strings.TrimSpace(existing) != "" {
		if err := json.Unmarshal([]byte(existing), &config); err != nil || config == nil {
			return "", fmt.Errorf("opencode-v2: OPENCODE_CONFIG_CONTENT must be a JSON object")
		}
	}
	agents := map[string]json.RawMessage{}
	if raw, ok := config["agents"]; ok {
		if err := json.Unmarshal(raw, &agents); err != nil || agents == nil {
			return "", fmt.Errorf("opencode-v2: OPENCODE_CONFIG_CONTENT agents must be an object")
		}
	}
	const name = "build"
	agent := map[string]any{}
	if raw, ok := agents[name]; ok {
		if err := json.Unmarshal(raw, &agent); err != nil || agent == nil {
			return "", fmt.Errorf("opencode-v2: OPENCODE_CONFIG_CONTENT agent %q must be an object", name)
		}
	}
	// agents.build.permissions is left untouched: inline config replaces arrays on
	// merge, so writing it would clear file-defined build-agent rules.
	agent["mode"] = "primary"
	if systemPrompt != "" {
		agent["system"] = systemPrompt
	}
	raw, err := json.Marshal(agent)
	if err != nil {
		return "", err
	}
	agents[name] = raw
	config["agents"], err = json.Marshal(agents)
	if err != nil {
		return "", err
	}
	config["default_agent"], err = json.Marshal(name)
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(config)
	if err != nil {
		return "", fmt.Errorf("opencode-v2: encode ACP agent config: %w", err)
	}
	return string(data), nil
}

func aoAgentName(sessionID string) string {
	name := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			return r
		}
		return '-'
	}, strings.TrimSpace(sessionID))
	name = strings.Trim(name, "-_")
	if name == "" {
		return "ao-system-prompt"
	}
	return "ao-" + name
}

// SessionInfo returns the standard session info.
func (p *Plugin) SessionInfo(ctx context.Context, session ports.SessionRef) (ports.SessionInfo, bool, error) {
	if err := ctx.Err(); err != nil {
		return ports.SessionInfo{}, false, err
	}
	info, ok := agentbase.StandardSessionInfo(session)
	return info, ok, nil
}

// AuthStatus reports authenticated integrations, including environment connections.
// Keep this probe private so readiness never starts/replaces a shared service.
func (p *Plugin) AuthStatus(ctx context.Context) (ports.AgentAuthStatus, error) {
	binary, err := opencode.ResolveBinaryForMajor(ctx, 2)
	if err != nil {
		return ports.AgentAuthStatusUnknown, err
	}
	dataHome, err := DataHome(ctx)
	if err != nil {
		return ports.AgentAuthStatusUnknown, err
	}
	probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	cmd := aoprocess.CommandContext(probeCtx, binary, "auth", "list", "--standalone", "--format", "json")
	cmd.Env = append(os.Environ(), "XDG_DATA_HOME="+dataHome)
	cmd.WaitDelay = 100 * time.Millisecond
	out, err := cmd.Output()
	if ctx.Err() != nil {
		return ports.AgentAuthStatusUnknown, ctx.Err()
	}
	if err != nil {
		return ports.AgentAuthStatusUnknown, nil
	}
	var integrations []struct {
		Connections []json.RawMessage `json:"connections"`
	}
	if err := json.Unmarshal(out, &integrations); err != nil {
		return ports.AgentAuthStatusUnknown, nil
	}
	for _, integration := range integrations {
		if len(integration.Connections) > 0 {
			ports.RecordAuthEvidence(ctx, domain.CommandSource(binary, "auth", "list"))
			return ports.AgentAuthStatusAuthorized, nil
		}
	}
	return ports.AgentAuthStatusUnknown, nil
}
