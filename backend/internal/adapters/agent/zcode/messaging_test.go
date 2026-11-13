package zcode

import (
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestTerminalMessagingDeclaresOneShot(t *testing.T) {
	var agent ports.Agent = New()
	declarer, ok := agent.(ports.AgentTerminalMessagingDeclarer)
	if !ok {
		t.Fatal("zcode does not implement ports.AgentTerminalMessagingDeclarer")
	}
	if got := declarer.TerminalMessaging(); got != ports.AgentTerminalMessagingOneShot {
		t.Fatalf("TerminalMessaging() = %q, want %q", got, ports.AgentTerminalMessagingOneShot)
	}
}
