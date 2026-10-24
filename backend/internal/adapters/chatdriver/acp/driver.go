// Package acp implements AO's provider-neutral Chat driver over the Agent
// Client Protocol. Provider packages supply only discovery, launch, metadata,
// and capability policy; this package owns the ACP lifecycle and translates ACP
// updates into AO's durable conversation vocabulary.
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

// Launch describes one ACP agent process. Command may be either AO's packaged
// protocol bridge or the exact user-installed provider executable resolved by
// the existing agent plugin.
type Launch struct {
	Command string
	Args    []string
	Env     map[string]string
}

// LaunchConfig is the resolved AO session context a provider binding may use to
// construct its process. It intentionally contains no install mechanism: binary
// ownership stays with the existing agent plugin.
type LaunchConfig struct {
	ReconnectOnly   bool
	SessionID       domain.SessionID
	DataDir         string
	WorkspacePath   string
	Env             map[string]string
	Model           string
	Permissions     ports.PermissionMode
	SystemPrompt    string
	DisableTools    bool
	ProviderScopeID string
	// ProviderConversationID carries the stored native conversation on resume so
	// a binding can migrate provider-side history into a fresh process home.
	// Empty on first start.
	ProviderConversationID string
}

// Config binds one harness to an ACP agent implementation.
type Config struct {
	Harness      domain.AgentHarness
	Capabilities ports.ChatCapabilities
	Probe        func(context.Context) error
	// InstallCheck optionally reports a missing chat adapter without starting
	// anything. See ports.ChatInstallChecker.
	InstallCheck func(context.Context) error
	// RetryStartupExit starts the agent once more if it exits during its first handshake.
	RetryStartupExit bool
	Launch           func(context.Context, LaunchConfig) (Launch, error)
	// ValidateInitialize optionally admits only the tested ACP distribution and
	// version after the protocol handshake identifies it. This is preferable to
	// invoking an adapter-specific version flag, which many stdio agents do not
	// implement and which can emit protocol bytes instead of a version string.
	ValidateInitialize func(acpsdk.InitializeResponse) error
	// AfterInitialize runs once on a freshly initialized provider connection,
	// before any session is created. It is for bindings whose agent needs a
	// one-time ACP call on the connection, such as authenticating with a method
	// the user has already completed elsewhere. It does not run when AO
	// reattaches to a persistent host, whose provider is already set up.
	AfterInitialize func(context.Context, *acpsdk.ClientSideConnection, acpsdk.InitializeResponse, LaunchConfig) error
	// SessionMeta carries adapter-defined ACP extensions whenever AO creates the
	// provider-side session object: session/new, session/load, or
	// session/resume. Standing context such as a system prompt is process input,
	// not transcript history, so a resumed native conversation must receive it
	// again even though the provider recovers the messages itself.
	//
	// Only use this for extensions the provider's session handlers actually read.
	// An agent that ignores _meta on session/new needs SessionPreamble instead.
	SessionMeta func(LaunchConfig) map[string]any
	// SessionPreamble returns standing context to attach to the first prompt of a
	// session as an embedded ACP resource. It is for agents that accept no
	// system-prompt launch flag and ignore session/new _meta, but do execute
	// prompt content blocks. It leads the first prompt of a new session. A
	// resumed conversation that already has turns received it then, so it is not
	// repeated (see conversation.setSessionPreamble).
	SessionPreamble func(LaunchConfig) []acpsdk.ContentBlock
	// SessionMode maps AO's approval vocabulary onto this ACP agent's mode ids.
	// Empty means "leave the provider/user default unchanged".
	SessionMode func(ports.PermissionMode) string
	// SessionOptions maps AO's per-turn choices onto ACP config option ids.
	SessionOptions func(ports.ChatTurnSettings) []SessionOption
	// LaunchSessionOptions returns config options whose value is fixed for the
	// whole session and known only from the launch, such as a persona the agent
	// lets AO select by id but not by launch flag. They are set once after
	// session/new or session/load, before the launch's turn settings.
	LaunchSessionOptions func(LaunchConfig) []SessionOption
	// RequireAdvertisedOptions refuses to send a config option, or a value, the
	// session did not advertise. Set it for agents that acknowledge an unknown
	// option without applying it, so the choice fails visibly instead.
	RequireAdvertisedOptions bool
	// CloseSessionBeforeReload closes the session before a history refresh
	// reloads it on the same connection. Set it for agents that answer a second
	// session/load with "already loaded". It applies only when the agent
	// advertises session/close.
	CloseSessionBeforeReload bool
	// PermissionPolicy lets a provider binding resolve permission requests that
	// have an exact AO policy mapping before the generic client parks them for a
	// human. Returning handled=false preserves the ordinary approval flow.
	PermissionPolicy PermissionPolicy
	// ClientExtension handles provider-defined agent-to-client JSON-RPC methods.
	ClientExtension ClientExtensionHandler
	// ClientExtensionAliases maps explicitly supported legacy wire methods onto
	// ACP-compliant underscore extension names handled by the pinned SDK.
	ClientExtensionAliases map[string]string
	// ValidateTurnSettings rejects provider settings that cannot be applied to a
	// live process. The initial permission mode is the launch-time value.
	ValidateTurnSettings TurnSettingsValidator
	// PromptResponseFailure lets a provider binding interpret its own structured
	// terminal metadata after a nominally successful ACP prompt response.
	PromptResponseFailure func(acpsdk.PromptResponse) error
	// SessionPromptResponseFailure is PromptResponseFailure given the provider
	// session, for a binding that reads the failure from the agent's own record,
	// and whether the turn brought any reply text or tool call.
	SessionPromptResponseFailure func(sessionID string, response acpsdk.PromptResponse, answered bool) error
	// HistoryNotice reports a replayed user message chunk that the agent wrote
	// into its own transcript. Replay starts no turn for it, so the agent's
	// output after it stays in the turn before, as it did live.
	HistoryNotice func(string) bool
	// HistoryUserChunkIsMessage makes each id-less replayed user chunk its own
	// prompt, for an agent that replays a user message as one chunk (pi-acp).
	HistoryUserChunkIsMessage bool
	// QuickStop marks a provider whose stop gains nothing from waiting (it never
	// reacts to stdin closing): a short session/close wait, then a quick signal.
	QuickStop bool
	// CanHibernate checks provider-owned work that outlives an ACP prompt.
	CanHibernate func(context.Context, *acpsdk.ClientSideConnection, acpsdk.SessionId) (bool, error)
	// EncodeProviderConversationID and DecodeProviderConversationID let a binding
	// persist ownership metadata around an opaque provider ID while keeping the
	// raw value on the ACP wire.
	EncodeProviderConversationID func(string) string
	DecodeProviderConversationID func(string) (string, error)
	// OnAuthRejected is called when the provider rejects the credential during
	// a live turn. It is how a cached "this credential works" verdict is
	// corrected the moment the provider says otherwise, and it is the only
	// correction that covers every credential source — including the Bedrock
	// and Vertex chains AO cannot inspect at all. Optional.
	OnAuthRejected func()
}

// TurnSettingsValidator validates live turn settings against launch-time state.
type TurnSettingsValidator func(ports.PermissionMode, ports.ChatTurnSettings) error

// PermissionPolicy maps the active AO permission mode and the provider's exact
// offered choices to an automatic response when the mapping is unambiguous.
type PermissionPolicy func(
	ports.PermissionMode,
	acpsdk.RequestPermissionRequest,
) (acpsdk.PermissionOptionId, bool)

// SessionOption is one ACP session configuration selection.
type SessionOption struct {
	ID    string
	Value string
	// Optional settings are skipped when the session rejects them.
	Optional bool
	// Name finds the choice when the agent lists it under another value, such as
	// Claude Code offering claude-opus-5-5 as "opus", named "Opus 5.5".
	Name string
}

// Driver opens ACP conversations for a single harness.
type Driver struct {
	cfg         Config
	log         *slog.Logger
	spawn       spawnFunc // short-lived catalog discovery only
	openProcess func(context.Context, LaunchConfig, func(context.Context) (map[string]string, error)) (*process, error)
	connectHost func(context.Context, persistenthost.Config) (*persistenthost.Transport, error)
}

var _ ports.ChatDriver = (*Driver)(nil)

// New returns an ACP Chat driver from a provider binding.
func New(cfg Config, log *slog.Logger) *Driver {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	driver := &Driver{cfg: cfg, log: log, spawn: spawnAgent, connectHost: persistenthost.ConnectOrStart}
	driver.openProcess = driver.connectProcess
	return driver
}

// DiscoverConfigOptions opens a short-lived ACP session and returns the
// provider-owned configuration catalog advertised by session/new. It sends no
// prompt and closes the process immediately after the handshake.
func DiscoverConfigOptions(
	ctx context.Context,
	launch Launch,
	workingDir string,
	log *slog.Logger,
) ([]ports.ChatConfigOption, error) {
	driver := New(Config{
		Launch: func(context.Context, LaunchConfig) (Launch, error) { return launch, nil },
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
	return normalizeConfigOptions(resp.ConfigOptions), nil
}

// Harness identifies the AO harness this ACP transport adapts.
func (d *Driver) Harness() domain.AgentHarness { return d.cfg.Harness }

// Probe checks the provider binding without creating an ACP session or worktree.
func (d *Driver) Probe(ctx context.Context) (ports.ChatCapabilities, error) {
	if d.cfg.Probe == nil || d.cfg.Launch == nil {
		return nil, fmt.Errorf("%w: incomplete ACP binding", ports.ErrChatDriverUnavailable)
	}
	if err := d.cfg.Probe(ctx); err != nil {
		return nil, err
	}
	return cloneCapabilities(d.cfg.Capabilities), nil
}

// CheckChatInstall reports a missing chat adapter; nil without a cheap check.
func (d *Driver) CheckChatInstall(ctx context.Context) error {
	if d.cfg.InstallCheck == nil {
		return nil
	}
	return d.cfg.InstallCheck(ctx)
}

// SupportsPermissionMode reports whether the agent can start under mode.
func (d *Driver) SupportsPermissionMode(mode ports.PermissionMode) bool {
	if d.cfg.ValidateTurnSettings == nil {
		return true
	}
	err := d.cfg.ValidateTurnSettings(mode, ports.ChatTurnSettings{Approval: mode})
	return !errors.Is(err, ports.ErrChatPermissionModeUnsupported)
}

// Start creates a new ACP session in the AO worktree.
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
		ProviderScopeID: cfg.ProviderScopeID,
	}
	connectStarted := time.Now()
	conv, init, live, err := d.connect(ctx, launchCfg, cfg.PrepareEnv)
	d.logStartStage(cfg.SessionID, "connect", connectStarted, err)
	if err != nil {
		return nil, err
	}
	if live != nil {
		// A fresh-start request colliding with a surviving host is a durable-state
		// mismatch. Detach without destroying a provider that may still be working.
		conv.discard()
		return nil, fmt.Errorf("%w: fresh ACP start found an existing initialized session", ports.ErrChatRecoveryInconclusive)
	}
	// An agent that does not advertise session/resume can still start new
	// sessions; it just cannot be resumed after a disconnect. The resume
	// capability is downgraded in conversationCapabilities, and Resume()
	// returns ErrChatResumeFailed if called.
	additional, err := normalizeAdditionalDirectories(cfg.WorkspacePath, cfg.AdditionalDirectories,
		init.AgentCapabilities.SessionCapabilities.AdditionalDirectories != nil)
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

// openNewSession creates a provider session on an initialized connection and
// applies the requested turn settings. The caller discards conv on error.
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
	conv.start(
		string(resp.SessionId), conversationCapabilities(d.cfg.Capabilities, init),
		d.cfg.SessionMode, d.cfg.SessionOptions, d.cfg.PermissionPolicy,
		settings.Approval, d.cfg.ValidateTurnSettings, resp.ConfigOptions,
		conv.legacyWire.modelState(), resp.Modes,
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
		// Initial model and permission mode may have been applied via launch-time
		// flags (e.g. kimchiacp passes --model, --auto, --yolo). An agent that
		// does not implement the runtime ACP setters returns -32601; tolerate it
		// at session start so those bindings can still open a session.
		if !errors.Is(err, ErrACPSetterUnsupported) {
			return fmt.Errorf("configure ACP session: %w", err)
		}
	}
	return nil
}

// applyLaunchSessionOptions sets the binding's session-scoped config options.
// A persistent-host reconnect skips it: that provider session already has them.
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

// Reconnect attaches to a surviving provider without launching a replacement.
func (d *Driver) Reconnect(ctx context.Context, cfg ports.ChatResumeConfig) (ports.ChatConversation, error) {
	cfg.ReconnectOnly = true
	return d.Resume(ctx, cfg)
}

// Resume reconnects to the stored ACP session. When the agent advertises
// session/load, AO uses it to recover both provider context and the normalized
// transcript; resume-only agents recover context but explicitly report that no
// typed history replay is available.
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
		ProviderScopeID: cfg.ProviderScopeID,
		ReconnectOnly:   cfg.ReconnectOnly,
		// The connect below spawns a fresh process, so a resumed conversation's
		// provider-side history has to be reachable from the new process home.
		ProviderConversationID: cfg.ProviderConversationID,
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
		conv.start(
			cfg.ProviderConversationID, conversationCapabilities(d.cfg.Capabilities, init),
			d.cfg.SessionMode, d.cfg.SessionOptions, d.cfg.PermissionPolicy,
			cfg.Permissions, d.cfg.ValidateTurnSettings, setup.ConfigOptions,
			setup.Models, setup.Modes,
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
		init.AgentCapabilities.SessionCapabilities.AdditionalDirectories != nil)
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
	conv.start(
		cfg.ProviderConversationID, conversationCapabilities(d.cfg.Capabilities, init),
		d.cfg.SessionMode, d.cfg.SessionOptions, d.cfg.PermissionPolicy,
		cfg.Permissions, d.cfg.ValidateTurnSettings, configOptions,
		conv.legacyWire.modelState(), modes,
	)
	conv.setReportedProviderConversationID(reportedProviderConversationID)
	// Standing context is process input, not transcript history, so a resumed
	// conversation must receive it again even though the provider recovers the
	// messages itself.
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

// resumeFresh opens a new provider session in place of a stored one the
// provider reports does not exist. Only a newly spawned provider reaches it: a
// surviving host that still holds the session is reattached before any reload.
// The caller's FreshIfMissing asserts the stored conversation never started, so
// no history is lost; the returned conversation reports the new id.
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
		// Nothing to recover: stop the host so it does not block every later
		// start, then start the agent afresh, which reloads the conversation.
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
	// Some agents exit during a cold first handshake (pi-acp while its
	// background daemon comes up); a second start finds it running.
	d.log.Info("chat: ACP agent exited during its first handshake; starting it once more",
		"sessionID", cfg.SessionID, "harness", d.cfg.Harness, "error", err)
	select {
	case <-time.After(startupRetryDelay):
	case <-ctx.Done():
		return nil, acpsdk.InitializeResponse{}, nil, err
	}
	return reopen("initialize_retry")
}

// errHostNeverStarted is a reattached persistent host that holds no session.
var errHostNeverStarted = errors.New("persistent ACP host never finished starting")

// startupRetryDelay lets an exited agent release its host before the retry.
const startupRetryDelay = 500 * time.Millisecond

// isACPPeerDisconnected reports a request unanswered because the agent exited.
func isACPPeerDisconnected(err error) bool {
	var requestErr *acpsdk.RequestError
	if !errors.As(err, &requestErr) {
		return false
	}
	data, _ := requestErr.Data.(map[string]any)
	reason, _ := data["error"].(string)
	// The handshake can also fail writing to the gone process's input.
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
		proc, d.log, cfg.ProviderScopeID, d.cfg.ClientExtension, d.cfg.ClientExtensionAliases,
	)
	conv.onAuthRejected = d.cfg.OnAuthRejected
	conv.promptResponseFailure = d.cfg.PromptResponseFailure
	conv.sessionPromptResponseFailure = d.cfg.SessionPromptResponseFailure
	conv.hibernationCheck = d.cfg.CanHibernate
	conv.decodeConversationID = d.cfg.DecodeProviderConversationID
	conv.historyNotice = d.cfg.HistoryNotice
	conv.historyUserChunkIsMessage = d.cfg.HistoryUserChunkIsMessage
	conv.requireAdvertised = d.cfg.RequireAdvertisedOptions
	if proc.reconnected {
		state := proc.acpState
		if state == nil || state.SessionID == "" {
			// The host's first handshake was cut short (a daemon stopped mid-start).
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
			// These two Claude bridge extensions enrich the transcript. They do
			// not grant the agent access to AO's terminal or filesystem APIs.
			Meta: map[string]any{
				// Cursor reports a model's effort, thinking and speed as their own
				// settings only to a client that asks. Other agents ignore it.
				"parameterizedModelPicker": true,
				"subagent-transcript":      true,
				"terminal_output":          true,
				// claude-agent-acp publishes retryable API/transport failures only
				// when the client opts into this namespaced metadata extension. It is
				// observational: AO receives status, but grants no new capability.
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
	// Ownership is stable across app upgrades and mutable model/approval choices.
	// An adopted provider keeps its original launch environment and instructions;
	// changes to those take effect only when the provider is explicitly replaced.
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
			// Binary resolution, generated provider files, and credential rotation
			// are launch-only effects. None run when reconnecting to a live host.
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

func conversationCapabilities(
	configured ports.ChatCapabilities,
	init acpsdk.InitializeResponse,
) ports.ChatCapabilities {
	caps := cloneCapabilities(configured)
	if !supportsSessionRestore(init) {
		caps[ports.ChatCapabilityResume] = false
	}
	caps[ports.ChatCapabilityHistory] = init.AgentCapabilities.LoadSession
	caps[ports.ChatCapabilityFork] = false // ACP has no turn-anchored fork in the pinned SDK.
	caps[ports.ChatCapabilityForkCurrent] = init.AgentCapabilities.SessionCapabilities.Fork != nil &&
		supportsSessionRestore(init)
	if extensionSupported(init.Meta, "steering") {
		caps[ports.ChatCapabilitySteer] = true
	}
	caps[ports.ChatCapabilityImages] = init.AgentCapabilities.PromptCapabilities.Image
	caps[ports.ChatCapabilityEmbeddedContext] = init.AgentCapabilities.PromptCapabilities.EmbeddedContext
	// ResourceLink is a baseline ACP content block rather than an optional prompt
	// capability. Every ACP conversation preserves it natively.
	caps[ports.ChatCapabilityResourceLinks] = true
	// These are facilities AO itself negotiated as the ACP client. An agent that
	// never uses them simply produces no matching events.
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

// isACPAuthRequired reports whether err is the agent telling us the credential
// was refused, so the caller can raise a reauth prompt instead of a generic
// turn failure.
func isACPAuthRequired(err error) bool {
	var requestErr *acpsdk.RequestError
	if !errors.As(err, &requestErr) {
		return false
	}
	return requestErr.Code == -32000
}

// acpResourceNotFound is ACP's JSON-RPC "Resource not found" error code. A
// session/load or session/resume for an id the agent never persisted returns it.
const acpResourceNotFound = -32002

func isACPResourceNotFound(err error) bool {
	var requestErr *acpsdk.RequestError
	return errors.As(err, &requestErr) && requestErr.Code == acpResourceNotFound
}

// isACPMethodNotFound reports whether err is a JSON-RPC -32601 "Method not
// found" from the ACP agent. Agents that implement a subset of the protocol
// (e.g. no session/set_mode or session/set_config_option) return this code;
// the driver treats those calls as optional so a minimal agent can still run.
func isACPMethodNotFound(err error) bool {
	var requestErr *acpsdk.RequestError
	return errors.As(err, &requestErr) && requestErr.Code == -32601
}

func isACPInvalidParams(err error) bool {
	var requestErr *acpsdk.RequestError
	return errors.As(err, &requestErr) && requestErr.Code == -32602
}

// isACPInternalError reports whether err is a JSON-RPC -32603 "Internal error"
// from the ACP agent. The SDK coerces any handler error the agent did not shape
// itself into this code, so it is how a provider reports its own failure (for
// example Claude Code's own "context deadline exceeded" while replaying a
// transcript) rather than a protocol or authentication problem.
func isACPInternalError(err error) bool {
	var requestErr *acpsdk.RequestError
	return errors.As(err, &requestErr) && requestErr.Code == -32603
}

func normalizeACPError(operation string, err error) error {
	if isACPAuthRequired(err) {
		return fmt.Errorf("%w: %s: %w", ports.ErrChatAuthRequired, operation, err)
	}
	return fmt.Errorf("%s: %w", operation, err)
}

// normalizeACPLoadError normalizes a failed ACP session/load whose calling
// context is still alive. A -32603 answer then means the provider itself could
// not replay the transcript; it is mapped to ports.ErrChatHistoryLoadFailed so
// the settle loop stops re-sending the same load and the interface transition
// reports a dedicated code instead of a generic resume failure. Callers must
// check their own context first: the SDK also synthesizes -32603 when the
// caller's context ends mid-request.
func normalizeACPLoadError(operation string, err error) error {
	if isACPInternalError(err) && !isACPAuthRequired(err) {
		return fmt.Errorf("%w: %s: %w", ports.ErrChatHistoryLoadFailed, operation, err)
	}
	return normalizeACPError(operation, err)
}
