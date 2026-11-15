package nativeacp

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"

	acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type Plugin interface {
	ResolveBinary(context.Context) (string, error)
	AuthStatus(context.Context) (ports.AgentAuthStatus, error)
}

type runtimeEnvAugmenter interface {
	AugmentRuntimeEnv(env map[string]string, dataDir string)
}

type Configure func(context.Context, acpdriver.LaunchConfig) (args []string, env map[string]string, err error)

type VersionProbe func(ctx context.Context, bin string) error

type Config struct {
	Harness                      domain.AgentHarness
	Capabilities                 ports.ChatCapabilities
	Configure                    Configure
	SessionMode                  func(ports.PermissionMode) string
	SessionOptions               func(ports.ChatTurnSettings) []acpdriver.SessionOption
	PromptFailureCode            func(error) domain.ConversationFailureCode
	LaunchSessionOptions         func(acpdriver.LaunchConfig) []acpdriver.SessionOption
	RequireAdvertisedOptions     bool
	CloseSessionBeforeReload     bool
	ValidateInitialize           func(acpsdk.InitializeResponse) error
	SessionMeta                  func(acpdriver.LaunchConfig) map[string]any
	SessionPreamble              func(acpdriver.LaunchConfig) []acpsdk.ContentBlock
	ResetsConversation           func([]acpsdk.ContentBlock) bool
	SuppressSessionModes         bool
	ImplicitWorkspaceDirectories bool
	PromptErrorFailure           func(error, bool) error
	HistoryReconciler            func([]ports.ChatEvent) []ports.ChatEvent
	PermissionPolicy             acpdriver.PermissionPolicy
	ClientExtension              acpdriver.ClientExtensionHandler
	ClientExtensionAliases       map[string]string
	SteerRequest                 *acpdriver.SteerRequest
	SteerDrain                   *acpdriver.SteerDrain
	SteerPrompt                  *acpdriver.SteerPrompt
	ValidateTurnSettings         acpdriver.TurnSettingsValidator
	EncodeProviderConversationID func(string) string
	DecodeProviderConversationID func(string) (string, error)
	VersionProbe                 VersionProbe
	Preflight                    func(context.Context) error
	ValidateEnvironment          func(context.Context, map[string]string) error
	QuickStop                    bool
}

func New(plugin Plugin, cfg Config, log *slog.Logger) ports.ChatDriver {
	return acpdriver.New(buildConfig(plugin, cfg, log), log)
}

func buildConfig(plugin Plugin, cfg Config, log *slog.Logger) acpdriver.Config {
	capabilities := ports.ChatCapabilities{
		ports.ChatCapabilityStreaming: true,
		ports.ChatCapabilityTools:     true,
		ports.ChatCapabilityApprovals: true,
		ports.ChatCapabilityInterrupt: true,
		ports.ChatCapabilityResume:    true,
	}
	for capability, enabled := range cfg.Capabilities {
		capabilities[capability] = enabled
	}

	return acpdriver.Config{
		Harness:             cfg.Harness,
		ValidateEnvironment: cfg.ValidateEnvironment,
		Capabilities:        capabilities,
		Probe: func(ctx context.Context) error {
			if cfg.Configure == nil {
				return fmt.Errorf("%w: incomplete native ACP binding for %s",
					ports.ErrChatDriverUnavailable, cfg.Harness)
			}
			if cfg.Preflight != nil {
				if err := cfg.Preflight(ctx); err != nil {
					return fmt.Errorf("%w: %w", ports.ErrChatDriverUnavailable, err)
				}
			}
			bin, err := plugin.ResolveBinary(ctx)
			if err != nil {
				return fmt.Errorf("%w: %w", ports.ErrChatDriverUnavailable, err)
			}
			if cfg.VersionProbe != nil {
				versionCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
				versionErr := cfg.VersionProbe(versionCtx, bin)
				cancel()
				if versionErr != nil {
					return fmt.Errorf("%w: %w", ports.ErrChatDriverIncompatible, versionErr)
				}
			}
			status, err := plugin.AuthStatus(ctx)
			if err == nil && status == ports.AgentAuthStatusUnauthorized {
				return ports.ErrChatAuthRequired
			}
			if err != nil && log != nil {
				log.Debug("native ACP auth probe inconclusive; continuing",
					"harness", cfg.Harness, "error", err)
			}
			return nil
		},
		Launch: func(ctx context.Context, launchCfg acpdriver.LaunchConfig) (acpdriver.Launch, error) {
			if cfg.Configure == nil {
				return acpdriver.Launch{}, fmt.Errorf("%w: incomplete native ACP binding for %s",
					ports.ErrChatDriverUnavailable, cfg.Harness)
			}
			binary, err := plugin.ResolveBinary(ctx)
			if err != nil {
				return acpdriver.Launch{}, fmt.Errorf("%w: %w", ports.ErrChatDriverUnavailable, err)
			}
			args, overrides, err := cfg.Configure(ctx, launchCfg)
			if err != nil {
				return acpdriver.Launch{}, err
			}
			env := make(map[string]string, len(launchCfg.Env)+len(overrides))
			for key, value := range launchCfg.Env {
				env[key] = value
			}
			if augmenter, ok := plugin.(runtimeEnvAugmenter); ok {
				augmenter.AugmentRuntimeEnv(env, launchCfg.DataDir)
			}
			for key, value := range overrides {
				env[key] = value
			}
			return acpdriver.Launch{
				Command: binary,
				Args:    append([]string(nil), args...),
				Env:     env,
			}, nil
		},
		SessionMode:                  cfg.SessionMode,
		SessionOptions:               cfg.SessionOptions,
		PromptFailureCode:            cfg.PromptFailureCode,
		LaunchSessionOptions:         cfg.LaunchSessionOptions,
		RequireAdvertisedOptions:     cfg.RequireAdvertisedOptions,
		CloseSessionBeforeReload:     cfg.CloseSessionBeforeReload,
		ValidateInitialize:           cfg.ValidateInitialize,
		SessionMeta:                  cfg.SessionMeta,
		SessionPreamble:              cfg.SessionPreamble,
		ResetsConversation:           cfg.ResetsConversation,
		SuppressSessionModes:         cfg.SuppressSessionModes,
		ImplicitWorkspaceDirectories: cfg.ImplicitWorkspaceDirectories,
		HistoryReconciler:            cfg.HistoryReconciler,
		PromptErrorFailure:           cfg.PromptErrorFailure,
		PermissionPolicy:             cfg.PermissionPolicy,
		ClientExtension:              cfg.ClientExtension,
		ClientExtensionAliases:       cfg.ClientExtensionAliases,
		SteerRequest:                 cfg.SteerRequest,
		SteerDrain:                   cfg.SteerDrain,
		SteerPrompt:                  cfg.SteerPrompt,
		ValidateTurnSettings:         cfg.ValidateTurnSettings,
		EncodeProviderConversationID: cfg.EncodeProviderConversationID,
		DecodeProviderConversationID: cfg.DecodeProviderConversationID,
		QuickStop:                    cfg.QuickStop,
	}
}
