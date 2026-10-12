// Package zcodeprotocol drives the stock ZCode app-server NDJSON protocol.
// It is not ACP. Native hooks provide hidden user-role reminder context while
// preserving ZCode's default prompt and native user/project rules.
package zcodeprotocol

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/zcode"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/persistenthost"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/processenv"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Driver opens ZCode conversations through its native app-server.
type Driver struct {
	plugin *zcode.Plugin
	log    *slog.Logger
}

// New creates a driver using the existing agent binary resolver.
func New(plugin *zcode.Plugin, log *slog.Logger) *Driver {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Driver{plugin: plugin, log: log}
}

var _ ports.ChatDriver = (*Driver)(nil)
var _ ports.ChatDriverReconnector = (*Driver)(nil)

// Harness identifies the native provider.
func (*Driver) Harness() domain.AgentHarness { return domain.HarnessZCode }
func capabilities() ports.ChatCapabilities {
	return ports.ChatCapabilities{
		ports.ChatCapabilityStreaming: true,
		ports.ChatCapabilityTools:     true, ports.ChatCapabilityApprovals: true, ports.ChatCapabilityInterrupt: true,
		ports.ChatCapabilityResume: true, ports.ChatCapabilityHistory: true,
	}
}

// Probe resolves the install; native authentication is checked by real requests.
func (d *Driver) Probe(ctx context.Context) (ports.ChatCapabilities, error) {
	if d.plugin == nil {
		return nil, ports.ErrChatDriverUnavailable
	}
	if _, err := d.plugin.ResolveBinary(ctx); err != nil {
		return nil, fmt.Errorf("%w: %w", ports.ErrChatDriverUnavailable, err)
	}
	return capabilities(), nil
}
func validateConfig(cfg ports.ChatStartConfig) (string, error) {
	if !filepath.IsAbs(cfg.WorkspacePath) || !filepath.IsAbs(cfg.DataDir) {
		return "", errors.New("zcode: absolute data/workspace paths are required")
	}
	if cfg.ReadOnly {
		return "", fmt.Errorf("%w: ZCode does not provide a read-only sandbox", ports.ErrChatPermissionModeUnsupported)
	}
	if len(cfg.AdditionalDirectories) > 0 {
		return "", fmt.Errorf("%w: ZCode additional roots are unsupported", ports.ErrChatCapabilityUnavailable)
	}
	if cfg.Model != "" || cfg.Effort != "" {
		return "", fmt.Errorf("%w: configure the model in ZCode's native provider settings", ports.ErrChatCapabilityUnavailable)
	}
	switch ports.NormalizePermissionMode(cfg.Permissions) {
	case ports.PermissionModeDefault, ports.PermissionModeAuto:
		return "build", nil
	case ports.PermissionModeAcceptEdits:
		return "edit", nil
	case ports.PermissionModeBypassPermissions:
		return "yolo", nil
	default:
		return "", ports.ErrChatPermissionModeUnsupported
	}
}
func nativeMCP(servers []ports.ChatMCPServerConfig) ([]map[string]any, error) {
	result := make([]map[string]any, 0, len(servers))
	for _, s := range servers {
		switch s.Type {
		case "stdio", "":
			if strings.TrimSpace(s.Name) == "" || strings.TrimSpace(s.Command) == "" {
				return nil, errors.New("zcode: incomplete stdio MCP server")
			}
			env := make([]map[string]string, 0, len(s.Env))
			keys := make([]string, 0, len(s.Env))
			for k := range s.Env {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				env = append(env, map[string]string{"name": k, "value": s.Env[k]})
			}
			args := s.Args
			if args == nil {
				args = []string{}
			}
			result = append(result, map[string]any{"name": s.Name, "command": s.Command, "args": args, "env": env, "isolation": "session"})
		default:
			return nil, fmt.Errorf("%w: ZCode currently accepts AO stdio MCP servers only", ports.ErrChatCapabilityUnavailable)
		}
	}
	return result, nil
}

// Start creates a fresh native conversation.
func (d *Driver) Start(ctx context.Context, cfg ports.ChatStartConfig) (ports.ChatConversation, error) {
	return d.open(ctx, cfg, "", false)
}

// Resume loads exact native history or attaches to its surviving host.
func (d *Driver) Resume(ctx context.Context, cfg ports.ChatResumeConfig) (ports.ChatConversation, error) {
	if cfg.ProviderConversationID == "" {
		return nil, ports.ErrChatResumeFailed
	}
	return d.open(ctx, ports.ChatStartConfig{SessionID: cfg.SessionID, DataDir: cfg.DataDir, WorkspacePath: cfg.WorkspacePath, Env: cfg.Env, PrepareEnv: cfg.PrepareEnv, Model: cfg.Model, Effort: cfg.Effort, Permissions: cfg.Permissions, ReadOnly: cfg.ReadOnly, SystemPrompt: cfg.SystemPrompt, ProviderScopeID: cfg.ProviderScopeID, AdditionalDirectories: cfg.AdditionalDirectories, MCPServers: cfg.MCPServers}, cfg.ProviderConversationID, cfg.ReconnectOnly)
}

// Reconnect attaches only to the existing host and never starts a replacement.
func (d *Driver) Reconnect(ctx context.Context, cfg ports.ChatResumeConfig) (ports.ChatConversation, error) {
	cfg.ReconnectOnly = true
	return d.Resume(ctx, cfg)
}
func (d *Driver) open(ctx context.Context, cfg ports.ChatStartConfig, nativeID string, reconnectOnly bool) (ports.ChatConversation, error) {
	mode, err := validateConfig(cfg)
	if err != nil {
		return nil, err
	}
	mcp, err := nativeMCP(cfg.MCPServers)
	if err != nil {
		return nil, err
	}
	owner, _ := json.Marshal([]any{filepath.Clean(cfg.WorkspacePath), domain.HarnessZCode, cfg.ProviderScopeID, mode, mcp})
	fingerprint := sha256.Sum256(owner)
	hostCfg := persistenthost.Config{SessionID: string(cfg.SessionID), DataDir: cfg.DataDir, Workdir: cfg.WorkspacePath, Protocol: persistenthost.ProtocolZCode, OwnershipFingerprint: hex.EncodeToString(fingerprint[:]), ReconnectOnly: reconnectOnly,
		Prepare: func(prepareCtx context.Context) (persistenthost.PreparedProvider, error) {
			env := cfg.Env
			if cfg.PrepareEnv != nil {
				var prepareErr error
				env, prepareErr = cfg.PrepareEnv(prepareCtx)
				if prepareErr != nil {
					return persistenthost.PreparedProvider{}, prepareErr
				}
			}
			copied := make(map[string]string, len(env)+2)
			for k, v := range env {
				copied[k] = v
			}
			copied["AO_SESSION_ID"] = string(cfg.SessionID)
			copied["AO_DATA_DIR"] = cfg.DataDir
			if nativeID != "" {
				if err := zcode.ValidateProtocolRestore(prepareCtx, cfg.WorkspacePath, nativeID, copied); err != nil {
					return persistenthost.PreparedProvider{}, fmt.Errorf("%w: %w", ports.ErrChatResumeFailed, err)
				}
			}
			binary, err := d.plugin.PrepareProtocol(prepareCtx, ports.WorkspaceHookConfig{DataDir: cfg.DataDir, WorkspacePath: cfg.WorkspacePath, SessionID: string(cfg.SessionID), SystemPrompt: cfg.SystemPrompt, Env: copied})
			if err != nil {
				return persistenthost.PreparedProvider{}, err
			}
			return persistenthost.PreparedProvider{Argv: []string{binary, "app-server"}, Env: processenv.Merge(copied)}, nil
		},
	}
	transport, err := persistenthost.ConnectOrStart(ctx, hostCfg)
	if err != nil {
		if errors.Is(err, persistenthost.ErrNotRunning) {
			return nil, ports.ErrChatHostNotRunning
		}
		if errors.Is(err, persistenthost.ErrOwnershipInconclusive) || errors.Is(err, persistenthost.ErrIncompatible) || errors.Is(err, persistenthost.ErrAttached) {
			return nil, fmt.Errorf("%w: %w", ports.ErrChatRecoveryInconclusive, err)
		}
		return nil, err
	}
	c := newConversation(cfg, transport, d.log)
	fail := func(err error) (ports.ChatConversation, error) {
		if transport.Reconnected {
			_ = c.Close()
		} else {
			_ = c.Terminate()
		}
		return nil, err
	}
	if nativeID == "" && transport.Reconnected {
		return fail(fmt.Errorf("%w: fresh start found an existing provider", ports.ErrChatRecoveryInconclusive))
	}
	c.mu.Lock()
	c.nativeID = nativeID
	c.mu.Unlock()
	if transport.Reconnected {
		var snapshot nativeSnapshot
		if err := c.call(ctx, "session/read", map[string]any{"sessionId": nativeID}, &snapshot); err != nil {
			return fail(fmt.Errorf("%w: %w", ports.ErrChatRecoveryInconclusive, err))
		}
		if err := checkSnapshot(snapshot, nativeID, cfg.WorkspacePath); err != nil {
			return fail(err)
		}
		if err := d.plugin.RefreshProtocolInstructions(ctx, ports.WorkspaceHookConfig{DataDir: cfg.DataDir, SessionID: string(cfg.SessionID), SystemPrompt: cfg.SystemPrompt}); err != nil {
			return fail(err)
		}
		if err := c.rehydrate(ctx, snapshot.Runtime.ActiveTurnID); err != nil {
			return fail(err)
		}
		if err := c.restorePending(snapshot.Runtime.PendingRequestIDs); err != nil {
			return fail(err)
		}
	} else {
		method := "session/create"
		params := map[string]any{"workspace": map[string]string{"workspacePath": cfg.WorkspacePath, "workspaceKey": cfg.WorkspacePath}, "mode": mode, "titleGenerationEnabled": false, "mcpServers": mcp}
		if nativeID != "" {
			method = "session/resume"
			delete(params, "mode")
			delete(params, "titleGenerationEnabled")
			params["sessionId"] = nativeID
		}
		var snapshot nativeSnapshot
		if err := c.call(ctx, method, params, &snapshot); err != nil {
			return fail(fmt.Errorf("%w: %w", ports.ErrChatResumeFailed, err))
		}
		if err := checkSnapshot(snapshot, nativeID, cfg.WorkspacePath); err != nil {
			return fail(err)
		}
		c.mu.Lock()
		c.nativeID = snapshot.Session.SessionID
		c.mu.Unlock()
		if nativeID != "" {
			if err := c.call(ctx, "session/setMode", map[string]any{"sessionId": nativeID, "mode": mode}, nil); err != nil {
				return fail(err)
			}
		}
		if err := c.rehydrate(ctx, ""); err != nil {
			return fail(err)
		}
		c.clearPending()
	}
	var subscription struct {
		Events []nativeEvent `json:"events"`
	}
	subCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := c.call(subCtx, "session/subscribe", map[string]any{"sessionId": c.ProviderConversationID(), "deliveryKind": "web-remote-replayable"}, &subscription); err != nil {
		return fail(err)
	}
	c.emit(ports.ChatEvent{Kind: ports.ChatEventControllerState, ControllerState: ports.ChatControllerReady})
	return c, nil
}
func checkSnapshot(s nativeSnapshot, id, workspace string) error {
	if s.Protocol.Name != "ZCode Protocol" || s.Protocol.Version != 1 {
		return ports.ErrChatDriverIncompatible
	}
	if s.Session.SessionID == "" || (id != "" && s.Session.SessionID != id) {
		return ports.ErrChatResumeFailed
	}
	actual, err := filepath.EvalSymlinks(s.Session.Workspace.WorkspacePath)
	if err != nil {
		return err
	}
	expected, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		return err
	}
	if actual != expected {
		return fmt.Errorf("%w: native session belongs to another worktree", ports.ErrChatResumeFailed)
	}
	if s.Session.ArchivedAt != nil {
		return fmt.Errorf("%w: native session is archived", ports.ErrChatResumeFailed)
	}
	return nil
}
