package lifecycle

import (
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/activitydispatch"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// TestKimiActivityCoversWorkingWaitingWorkingIdle walks the #2814 sequence
// through the dispatch token Kimi's installed hooks report and lifecycle
// precedence: mid-turn tool use keeps the session active instead of parking
// on idle after a prior Stop, PermissionRequest parks it on waiting_input
// that tool traffic cannot clear, PermissionResult — installed as AO's
// canonical permission-resolved — resolves it back to active, and Stop ends
// the turn on idle.
func TestKimiActivityCoversWorkingWaitingWorkingIdle(t *testing.T) {
	m, st, _ := newManager()
	seedSignaled(st, "mer-1", domain.ActivityActive)

	applyKimi(t, m, "pre-tool-use")
	applyKimi(t, m, "post-tool-use")
	if got := stateOf(st, "mer-1"); got != domain.ActivityActive {
		t.Fatalf("state after mid-turn tool use = %q, want active", got)
	}

	applyKimi(t, m, "permission-request")
	if got := stateOf(st, "mer-1"); got != domain.ActivityWaitingInput {
		t.Fatalf("state after permission request = %q, want waiting_input", got)
	}

	applyKimi(t, m, "post-tool-use-failure")
	if got := stateOf(st, "mer-1"); got != domain.ActivityWaitingInput {
		t.Fatalf("state after tool event while approval pending = %q, want waiting_input", got)
	}

	applyKimi(t, m, "permission-resolved")
	if got := stateOf(st, "mer-1"); got != domain.ActivityActive {
		t.Fatalf("state after permission resolved = %q, want active", got)
	}

	applyKimi(t, m, "pre-tool-use")
	applyKimi(t, m, "post-tool-use")
	if got := stateOf(st, "mer-1"); got != domain.ActivityActive {
		t.Fatalf("state after resumed tool use = %q, want active", got)
	}

	applyKimi(t, m, "stop")
	if got := stateOf(st, "mer-1"); got != domain.ActivityIdle {
		t.Fatalf("state after stop = %q, want idle", got)
	}
}

func applyKimi(t *testing.T, m *Manager, event string) {
	t.Helper()
	state, ok := activitydispatch.Derive("kimi", event, []byte(`{"session_id":"kimi-native-1"}`))
	if !ok {
		t.Fatalf("Derive(kimi, %q) reported no activity signal", event)
	}
	mustApply(t, m, "mer-1", sig(state, event, "Read", ""))
}

// A later approval in the same session re-arms waiting_input: resolving one
// dialog with PermissionResult must not immunize the session against the next
// permission request. (Concurrent unresolved approvals are a known limitation:
// a PermissionResult carries no correlation AO's waiting_input path tracks, so
// one dialog resolving can leave the pill on Working while another is still
// pending. Kimi's payloads do carry toolCallId/agentId for a future fix.)
func TestKimiActivityNextApprovalReArmsWaitingInput(t *testing.T) {
	m, st, _ := newManager()
	seedSignaled(st, "mer-1", domain.ActivityActive)

	applyKimi(t, m, "permission-request")
	applyKimi(t, m, "permission-resolved")
	if got := stateOf(st, "mer-1"); got != domain.ActivityActive {
		t.Fatalf("state after first approval resolved = %q, want active", got)
	}

	applyKimi(t, m, "permission-request")
	if got := stateOf(st, "mer-1"); got != domain.ActivityWaitingInput {
		t.Fatalf("state after next permission request = %q, want waiting_input", got)
	}
}
