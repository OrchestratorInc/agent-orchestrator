package fx

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/agentbase"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const adapterID = "fx"

type Plugin struct {
	agentbase.Base
	binaryMu       sync.Mutex
	resolvedBinary string
	statusRunner   commandRunner
	statusTimeout  time.Duration
}

func New() *Plugin { return &Plugin{} }

var _ adapters.Adapter = (*Plugin)(nil)
var _ ports.Agent = (*Plugin)(nil)
var _ ports.AgentBinaryResolver = (*Plugin)(nil)
var _ ports.AgentPromptReadinessProvider = (*Plugin)(nil)
var _ ports.AgentAfterStartPromptBuilder = (*Plugin)(nil)
var _ ports.AgentContinuationCapabilityProvider = (*Plugin)(nil)
var _ ports.AgentNativeSessionConfigProvider = (*Plugin)(nil)

func (p *Plugin) Manifest() adapters.Manifest {
	return adapters.Manifest{
		ID:          adapterID,
		Name:        "fx",
		Description: "Run fx worker sessions.",
		Version:     "0.0.1",
		Capabilities: []adapters.Capability{
			adapters.CapabilityAgent,
		},
	}
}

func (p *Plugin) GetConfigSpec(ctx context.Context) (ports.ConfigSpec, error) {
	return agentbase.ModelConfigSpec(ctx, "Model override passed through `FX_MODEL`.")
}

func (p *Plugin) GetLaunchCommand(ctx context.Context, cfg ports.LaunchConfig) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	binary, err := p.fxBinary(ctx)
	if err != nil {
		return nil, err
	}
	return commandWithOverrides(binary, cfg.Config, cfg.Permissions), nil
}

func (p *Plugin) GetPromptDeliveryStrategy(ctx context.Context, _ ports.LaunchConfig) (ports.PromptDeliveryStrategy, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return ports.PromptDeliveryAfterStart, nil
}

func (p *Plugin) PromptReadinessHints(ctx context.Context, _ ports.LaunchConfig) (ports.PromptReadinessHints, error) {
	if err := ctx.Err(); err != nil {
		return ports.PromptReadinessHints{}, err
	}
	return ports.PromptReadinessHints{InitialDelay: 750 * time.Millisecond}, nil
}

func (p *Plugin) BuildAfterStartPrompt(ctx context.Context, cfg ports.LaunchConfig) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return "## Orchestrator Standing Instructions\n\n" + strings.TrimSpace(cfg.SystemPrompt) +
		"\n\n## Orchestrator Task\n\n" + strings.TrimSpace(cfg.Prompt), nil
}

func (p *Plugin) ContinuationCapabilities() ports.ContinuationCapabilities {
	return ports.ContinuationCapabilities{
		FreshNativeSessionID: ports.FreshNativeSessionIDProviderAssigned,
	}
}

func (p *Plugin) NativeSessionConfigDir(ctx context.Context, env map[string]string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	home := strings.TrimSpace(env["HOME"])
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return "", err
		}
	}
	return filepath.Join(home, ".fx"), nil
}

func (p *Plugin) GetAgentHooks(ctx context.Context, _ ports.WorkspaceHookConfig) error {
	return ctx.Err()
}

func (p *Plugin) GetRestoreCommand(ctx context.Context, cfg ports.RestoreConfig) ([]string, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	agentSessionID := strings.TrimSpace(cfg.Session.Metadata[ports.MetadataKeyAgentSessionID])
	if agentSessionID == "" {
		return nil, false, nil
	}
	binary, err := p.fxBinary(ctx)
	if err != nil {
		return nil, false, err
	}
	cmd := commandWithOverrides(binary, cfg.Config, cfg.Permissions)
	return append(cmd, "--resume", agentSessionID), true, nil
}

func (p *Plugin) SessionInfo(ctx context.Context, session ports.SessionRef) (ports.SessionInfo, bool, error) {
	if err := ctx.Err(); err != nil {
		return ports.SessionInfo{}, false, err
	}
	info, ok := agentbase.StandardSessionInfo(session)
	return info, ok, nil
}

func commandWithOverrides(binary string, cfg ports.AgentConfig, permissions ports.PermissionMode) []string {
	env := make([]string, 0, 3)
	if cfg.Model != "" {
		provider, model := splitCatalogModel(cfg.Model)
		if provider != "" {
			env = append(env, "FX_PROVIDER="+provider)
		}
		env = append(env, "FX_MODEL="+model)
	}
	if mode := permissionMode(permissions); mode != "" {
		env = append(env, "FX_PERMISSION_MODE="+mode)
	}
	if len(env) == 0 {
		return []string{binary}
	}
	cmd := append([]string{"env"}, env...)
	return append(cmd, binary)
}

func permissionMode(mode ports.PermissionMode) string {
	switch ports.NormalizePermissionMode(mode) {
	case ports.PermissionModeAcceptEdits:
		return "ask"
	case ports.PermissionModeAuto:
		return "auto"
	case ports.PermissionModeBypassPermissions:
		return "full-access"
	default:
		return ""
	}
}
