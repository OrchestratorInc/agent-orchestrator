package opencodeacp_test

import (
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/opencode"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Registering the capability is what turns the in-session "Open Chat" /
// "Open Terminal UI" action on: Session Manager type-asserts the adapter and
// otherwise refuses the switch with ErrInterfaceHandoffUnsupported. Losing the
// assertion would disable switching silently rather than fail a build, so
// assert it here the way piacp asserts Pi's deliberate absence.
//
// The identity claim behind it is covered live by TestLiveOpenCodeTUIToChatHandoff
// and TestLiveOpenCodeChatToTUIHandoff.
func TestOpenCodeDeclaresInterfaceHandoff(t *testing.T) {
	var plugin any = opencode.New()
	if _, ok := plugin.(ports.AgentInterfaceHandoff); !ok {
		t.Fatal("opencode no longer declares TUI/Chat handoff, so interface switching is disabled")
	}
}
