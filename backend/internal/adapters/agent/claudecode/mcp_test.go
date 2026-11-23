package claudecode

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// The worker path offers Claude Code AO's own tool server. Claude Code takes
// MCP servers through --mcp-config, which registers them for this session
// alone: nothing is written to the project's .mcp.json, so the user is never
// asked to approve a server they did not add.

func TestMCPServerArgsRegistersTheServerWithAnInlineDocument(t *testing.T) {
	plugin := &Plugin{}
	args, err := plugin.MCPServerArgs([]ports.MCPServerConfig{{
		Name:    "ao",
		Command: "/opt/ao/bin/ao",
		Args:    []string{"mcp"},
		Env:     map[string]string{"AO_SESSION_ID": "mer-1"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(args) != 2 || args[0] != "--mcp-config" {
		t.Fatalf("args = %#v, want --mcp-config with a document", args)
	}
	var document struct {
		Servers map[string]struct {
			Command string            `json:"command"`
			Args    []string          `json:"args"`
			Env     map[string]string `json:"env"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(args[1]), &document); err != nil {
		t.Fatalf("the document is not JSON: %v", err)
	}
	server, ok := document.Servers["ao"]
	if !ok {
		t.Fatalf("document %#v has no ao server", document)
	}
	if server.Command != "/opt/ao/bin/ao" || !reflect.DeepEqual(server.Args, []string{"mcp"}) {
		t.Fatalf("server = %#v, want the ao binary with the mcp argument", server)
	}
	if server.Env["AO_SESSION_ID"] != "mer-1" {
		t.Fatalf("env = %#v, want the session id", server.Env)
	}
}

// The document names no credential: the server's environment carries only the
// pointers it needs to find AO's own state.
func TestMCPServerArgsCarriesNoCredential(t *testing.T) {
	plugin := &Plugin{}
	args, err := plugin.MCPServerArgs([]ports.MCPServerConfig{{
		Name: "ao", Command: "ao", Env: map[string]string{"AO_SESSION_ID": "mer-1"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"GMI_API_KEY", "API_KEY", "TOKEN", "SECRET"} {
		if strings.Contains(args[1], forbidden) {
			t.Fatalf("the document names %s: %s", forbidden, args[1])
		}
	}
}

func TestMCPServerArgsRejectsAServerWithoutACommand(t *testing.T) {
	plugin := &Plugin{}
	if _, err := plugin.MCPServerArgs([]ports.MCPServerConfig{{Name: "ao"}}); err == nil {
		t.Fatal("MCPServerArgs accepted a server without a command")
	}
	if _, err := plugin.MCPServerArgs([]ports.MCPServerConfig{{Command: "ao"}}); err == nil {
		t.Fatal("MCPServerArgs accepted a server without a name")
	}
}

// An allow rule only pre-approves a tool, so adding the server's tools leaves
// the agent's own tools alone.
func TestClaudeAllowedToolsPreApprovesOnlyTheServersTools(t *testing.T) {
	allowed := claudeAllowedTools([]string{"Read"}, []ports.MCPServerConfig{{Name: "ao", Command: "ao"}})
	if !reflect.DeepEqual(allowed, []string{"Read", "mcp__ao__*"}) {
		t.Fatalf("allowed = %#v, want the session's tools plus the server's", allowed)
	}
	if got := claudeAllowedTools([]string{"Read"}, nil); !reflect.DeepEqual(got, []string{"Read"}) {
		t.Fatalf("allowed without a server = %#v, want the session's tools untouched", got)
	}
}

func TestGetLaunchCommandAddsNoMCPFlagsWithoutAServer(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "claude"}
	cmd, err := plugin.GetLaunchCommand(context.Background(), ports.LaunchConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if contains(cmd, "--mcp-config") {
		t.Fatalf("command carries --mcp-config without a server: %#v", cmd)
	}
}

func TestGetLaunchCommandCarriesTheMCPServer(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "claude"}
	cmd, err := plugin.GetLaunchCommand(context.Background(), ports.LaunchConfig{
		MCPServers: []ports.MCPServerConfig{{Name: "ao", Command: "/opt/ao/bin/ao", Args: []string{"mcp"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !containsSubsequence(cmd, []string{"--mcp-config", `{"mcpServers":{"ao":{"args":["mcp"],"command":"/opt/ao/bin/ao"}}}`}) {
		t.Fatalf("command %#v does not register the server", cmd)
	}
	// The server's tools are pre-approved, so an unattended worker does not
	// stop on a permission prompt for a tool AO put in its hands.
	if !containsSubsequence(cmd, []string{"--allowedTools", "mcp__ao__*"}) {
		t.Fatalf("command %#v does not pre-approve the server's tools", cmd)
	}
}
