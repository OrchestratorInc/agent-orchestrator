package agyacp

import (
	"context"
	"fmt"
	"log/slog"
	"runtime"
	"strings"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/agy"
	acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type Plugin interface {
	ResolveBinary(context.Context) (string, error)
	AuthStatus(context.Context) (ports.AgentAuthStatus, error)
}

const (
	modeDefault  = "default"
	modeAutoEdit = "auto_edit"
	modeYolo     = "yolo"
)

const cliModeAcceptEdits = "accept-edits"

func New(plugin Plugin, log *slog.Logger) ports.ChatDriver {
	var agyCLI func(context.Context) (string, error)
	if plugin != nil {
		agyCLI = plugin.ResolveBinary
	}
	return acpdriver.New(buildConfig(plugin, newResolver(agyCLI), log), log)
}

func buildConfig(plugin Plugin, resolve resolver, log *slog.Logger) acpdriver.Config {
	return acpdriver.Config{
		Harness: domain.HarnessAgy,
		Capabilities: ports.ChatCapabilities{
			ports.ChatCapabilityStreaming: true,
			ports.ChatCapabilityTools:     true,
			ports.ChatCapabilityApprovals: true,
			ports.ChatCapabilityInterrupt: true,
			ports.ChatCapabilityResume:    true,
		},
		Probe: func(ctx context.Context) error {
			if _, err := resolve.target(ctx); err != nil {
				return fmt.Errorf("%w: %w", ports.ErrChatDriverUnavailable, err)
			}
			if plugin != nil {
				status, err := plugin.AuthStatus(ctx)
				if err == nil && status == ports.AgentAuthStatusUnauthorized {
					return ports.ErrChatAuthRequired
				}
				if err != nil && log != nil {
					log.Debug("agy auth probe inconclusive; continuing", "error", err)
				}
			}
			return nil
		},
		Launch: func(ctx context.Context, cfg acpdriver.LaunchConfig) (acpdriver.Launch, error) {
			target, err := resolve.target(ctx)
			if err != nil {
				return acpdriver.Launch{}, fmt.Errorf("%w: %w", ports.ErrChatDriverUnavailable, err)
			}
			env := make(map[string]string, len(cfg.Env)+2)
			for key, value := range cfg.Env {
				env[key] = value
			}
			if env[disableWorkspaceTrustEnvVar] == "" {
				env[disableWorkspaceTrustEnvVar] = "1"
			}
			if target.cli {
				return acpdriver.Launch{Command: target.path, Args: cliLaunchArgs(env, resolve.getenv), Env: env}, nil
			}
			if env[harnessPathEnvVar] == "" {
				if harness := resolve.harnessPath(target.path); harness != "" {
					env[harnessPathEnvVar] = harness
				}
			}
			return acpdriver.Launch{Command: target.path, Args: launchArgs(runtime.GOOS), Env: env}, nil
		},
		ValidateInitialize:  validateInitialize,
		AfterInitialize:     afterInitialize(newAuthBootstrap(agy.GeminiHome)),
		SessionMode:         sessionMode,
		SessionModeForAgent: sessionModeForAgent,
		SessionOptions:      sessionOptions,
		SessionPreamble:     sessionPreamble,
		PermissionPolicy:    permissionPolicy,
		SteerPrompt:         &acpdriver.SteerPrompt{Supported: cliSteers},
	}
}

func cliLaunchArgs(env map[string]string, getenv func(string) string) []string {
	args := []string{"acp", "--app-data-dir", agy.ACPStorageDir}
	home := strings.TrimSpace(env["GEMINI_HOME"])
	if home == "" {
		home = strings.TrimSpace(getenv("GEMINI_HOME"))
	}
	if home != "" {
		args = append(args, "--gemini-dir", home)
	}
	return args
}

func afterInitialize(bootstrap authBootstrap) func(context.Context, *acpsdk.ClientSideConnection, acpsdk.InitializeResponse, acpdriver.LaunchConfig) error {
	return func(ctx context.Context, conn *acpsdk.ClientSideConnection, init acpsdk.InitializeResponse, cfg acpdriver.LaunchConfig) error {
		if isCLIServer(init) {
			return nil
		}
		return bootstrap.run(ctx, conn, init, cfg)
	}
}

func sessionModeForAgent(init acpsdk.InitializeResponse) func(ports.PermissionMode) string {
	if !isCLIServer(init) {
		return nil
	}
	return func(permission ports.PermissionMode) string {
		switch ports.NormalizePermissionMode(permission) {
		case ports.PermissionModeDefault:
			return modeDefault
		case ports.PermissionModeAcceptEdits, ports.PermissionModeAuto, ports.PermissionModeBypassPermissions:
			return cliModeAcceptEdits
		default:
			return ""
		}
	}
}

func permissionPolicy(permission ports.PermissionMode, request acpsdk.RequestPermissionRequest) (acpsdk.PermissionOptionId, bool) {
	switch ports.NormalizePermissionMode(permission) {
	case ports.PermissionModeAuto, ports.PermissionModeBypassPermissions:
	default:
		return "", false
	}
	for _, option := range request.Options {
		if option.Kind == acpsdk.PermissionOptionKindAllowOnce {
			return option.OptionId, true
		}
	}
	return "", false
}

func launchArgs(goos string) []string {
	if goos == "linux" {
		return []string{"--uid="}
	}
	return nil
}

func sessionMode(permission ports.PermissionMode) string {
	switch ports.NormalizePermissionMode(permission) {
	case ports.PermissionModeDefault:
		return modeDefault
	case ports.PermissionModeAcceptEdits:
		return modeAutoEdit
	case ports.PermissionModeAuto, ports.PermissionModeBypassPermissions:
		return modeYolo
	default:
		return ""
	}
}

func sessionOptions(settings ports.ChatTurnSettings) []acpdriver.SessionOption {
	if model := strings.TrimSpace(settings.Model); model != "" {
		return []acpdriver.SessionOption{{ID: "model", Value: model}}
	}
	return nil
}

func sessionPreamble(cfg acpdriver.LaunchConfig) []acpsdk.ContentBlock {
	standing := strings.TrimSpace(cfg.SystemPrompt)
	if standing == "" {
		return nil
	}
	return []acpsdk.ContentBlock{acpsdk.TextBlock(
		"Standing instructions from Agent Orchestrator. Follow them for this whole session:\n" +
			"<ao_standing_instructions>\n" + standing + "\n</ao_standing_instructions>",
	)}
}
