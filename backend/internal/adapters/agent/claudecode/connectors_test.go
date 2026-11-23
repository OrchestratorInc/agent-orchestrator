package claudecode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/pkg/agentruntime"
)

func TestConnectorArgsKeepTheTokenInAPrivateFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "prompts", "mer-1", "connectors.json")
	servers := []ports.ChatMCPServerConfig{
		{Name: "linear", Type: "http", URL: "http://127.0.0.1:3011/mcp/s/mer-1/connectors/linear", Headers: map[string]string{"Authorization": "Bearer secret-token"}},
		{Name: "paper", Type: "stdio", Command: "/opt/ao-mcp", Env: map[string]string{"AO_MCP_LAUNCH_URL": "http://127.0.0.1:3011/mcp/s/mer-1/connectors/paper/launch"}},
	}
	args, env, err := (&Plugin{}).ConnectorArgs(servers, "secret-token", file)
	if err != nil || len(env) != 0 || !slices.Equal(args, []string{"--mcp-config", file}) {
		t.Fatalf("args = %v, env = %v, err = %v", args, env, err)
	}
	info, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("file mode = %v", info.Mode().Perm())
	}
	raw, _ := os.ReadFile(file)
	var got struct {
		MCPServers map[string]connectorServer `json:"mcpServers"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.MCPServers["linear"].Type != "http" || got.MCPServers["linear"].Headers["Authorization"] != "Bearer secret-token" ||
		got.MCPServers["paper"].Command != "/opt/ao-mcp" || got.MCPServers["paper"].Type != "stdio" {
		t.Fatalf("file = %s", raw)
	}

	launch, err := agentruntime.BuildLaunchCommand(agentruntime.LaunchConfig{Harness: agentruntime.HarnessClaudeCode, Binary: "claude", SessionID: "mer-1", Prompt: "do the task"})
	if err != nil {
		t.Fatal(err)
	}
	argv := slices.Concat(launch[:1], args, launch[1:])
	if strings.Contains(strings.Join(argv, " "), "secret-token") || !strings.HasPrefix(argv[3], "-") {
		t.Fatalf("argv = %v", argv)
	}
}
