package sessionmanager

import (
	"context"
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
		return []ports.ChatMCPServerConfig{{Name: "linear", Type: "http", URL: "http://127.0.0.1:1/mcp/s/mer-1/connectors/linear",
			Headers: map[string]string{"Authorization": "Bearer t"}}}
	})
	got := m.sessionMCPServers(context.Background(), domain.HarnessClaudeCode, "p1", "/work", env)
	if len(got) != 2 || got[0].Name != "ao" || got[1].Name != "linear" {
		t.Fatalf("with connectors = %+v", got)
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
