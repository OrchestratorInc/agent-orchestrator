package acp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var _ ports.ChatIsolatedForker = (*Driver)(nil)

// ForkIntoHost copies retained provider context without changing the parent host.
func (d *Driver) ForkIntoHost(ctx context.Context, sourceID, lastTurnID string, cfg ports.ChatStartConfig) (ports.ChatConversation, error) {
	if sourceID == "" || lastTurnID != "" {
		return nil, fmt.Errorf("%w: ACP forks require a source session and copy current context only", ports.ErrChatCapabilityUnavailable)
	}
	if d.cfg.DecodeProviderConversationID != nil {
		var err error
		sourceID, err = d.cfg.DecodeProviderConversationID(sourceID)
		if err != nil {
			return nil, err
		}
	}
	if !filepath.IsAbs(cfg.WorkspacePath) {
		return nil, fmt.Errorf("workspace path must be absolute, got %q", cfg.WorkspacePath)
	}
	settings := ports.ChatTurnSettings{Model: cfg.Model, Effort: cfg.Effort, Approval: cfg.Permissions}
	if d.cfg.ValidateTurnSettings != nil {
		if err := d.cfg.ValidateTurnSettings(cfg.Permissions, settings); err != nil {
			return nil, err
		}
	}
	launchCfg := LaunchConfig{
		SessionID: cfg.SessionID, DataDir: cfg.DataDir, WorkspacePath: cfg.WorkspacePath,
		Env: cfg.Env, Model: cfg.Model, Permissions: cfg.Permissions,
		SystemPrompt: cfg.SystemPrompt, ProviderScopeID: cfg.ProviderScopeID,
	}
	conv, init, live, err := d.connect(ctx, launchCfg, cfg.PrepareEnv)
	if err != nil {
		return nil, err
	}
	if live != nil {
		conv.discard()
		return nil, fmt.Errorf("%w: destination ACP host already owns a session", ports.ErrChatRecoveryInconclusive)
	}
	if !conversationCapabilities(d.cfg.Capabilities, init).Has(ports.ChatCapabilityForkCurrent) {
		conv.discard()
		return nil, fmt.Errorf("%w: ACP agent does not advertise resumable session/fork", ports.ErrChatCapabilityUnavailable)
	}
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
	// The SDK gives stable and unstable MCP unions distinct Go types but the
	// same wire shape, including HTTP/SSE transport credentials.
	encoded, err := json.Marshal(mcpServers)
	var forkMCP []acpsdk.UnstableMcpServer
	if err == nil {
		err = json.Unmarshal(encoded, &forkMCP)
	}
	if err != nil {
		conv.discard()
		return nil, fmt.Errorf("encode ACP fork MCP servers: %w", err)
	}
	meta := map[string]any(nil)
	if d.cfg.SessionMeta != nil {
		meta = d.cfg.SessionMeta(launchCfg)
	}
	operationCtx := ctx
	var release func()
	if cfg.AcquireForkSource != nil {
		ctx, release, err = cfg.AcquireForkSource(ctx)
		if err != nil {
			conv.discard()
			return nil, err
		}
	}
	// Cancellation tears down the destination host before releasing the source.
	forkCtx, cancel := context.WithTimeout(ctx, handshakeTimeout)
	defer cancel()
	resp, err := conv.conn.UnstableForkSession(forkCtx, acpsdk.UnstableForkSessionRequest{
		SessionId: acpsdk.SessionId(sourceID), Cwd: cfg.WorkspacePath,
		Meta: meta, AdditionalDirectories: additional, McpServers: forkMCP,
	})
	if release != nil {
		defer func() {
			if release != nil {
				release()
			}
		}()
	}
	if err != nil {
		disconnected := false
		select {
		case <-conv.conn.Done():
			disconnected = true
		default:
		}
		conv.discard()
		if disconnected || errors.Is(err, context.DeadlineExceeded) || forkCtx.Err() != nil {
			return nil, fmt.Errorf("%w: ACP fork outcome is unknown: %w", ports.ErrChatRecoveryInconclusive, errors.Join(err, forkCtx.Err()))
		}
		return nil, normalizeACPError("ACP session/fork", err)
	}
	childID := string(resp.SessionId)
	if childID == "" || childID == sourceID {
		conv.discard()
		return nil, fmt.Errorf("ACP session/fork returned no independent session id")
	}
	conv.unadoptedForkID = childID
	if release != nil {
		release()
		release = nil
	}
	ctx = operationCtx
	reportedID := childID
	if d.cfg.EncodeProviderConversationID != nil {
		reportedID = d.cfg.EncodeProviderConversationID(childID)
	}
	if cfg.RecordFork != nil {
		if err := cfg.RecordFork(context.WithoutCancel(operationCtx), reportedID); err != nil {
			conv.discard()
			return nil, fmt.Errorf("record ACP fork: %w", err)
		}
	}
	if err := ctx.Err(); err != nil {
		conv.discard()
		return nil, err
	}
	// Some adapters persist a fork without activating it. Restore the child on
	// this same host before applying settings or accepting its first prompt.
	child, err := d.resumeSession(ctx, conv, init, launchCfg, ports.ChatResumeConfig{
		SessionID: cfg.SessionID, ProviderConversationID: childID,
		DataDir: cfg.DataDir, WorkspacePath: cfg.WorkspacePath, Env: cfg.Env,
		Model: cfg.Model, Effort: cfg.Effort, Permissions: cfg.Permissions,
		SystemPrompt: cfg.SystemPrompt, AdditionalDirectories: cfg.AdditionalDirectories,
		MCPServers: cfg.MCPServers, ProviderScopeID: cfg.ProviderScopeID,
	}, reportedID)
	if err != nil {
		return nil, err
	}
	conv.unadoptedForkID = ""
	return child, nil
}

// DeleteFork is called only for side IDs registered by AO after their host stops.
func (c *conversation) DeleteFork(ctx context.Context, id string) error {
	if c.decodeConversationID != nil {
		var err error
		id, err = c.decodeConversationID(id)
		if err != nil {
			return err
		}
	}
	c.mu.Lock()
	parentID := c.sessionID
	c.mu.Unlock()
	if id == "" || id == parentID {
		return fmt.Errorf("refusing to delete the active ACP conversation")
	}
	if !c.canDeleteFork {
		return fmt.Errorf("%w: ACP agent does not advertise session/delete", ports.ErrChatCapabilityUnavailable)
	}
	_, err := c.conn.UnstableDeleteSession(ctx, acpsdk.UnstableDeleteSessionRequest{SessionId: acpsdk.SessionId(id)})
	if isACPResourceNotFound(err) {
		return nil
	}
	if err != nil {
		return normalizeACPError("ACP session/delete", err)
	}
	return nil
}
