package codex

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// The worker path offers Codex AO's own tool server. Codex takes MCP servers
// as config overrides on the command line, so the server is registered for
// this session alone: nothing is written to ~/.codex/config.toml, and the
// servers already configured there keep working.

func TestMCPServerArgsRegistersTheServerAsConfigOverrides(t *testing.T) {
	plugin := &Plugin{}
	args, err := plugin.MCPServerArgs([]ports.MCPServerConfig{{
		Name:    "ao",
		Command: "/opt/ao/bin/ao",
		Args:    []string{"mcp"},
		Env:     map[string]string{"AO_SESSION_ID": "mer-1", "AO_DATA_DIR": "/ao/data"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"-c", "mcp_servers.ao.command='/opt/ao/bin/ao'",
		"-c", "mcp_servers.ao.default_tools_approval_mode='approve'",
		"-c", "mcp_servers.ao.args=['mcp']",
		"-c", "mcp_servers.ao.env.AO_DATA_DIR='/ao/data'",
		"-c", "mcp_servers.ao.env.AO_SESSION_ID='mer-1'",
	}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("args = %#v, want %#v", args, want)
	}
}

// An unattended worker must not stop on a permission prompt for a tool AO
// put in its hands, so the server is pre-approved.
func TestMCPServerArgsApprovesTheServersTools(t *testing.T) {
	plugin := &Plugin{}
	args, err := plugin.MCPServerArgs([]ports.MCPServerConfig{{Name: "ao", Command: "ao"}})
	if err != nil {
		t.Fatal(err)
	}
	if !containsSubsequence(args, []string{"-c", "mcp_servers.ao.default_tools_approval_mode='approve'"}) {
		t.Fatalf("args %#v do not approve the server's tools", args)
	}
}

func TestMCPServerArgsKeepsTheEnvironmentOrderStable(t *testing.T) {
	plugin := &Plugin{}
	first, err := plugin.MCPServerArgs([]ports.MCPServerConfig{{
		Name: "ao", Command: "ao", Env: map[string]string{"B": "2", "A": "1", "C": "3"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	second, err := plugin.MCPServerArgs([]ports.MCPServerConfig{{
		Name: "ao", Command: "ao", Env: map[string]string{"C": "3", "B": "2", "A": "1"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("args differ between runs: %#v and %#v", first, second)
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

// A session with no server gets no MCP flags at all, so a Codex install
// without the server behaves exactly as it did before.
func TestGetLaunchCommandAddsNoMCPFlagsWithoutAServer(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "codex"}
	cmd, err := plugin.GetLaunchCommand(context.Background(), ports.LaunchConfig{})
	if err != nil {
		t.Fatal(err)
	}
	for _, arg := range cmd {
		if strings.HasPrefix(arg, "mcp_servers.") {
			t.Fatalf("command carries an MCP override without a server: %#v", cmd)
		}
	}
}

func TestGetLaunchCommandCarriesTheMCPServerOverrides(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "codex"}
	cmd, err := plugin.GetLaunchCommand(context.Background(), ports.LaunchConfig{
		MCPServers: []ports.MCPServerConfig{{Name: "ao", Command: "/opt/ao/bin/ao", Args: []string{"mcp"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !containsSubsequence(cmd, []string{"-c", "mcp_servers.ao.command='/opt/ao/bin/ao'"}) {
		t.Fatalf("command %#v does not register the server", cmd)
	}
}
