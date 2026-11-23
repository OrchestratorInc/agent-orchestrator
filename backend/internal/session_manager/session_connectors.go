package sessionmanager

import (
	"context"
	"path/filepath"

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

// SetSessionConnectors supplies the connectors a session gets after the ao tool
// server. Sessions already running keep what they started with.
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
	servers := m.aoMCPServers(ctx, harness, env)
	connectors := m.sessionConnectors(ctx, SessionConnectorsInput{
		SessionID: env[EnvSessionID], ProjectID: projectID, Harness: harness, Workspace: workspace, Env: env,
	})
	executable, err := m.executable()
	for i := range connectors {
		if connectors[i].Type != "http" || err != nil || !filepath.IsAbs(executable) {
			continue
		}
		command, args := mcpServerCommand(executable)
		connectors[i].Relay = &ports.ChatMCPServerConfig{
			Name: connectors[i].Name, Type: "stdio", Command: command, Args: args,
			Env: map[string]string{
				mcpserver.EnvRelayURL:           connectors[i].URL,
				mcpserver.EnvRelayAuthorization: connectors[i].Headers["Authorization"],
			},
		}
	}
	return append(servers, connectors...)
}
