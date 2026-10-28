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

func applyKimiApproval(t *testing.T, m *Manager, event, toolCallID string) {
	t.Helper()
	state, ok := activitydispatch.Derive("kimi", event, []byte(`{"session_id":"kimi-native-1","tool_call_id":"`+toolCallID+`"}`))
	if !ok {
		t.Fatalf("Derive(kimi, %q) reported no activity signal", event)
	}
	mustApply(t, m, "mer-1", sig(state, event, "Bash", toolCallID))
}

// A later approval in the same session re-arms waiting_input: resolving one
// dialog with PermissionResult must not immunize the session against the next
// permission request. This is the id-less path; the correlated guard is
// covered by TestKimiPermissionResultWithAnotherApprovalPendingStaysWaiting.
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

// A PermissionResult correlates to its own request through the tool_call_id
// lifted onto the signal: while another recorded approval is still pending,
// one dialog resolving must not flip the session to Working. This covers both
// concurrently open dialogs and a late, reordered result delivered after the
// next request (kimi's fire-and-forget hooks are independent processes, so
// delivery order is unguaranteed); the last result lifts waiting_input.
func TestKimiPermissionResultWithAnotherApprovalPendingStaysWaiting(t *testing.T) {
	m, st, _ := newManager()
	seedSignaled(st, "mer-1", domain.ActivityActive)

	applyKimiApproval(t, m, "permission-request", "call-a")
	if got := stateOf(st, "mer-1"); got != domain.ActivityWaitingInput {
		t.Fatalf("state after first request = %q, want waiting_input", got)
	}
	applyKimiApproval(t, m, "permission-request", "call-b")
	if got := stateOf(st, "mer-1"); got != domain.ActivityWaitingInput {
		t.Fatalf("state after second request = %q, want waiting_input", got)
	}

	applyKimiApproval(t, m, "permission-resolved", "call-a")
	if got := stateOf(st, "mer-1"); got != domain.ActivityWaitingInput {
		t.Fatalf("state after first result while second approval pending = %q, want waiting_input", got)
	}

	applyKimiApproval(t, m, "permission-resolved", "call-b")
	if got := stateOf(st, "mer-1"); got != domain.ActivityActive {
		t.Fatalf("state after final result = %q, want active", got)
	}
}

// An id-less PermissionResult (older CLI, or a payload without tool_call_id)
// cannot prove which approval it resolved, so while correlated approvals are
// tracked it must keep the session on waiting_input; only the last pending
// approval's own result lifts the wait. The legacy all-id-less path — no
// tracked approvals, unconditional clear — is pinned by
// TestKimiActivityNextApprovalReArmsWaitingInput above and the cline
// sequence test.
func TestKimiIdLessPermissionResultKeepsWaitingWhileCorrelatedApprovalsPending(t *testing.T) {
	m, st, _ := newManager()
	seedSignaled(st, "mer-1", domain.ActivityActive)

	applyKimiApproval(t, m, "permission-request", "call-a")
	applyKimiApproval(t, m, "permission-request", "call-b")
	if got := stateOf(st, "mer-1"); got != domain.ActivityWaitingInput {
		t.Fatalf("state after requests = %q, want waiting_input", got)
	}

	applyKimi(t, m, "permission-resolved")
	if got := stateOf(st, "mer-1"); got != domain.ActivityWaitingInput {
		t.Fatalf("state after id-less result with tracked approvals = %q, want waiting_input", got)
	}

	applyKimiApproval(t, m, "permission-resolved", "call-a")
	if got := stateOf(st, "mer-1"); got != domain.ActivityWaitingInput {
		t.Fatalf("state after first correlated result = %q, want waiting_input", got)
	}

	applyKimiApproval(t, m, "permission-resolved", "call-b")
	if got := stateOf(st, "mer-1"); got != domain.ActivityActive {
		t.Fatalf("state after final correlated result = %q, want active", got)
	}
}
