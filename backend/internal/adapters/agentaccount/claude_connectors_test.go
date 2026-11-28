package agentaccount

import (
	"reflect"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// The shape `claude mcp list` prints (Claude Code 2.1.331): a health-check
// banner, then one "<name>: <target> - <status>" line per server.
func TestParseClaudeMCPListKeepsClaudeAIConnectors(t *testing.T) {
	out := []byte(`Checking MCP server health...

claude.ai Gmail: https://gmail.mcp.claude.com/mcp - ✔ Connected
claude.ai Google Calendar: https://gcal.mcp.claude.com/mcp - ✔ Connected
claude.ai AI Producer by OpusClip: https://mcp.opus.pro/mcp - ! Needs authentication
claude.ai Broken - Thing: https://broken.example/mcp - ✗ Failed to connect
desktop-commander: npx -y @wonderwhy-er/desktop-commander - ✔ Connected
magic: https://magic.example/mcp (HTTP) - ✗ Failed to connect
claude.ai Odd: https://odd.example/mcp - ? Something new
`)
	got := parseClaudeMCPList(out)
	want := []domain.AgentConnector{
		{Name: "Gmail", Status: domain.AgentConnectorConnected},
		{Name: "Google Calendar", Status: domain.AgentConnectorConnected},
		{Name: "AI Producer by OpusClip", Status: domain.AgentConnectorNeedsAuth},
		{Name: "Broken - Thing", Status: domain.AgentConnectorFailed},
		{Name: "Odd", Status: domain.AgentConnectorUnknown},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("connectors = %+v\nwant         %+v", got, want)
	}
}

func TestParseClaudeMCPListWithoutConnectors(t *testing.T) {
	for _, out := range []string{
		"",
		"No MCP servers configured. Use `claude mcp add` to add a server.\n",
		"Checking MCP server health...\n\nmagic: https://magic.example/mcp (HTTP) - ✔ Connected\n",
	} {
		if got := parseClaudeMCPList([]byte(out)); len(got) != 0 {
			t.Fatalf("parse(%q) = %+v, want none", out, got)
		}
	}
}
