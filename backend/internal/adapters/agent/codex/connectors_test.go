package codex

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestConnectorArgsPassTheTokenThroughTheEnvironment(t *testing.T) {
	servers := []ports.ChatMCPServerConfig{
		{Name: "linear", Type: "http", URL: "http://127.0.0.1:3011/mcp/s/mer-1/connectors/linear", Headers: map[string]string{"Authorization": "Bearer secret-token"}},
		{Name: "paper", Type: "stdio", Command: "/opt/ao mcp/ao-mcp", Env: map[string]string{
			"AO_MCP_LAUNCH_URL": "http://127.0.0.1:3011/mcp/s/mer-1/connectors/paper/launch", "AO_MCP_RELAY_AUTHORIZATION": "Bearer secret-token",
		}},
	}
	args, env, err := (&Plugin{}).ConnectorArgs(context.Background(), servers, "secret-token", "")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"-c", `mcp_servers.linear.url="http://127.0.0.1:3011/mcp/s/mer-1/connectors/linear"`,
		"-c", `mcp_servers.linear.bearer_token_env_var="AO_CONNECTOR_TOKEN"`,
		"-c", `mcp_servers.paper.command="/opt/ao mcp/ao-mcp"`,
		"-c", `mcp_servers.paper.args=[]`,
		"-c", `mcp_servers.paper.env.AO_MCP_LAUNCH_URL="http://127.0.0.1:3011/mcp/s/mer-1/connectors/paper/launch"`,
		"-c", `mcp_servers.paper.env_vars=["AO_CONNECTOR_TOKEN"]`,
	}
	if !slices.Equal(args, want) {
		t.Fatalf("args =\n%s", strings.Join(args, "\n"))
	}
	if strings.Contains(strings.Join(args, " "), "secret-token") || env[config.EnvConnectorToken] != "secret-token" || len(env) != 1 {
		t.Fatalf("args = %v, env = %v", args, env)
	}
}
