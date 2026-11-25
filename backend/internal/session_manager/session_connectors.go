package sessionmanager

import (
	"context"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/mcpserver"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// SessionConnectorsInput names the session that asks for the tool servers of its connectors.
type SessionConnectorsInput struct {
	SessionID string
	ProjectID domain.ProjectID
	Harness   domain.AgentHarness
	Workspace string
	Env       map[string]string
}

// SetSessionConnectors supplies the connectors a new session gets after the ao tool server.
func (m *Manager) SetSessionConnectors(servers func(context.Context, SessionConnectorsInput) []ports.ChatMCPServerConfig) {
	m.connectorsMu.Lock()
	defer m.connectorsMu.Unlock()
	m.connectors = servers
}

func (m *Manager) sessionConnectors(ctx context.Context, in SessionConnectorsInput) []ports.ChatMCPServerConfig {
	m.connectorsMu.Lock()
	servers := m.connectors
	m.connectorsMu.Unlock()
	if servers == nil || in.SessionID == "" {
		return nil
	}
	return servers(ctx, in)
}

func (m *Manager) sessionMCPServers(ctx context.Context, harness domain.AgentHarness, projectID domain.ProjectID, workspace string, env map[string]string) []ports.ChatMCPServerConfig {
	if harness == domain.HarnessUnreal {
		return nil
	}
	return append(m.aoMCPServers(ctx, harness, env), m.connectorServers(ctx, harness, projectID, workspace, env)...)
}

func (m *Manager) connectorServers(ctx context.Context, harness domain.AgentHarness, projectID domain.ProjectID, workspace string, env map[string]string) []ports.ChatMCPServerConfig {
	executable, err := m.executable()
	if err != nil || !filepath.IsAbs(executable) {
		m.logger.Warn("session gets no connectors: the ao executable is not available")
		return nil
	}
	connectors := m.sessionConnectors(ctx, SessionConnectorsInput{
		SessionID: env[EnvSessionID], ProjectID: projectID, Harness: harness, Workspace: workspace, Env: env,
	})
	servers := make([]ports.ChatMCPServerConfig, 0, len(connectors))
	command, args := mcpServerCommand(executable)
	for _, connector := range connectors {
		via := ports.ChatMCPServerConfig{Name: connector.Name, Type: "stdio", Command: command, Args: args, Env: map[string]string{
			mcpserver.EnvRelayURL: connector.URL, mcpserver.EnvRelayAuthorization: connector.Headers["Authorization"],
		}}
		if connector.Type == "stdio" {
			via.Env = map[string]string{mcpserver.EnvLaunchURL: connector.URL, mcpserver.EnvRelayAuthorization: connector.Headers["Authorization"]}
			connector = via
		} else {
			connector.Relay = &via
		}
		servers = append(servers, connector)
	}
	return servers
}

func (m *Manager) terminalConnectors(ctx context.Context, agent ports.Agent, harness domain.AgentHarness, id domain.SessionID, projectID domain.ProjectID, workspace string, argv []string, env map[string]string) []string {
	adapter, ok := agent.(ports.AgentConnectorArgs)
	dir := m.systemPromptDir(id)
	if !ok || len(argv) == 0 || dir == "" {
		return argv
	}
	servers := m.connectorServers(ctx, harness, projectID, workspace, env)
	if len(servers) == 0 {
		return argv
	}
	token := ""
	for _, server := range servers {
		for _, value := range []string{server.Headers["Authorization"], server.Env[mcpserver.EnvRelayAuthorization]} {
			if bearer, found := strings.CutPrefix(value, "Bearer "); found {
				token = bearer
			}
		}
	}
	args, extra, err := adapter.ConnectorArgs(ctx, servers, token, filepath.Join(dir, "connectors.json"))
	if err != nil {
		m.logger.Warn("terminal session gets no connectors", "session", id, "error", err)
		return argv
	}
	maps.Copy(env, extra)
	return slices.Concat(argv[:1], args, argv[1:])
}
