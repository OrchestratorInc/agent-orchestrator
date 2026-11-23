package mcpserver

import (
	"context"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/providerkeys"
)

// A chat session offers every group the server can: its thread can show a
// rendered page, and it has a computer-use group when the user asked for one.
func TestMCPOffersEveryGroupWhenNoGroupIsNamed(t *testing.T) {
	t.Setenv(config.EnvMCPToolGroups, "")
	if !mcpToolGroupEnabled(config.MCPToolGroupHTML) ||
		!mcpToolGroupEnabled(config.MCPToolGroupCUA) ||
		!mcpToolGroupEnabled(config.MCPToolGroupGMI) {
		t.Fatal("a group is off even though no group was named")
	}
}

// A worker session has no chat thread, so it names the groups it can use and
// the server offers those alone.
func TestMCPOffersOnlyTheNamedGroups(t *testing.T) {
	t.Setenv(config.EnvMCPToolGroups, config.MCPToolGroupGMI)
	if !mcpToolGroupEnabled(config.MCPToolGroupGMI) {
		t.Fatal("the named group is off")
	}
	if mcpToolGroupEnabled(config.MCPToolGroupHTML) || mcpToolGroupEnabled(config.MCPToolGroupCUA) {
		t.Fatal("a group that was not named is on")
	}
}

func TestMCPGroupNamesAreMatchedWithoutRegardToCaseOrSpacing(t *testing.T) {
	t.Setenv(config.EnvMCPToolGroups, " GMI , html ")
	if !mcpToolGroupEnabled(config.MCPToolGroupGMI) || !mcpToolGroupEnabled(config.MCPToolGroupHTML) {
		t.Fatal("a named group is off")
	}
	if mcpToolGroupEnabled(config.MCPToolGroupCUA) {
		t.Fatal("a group that was not named is on")
	}
}

// A worker names the GMI group, so the server offers the GMI tools and leaves
// the HTML tools out: they publish into a thread the worker does not have.
func TestWorkerServerOffersGMIWithoutTheHTMLTools(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AO_DATA_DIR", dir)
	t.Setenv(config.EnvMCPToolGroups, config.MCPToolGroupGMI)
	if err := providerkeys.Save(context.Background(), dir, gmiAPIKeyName, "gmi-key"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	s := &server{}
	state := &mcpState{}
	if tools := s.htmlTools(); tools != nil {
		t.Fatalf("htmlTools = %d tools for a worker, want none", len(tools))
	}
	if tools := s.gmiTools(state); len(tools) != len(gmiToolDefs) {
		t.Fatalf("gmiTools = %d tools, want %d", len(tools), len(gmiToolDefs))
	}
}

// The chat path is unchanged: with no group named, the HTML tools are offered.
func TestChatServerStillOffersTheHTMLTools(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AO_DATA_DIR", dir)
	t.Setenv(config.EnvMCPToolGroups, "")
	s := &server{}
	if tools := s.htmlTools(); len(tools) != len(mcpTools) {
		t.Fatalf("htmlTools = %d tools, want %d", len(tools), len(mcpTools))
	}
}

// The GMI Cloud tools spend money, so they are offered only to a server that
// was told it may use them.
func TestTheGMIGroupMustBeNamed(t *testing.T) {
	for _, groups := range []string{"", "html,cua"} {
		t.Setenv(config.EnvMCPToolGroups, groups)
		if mcpToolGroupNamed(config.MCPToolGroupGMI) {
			t.Fatalf("the GMI group is on for AO_MCP_TOOLS=%q", groups)
		}
	}
	t.Setenv(config.EnvMCPToolGroups, "html,gmi")
	if !mcpToolGroupNamed(config.MCPToolGroupGMI) {
		t.Fatal("the GMI group is off even though it was named")
	}
}
