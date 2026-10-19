// Package stakpakacp contains an unregistered, fresh-conversation-only candidate.
// Stock v0.3.88 does not load/replay native history and its foreground cancellation
// is unqualified. This package deliberately cannot satisfy AO's production floor.
package stakpakacp

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	acpsdk "github.com/coder/acp-go-sdk"

	acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/nativeacp"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Driver delegates structured conversation transport and live host ownership to
// nativeacp. It never turns the released session/load stub into a successful restore.
type Driver struct{ native ports.ChatDriver }

var _ ports.ChatDriver = (*Driver)(nil)
var _ ports.ChatDriverReconnector = (*Driver)(nil)

// New creates a development candidate using the existing Stakpak plugin's binary
// discovery. It is intentionally absent from the production registry. Callers must
// stage a private native profile under DataDir, including the user's configuration;
// this adapter never copies or writes the source profile. Stock ACP checks for
// updates on startup: qualification must also isolate the executable installation.
func New(plugin nativeacp.Plugin, log *slog.Logger) *Driver {
	return &Driver{native: nativeacp.New(plugin, nativeacp.Config{
		Harness: "stakpak", Configure: func(ctx context.Context, cfg acpdriver.LaunchConfig) ([]string, map[string]string, error) {
			args, env, err := configure(ctx, cfg)
			if err != nil {
				return nil, nil, err
			}
			bin, err := plugin.ResolveBinary(ctx)
			if err != nil {
				return nil, nil, err
			}
			root, err := filepath.EvalSymlinks(cfg.DataDir)
			if err != nil {
				return nil, nil, ports.ErrChatDriverUnavailable
			}
			resolved, err := filepath.EvalSymlinks(bin)
			if err != nil {
				return nil, nil, ports.ErrChatDriverUnavailable
			}
			rel, err := filepath.Rel(root, resolved)
			if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return nil, nil, fmt.Errorf("%w: stock Stakpak self-updates; stage an isolated executable beneath DataDir", ports.ErrChatDriverUnavailable)
			}
			return args, env, nil
		}, Capabilities: candidateCapabilities(),
		ValidateTurnSettings: validateSettings,
		ValidateInitialize: func(init acpsdk.InitializeResponse) error {
			if init.AgentInfo == nil || init.AgentInfo.Name != "stakpak" || init.AgentInfo.Version != "0.3.88" {
				return fmt.Errorf("%w: Stakpak candidate requires the inspected 0.3.88 ACP contract", ports.ErrChatDriverIncompatible)
			}
			return nil
		},
	}, log)}
}
func (*Driver) Harness() domain.AgentHarness { return "stakpak" }
func (d *Driver) Probe(ctx context.Context) (ports.ChatCapabilities, error) {
	if _, err := d.native.Probe(ctx); err != nil {
		return nil, err
	}
	return candidateCapabilities(), nil
}
func (d *Driver) Start(ctx context.Context, cfg ports.ChatStartConfig) (ports.ChatConversation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateSettings(cfg.Permissions, ports.ChatTurnSettings{Model: cfg.Model, Effort: cfg.Effort, Approval: cfg.Permissions}); err != nil {
		return nil, err
	}
	if cfg.SystemPrompt != "" || cfg.ReadOnly || cfg.DisableNetwork || cfg.Ephemeral || len(cfg.AdditionalDirectories) != 0 || len(cfg.MCPServers) != 0 {
		return nil, fmt.Errorf("%w: Stakpak candidate cannot deliver additive standing instructions, sandbox, ephemeral, extra-directory or MCP settings", ports.ErrChatCapabilityUnavailable)
	}
	c, err := d.native.Start(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return &conversation{ChatConversation: c}, nil
}
func (d *Driver) Resume(ctx context.Context, cfg ports.ChatResumeConfig) (ports.ChatConversation, error) {
	if cfg.ReconnectOnly {
		return d.Reconnect(ctx, cfg)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("%w: released Stakpak session/load does not restore or replay native history; fresh fallback is forbidden", ports.ErrChatResumeFailed)
}
func (d *Driver) Reconnect(ctx context.Context, cfg ports.ChatResumeConfig) (ports.ChatConversation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateSettings(cfg.Permissions, ports.ChatTurnSettings{Model: cfg.Model, Effort: cfg.Effort, Approval: cfg.Permissions}); err != nil {
		return nil, err
	}
	if cfg.SystemPrompt != "" || cfg.ReadOnly || len(cfg.AdditionalDirectories) != 0 || len(cfg.MCPServers) != 0 {
		return nil, fmt.Errorf("%w: Stakpak candidate cannot apply requested reconnect context", ports.ErrChatCapabilityUnavailable)
	}
	reconnect, ok := d.native.(ports.ChatDriverReconnector)
	if !ok {
		return nil, ports.ErrChatHostNotRunning
	}
	cfg.ReconnectOnly = true
	c, err := reconnect.Reconnect(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return &conversation{ChatConversation: c}, nil
}
func candidateCapabilities() ports.ChatCapabilities {
	return ports.ChatCapabilities{ports.ChatCapabilityStreaming: true, ports.ChatCapabilityTools: true,
		ports.ChatCapabilityApprovals: false, ports.ChatCapabilityInterrupt: false, ports.ChatCapabilityResume: false}
}
func validateSettings(initial ports.PermissionMode, s ports.ChatTurnSettings) error {
	if (initial != "" && initial != ports.PermissionModeDefault) || (s.Approval != "" && s.Approval != ports.PermissionModeDefault) {
		return fmt.Errorf("%w: Stakpak candidate preserves native manual decisions only", ports.ErrChatPermissionModeUnsupported)
	}
	if s.Model != "" || s.Effort != "" {
		return fmt.Errorf("%w: Stakpak candidate preserves native configured model and reasoning defaults", ports.ErrChatConfigOptionInvalid)
	}
	return nil
}
func configure(ctx context.Context, cfg acpdriver.LaunchConfig) ([]string, map[string]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if err := validateSettings(cfg.Permissions, ports.ChatTurnSettings{Model: cfg.Model}); err != nil {
		return nil, nil, err
	}
	if cfg.SystemPrompt != "" {
		return nil, nil, fmt.Errorf("%w: stock ACP instruction-file input is not qualified additive context", ports.ErrChatCapabilityUnavailable)
	}
	home := cfg.Env["HOME"]
	if !filepath.IsAbs(cfg.DataDir) || !filepath.IsAbs(home) {
		return nil, nil, fmt.Errorf("%w: explicit AO-owned Stakpak profile required", ports.ErrChatDriverUnavailable)
	}
	root, err := filepath.EvalSymlinks(cfg.DataDir)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: AO data directory unavailable", ports.ErrChatDriverUnavailable)
	}
	resolved, err := filepath.EvalSymlinks(home)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: staged Stakpak profile unavailable", ports.ErrChatDriverUnavailable)
	}
	rel, err := filepath.Rel(root, resolved)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, nil, fmt.Errorf("%w: Stakpak profile must be beneath AO data directory", ports.ErrChatDriverUnavailable)
	}
	config := filepath.Join(resolved, "config.toml")
	info, err := os.Lstat(config)
	if err != nil || !info.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("%w: stage a regular native config.toml in the owned profile", ports.ErrChatDriverUnavailable)
	}
	return []string{"--config", config, "acp"}, map[string]string{
		"HOME": resolved, "USERPROFILE": resolved, "XDG_CONFIG_HOME": filepath.Join(resolved, "config"),
		"XDG_DATA_HOME": filepath.Join(resolved, "data"), "XDG_CACHE_HOME": filepath.Join(resolved, "cache"),
		"APPDATA": filepath.Join(resolved, "config"), "LOCALAPPDATA": filepath.Join(resolved, "data"),
	}, nil
}

// Embedding only the base port hides the generic ACP history/config/hibernate
// surfaces: native advertises image/load features it does not actually implement.
// Close detaches; explicit Kill retains the persistent host's terminator.
type conversation struct{ ports.ChatConversation }

var _ ports.ChatProviderTerminator = (*conversation)(nil)
var _ ports.ChatProviderPreserver = (*conversation)(nil)

func (*conversation) Capabilities() ports.ChatCapabilities { return candidateCapabilities() }
func (c *conversation) SendTurn(ctx context.Context, msg ports.ChatUserMessage) (ports.ChatTurnRef, error) {
	if len(msg.Content) != 0 {
		return ports.ChatTurnRef{}, fmt.Errorf("%w: Stakpak ACP accepts text only", ports.ErrChatCapabilityUnavailable)
	}
	if err := validateSettings(ports.PermissionModeDefault, msg.Settings); err != nil {
		return ports.ChatTurnRef{}, err
	}
	return c.ChatConversation.SendTurn(ctx, msg)
}
func (*conversation) Interrupt(context.Context, string) error {
	return fmt.Errorf("%w: Stakpak executing-child interrupt is unqualified", ports.ErrChatCapabilityUnavailable)
}
func (c *conversation) PreservesProviderOnClose() bool {
	p, ok := c.ChatConversation.(ports.ChatProviderPreserver)
	return ok && p.PreservesProviderOnClose()
}
func (c *conversation) Terminate() error {
	if p, ok := c.ChatConversation.(ports.ChatProviderTerminator); ok {
		return p.Terminate()
	}
	return fmt.Errorf("%w: native controller has no explicit terminator", ports.ErrChatCapabilityUnavailable)
}

// These lifecycle ports are transport guarantees, not extra native features.
// The service must persist the turn correlation before dispatch and release
// replay only after adopting the same host; durable event commits drive ACKs.
var _ ports.ChatDeferredTurnStarter = (*conversation)(nil)
var _ ports.ChatLiveReconnector = (*conversation)(nil)
var _ ports.ChatLiveReconnectActivator = (*conversation)(nil)
var _ ports.ChatProviderEventAcknowledger = (*conversation)(nil)

func (c *conversation) StartDeferredTurn(id string) error {
	if p, ok := c.ChatConversation.(ports.ChatDeferredTurnStarter); ok {
		return p.StartDeferredTurn(id)
	}
	return fmt.Errorf("%w: deferred turn transport unavailable", ports.ErrChatDriverIncompatible)
}
func (c *conversation) DiscardDeferredTurn(id string) {
	if p, ok := c.ChatConversation.(ports.ChatDeferredTurnStarter); ok {
		p.DiscardDeferredTurn(id)
	}
}
func (c *conversation) ReconnectedLive() bool {
	p, ok := c.ChatConversation.(ports.ChatLiveReconnector)
	return ok && p.ReconnectedLive()
}
func (c *conversation) ActivateLiveReconnect(ctx context.Context, id string) error {
	if p, ok := c.ChatConversation.(ports.ChatLiveReconnectActivator); ok {
		return p.ActivateLiveReconnect(ctx, id)
	}
	return fmt.Errorf("%w: live replay transport unavailable", ports.ErrChatRecoveryInconclusive)
}
func (c *conversation) AcknowledgeProviderEvent(ctx context.Context, id string) error {
	if p, ok := c.ChatConversation.(ports.ChatProviderEventAcknowledger); ok {
		return p.AcknowledgeProviderEvent(ctx, id)
	}
	return fmt.Errorf("%w: durable event acknowledgement unavailable", ports.ErrChatDriverIncompatible)
}
