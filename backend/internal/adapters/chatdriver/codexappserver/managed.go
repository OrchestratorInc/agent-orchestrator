package codexappserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/persistenthost"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type managedHost struct {
	process  *process
	identity string
}

// ManagedDriver is opt-in; the native driver and account login remain unchanged.
type ManagedDriver struct {
	plugin codexPlugin
	log    *slog.Logger
	open   func(context.Context, persistenthost.Config) (managedHost, error)
}

// NewManaged requires explicit per-session authorization instead of device login.
func NewManaged(plugin codexPlugin, log *slog.Logger) *ManagedDriver {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &ManagedDriver{plugin: plugin, log: log, open: openManagedHost}
}

// Harness shares the provider identifier without replacing the native driver.
func (*ManagedDriver) Harness() domain.AgentHarness { return domain.HarnessCodex }

// Probe checks the installed protocol version without consulting device credentials.
func (d *ManagedDriver) Probe(ctx context.Context) (ports.ChatCapabilities, error) {
	bin, err := d.plugin.ResolveBinary(ctx)
	if err != nil {
		return nil, errors.Join(ports.ErrChatDriverUnavailable, err)
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	output, err := installedCodexVersion(ctx, bin)
	version, parsed := parseCodexVersion(output)
	minimum, _ := parseCodexVersion(minimumCodexVersion)
	if err != nil || !parsed || version.less(minimum) {
		return nil, ports.ErrChatDriverIncompatible
	}
	return managedCapabilities(), nil
}

func managedCapabilities() ports.ChatCapabilities {
	caps := capabilities()
	delete(caps, ports.ChatCapabilityRateLimits)
	return caps
}

type managedConversation struct {
	*conversation
	hostIdentity string
}

func (c *managedConversation) HostIdentity() string               { return c.hostIdentity }
func (*managedConversation) Capabilities() ports.ChatCapabilities { return managedCapabilities() }

func (*managedConversation) ReadRateLimits(context.Context) (ports.ChatRateLimits, error) {
	return ports.ChatRateLimits{}, ports.ErrChatCapabilityUnavailable
}

// Start refuses missing routes and never adopts an already-running conversation.
func (d *ManagedDriver) Start(ctx context.Context, cfg ports.ChatStartConfig) (ports.ChatConversation, error) {
	return d.openConversation(ctx, cfg, "")
}

// Resume preserves the requested history identity without falling back to a new thread.
func (d *ManagedDriver) Resume(ctx context.Context, cfg ports.ChatResumeConfig) (ports.ChatConversation, error) {
	if cfg.ProviderConversationID == "" {
		return nil, ports.ErrChatResumeFailed
	}
	return d.openConversation(ctx, ports.ChatStartConfig{
		SessionID: cfg.SessionID, ControllerGeneration: cfg.ControllerGeneration, DataDir: cfg.DataDir, WorkspacePath: cfg.WorkspacePath,
		Env: cfg.Env, PrepareEnv: cfg.PrepareEnv, Model: cfg.Model, Effort: cfg.Effort, Permissions: cfg.Permissions, ReadOnly: cfg.ReadOnly,
		SystemPrompt: cfg.SystemPrompt, ProviderScopeID: cfg.ProviderScopeID, ProviderIDsScoped: cfg.ProviderIDsScoped,
		AdditionalDirectories: cfg.AdditionalDirectories, MCPServers: cfg.MCPServers, Route: cfg.Route,
	}, cfg.ProviderConversationID)
}

func (d *ManagedDriver) openConversation(ctx context.Context, cfg ports.ChatStartConfig, nativeID string) (ports.ChatConversation, error) {
	if len(cfg.AdditionalDirectories) != 0 || len(cfg.MCPServers) != 0 {
		return nil, ports.ErrChatCapabilityUnavailable
	}
	bin, err := d.plugin.ResolveBinary(ctx)
	if err != nil {
		return nil, errors.Join(ports.ErrChatDriverUnavailable, err)
	}
	launch, err := managedLaunchConfig(ctx, bin, cfg)
	if err != nil {
		return nil, err
	}
	if cfg.PrepareEnv != nil {
		launch.Prepare = func(ctx context.Context) (persistenthost.PreparedProvider, error) {
			prepared := cfg
			var prepareErr error
			prepared.Env, prepareErr = cfg.PrepareEnv(ctx)
			if prepareErr != nil {
				return persistenthost.PreparedProvider{}, prepareErr
			}
			current, err := managedLaunchConfig(ctx, bin, prepared)
			return persistenthost.PreparedProvider{Env: current.Env, Argv: current.Argv}, err
		}
	}
	host, err := d.open(ctx, launch)
	if err != nil {
		return nil, errors.Join(ports.ErrChatRecoveryInconclusive, err)
	}
	if host.identity == "" || host.process == nil || host.process.terminate == nil {
		if host.process != nil && host.process.stop != nil {
			_ = host.process.stop()
		}
		return nil, ports.ErrChatRecoveryInconclusive
	}
	scope := cfg.ProviderScopeID
	if !cfg.ProviderIDsScoped {
		scope = ""
	}
	conv := &managedConversation{conversation: newConversation(host.process, d.log, scope), hostIdentity: host.identity}
	conv.readOnly = cfg.ReadOnly
	if host.process.reconnected {
		if nativeID == "" {
			_ = conv.Close()
			return nil, ports.ErrChatRecoveryInconclusive
		}
		conv.start(nativeID, cfg.Model, cfg.Effort)
		return conv, nil
	}
	ctx, cancel := context.WithTimeout(ctx, handshakeTimeout)
	defer cancel()
	if err := initializeConnection(ctx, conv.conn); err != nil {
		return nil, errors.Join(err, conv.Terminate())
	}
	policy, sandbox, reviewer := launchApprovalSettings(cfg.Permissions, cfg.ReadOnly)
	params := map[string]any{"cwd": cfg.WorkspacePath, "approvalPolicy": policy, "approvalsReviewer": reviewer,
		"sandbox": sandbox, "modelProvider": "ao_accounts_manager"}
	if cfg.Model != "" {
		params["model"] = cfg.Model
	}
	if cfg.Effort != "" {
		params["config"] = map[string]any{"model_reasoning_effort": cfg.Effort}
	}
	if cfg.SystemPrompt != "" {
		params["developerInstructions"] = cfg.SystemPrompt
	}
	method := "thread/start"
	if nativeID != "" {
		method, params["threadId"] = "thread/resume", nativeID
	} else if cfg.Ephemeral {
		params["ephemeral"] = true
	}
	var response struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
		Model           string `json:"model"`
		ReasoningEffort string `json:"reasoningEffort"`
	}
	err = conv.conn.request(ctx, method, params, &response)
	if err == nil && (response.Thread.ID == "" || nativeID != "" && response.Thread.ID != nativeID) {
		err = errors.New("missing or mismatched provider thread identity")
	}
	if err != nil {
		if nativeID != "" {
			err = errors.Join(ports.ErrChatResumeFailed, err)
		}
		return nil, errors.Join(fmt.Errorf("managed %s: %w", method, err), conv.Terminate())
	}
	conv.start(response.Thread.ID, response.Model, response.ReasoningEffort)
	return conv, nil
}

func openManagedHost(ctx context.Context, cfg persistenthost.Config) (managedHost, error) {
	transport, err := persistenthost.ConnectOrStart(ctx, cfg)
	if err != nil {
		return managedHost{}, err
	}
	identity := transport.HostIdentity()
	if identity == "" {
		_ = transport.Stdin.Close()
		return managedHost{}, ports.ErrChatRecoveryInconclusive
	}
	proc := &process{stdin: transport.Stdin, stdout: transport.Stdout, stop: transport.Stdin.Close,
		reconnected: transport.Reconnected, nextRequestID: transport.NextRequestID}
	proc.terminate = func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		stopErr := persistenthost.ShutdownHost(ctx, cfg.DataDir, cfg.SessionID, identity)
		closeErr := transport.Stdin.Close()
		if stopErr != nil {
			return fmt.Errorf("%w: %w", ports.ErrChatRecoveryInconclusive, stopErr)
		}
		return closeErr
	}
	return managedHost{process: proc, identity: identity}, nil
}

var _ ports.ChatDriver = (*ManagedDriver)(nil)
