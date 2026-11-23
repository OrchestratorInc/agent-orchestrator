package sessionmanager

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/mcpserver"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestSessionMCPServersAddConnectorsAfterTheAOServer(t *testing.T) {
	st := newFakeStore()
	m := New(Deps{
		Runtime: &fakeRuntime{}, Agents: singleAgent{agent: &recordingAgent{}}, Workspace: &fakeWorkspace{},
		Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st}, DataDir: t.TempDir(),
		LookPath: func(string) (string, error) { return "/bin/true", nil },
	})
	env := map[string]string{EnvSessionID: "mer-1"}
	if got := m.sessionMCPServers(context.Background(), domain.HarnessClaudeCode, "p1", "/work", env); len(got) != 1 || got[0].Name != "ao" {
		t.Fatalf("without connectors = %+v", got)
	}
	var asked SessionConnectorsInput
	m.SetSessionConnectors(func(_ context.Context, in SessionConnectorsInput) []ports.ChatMCPServerConfig {
		asked = in
		return []ports.ChatMCPServerConfig{
			{Name: "linear", Type: "http", URL: "http://127.0.0.1:1/mcp/s/mer-1/connectors/linear", Headers: map[string]string{"Authorization": "Bearer t"}},
			{Name: "paper", Type: "stdio", URL: "http://127.0.0.1:1/mcp/s/mer-1/connectors/paper/launch", Headers: map[string]string{"Authorization": "Bearer t"}},
		}
	})
	got := m.sessionMCPServers(context.Background(), domain.HarnessClaudeCode, "p1", "/work", env)
	if len(got) != 3 || got[0].Name != "ao" || got[1].Name != "linear" || got[2].Name != "paper" {
		t.Fatalf("with connectors = %+v", got)
	}
	if paper := got[2]; paper.Type != "stdio" || paper.Command != got[0].Command || paper.URL != "" || len(paper.Headers) != 0 ||
		paper.Env[mcpserver.EnvLaunchURL] != "http://127.0.0.1:1/mcp/s/mer-1/connectors/paper/launch" || paper.Env[mcpserver.EnvRelayAuthorization] != "Bearer t" {
		t.Fatalf("command connector = %+v", paper)
	}
	if relay := got[1].Relay; relay == nil || relay.Type != "stdio" || relay.Command == "" ||
		relay.Env[mcpserver.EnvRelayURL] != got[1].URL || relay.Env[mcpserver.EnvRelayAuthorization] != "Bearer t" {
		t.Fatalf("relay = %+v", got[1].Relay)
	}
	if asked.SessionID != "mer-1" || asked.ProjectID != "p1" || asked.Harness != domain.HarnessClaudeCode || asked.Workspace != "/work" {
		t.Fatalf("asked = %+v", asked)
	}
	if got := m.sessionMCPServers(context.Background(), domain.HarnessUnreal, "p1", "/work", env); got != nil {
		t.Fatalf("unreal agent got tool servers: %+v", got)
	}
	if got := m.sessionMCPServers(context.Background(), domain.HarnessCodex, "p1", "/work", nil); len(got) != 1 {
		t.Fatalf("a session with no ID got connectors: %+v", got)
	}
}

type connectorArgsAgent struct {
	*recordingAgent
	servers []ports.ChatMCPServerConfig
	token   string
	file    string
}

func (a *connectorArgsAgent) ConnectorArgs(servers []ports.ChatMCPServerConfig, token, file string) ([]string, map[string]string, error) {
	a.servers, a.token, a.file = servers, token, file
	return []string{"--tools", file}, map[string]string{"AO_CONNECTOR_TOKEN": token}, nil
}

func TestTerminalConnectorsGoAfterTheAgentBinary(t *testing.T) {
	st := newFakeStore()
	dataDir := t.TempDir()
	m := New(Deps{
		Runtime: &fakeRuntime{}, Agents: singleAgent{agent: &recordingAgent{}}, Workspace: &fakeWorkspace{},
		Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st}, DataDir: dataDir,
		LookPath: func(string) (string, error) { return "/bin/true", nil },
	})
	ctx := context.Background()
	argv := []string{"claude", "--session-id", "x", "do the task"}
	env := map[string]string{EnvSessionID: "mer-1"}
	agent := &connectorArgsAgent{recordingAgent: &recordingAgent{}}
	if got := m.terminalConnectors(ctx, agent, domain.HarnessClaudeCode, "mer-1", "p1", "/work", argv, env); len(got) != len(argv) || len(env) != 1 {
		t.Fatalf("without connectors: argv = %v, env = %v", got, env)
	}
	m.SetSessionConnectors(func(context.Context, SessionConnectorsInput) []ports.ChatMCPServerConfig {
		return []ports.ChatMCPServerConfig{
			{Name: "linear", Type: "http", URL: "http://127.0.0.1:1/mcp/s/mer-1/connectors/linear", Headers: map[string]string{"Authorization": "Bearer t-1"}},
		}
	})
	got := m.terminalConnectors(ctx, agent, domain.HarnessClaudeCode, "mer-1", "p1", "/work", argv, env)
	file := filepath.Join(dataDir, "prompts", "mer-1", "connectors.json")
	if want := []string{"claude", "--tools", file, "--session-id", "x", "do the task"}; !slices.Equal(got, want) {
		t.Fatalf("argv = %v", got)
	}
	if env["AO_CONNECTOR_TOKEN"] != "t-1" || agent.token != "t-1" || agent.file != file || len(agent.servers) != 1 || agent.servers[0].Name != "linear" {
		t.Fatalf("env = %v, agent = %+v", env, agent)
	}
	if got := m.terminalConnectors(ctx, &recordingAgent{}, domain.HarnessAider, "mer-1", "p1", "/work", argv, env); !slices.Equal(got, argv) {
		t.Fatalf("an agent without connector arguments: argv = %v", got)
	}
}
