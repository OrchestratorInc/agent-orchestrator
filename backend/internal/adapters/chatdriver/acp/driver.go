package acp

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/persistenthost"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/processenv"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const handshakeTimeout = 60 * time.Second

type Launch struct {
	Command string
	Args    []string
	Env     map[string]string
}

type LaunchConfig struct {
	ReconnectOnly          bool
	SessionID              domain.SessionID
	DataDir                string
	WorkspacePath          string
	Env                    map[string]string
	Model                  string
	Permissions            ports.PermissionMode
	SystemPrompt           string
	DisableTools           bool
	ReadOnly               bool
	ProviderScopeID        string
	ProviderConversationID string
}

type Config struct {
	Harness                      domain.AgentHarness
	Capabilities                 ports.ChatCapabilities
	Probe                        func(context.Context) error
	InstallCheck                 func(context.Context) error
	RetryStartupExit             bool
	Launch                       func(context.Context, LaunchConfig) (Launch, error)
	ValidateEnvironment          func(context.Context, map[string]string) error
	ValidateInitialize           func(acpsdk.InitializeResponse) error
	AfterInitialize              func(context.Context, *acpsdk.ClientSideConnection, acpsdk.InitializeResponse, LaunchConfig) error
	SessionMeta                  func(LaunchConfig) map[string]any
	SessionPreamble              func(LaunchConfig) []acpsdk.ContentBlock
	ResetsConversation           func([]acpsdk.ContentBlock) bool
	SessionMode                  func(ports.PermissionMode) string
	SessionModeForAgent          func(acpsdk.InitializeResponse) func(ports.PermissionMode) string
	SuppressSessionModes         bool
	ImplicitWorkspaceDirectories bool
	SessionOptions               func(ports.ChatTurnSettings) []SessionOption
	LaunchSessionOptions         func(LaunchConfig) []SessionOption
	RequireAdvertisedOptions     bool
	CloseSessionBeforeReload     bool
	PermissionPolicy             PermissionPolicy
	ReadOnly                     func(LaunchConfig) ReadOnlySession
	ClientExtension              ClientExtensionHandler
	ClientExtensionAliases       map[string]string
	SteerRequest                 *SteerRequest
	SteerDrain                   *SteerDrain
	SteerPrompt                  *SteerPrompt
	ValidateTurnSettings         TurnSettingsValidator
	PromptResponseFailure        func(response acpsdk.PromptResponse, model string) error
	ServedModel                  func(ctx context.Context, cfg LaunchConfig, sessionID, selectedModel string) (served, reason string)
	ValidateModel                func(ctx context.Context, cfg LaunchConfig, model string) error
	PromptFailureCode            func(error) domain.ConversationFailureCode
	SessionPromptResponseFailure func(sessionID string, response acpsdk.PromptResponse, answered bool) error
	PromptErrorFailure           func(err error, answered bool) error
	HistoryNotice                func(string) bool
	HistoryUserChunkIsMessage    bool
	HistoryReconciler            func([]ports.ChatEvent) []ports.ChatEvent
	PlansSpanTurns               bool
	QuickStop                    bool
	CanHibernate                 func(context.Context, *acpsdk.ClientSideConnection, acpsdk.SessionId) (bool, error)
	EncodeProviderConversationID func(string) string
	DecodeProviderConversationID func(string) (string, error)
	OnAuthRejected               func()
}

type TurnSettingsValidator func(ports.PermissionMode, ports.ChatTurnSettings) error

type PermissionPolicy func(
	ports.PermissionMode,
	acpsdk.RequestPermissionRequest,
) (acpsdk.PermissionOptionId, bool)

const CancelPermission acpsdk.PermissionOptionId = ""

type ReadOnlySession struct {
	Mode             string
	PermissionPolicy PermissionPolicy
}

type SessionOption struct {
	ID       string
	Value    string
	Optional bool
	Name     string
}

type Driver struct {
	cfg         Config
	log         *slog.Logger
	spawn       spawnFunc
	openProcess func(context.Context, LaunchConfig, func(context.Context) (map[string]string, error)) (*process, error)
	connectHost func(context.Context, persistenthost.Config) (*persistenthost.Transport, error)
}

var _ ports.ChatDriver = (*Driver)(nil)
var _ ports.ChatEnvironmentValidator = (*Driver)(nil)

func New(cfg Config, log *slog.Logger) *Driver {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	driver := &Driver{cfg: cfg, log: log, spawn: spawnAgent, connectHost: persistenthost.ConnectOrStart}
	driver.openProcess = driver.connectProcess
	return driver
}

func DiscoverConfigOptions(
	ctx context.Context,
	launch Launch,
	workingDir string,
	log *slog.Logger,
) ([]ports.ChatConfigOption, error) {
	return DiscoverConfigOptionsForHarness(ctx, launch, workingDir, "", log)
}

func DiscoverConfigOptionsForHarness(
	ctx context.Context,
	launch Launch,
	workingDir string,
	harness domain.AgentHarness,
	log *slog.Logger,
) ([]ports.ChatConfigOption, error) {
	driver := New(Config{
		Harness: harness,
		Launch:  func(context.Context, LaunchConfig) (Launch, error) { return launch, nil },
	}, log)
	return driver.discoverConfigOptions(ctx, workingDir)
}

func (d *Driver) discoverConfigOptions(ctx context.Context, workingDir string) ([]ports.ChatConfigOption, error) {
	if !filepath.IsAbs(workingDir) {
		return nil, fmt.Errorf("workspace path must be absolute, got %q", workingDir)
	}
	cfg := LaunchConfig{WorkspacePath: workingDir}
	launch, err := d.cfg.Launch(ctx, cfg)
	if err != nil {
		return nil, err
	}
	proc, err := d.spawn(launch, workingDir)
	if err != nil {
		return nil, err
	}
	conv, _, _, err := d.initialize(ctx, cfg, proc)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conv.Close() }()

	openCtx, cancel := context.WithTimeout(ctx, handshakeTimeout)
	defer cancel()
	resp, err := conv.conn.NewSession(openCtx, acpsdk.NewSessionRequest{
		Cwd:        workingDir,
		McpServers: []acpsdk.McpServer{},
	})
	if err != nil {
		return nil, normalizeACPError("ACP session/new", err)
	}
	if d.cfg.Harness != domain.HarnessHermes {
		return normalizeConfigOptions(resp.ConfigOptions), nil
	}
	return normalizeSessionOptions(d.suppressedModeOptions(resp.ConfigOptions), conv.legacyWire.modelState(), d.sessionModes(resp.Modes)), nil
}

func (d *Driver) sessionModes(modes *acpsdk.SessionModeState) *acpsdk.SessionModeState {
	if d.cfg.SuppressSessionModes {
		return nil
	}
	return modes
}

func (d *Driver) suppressedModeOptions(options []acpsdk.SessionConfigOption) []acpsdk.SessionConfigOption {
	if !d.cfg.SuppressSessionModes {
		return options
	}
	return withoutModeOptions(options)
}

func withoutModeOptions(options []acpsdk.SessionConfigOption) []acpsdk.SessionConfigOption {
	out := make([]acpsdk.SessionConfigOption, 0, len(options))
	for _, option := range options {
		if option.Select != nil && option.Select.Category != nil &&
			*option.Select.Category == acpsdk.SessionConfigOptionCategoryMode {
			continue
		}
		if option.Boolean != nil && option.Boolean.Category != nil &&
			*option.Boolean.Category == acpsdk.SessionConfigOptionCategoryMode {
			continue
		}
		out = append(out, option)
	}
	return out
}

func (d *Driver) Harness() domain.AgentHarness { return d.cfg.Harness }

func (d *Driver) sessionPolicy(
	cfg LaunchConfig,
	init acpsdk.InitializeResponse,
) (func(ports.PermissionMode) string, PermissionPolicy) {
	if !cfg.ReadOnly || d.cfg.ReadOnly == nil {
		return d.sessionMode(init), d.cfg.PermissionPolicy
	}
	session := d.cfg.ReadOnly(cfg)
	return func(ports.PermissionMode) string { return session.Mode }, session.PermissionPolicy
}

func (d *Driver) checkReadOnly(cfg LaunchConfig) error {
	if cfg.ReadOnly && !cfg.DisableTools && d.cfg.ReadOnly == nil {
		return fmt.Errorf("%w: %s chat cannot run read-only", ports.ErrChatCapabilityUnavailable, d.cfg.Harness)
	}
	return nil
}

func (d *Driver) Probe(ctx context.Context) (ports.ChatCapabilities, error) {
	if d.cfg.Probe == nil || d.cfg.Launch == nil {
		return nil, fmt.Errorf("%w: incomplete ACP binding", ports.ErrChatDriverUnavailable)
	}
	if err := d.cfg.Probe(ctx); err != nil {
		return nil, err
	}
	return cloneCapabilities(d.cfg.Capabilities), nil
}

func (d *Driver) ValidateEnvironment(ctx context.Context, env map[string]string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if d.cfg.ValidateEnvironment != nil {
		return d.cfg.ValidateEnvironment(ctx, env)
	}
	return nil
}

func (d *Driver) CheckChatInstall(ctx context.Context) error {
	if d.cfg.InstallCheck == nil {
		return nil
	}
	return d.cfg.InstallCheck(ctx)
}

func (d *Driver) SupportsPermissionMode(mode ports.PermissionMode) bool {
	if d.cfg.ValidateTurnSettings == nil {
		return true
	}
	err := d.cfg.ValidateTurnSettings(mode, ports.ChatTurnSettings{Approval: mode})
	return !errors.Is(err, ports.ErrChatPermissionModeUnsupported)
}

func (d *Driver) Start(ctx context.Context, cfg ports.ChatStartConfig) (ports.ChatConversation, error) {
	totalStarted := time.Now()
	if !filepath.IsAbs(cfg.WorkspacePath) {
		return nil, fmt.Errorf("workspace path must be absolute, got %q", cfg.WorkspacePath)
	}
	if d.cfg.ValidateTurnSettings != nil {
		if err := d.cfg.ValidateTurnSettings(cfg.Permissions, ports.ChatTurnSettings{
			Model: cfg.Model, Effort: cfg.Effort, Approval: cfg.Permissions,
		}); err != nil {
			return nil, fmt.Errorf("validate ACP session settings: %w", err)
		}
	}
	launchCfg := LaunchConfig{
		SessionID: cfg.SessionID, DataDir: cfg.DataDir, WorkspacePath: cfg.WorkspacePath,
		Env:   cfg.Env,
		Model: cfg.Model, Permissions: cfg.Permissions, SystemPrompt: cfg.SystemPrompt,
		DisableTools:    cfg.DisableTools,
		ReadOnly:        cfg.ReadOnly,
		ProviderScopeID: cfg.ProviderScopeID,
	}
	if err := d.checkReadOnly(launchCfg); err != nil {
		return nil, err
	}
	connectStarted := time.Now()
	conv, init, live, err := d.connect(ctx, launchCfg, cfg.PrepareEnv)
	d.logStartStage(cfg.SessionID, "connect", connectStarted, err)
	if err != nil {
		return nil, err
	}
	if live != nil {
		conv.discard()
		return nil, fmt.Errorf("%w: fresh ACP start found an existing initialized session", ports.ErrChatRecoveryInconclusive)
	}
	additional, err := normalizeAdditionalDirectories(cfg.WorkspacePath, cfg.AdditionalDirectories,
		init.AgentCapabilities.SessionCapabilities.AdditionalDirectories != nil,
		d.cfg.ImplicitWorkspaceDirectories)
	if err != nil {
		conv.discard()
		return nil, err
	}
	mcpServers, err := normalizeMCPServers(cfg.MCPServers, init.AgentCapabilities.McpCapabilities)
	if err != nil {
		conv.discard()
		return nil, err
	}

	settings := ports.ChatTurnSettings{Model: cfg.Model, Effort: cfg.Effort, Approval: cfg.Permissions}
	if err := d.openNewSession(ctx, conv, init, launchCfg, settings, additional, mcpServers); err != nil {
		conv.discard()
		return nil, err
	}
	d.logStartStage(cfg.SessionID, "total", totalStarted, nil)
	return conv, nil
}

func (d *Driver) openNewSession(
	ctx context.Context,
	conv *conversation,
	init acpsdk.InitializeResponse,
	launchCfg LaunchConfig,
	settings ports.ChatTurnSettings,
	additional []string,
	mcpServers []acpsdk.McpServer,
) error {
	meta := map[string]any(nil)
	if d.cfg.SessionMeta != nil {
		meta = d.cfg.SessionMeta(launchCfg)
	}
	openCtx, cancel := context.WithTimeout(ctx, handshakeTimeout)
	defer cancel()
	sessionStarted := time.Now()
	resp, err := conv.conn.NewSession(openCtx, acpsdk.NewSessionRequest{
		Meta:                  meta,
		Cwd:                   launchCfg.WorkspacePath,
		AdditionalDirectories: additional,
		McpServers:            mcpServers,
	})
	d.logStartStage(launchCfg.SessionID, "session_new", sessionStarted, err)
	if err != nil {
		return normalizeACPError("ACP session/new", err)
	}
	if resp.SessionId == "" {
		return errors.New("ACP session/new returned no session id")
	}
	modeFor, permissionFor := d.sessionPolicy(launchCfg, init)
	conv.start(
		string(resp.SessionId), conversationCapabilities(d.cfg.Capabilities, init, steeringFor(d.cfg)),
		modeFor, d.cfg.SessionOptions, permissionFor,
		settings.Approval, d.cfg.ValidateTurnSettings, d.suppressedModeOptions(resp.ConfigOptions),
		conv.legacyWire.modelState(), d.sessionModes(resp.Modes),
	)
	if d.cfg.EncodeProviderConversationID != nil {
		conv.setReportedProviderConversationID(d.cfg.EncodeProviderConversationID(string(resp.SessionId)))
	}
	conv.setSessionPreamble(d.sessionPreamble(launchCfg))
	if err := d.applyLaunchSessionOptions(ctx, conv, launchCfg); err != nil {
		return fmt.Errorf("configure ACP session: %w", err)
	}
	settingsStarted := time.Now()
	err = conv.applyTurnSettings(ctx, settings)
	d.logStartStage(launchCfg.SessionID, "initial_settings", settingsStarted, err)
	if err != nil {
		if !errors.Is(err, ErrACPSetterUnsupported) {
			return fmt.Errorf("configure ACP session: %w", err)
		}
	}
	return nil
}

func (d *Driver) applyLaunchSessionOptions(ctx context.Context, conv *conversation, cfg LaunchConfig) error {
	if d.cfg.LaunchSessionOptions == nil {
		return nil
	}
	return conv.applySessionOptions(ctx, d.cfg.LaunchSessionOptions(cfg))
}

func (d *Driver) sessionPreamble(cfg LaunchConfig) []acpsdk.ContentBlock {
	if d.cfg.SessionPreamble == nil {
		return nil
	}
	return d.cfg.SessionPreamble(cfg)
}

func (d *Driver) logStartStage(sessionID domain.SessionID, stage string, started time.Time, err error) {
	outcome := "ok"
	if err != nil {
		outcome = "error"
	}
	d.log.Info("chat: ACP start stage",
		"sessionID", sessionID,
		"harness", d.cfg.Harness,
		"stage", stage,
		"duration", time.Since(started),
		"outcome", outcome,
	)
}

func (d *Driver) Reconnect(ctx context.Context, cfg ports.ChatResumeConfig) (ports.ChatConversation, error) {
	cfg.ReconnectOnly = true
	return d.Resume(ctx, cfg)
}

func (d *Driver) Resume(ctx context.Context, cfg ports.ChatResumeConfig) (ports.ChatConversation, error) {
	if cfg.ProviderConversationID == "" {
		return nil, fmt.Errorf("%w: no stored ACP session id", ports.ErrChatResumeFailed)
	}
	reportedProviderConversationID := cfg.ProviderConversationID
	if d.cfg.DecodeProviderConversationID != nil {
		providerConversationID, err := d.cfg.DecodeProviderConversationID(cfg.ProviderConversationID)
		if err != nil {
			return nil, err
		}
		cfg.ProviderConversationID = providerConversationID
	}
	if !filepath.IsAbs(cfg.WorkspacePath) {
		return nil, fmt.Errorf("workspace path must be absolute, got %q", cfg.WorkspacePath)
	}
	if d.cfg.ValidateTurnSettings != nil {
		if err := d.cfg.ValidateTurnSettings(cfg.Permissions, ports.ChatTurnSettings{
			Model: cfg.Model, Effort: cfg.Effort, Approval: cfg.Permissions,
		}); err != nil {
			return nil, fmt.Errorf("%w: validate ACP session settings: %w", ports.ErrChatResumeFailed, err)
		}
	}
	launchCfg := LaunchConfig{
		SessionID: cfg.SessionID, DataDir: cfg.DataDir, WorkspacePath: cfg.WorkspacePath,
		Env:   cfg.Env,
		Model: cfg.Model, Permissions: cfg.Permissions, SystemPrompt: cfg.SystemPrompt,
		ReadOnly:               cfg.ReadOnly,
		ProviderScopeID:        cfg.ProviderScopeID,
		ReconnectOnly:          cfg.ReconnectOnly,
		ProviderConversationID: cfg.ProviderConversationID,
	}
	if err := d.checkReadOnly(launchCfg); err != nil {
		return nil, fmt.Errorf("%w: %w", ports.ErrChatResumeFailed, err)
	}
	conv, init, live, err := d.connect(ctx, launchCfg, cfg.PrepareEnv)
	if err != nil {
		return nil, err
	}
	if live != nil {
		if live.SessionID != cfg.ProviderConversationID {
			conv.discard()
			return nil, fmt.Errorf("%w: persistent ACP session %q does not match %q",
				ports.ErrChatRecoveryInconclusive, live.SessionID, cfg.ProviderConversationID)
		}
		var setup struct {
			ConfigOptions []acpsdk.SessionConfigOption `json:"configOptions,omitempty"`
			Modes         *acpsdk.SessionModeState     `json:"modes,omitempty"`
			Models        *legacySessionModelState     `json:"models,omitempty"`
		}
		if len(live.SessionResult) == 0 {
			conv.discard()
			return nil, fmt.Errorf("%w: persistent ACP session has no setup response", ports.ErrChatRecoveryInconclusive)
		}
		if decodeErr := json.Unmarshal(live.SessionResult, &setup); decodeErr != nil {
			conv.discard()
			return nil, fmt.Errorf("%w: decode persistent ACP session: %w", ports.ErrChatRecoveryInconclusive, decodeErr)
		}
		if d.cfg.ValidateTurnSettings != nil {
			if err := d.cfg.ValidateTurnSettings(ports.PermissionMode(live.InitialPermissions), ports.ChatTurnSettings{
				Model: cfg.Model, Effort: cfg.Effort, Approval: cfg.Permissions,
			}); err != nil {
				conv.discard()
				return nil, fmt.Errorf("%w: live ACP launch settings: %w", ports.ErrChatRecoveryInconclusive, err)
			}
		}
		conv.liveState = live
		conv.hasDurableTurns = cfg.HasPriorTurns
		if len(live.AvailableCommands) > 0 {
			var commands []acpsdk.AvailableCommand
			if json.Unmarshal(live.AvailableCommands, &commands) == nil {
				conv.replaceAvailableCommands(commands)
			}
		}
		modeFor, permissionFor := d.sessionPolicy(launchCfg, init)
		conv.start(
			cfg.ProviderConversationID, conversationCapabilities(d.cfg.Capabilities, init, steeringFor(d.cfg)),
			modeFor, d.cfg.SessionOptions, permissionFor,
			cfg.Permissions, d.cfg.ValidateTurnSettings, d.suppressedModeOptions(setup.ConfigOptions),
			setup.Models, d.sessionModes(setup.Modes),
		)
		conv.setReportedProviderConversationID(reportedProviderConversationID)
		conv.initialPermission = ports.PermissionMode(live.InitialPermissions)
		conv.setSessionPreamble(d.sessionPreamble(launchCfg))
		return conv, nil
	}
	return d.resumeSession(ctx, conv, init, launchCfg, cfg, reportedProviderConversationID)
}

func (d *Driver) resumeSession(
	ctx context.Context,
	conv *conversation,
	init acpsdk.InitializeResponse,
	launchCfg LaunchConfig,
	cfg ports.ChatResumeConfig,
	reportedProviderConversationID string,
) (ports.ChatConversation, error) {
	if !supportsSessionRestore(init) {
		conv.discard()
		return nil, fmt.Errorf("%w: ACP agent supports neither session/load nor session/resume", ports.ErrChatResumeFailed)
	}
	additional, err := normalizeAdditionalDirectories(cfg.WorkspacePath, cfg.AdditionalDirectories,
		init.AgentCapabilities.SessionCapabilities.AdditionalDirectories != nil,
		d.cfg.ImplicitWorkspaceDirectories)
	if err != nil {
		conv.discard()
		if isACPAuthRequired(err) {
			return nil, normalizeACPError("ACP session/resume", err)
		}
		return nil, fmt.Errorf("%w: %w", ports.ErrChatResumeFailed, err)
	}
	mcpServers, err := normalizeMCPServers(cfg.MCPServers, init.AgentCapabilities.McpCapabilities)
	if err != nil {
		conv.discard()
		return nil, fmt.Errorf("%w: %w", ports.ErrChatResumeFailed, err)
	}

	resumeCtx, cancel := context.WithTimeout(ctx, handshakeTimeout)
	defer cancel()
	meta := map[string]any(nil)
	if d.cfg.SessionMeta != nil {
		meta = d.cfg.SessionMeta(launchCfg)
	}
	var configOptions []acpsdk.SessionConfigOption
	var modes *acpsdk.SessionModeState
	var historyConversation *refreshableConversation
	if init.AgentCapabilities.LoadSession {
		historyConversation = newRefreshableConversation(conv, acpsdk.LoadSessionRequest{
			Meta:                  meta,
			SessionId:             acpsdk.SessionId(cfg.ProviderConversationID),
			Cwd:                   cfg.WorkspacePath,
			AdditionalDirectories: additional,
			McpServers:            mcpServers,
		})
		historyConversation.closeBeforeReload = d.cfg.CloseSessionBeforeReload &&
			init.AgentCapabilities.SessionCapabilities.Close != nil
		loadStarted := time.Now()
		resp, err := historyConversation.loadHistory(resumeCtx)
		d.logStartStage(launchCfg.SessionID, "session_load", loadStarted, err)
		if err != nil {
			if cfg.FreshIfMissing && !cfg.ReconnectOnly && isACPResourceNotFound(err) {
				return d.resumeFresh(ctx, conv, init, launchCfg, cfg, additional, mcpServers)
			}
			conv.discard()
			return nil, fmt.Errorf("%w: %w", ports.ErrChatResumeFailed, normalizeACPLoadError("ACP session/load", err))
		}
		configOptions = resp.ConfigOptions
		modes = resp.Modes
	} else {
		resumeStarted := time.Now()
		resp, err := conv.conn.ResumeSession(resumeCtx, acpsdk.ResumeSessionRequest{
			Meta:                  meta,
			SessionId:             acpsdk.SessionId(cfg.ProviderConversationID),
			Cwd:                   cfg.WorkspacePath,
			AdditionalDirectories: additional,
			McpServers:            mcpServers,
		})
		d.logStartStage(launchCfg.SessionID, "session_resume", resumeStarted, err)
		if err != nil {
			if cfg.FreshIfMissing && !cfg.ReconnectOnly && isACPResourceNotFound(err) {
				return d.resumeFresh(ctx, conv, init, launchCfg, cfg, additional, mcpServers)
			}
			conv.discard()
			return nil, fmt.Errorf("%w: %w", ports.ErrChatResumeFailed, err)
		}
		configOptions = resp.ConfigOptions
		modes = resp.Modes
	}
	conv.hasDurableTurns = cfg.HasPriorTurns
	modeFor, permissionFor := d.sessionPolicy(launchCfg, init)
	conv.start(
		cfg.ProviderConversationID, conversationCapabilities(d.cfg.Capabilities, init, steeringFor(d.cfg)),
		modeFor, d.cfg.SessionOptions, permissionFor,
		cfg.Permissions, d.cfg.ValidateTurnSettings, d.suppressedModeOptions(configOptions),
		conv.legacyWire.modelState(), d.sessionModes(modes),
	)
	conv.setReportedProviderConversationID(reportedProviderConversationID)
	conv.setSessionPreamble(d.sessionPreamble(launchCfg))
	if err := d.applyLaunchSessionOptions(ctx, conv, launchCfg); err != nil {
		conv.discard()
		return nil, fmt.Errorf("%w: configure ACP session: %w", ports.ErrChatResumeFailed, err)
	}
	if err := conv.applyTurnSettings(ctx, ports.ChatTurnSettings{Model: cfg.Model, Effort: cfg.Effort, Approval: cfg.Permissions}); err != nil {
		if !errors.Is(err, ErrACPSetterUnsupported) {
			conv.discard()
			return nil, fmt.Errorf("%w: configure ACP session: %w", ports.ErrChatResumeFailed, err)
		}
	}
	if historyConversation != nil {
		return historyConversation, nil
	}
	return conv, nil
}

func (d *Driver) resumeFresh(
	ctx context.Context,
	conv *conversation,
	init acpsdk.InitializeResponse,
	launchCfg LaunchConfig,
	cfg ports.ChatResumeConfig,
	additional []string,
	mcpServers []acpsdk.McpServer,
) (ports.ChatConversation, error) {
	settings := ports.ChatTurnSettings{Model: cfg.Model, Effort: cfg.Effort, Approval: cfg.Permissions}
	if err := d.openNewSession(ctx, conv, init, launchCfg, settings, additional, mcpServers); err != nil {
		conv.discard()
		return nil, fmt.Errorf("%w: start fresh in place of missing ACP session: %w", ports.ErrChatResumeFailed, err)
	}
	d.log.Info("chat: ACP session missing; started fresh",
		"sessionID", cfg.SessionID, "harness", d.cfg.Harness)
	return conv, nil
}

func (d *Driver) connect(
	ctx context.Context,
	cfg LaunchConfig,
	prepareEnv func(context.Context) (map[string]string, error),
) (*conversation, acpsdk.InitializeResponse, *persistenthost.ACPState, error) {
	if cfg.ReconnectOnly && (cfg.DataDir == "" || cfg.SessionID == "") {
		return nil, acpsdk.InitializeResponse{}, nil, ports.ErrChatHostNotRunning
	}
	processStarted := time.Now()
	proc, err := d.openProcess(ctx, cfg, prepareEnv)
	d.logStartStage(cfg.SessionID, "process_open", processStarted, err)
	if err != nil {
		return nil, acpsdk.InitializeResponse{}, nil, err
	}
	initializeStarted := time.Now()
	conv, init, state, err := d.initialize(ctx, cfg, proc)
	d.logStartStage(cfg.SessionID, "initialize", initializeStarted, err)
	reopen := func(stage string) (*conversation, acpsdk.InitializeResponse, *persistenthost.ACPState, error) {
		proc, err := d.openProcess(ctx, cfg, prepareEnv)
		if err != nil {
			return nil, acpsdk.InitializeResponse{}, nil, err
		}
		started := time.Now()
		conv, init, state, err := d.initialize(ctx, cfg, proc)
		d.logStartStage(cfg.SessionID, stage, started, err)
		return conv, init, state, err
	}
	if errors.Is(err, errHostNeverStarted) {
		d.log.Info("chat: ACP host never finished starting; replacing it", "sessionID", cfg.SessionID, "harness", d.cfg.Harness)
		if stopErr := proc.terminate(); stopErr != nil {
			return nil, acpsdk.InitializeResponse{}, nil, fmt.Errorf(
				"%w: stop ACP host that never finished starting: %w", ports.ErrChatRecoveryInconclusive, stopErr)
		}
		if cfg.ReconnectOnly {
			return nil, acpsdk.InitializeResponse{}, nil, ports.ErrChatHostNotRunning
		}
		return reopen("initialize_replaced")
	}
	if err == nil || !d.cfg.RetryStartupExit || proc.reconnected || !isACPPeerDisconnected(err) {
		return conv, init, state, err
	}
	d.log.Info("chat: ACP agent exited during its first handshake; starting it once more",
		"sessionID", cfg.SessionID, "harness", d.cfg.Harness, "error", err)
	select {
	case <-time.After(startupRetryDelay):
	case <-ctx.Done():
		return nil, acpsdk.InitializeResponse{}, nil, errors.Join(ctx.Err(), err)
	}
	return reopen("initialize_retry")
}

var errHostNeverStarted = errors.New("persistent ACP host never finished starting")

const startupRetryDelay = 500 * time.Millisecond

func isACPPeerDisconnected(err error) bool {
	var requestErr *acpsdk.RequestError
	if !errors.As(err, &requestErr) {
		return false
	}
	data, _ := requestErr.Data.(map[string]any)
	reason, _ := data["error"].(string)
	for _, gone := range []string{"peer disconnected", "io: read/write on closed pipe", "broken pipe", "file already closed"} {
		if strings.Contains(reason, gone) {
			return true
		}
	}
	return false
}

func (d *Driver) initialize(
	ctx context.Context,
	cfg LaunchConfig,
	proc *process,
) (*conversation, acpsdk.InitializeResponse, *persistenthost.ACPState, error) {
	conv := newConversation(
		proc, d.log, cfg.ProviderScopeID, d.cfg.ClientExtension, d.clientExtensionAliases(),
	)
	conv.harness = d.cfg.Harness
	conv.suppressSessionModes = d.cfg.SuppressSessionModes
	conv.resetsConversation = d.cfg.ResetsConversation
	conv.steering = steeringFor(d.cfg)
	conv.onAuthRejected = d.cfg.OnAuthRejected
	conv.promptResponseFailure = d.cfg.PromptResponseFailure
	conv.promptFailureCode = d.cfg.PromptFailureCode
	conv.sessionPromptResponseFailure = d.cfg.SessionPromptResponseFailure
	conv.promptErrorFailure = d.cfg.PromptErrorFailure
	conv.hibernationCheck = d.cfg.CanHibernate
	conv.decodeConversationID = d.cfg.DecodeProviderConversationID
	conv.historyNotice = d.cfg.HistoryNotice
	conv.historyUserChunkIsMessage = d.cfg.HistoryUserChunkIsMessage
	conv.historyReconciler = d.cfg.HistoryReconciler
	conv.plansSpanTurns = d.cfg.PlansSpanTurns
	conv.requireAdvertised = d.cfg.RequireAdvertisedOptions
	if d.cfg.ServedModel != nil {
		servedModel := d.cfg.ServedModel
		conv.servedModel = func(ctx context.Context, sessionID, selectedModel string) (string, string) {
			return servedModel(ctx, cfg, sessionID, selectedModel)
		}
	}
	if d.cfg.ValidateModel != nil {
		validateModel := d.cfg.ValidateModel
		conv.validateModel = func(ctx context.Context, model string) error {
			return validateModel(ctx, cfg, model)
		}
	}
	if proc.reconnected {
		state := proc.acpState
		if state == nil || state.SessionID == "" {
			conv.discard()
			return nil, acpsdk.InitializeResponse{}, nil, errHostNeverStarted
		}
		if len(state.InitializeResult) == 0 || len(state.SessionResult) == 0 {
			conv.discard()
			return nil, acpsdk.InitializeResponse{}, nil, fmt.Errorf(
				"%w: persistent ACP host has incomplete reconnect state", ports.ErrChatRecoveryInconclusive)
		}
		var init acpsdk.InitializeResponse
		if err := json.Unmarshal(state.InitializeResult, &init); err != nil {
			conv.discard()
			return nil, acpsdk.InitializeResponse{}, nil, fmt.Errorf(
				"%w: decode persistent ACP initialize: %w", ports.ErrChatRecoveryInconclusive, err)
		}
		if d.cfg.ValidateInitialize != nil {
			if err := d.cfg.ValidateInitialize(init); err != nil {
				conv.discard()
				return nil, acpsdk.InitializeResponse{}, nil, fmt.Errorf("%w: %w", ports.ErrChatDriverIncompatible, err)
			}
		}
		conv.canDeleteFork = init.AgentCapabilities.SessionCapabilities.Delete != nil
		conv.handshake = init
		return conv, init, state, nil
	}

	initCtx, cancel := context.WithTimeout(ctx, handshakeTimeout)
	defer cancel()
	init, err := conv.conn.Initialize(initCtx, acpsdk.InitializeRequest{
		Meta: map[string]any{
			persistenthost.ACPInitialPermissionsMetaKey: string(ports.NormalizePermissionMode(cfg.Permissions)),
		},
		ProtocolVersion: acpsdk.ProtocolVersionNumber,
		ClientInfo: &acpsdk.Implementation{
			Name:    "agent-orchestrator",
			Title:   pointer("Agent Orchestrator"),
			Version: "0.1.0",
		},
		ClientCapabilities: acpsdk.ClientCapabilities{
			Meta: map[string]any{
				"parameterizedModelPicker": true,
				"subagent-transcript":      true,
				"terminal_output":          true,
				"jetbrains": map[string]any{
					"air": map[string]any{
						"version":      1,
						"capabilities": []string{"sessionFailure"},
					},
				},
			},
			Elicitation: &acpsdk.ElicitationCapabilities{
				Form: &acpsdk.ElicitationFormCapabilities{},
				Url:  &acpsdk.ElicitationUrlCapabilities{},
			},
			PlanCapabilities: &acpsdk.PlanCapabilities{},
		},
	})
	if err != nil {
		conv.discard()
		if isACPAuthRequired(err) {
			return nil, acpsdk.InitializeResponse{}, nil, normalizeACPError("ACP initialize", err)
		}
		return nil, acpsdk.InitializeResponse{}, nil, fmt.Errorf("%w: ACP initialize: %w", ports.ErrChatDriverIncompatible, err)
	}
	if d.cfg.ValidateInitialize != nil {
		if err := d.cfg.ValidateInitialize(init); err != nil {
			conv.discard()
			return nil, acpsdk.InitializeResponse{}, nil, fmt.Errorf("%w: %w", ports.ErrChatDriverIncompatible, err)
		}
	}
	conv.canDeleteFork = init.AgentCapabilities.SessionCapabilities.Delete != nil
	conv.handshake = init
	if d.cfg.AfterInitialize != nil {
		if err := d.cfg.AfterInitialize(ctx, conv.conn, init, cfg); err != nil {
			conv.discard()
			return nil, acpsdk.InitializeResponse{}, nil, normalizeACPError("ACP after initialize", err)
		}
	}
	return conv, init, nil, nil
}

func (d *Driver) connectProcess(
	ctx context.Context,
	cfg LaunchConfig,
	prepareEnv func(context.Context) (map[string]string, error),
) (*process, error) {
	owner, _ := json.Marshal([]string{filepath.Clean(cfg.WorkspacePath), string(d.cfg.Harness), cfg.ProviderScopeID})
	identity := fmt.Sprintf("%x", sha256.Sum256(owner))
	hostConfig := persistenthost.Config{
		SessionID: string(cfg.SessionID), DataDir: cfg.DataDir, Workdir: cfg.WorkspacePath,
		Protocol: persistenthost.ProtocolACP, OwnershipFingerprint: identity,
		ReconnectOnly: cfg.ReconnectOnly,
		Prepare: func(prepareCtx context.Context) (persistenthost.PreparedProvider, error) {
			if prepareEnv != nil {
				env, err := prepareEnv(prepareCtx)
				if err != nil {
					return persistenthost.PreparedProvider{}, err
				}
				cfg.Env = env
			}
			launch, err := d.cfg.Launch(prepareCtx, cfg)
			if err != nil {
				return persistenthost.PreparedProvider{}, err
			}
			if launch.Command == "" {
				return persistenthost.PreparedProvider{}, fmt.Errorf("%w: ACP launch command is empty", ports.ErrChatDriverUnavailable)
			}
			return persistenthost.PreparedProvider{
				Env:  processenv.Merge(launch.Env),
				Argv: append([]string{launch.Command}, launch.Args...),
			}, nil
		},
	}
	transport, err := d.connectHost(ctx, hostConfig)
	if err != nil {
		if errors.Is(err, persistenthost.ErrNotRunning) {
			return nil, ports.ErrChatHostNotRunning
		}
		if errors.Is(err, persistenthost.ErrOwnershipInconclusive) ||
			errors.Is(err, persistenthost.ErrAttached) ||
			errors.Is(err, persistenthost.ErrIncompatible) ||
			errors.Is(err, persistenthost.ErrUnauthorized) {
			return nil, fmt.Errorf("%w: persistent ACP host: %w", ports.ErrChatRecoveryInconclusive, err)
		}
		return nil, fmt.Errorf("%w: persistent ACP host: %w", ports.ErrChatDriverUnavailable, err)
	}
	gate := newGatedReader(transport.Stdout, !transport.Reconnected)
	proc := &process{
		stdin: transport.Stdin, stdout: gate, gate: gate,
		reconnected: transport.Reconnected, acpState: transport.ACPState,
	}
	proc.stop = persistentStop(gate, transport.Stdin)
	if d.cfg.QuickStop {
		proc.quickStop.Store(true)
	}
	proc.terminate = persistentTerminate(gate, transport.Stdin, &proc.quickStop, func(shutdownCtx context.Context, quick bool) error {
		if quick {
			return persistenthost.ShutdownQuick(shutdownCtx, cfg.DataDir, string(cfg.SessionID))
		}
		return persistenthost.Shutdown(shutdownCtx, cfg.DataDir, string(cfg.SessionID))
	})
	return proc, nil
}

func cloneCapabilities(in ports.ChatCapabilities) ports.ChatCapabilities {
	out := make(ports.ChatCapabilities, len(in))
	for capability, enabled := range in {
		out[capability] = enabled
	}
	return out
}

func (d *Driver) sessionMode(init acpsdk.InitializeResponse) func(ports.PermissionMode) string {
	if d.cfg.SessionModeForAgent != nil {
		if modeFor := d.cfg.SessionModeForAgent(init); modeFor != nil {
			return modeFor
		}
	}
	return d.cfg.SessionMode
}

func (d *Driver) clientExtensionAliases() map[string]string {
	drain := d.cfg.SteerDrain
	if drain == nil || drain.Method == "" || strings.HasPrefix(drain.Method, "_") {
		return d.cfg.ClientExtensionAliases
	}
	aliases := make(map[string]string, len(d.cfg.ClientExtensionAliases)+1)
	for method, alias := range d.cfg.ClientExtensionAliases {
		aliases[method] = alias
	}
	aliases[drain.Method] = "_" + drain.Method
	return aliases
}

func conversationCapabilities(
	configured ports.ChatCapabilities,
	init acpsdk.InitializeResponse,
	steer steering,
) ports.ChatCapabilities {
	caps := cloneCapabilities(configured)
	if !supportsSessionRestore(init) {
		caps[ports.ChatCapabilityResume] = false
	}
	caps[ports.ChatCapabilityHistory] = init.AgentCapabilities.LoadSession
	caps[ports.ChatCapabilityFork] = false
	caps[ports.ChatCapabilityForkCurrent] = init.AgentCapabilities.SessionCapabilities.Fork != nil &&
		supportsSessionRestore(init)
	if steer.supported(init) {
		caps[ports.ChatCapabilitySteer] = true
	}
	caps[ports.ChatCapabilityImages] = init.AgentCapabilities.PromptCapabilities.Image
	caps[ports.ChatCapabilityEmbeddedContext] = init.AgentCapabilities.PromptCapabilities.EmbeddedContext
	caps[ports.ChatCapabilityResourceLinks] = true
	caps[ports.ChatCapabilityElicitation] = true
	caps[ports.ChatCapabilityNestedAgents] = true
	caps[ports.ChatCapabilityTerminalOutput] = true
	return caps
}

func supportsSessionRestore(init acpsdk.InitializeResponse) bool {
	return init.AgentCapabilities.LoadSession ||
		init.AgentCapabilities.SessionCapabilities.Resume != nil
}

func extensionSupported(meta map[string]any, name string) bool {
	extension, ok := meta[name].(map[string]any)
	if !ok {
		return false
	}
	supported, _ := extension["supported"].(bool)
	return supported
}

func pointer[T any](value T) *T { return &value }

func isACPAuthRequired(err error) bool {
	var requestErr *acpsdk.RequestError
	if !errors.As(err, &requestErr) {
		return false
	}
	return requestErr.Code == -32000
}

const acpResourceNotFound = -32002

func isACPResourceNotFound(err error) bool {
	var requestErr *acpsdk.RequestError
	return errors.As(err, &requestErr) && requestErr.Code == acpResourceNotFound
}

func isACPMethodNotFound(err error) bool {
	var requestErr *acpsdk.RequestError
	return errors.As(err, &requestErr) && requestErr.Code == -32601
}

func isACPInvalidParams(err error) bool {
	var requestErr *acpsdk.RequestError
	return errors.As(err, &requestErr) && requestErr.Code == -32602
}

func isACPInternalError(err error) bool {
	var requestErr *acpsdk.RequestError
	return errors.As(err, &requestErr) && requestErr.Code == -32603
}

func isACPPromptStalled(err error) bool {
	var requestErr *acpsdk.RequestError
	if !errors.As(err, &requestErr) || requestErr.Code != -32603 {
		return false
	}
	data, ok := requestErr.Data.(map[string]any)
	if !ok {
		return false
	}
	if stalled, _ := data[persistenthost.ACPPromptStalledDataKey].(bool); stalled {
		return true
	}
	provider, _ := data["providerData"].(map[string]any)
	stalled, _ := provider[persistenthost.ACPPromptStalledDataKey].(bool)
	return stalled
}

type promptStalledError struct{ error }

func (e promptStalledError) Unwrap() error { return e.error }

func (e promptStalledError) Is(target error) bool { return target == ports.ErrChatPromptStalled }

const acpSessionNotFoundDetail = "session not found"

func isACPSessionMissing(err error) bool {
	var requestErr *acpsdk.RequestError
	if !errors.As(err, &requestErr) || requestErr.Code != -32603 {
		return false
	}
	data, _ := requestErr.Data.(map[string]any)
	if provider, ok := data["providerData"].(map[string]any); ok {
		data = provider
	}
	details, _ := data["details"].(string)
	return strings.EqualFold(strings.TrimSpace(details), acpSessionNotFoundDetail)
}

func normalizeACPError(operation string, err error) error {
	if isACPAuthRequired(err) {
		return fmt.Errorf("%w: %s: %w", ports.ErrChatAuthRequired, operation, err)
	}
	if isACPPromptStalled(err) {
		return fmt.Errorf("%s: %w", operation, promptStalledError{err})
	}
	return fmt.Errorf("%s: %w", operation, err)
}

func normalizeACPLoadError(operation string, err error) error {
	if isACPInternalError(err) && !isACPAuthRequired(err) {
		return fmt.Errorf("%w: %s: %w", ports.ErrChatHistoryLoadFailed, operation, err)
	}
	return normalizeACPError(operation, err)
}
