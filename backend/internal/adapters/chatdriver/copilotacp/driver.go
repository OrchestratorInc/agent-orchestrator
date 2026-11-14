// Package copilotacp binds the user's own GitHub Copilot CLI installation to
// AO's reusable ACP Chat transport.
//
// The behavior this binding relies on was verified live against Copilot CLI
// 1.0.93 (see docs/harnesses/copilot.md):
//
//   - `copilot --acp` speaks ACP over stdio and supports session/load.
//   - `--agent` is ignored under --acp. Custom agent profiles in .github/agents
//     are offered as values of the "agent" config option instead.
//   - `--model` and COPILOT_MODEL are ignored under --acp, and an unadvertised
//     "model" config option is acknowledged without being applied.
//   - Approval: the --allow-tool / --allow-all-tools launch flags set a fixed
//     baseline, gated tools raise session/request_permission, and a rejection
//     is honored. The "allow_all" config option switches full bypass on and off
//     over that baseline. A process launched with --allow-all cannot leave
//     bypass, so bypass is selected through "allow_all" instead of that flag.
//   - A second session/load on one connection fails with "already loaded";
//     closing the session first lets it reload, keeping its config options.
//   - Copilot's session modes (agent, plan, autopilot) are task modes, not an
//     approval vocabulary, so AO never maps approval onto them.
package copilotacp

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/copilot"
	acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/nativeacp"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Plugin is the Copilot agent-plugin surface the binding reuses: binary
// resolution, the auth probe, and the per-session agent profile writer.
type Plugin interface {
	nativeacp.Plugin
	InstallAgentProfile(context.Context, ports.WorkspaceHookConfig) error
}

// Copilot's ACP config option ids.
const (
	optionAgent    = "agent"
	optionAllowAll = "allow_all"
	optionModel    = "model"
)

// New launches `copilot --acp` from the exact binary the Copilot agent plugin
// resolves. Login, entitlement, models, MCP servers, and settings stay with
// the user's installation.
func New(plugin Plugin, log *slog.Logger) ports.ChatDriver {
	return nativeacp.New(plugin, config(plugin), log)
}

func config(plugin Plugin) nativeacp.Config {
	return nativeacp.Config{
		Harness: domain.HarnessCopilot,
		Capabilities: ports.ChatCapabilities{
			// Copilot reports token usage on every prompt response.
			ports.ChatCapabilityUsage: true,
		},
		Configure: func(ctx context.Context, cfg acpdriver.LaunchConfig) ([]string, map[string]string, error) {
			return configure(ctx, plugin, cfg)
		},
		LaunchSessionOptions:     launchSessionOptions,
		RequireAdvertisedOptions: true,
		// Copilot answers a second session/load on the same connection with
		// "already loaded", which broke switching back from the terminal.
		CloseSessionBeforeReload: true,
		SessionOptions:           sessionOptions,
		ValidateTurnSettings:     validateTurnSettings,
		ValidateInitialize:       validateInitialize,
	}
}

// configure builds the `copilot --acp` argv. Copilot takes ACP as a flag, not
// a subcommand. Copilot reads .github/agents once at process start, so the
// session's agent profile is written before launch. Spawn installs it already;
// writing it here too covers a restore into a workspace that lost it.
func configure(ctx context.Context, plugin Plugin, cfg acpdriver.LaunchConfig) ([]string, map[string]string, error) {
	// AO controls release switching for Chat as well as the terminal.
	args := append([]string{"--acp", "--no-auto-update"}, launchApprovalArgs(cfg.Permissions)...)
	if strings.TrimSpace(cfg.SystemPrompt) != "" {
		if err := plugin.InstallAgentProfile(ctx, ports.WorkspaceHookConfig{
			SessionID:     string(cfg.SessionID),
			WorkspacePath: cfg.WorkspacePath,
			SystemPrompt:  cfg.SystemPrompt,
		}); err != nil {
			return nil, nil, err
		}
	}
	return args, nil, nil
}

// launchApprovalArgs is the terminal's approval flags, except bypass. A process
// launched with --allow-all stays in bypass even after "allow_all" is turned
// off, so a bypass session launches at the default baseline and turns bypass
// on through "allow_all", which it can then turn off again.
func launchApprovalArgs(permissions ports.PermissionMode) []string {
	if ports.NormalizePermissionMode(permissions) == ports.PermissionModeBypassPermissions {
		return nil
	}
	return copilot.ApprovalArgs(permissions)
}

// launchSessionOptions selects AO's per-session agent profile, which carries
// the standing instructions. Copilot ignores --agent under --acp but offers
// every profile as an "agent" value and keeps the choice across session/load.
func launchSessionOptions(cfg acpdriver.LaunchConfig) []acpdriver.SessionOption {
	name := copilot.AgentProfileName(string(cfg.SessionID), cfg.SystemPrompt, "")
	if name == "" {
		return nil
	}
	return []acpdriver.SessionOption{{ID: optionAgent, Value: name}}
}

// sessionOptions maps AO's turn settings onto Copilot's config options. A model
// is sent only as the "model" option, and only if the session advertises it:
// Copilot ignores --model under --acp, and RequireAdvertisedOptions turns an
// unadvertised model into an error rather than a silent no-op.
//
// Effort is not sent. Copilot advertises no reasoning-effort option over ACP,
// and an effort carried over from another harness's settings must not fail
// the session, so Copilot keeps its own effort.
func sessionOptions(settings ports.ChatTurnSettings) []acpdriver.SessionOption {
	var options []acpdriver.SessionOption
	if model := strings.TrimSpace(settings.Model); model != "" {
		options = append(options, acpdriver.SessionOption{ID: optionModel, Value: model})
	}
	if settings.Approval != "" {
		options = append(options, acpdriver.SessionOption{ID: optionAllowAll, Value: allowAll(settings.Approval)})
	}
	return options
}

func allowAll(mode ports.PermissionMode) string {
	if ports.NormalizePermissionMode(mode) == ports.PermissionModeBypassPermissions {
		return "on"
	}
	return "off"
}

// validateTurnSettings admits only the changes a live Copilot process can make.
//
// The launch flags fix a baseline for the process, and "allow_all" only toggles
// full bypass over it. A session can therefore move between its baseline and
// bypass, and nowhere else; any other approval fails with the reachable modes
// instead of quietly running under a different policy.
func validateTurnSettings(initial ports.PermissionMode, settings ports.ChatTurnSettings) error {
	if settings.Approval == "" {
		return nil
	}
	requested := ports.NormalizePermissionMode(settings.Approval)
	baseline := baselineMode(initial)
	if requested == ports.PermissionModeBypassPermissions || requested == baseline {
		return nil
	}
	return fmt.Errorf(
		"%w: a Copilot Chat session started in %s can switch only between %s and %s; restart Chat to use %s",
		acpdriver.ErrACPSetterUnsupported, ports.NormalizePermissionMode(initial),
		baseline, ports.PermissionModeBypassPermissions, requested)
}

// baselineMode is the policy a session returns to when "allow_all" is off. A
// bypass launch carries no approval flag, so its baseline is default.
func baselineMode(initial ports.PermissionMode) ports.PermissionMode {
	mode := ports.NormalizePermissionMode(initial)
	if mode == ports.PermissionModeBypassPermissions {
		return ports.PermissionModeDefault
	}
	return mode
}
