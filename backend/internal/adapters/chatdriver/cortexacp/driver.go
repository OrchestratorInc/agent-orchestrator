// Package cortexacp contains an unregistered Cortex Code ACP candidate.
//
// The official transport is documented at
// https://docs.snowflake.com/en/user-guide/cortex-code/cortex-code-acp.
// Native instruction delivery, permission enforcement and exact history still
// need authenticated qualification. Do not add this candidate to registry.Build.
package cortexacp

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/nativeacp"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	acpsdk "github.com/coder/acp-go-sdk"
)

const harness domain.AgentHarness = "cortex-code"

type candidate struct {
	driver       ports.ChatDriver
	reconnect    ports.ChatDriverReconnector
	instructions bool
}

var _ ports.ChatDriver = (*candidate)(nil)
var _ ports.ChatDriverReconnector = (*candidate)(nil)

// NewCandidate uses the caller's existing installation discovery/auth plugin;
// it never downloads a CLI or creates credentials. The caller must prepare an
// isolated HOME underneath DataDir, including native user rules and connection
// configuration. No source profile is read, copied or changed by this binding.
// Probe deliberately rejects production admission. Explicit qualification can
// exercise Start/Resume using empty standing instructions; real AO instructions
// fail closed until a supported additive ACP route has been qualified.
func NewCandidate(plugin nativeacp.Plugin, log *slog.Logger) ports.ChatDriver {
	return newCandidate(plugin, log, false)
}

// NewInstructionCandidate explicitly enables the experimental additive plugin
// route for qualification in isolated state. It is not production admission:
// Probe and registry remain closed until real fresh/restore consumption, native
// default/project-rule preservation and the remaining lifecycle gates pass.
// An adopted live host retains its original launch context; changed instructions
// take effect on a cold exact restore after confirmed termination.
func NewInstructionCandidate(plugin nativeacp.Plugin, log *slog.Logger) ports.ChatDriver {
	return newCandidate(plugin, log, true)
}

func newCandidate(plugin nativeacp.Plugin, log *slog.Logger, instructions bool) ports.ChatDriver {
	driver := nativeacp.New(plugin, nativeacp.Config{
		Harness: harness,
		Configure: func(ctx context.Context, cfg acpdriver.LaunchConfig) ([]string, map[string]string, error) {
			return configureInstructions(ctx, cfg, instructions)
		},
		SessionOptions: sessionOptions,
		LaunchSessionOptions: func(cfg acpdriver.LaunchConfig) []acpdriver.SessionOption {
			return sessionOptions(ports.ChatTurnSettings{Model: cfg.Model, Approval: cfg.Permissions})
		},
		RequireAdvertisedOptions: true,
		ValidateInitialize: func(init acpsdk.InitializeResponse) error {
			if !init.AgentCapabilities.LoadSession {
				return fmt.Errorf("%w: Cortex candidate requires native load/history replay", ports.ErrChatDriverIncompatible)
			}
			return nil
		},
		ValidateTurnSettings: validateSettings,
		// Every native request remains in the human approval flow. In particular,
		// default never auto-approves tools based on their names or input shape.
		PermissionPolicy: func(ports.PermissionMode, acpsdk.RequestPermissionRequest) (acpsdk.PermissionOptionId, bool) {
			return "", false
		},
	}, log)
	return &candidate{driver: driver, reconnect: driver.(ports.ChatDriverReconnector), instructions: instructions}
}

func (d *candidate) Harness() domain.AgentHarness { return harness }

func (d *candidate) Probe(context.Context) (ports.ChatCapabilities, error) {
	return nil, fmt.Errorf("%w: Cortex ACP candidate is not qualified for production Chat", ports.ErrChatDriverIncompatible)
}

func (d *candidate) Start(ctx context.Context, cfg ports.ChatStartConfig) (ports.ChatConversation, error) {
	if cfg.ReadOnly || cfg.DisableNetwork || cfg.Ephemeral {
		return nil, fmt.Errorf("%w: Cortex ACP sandbox, network isolation and ephemeral sessions are not qualified", ports.ErrChatUnsupported)
	}
	if err := validateSettings(cfg.Permissions, ports.ChatTurnSettings{Model: cfg.Model, Effort: cfg.Effort, Approval: cfg.Permissions}); err != nil {
		return nil, err
	}
	if err := d.validateLaunch(cfg.DataDir, cfg.WorkspacePath, cfg.SystemPrompt, cfg.Env); err != nil {
		return nil, err
	}
	return guardConversation(d.driver.Start(ctx, cfg))
}

func (d *candidate) Resume(ctx context.Context, cfg ports.ChatResumeConfig) (ports.ChatConversation, error) {
	if err := d.validateResume(cfg); err != nil {
		return nil, err
	}
	if err := validateNativeHistory(ctx, cfg); err != nil {
		return nil, err
	}
	// Shared host preparation can replace Env just before a cold launch. The
	// admitted snapshot must belong to that actual profile, not only the
	// caller's preliminary environment. Live adoption never invokes PrepareEnv.
	if prepare := cfg.PrepareEnv; prepare != nil {
		original := cfg
		cfg.PrepareEnv = func(prepareCtx context.Context) (map[string]string, error) {
			env, err := prepare(prepareCtx)
			if err != nil {
				return nil, err
			}
			prepared := original
			prepared.Env = env
			if err := d.validateLaunch(prepared.DataDir, prepared.WorkspacePath, prepared.SystemPrompt, env); err != nil {
				return nil, err
			}
			if err := validateNativeHistory(prepareCtx, prepared); err != nil {
				return nil, err
			}
			return env, nil
		}
	}
	return guardConversation(d.driver.Resume(ctx, cfg))
}

func (d *candidate) Reconnect(ctx context.Context, cfg ports.ChatResumeConfig) (ports.ChatConversation, error) {
	if err := d.validateResume(cfg); err != nil {
		return nil, err
	}
	cfg.ReconnectOnly = true
	return guardConversation(d.reconnect.Reconnect(ctx, cfg))
}

func (d *candidate) validateResume(cfg ports.ChatResumeConfig) error {
	if strings.TrimSpace(cfg.ProviderConversationID) == "" || cfg.FreshIfMissing {
		return fmt.Errorf("%w: Cortex requires exact native history without fresh fallback", ports.ErrChatResumeFailed)
	}
	if cfg.ReadOnly {
		return fmt.Errorf("%w: Cortex ACP read-only sandbox is not qualified", ports.ErrChatUnsupported)
	}
	if err := validateSettings(cfg.Permissions, ports.ChatTurnSettings{Model: cfg.Model, Effort: cfg.Effort, Approval: cfg.Permissions}); err != nil {
		return err
	}
	return d.validateLaunch(cfg.DataDir, cfg.WorkspacePath, cfg.SystemPrompt, cfg.Env)
}

func (d *candidate) validateLaunch(dataDir, workspace, prompt string, env map[string]string) error {
	if err := validateInstructions(prompt, d.instructions); err != nil {
		return err
	}
	return validateLaunch(dataDir, workspace, "", env)
}

func validateLaunch(dataDir, workspace, prompt string, env map[string]string) error {
	if prompt != "" {
		return fmt.Errorf("%w: Cortex ACP additive standing instructions are not qualified", ports.ErrChatDriverIncompatible)
	}
	// Resolve both directories: a lexically isolated HOME that links into the
	// user's profile must never authorize a candidate launch. Existing profiles
	// only; the adapter must not initialize or modify source configuration.
	root, rootErr := filepath.EvalSymlinks(dataDir)
	home, homeErr := filepath.EvalSymlinks(env["HOME"])
	if !filepath.IsAbs(dataDir) || !filepath.IsAbs(env["HOME"]) || rootErr != nil || homeErr != nil {
		return fmt.Errorf("%w: Cortex candidate requires an existing isolated HOME under DataDir", ports.ErrChatDriverIncompatible)
	}
	info, err := os.Stat(home)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("%w: Cortex candidate HOME must be a directory", ports.ErrChatDriverIncompatible)
	}
	relative, err := filepath.Rel(root, home)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return fmt.Errorf("%w: Cortex candidate HOME is outside its isolated DataDir", ports.ErrChatDriverIncompatible)
	}
	if !filepath.IsAbs(workspace) {
		return fmt.Errorf("%w: Cortex candidate requires an absolute workspace", ports.ErrChatDriverIncompatible)
	}
	return nil
}

func configure(ctx context.Context, cfg acpdriver.LaunchConfig) ([]string, map[string]string, error) {
	return configureInstructions(ctx, cfg, false)
}

func configureInstructions(ctx context.Context, cfg acpdriver.LaunchConfig, instructions bool) ([]string, map[string]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if err := validateInstructions(cfg.SystemPrompt, instructions); err != nil {
		return nil, nil, err
	}
	if err := validateLaunch(cfg.DataDir, cfg.WorkspacePath, "", cfg.Env); err != nil {
		return nil, nil, err
	}
	args := []string{"acp", "serve", "--workdir", cfg.WorkspacePath}
	if cfg.Model != "" {
		args = append(args, "--model", cfg.Model)
	}
	if cfg.SystemPrompt != "" {
		pluginDir, err := instructionPlugin(ctx, cfg.DataDir, cfg.SystemPrompt)
		if err != nil {
			return nil, nil, err
		}
		args = append(args, "--plugin-dir", pluginDir)
	}
	// Do not set --bypass, replace the native prompt, or alter user/project
	// plugins and safety defaults. Standard mode is required through the
	// advertised config option before the first turn, including after load.
	// Override all standard home/config selectors after plugin augmentation so
	// an inherited XDG or Windows profile cannot redirect this isolated launch.
	home := cfg.Env["HOME"]
	return args, map[string]string{
		"HOME":            home,
		"USERPROFILE":     home,
		"XDG_CONFIG_HOME": filepath.Join(home, ".config"),
	}, nil
}

func validateSettings(_ ports.PermissionMode, settings ports.ChatTurnSettings) error {
	if settings.Effort != "" {
		return fmt.Errorf("%w: Cortex ACP does not advertise an AO effort mapping", ports.ErrChatUnsupported)
	}
	switch settings.Approval {
	case "", ports.PermissionModeDefault:
		return nil
	default:
		return fmt.Errorf("%w: Cortex ACP approval mode %q is not qualified", ports.ErrChatUnsupported, settings.Approval)
	}
}

func sessionOptions(settings ports.ChatTurnSettings) []acpdriver.SessionOption {
	options := []acpdriver.SessionOption{{ID: "mode", Value: "standard"}}
	if settings.Model != "" {
		options = append(options, acpdriver.SessionOption{ID: "model", Value: settings.Model})
	}
	return options
}
