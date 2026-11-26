// Package opencodev2 implements the official OpenCode 2 TUI contract. The
// executable name is still opencode; the registry identity is opencode-v2.
package opencodev2

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/agentbase"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/opencode"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

// Plugin is the OpenCode 2 TUI agent adapter.
type Plugin struct{ agentbase.Base }

// New returns an OpenCode 2 adapter.
func New() *Plugin { return &Plugin{} }

var _ adapters.Adapter = (*Plugin)(nil)
var _ ports.Agent = (*Plugin)(nil)
var _ ports.AgentAuthChecker = (*Plugin)(nil)
var _ ports.AgentBinaryResolver = (*Plugin)(nil)
var _ ports.SemanticMessageAcceptanceSignaler = (*Plugin)(nil)
var _ ports.AgentPromptReadinessProvider = (*Plugin)(nil)

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
	return command(ctx, binary, cfg, "")
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
	cmd, err := command(ctx, binary, ports.LaunchConfig{Config: cfg.Config, SessionID: cfg.Session.ID, Permissions: cfg.Permissions, Prompt: cfg.Prompt, SystemPrompt: cfg.SystemPrompt, SystemPromptFile: cfg.SystemPromptFile}, nativeID)
	return cmd, err == nil, err
}

// GetPromptDeliveryStrategy types the first task into the TUI once AO's agent
// is active whenever AO selects one. OpenCode 2 loads config agents and
// providers in the background, and its TUI submits --prompt as soon as any
// agent is listed, so a --prompt task can start on the built-in build agent
// and the fallback model (#9067). Without an AO agent, build is the intended
// agent and --prompt stays.
func (p *Plugin) GetPromptDeliveryStrategy(ctx context.Context, cfg ports.LaunchConfig) (ports.PromptDeliveryStrategy, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	prompt, err := systemPrompt(cfg)
	if err != nil {
		return "", err
	}
	if selectsAOAgent(prompt, cfg.Config.Model, cfg.Permissions) {
		return ports.PromptDeliveryAfterStart, nil
	}
	return ports.PromptDeliveryInCommand, nil
}

// PromptReadinessHints holds the first task until the TUI footer names AO's
// agent ("Ao-W-1 · model", or "Ao-W-1 auto · model" in auto mode), which shows
// the inline config has loaded for the session the task creates. While
// starting, the footer can name AO's agent before any provider has loaded
// ("No provider selected"); Enter then opens the connect dialog instead of
// sending, so that state holds delivery. If the agent never appears, AO still
// types the task when the wait ends.
func (p *Plugin) PromptReadinessHints(ctx context.Context, cfg ports.LaunchConfig) (ports.PromptReadinessHints, error) {
	if err := ctx.Err(); err != nil {
		return ports.PromptReadinessHints{}, err
	}
	return ports.PromptReadinessHints{
		InitialDelay:     500 * time.Millisecond,
		Patterns:         []string{agentLabel(aoAgentName(cfg.SessionID))},
		BlockingPatterns: []string{noProviderSelected},
		PollInterval:     250 * time.Millisecond,
		Timeout:          45 * time.Second,
		Lines:            200,
		// The footer never ends with a newline, so only the rendered screen shows it.
		RenderedScreen: true,
	}, nil
}

// noProviderSelected is the TUI footer's model label before any provider loads.
const noProviderSelected = "No provider selected"

// agentLabel is how the TUI footer names an agent: Locale.titlecase, which
// upper-cases each word character that follows a non-word character.
func agentLabel(name string) string {
	word := func(b byte) bool {
		return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '_'
	}
	label := []byte(name)
	for i := range label {
		if word(label[i]) && (i == 0 || !word(label[i-1])) && label[i] >= 'a' && label[i] <= 'z' {
			label[i] -= 'a' - 'A'
		}
	}
	return string(label)
}

func systemPrompt(cfg ports.LaunchConfig) (string, error) {
	if cfg.SystemPrompt != "" || cfg.SystemPromptFile == "" {
		return cfg.SystemPrompt, nil
	}
	data, err := os.ReadFile(cfg.SystemPromptFile)
	if err != nil {
		return "", fmt.Errorf("opencode-v2: read system prompt: %w", err)
	}
	return string(data), nil
}

// selectsAOAgent reports whether prepareConfigContent selects AO's agent.
func selectsAOAgent(prompt, model string, mode ports.PermissionMode) bool {
	mode = ports.NormalizePermissionMode(mode)
	return prompt != "" || strings.TrimSpace(model) != "" || mode == ports.PermissionModeAcceptEdits || mode == ports.PermissionModeBypassPermissions
}

// cliConfigEnv carries OpenCode 2's TUI settings, merged over the user's
// cli.json. After an OpenCode update the first TUI opens a "What's new" dialog
// that hides the agent marker and takes the typed task, so AO turns release
// highlights off whenever it types the task itself.
const cliConfigEnv = "OPENCODE_CLI_CONFIG_CONTENT"

func prepareCLIConfigContent(existing string) (string, error) {
	config := map[string]json.RawMessage{}
	if strings.TrimSpace(existing) != "" {
		if err := json.Unmarshal([]byte(existing), &config); err != nil || config == nil {
			return "", fmt.Errorf("opencode-v2: %s must be a JSON object", cliConfigEnv)
		}
	}
	config["highlights"] = json.RawMessage("false")
	data, err := json.Marshal(config)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// The v2 root TUI has no --model/--agent flags. Select the AO agent through
// default_agent in the highest-precedence inline config. --prompt=<value>
// keeps even leading-dash prompts a single string value in the Effect CLI.
func command(ctx context.Context, binary string, cfg ports.LaunchConfig, nativeID string) ([]string, error) {
	prompt, err := systemPrompt(cfg)
	if err != nil {
		return nil, err
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
	var envArgs []string
	envArgs = append(envArgs, "XDG_DATA_HOME="+dataHome)
	if content != "" {
		envArgs = append(envArgs, "OPENCODE_CONFIG_CONTENT="+content)
	}
	// A fresh launch with an AO agent gets its task typed by AO, so the manager
	// has cleared cfg.Prompt (see GetPromptDeliveryStrategy); a launch with no
	// task looks the same and gets the same setting.
	if content != "" && cfg.Prompt == "" && nativeID == "" {
		cliContent, err := prepareCLIConfigContent(os.Getenv(cliConfigEnv))
		if err != nil {
			return nil, err
		}
		envArgs = append(envArgs, cliConfigEnv+"="+cliContent)
	}
	var cmd []string
	if len(envArgs) > 0 {
		cmd = append([]string{"env"}, envArgs...)
	}
	cmd = append(cmd, binary, "--standalone")
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
