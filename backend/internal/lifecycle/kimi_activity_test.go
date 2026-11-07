package lifecycle

import (
	"fmt"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/activitydispatch"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
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
	seedKimi(st)

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

func applyKimiApproval(t *testing.T, m *Manager, event, approval string) {
	t.Helper()
	mustApply(t, m, "mer-1", kimiApprovalSignal(event, approval))
}

// These keys stand for the scoped native approval identities emitted by the
// CLI; they deliberately do not identify tool calls, which can be reused.
func kimiApprovalSignal(event, approval string) ports.ActivitySignal {
	state := domain.ActivityActive
	if event == "permission-request" {
		state = domain.ActivityWaitingInput
	}
	id := ""
	if approval != "" {
		id = "kimi-approval:" + approval
	}
	s := sig(state, event, "Bash", id)
	s.ExpectedHarness = domain.HarnessKimi
	return s
}

func seedKimi(st *fakeStore) {
	seedSignaled(st, "mer-1", domain.ActivityActive)
	rec := st.sessions["mer-1"]
	rec.Harness = domain.HarnessKimi
	st.sessions["mer-1"] = rec
}

func TestKimiApprovalCallbackOrdering(t *testing.T) {
	type step struct {
		event string
		id    string
		want  domain.ActivityState
	}
	for _, tt := range []struct {
		name  string
		steps []step
	}{
		{"result before own request", []step{
			{"permission-resolved", "a", domain.ActivityActive},
			{"permission-request", "a", domain.ActivityActive},
		}},
		{"duplicates", []step{
			{"permission-request", "a", domain.ActivityWaitingInput},
			{"permission-request", "a", domain.ActivityWaitingInput},
			{"permission-resolved", "a", domain.ActivityActive},
			{"permission-resolved", "a", domain.ActivityActive},
			{"permission-request", "a", domain.ActivityActive},
			{"permission-request", "b", domain.ActivityWaitingInput},
			{"permission-resolved", "a", domain.ActivityWaitingInput},
			{"permission-resolved", "b", domain.ActivityActive},
		}},
		{"concurrent requests", []step{
			{"permission-request", "a", domain.ActivityWaitingInput},
			{"permission-request", "b", domain.ActivityWaitingInput},
			{"permission-resolved", "a", domain.ActivityWaitingInput},
			{"permission-resolved", "b", domain.ActivityActive},
		}},
		{"early result while another request pending", []step{
			{"permission-request", "b", domain.ActivityWaitingInput},
			{"permission-resolved", "a", domain.ActivityWaitingInput},
			{"permission-request", "a", domain.ActivityWaitingInput},
			{"permission-resolved", "b", domain.ActivityActive},
		}},
		{"id-less compatibility guard", []step{
			{"permission-request", "a", domain.ActivityWaitingInput},
			{"permission-resolved", "", domain.ActivityWaitingInput},
			{"permission-resolved", "a", domain.ActivityActive},
			{"permission-request", "", domain.ActivityWaitingInput},
			{"permission-resolved", "", domain.ActivityActive},
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m, st, _ := newManager()
			seedKimi(st)
			for i, step := range tt.steps {
				mustApply(t, m, "mer-1", kimiApprovalSignal(step.event, step.id))
				if got := stateOf(st, "mer-1"); got != step.want {
					t.Fatalf("step %d (%s %s): got %s, want %s", i, step.event, step.id, got, step.want)
				}
			}
		})
	}
}

func TestKimiApprovalCompletionSurvivesTurnBoundaries(t *testing.T) {
	for _, event := range []string{"stop", "user-prompt-submit", "session-start"} {
		t.Run(event, func(t *testing.T) {
			m, st, _ := newManager()
			seedKimi(st)
			mustApply(t, m, "mer-1", kimiApprovalSignal("permission-resolved", "a"))
			applyKimi(t, m, event)
			want := stateOf(st, "mer-1")
			mustApply(t, m, "mer-1", kimiApprovalSignal("permission-request", "a"))
			mustApply(t, m, "mer-1", kimiApprovalSignal("permission-resolved", "a"))
			if got := stateOf(st, "mer-1"); got != want {
				t.Fatalf("duplicate callbacks after %s: got %s, want %s", event, got, want)
			}
			// A distinct approval still re-arms the wait in the next turn.
			mustApply(t, m, "mer-1", kimiApprovalSignal("permission-request", "b"))
			if got := stateOf(st, "mer-1"); got != domain.ActivityWaitingInput {
				t.Fatalf("new approval after %s: got %s, want waiting_input", event, got)
			}
		})
	}
}

func TestKimiApprovalBoundEvictsCompletedBeforePending(t *testing.T) {
	m, st, _ := newManager()
	seedKimi(st)
	mustApply(t, m, "mer-1", kimiApprovalSignal("permission-request", "pending"))
	for i := range 128 {
		mustApply(t, m, "mer-1", kimiApprovalSignal("permission-resolved", fmt.Sprint(i)))
	}
	// The oldest completed entry was evicted; the pending entry was not.
	mustApply(t, m, "mer-1", kimiApprovalSignal("permission-request", "127"))
	if got := stateOf(st, "mer-1"); got != domain.ActivityWaitingInput {
		t.Fatalf("completed callback cleared pending approval: %s", got)
	}
	mustApply(t, m, "mer-1", kimiApprovalSignal("permission-resolved", "pending"))
	if got := stateOf(st, "mer-1"); got != domain.ActivityActive {
		t.Fatalf("pending approval was evicted: %s", got)
	}
	mustApply(t, m, "mer-1", kimiApprovalSignal("permission-request", "0"))
	if got := stateOf(st, "mer-1"); got != domain.ActivityWaitingInput {
		t.Fatalf("oldest completion was not evicted: %s", got)
	}
}

func TestKimiApprovalOverflowFailsClosedUntilBoundary(t *testing.T) {
	m, st, _ := newManager()
	seedKimi(st)
	for i := range 129 {
		mustApply(t, m, "mer-1", kimiApprovalSignal("permission-request", fmt.Sprint(i)))
	}
	for i := range 129 {
		mustApply(t, m, "mer-1", kimiApprovalSignal("permission-resolved", fmt.Sprint(i)))
	}
	if got := stateOf(st, "mer-1"); got != domain.ActivityWaitingInput {
		t.Fatalf("overflow cleared without a boundary: %s", got)
	}
	applyKimi(t, m, "stop")
	mustApply(t, m, "mer-1", kimiApprovalSignal("permission-request", "new"))
	mustApply(t, m, "mer-1", kimiApprovalSignal("permission-resolved", "new"))
	if got := stateOf(st, "mer-1"); got != domain.ActivityActive {
		t.Fatalf("boundary failed to reset overflow: %s", got)
	}
}

func TestKimiApprovalObservedPendingSettlesAtBoundary(t *testing.T) {
	for _, event := range []string{"stop", "user-prompt-submit"} {
		t.Run(event, func(t *testing.T) {
			m, st, _ := newManager()
			seedKimi(st)
			mustApply(t, m, "mer-1", kimiApprovalSignal("permission-request", "a"))
			applyKimi(t, m, event)
			want := stateOf(st, "mer-1")
			mustApply(t, m, "mer-1", kimiApprovalSignal("permission-request", "a"))
			mustApply(t, m, "mer-1", kimiApprovalSignal("permission-resolved", "a"))
			if got := stateOf(st, "mer-1"); got != want {
				t.Fatalf("late callbacks revived settled turn: got %s, want %s", got, want)
			}
		})
	}
}

func TestKimiApprovalStateReleasedOnTeardown(t *testing.T) {
	for _, terminate := range []bool{false, true} {
		t.Run(fmt.Sprint("terminate=", terminate), func(t *testing.T) {
			m, st, _ := newManager()
			seedKimi(st)
			mustApply(t, m, "mer-1", kimiApprovalSignal("permission-resolved", "a"))
			if terminate {
				if err := m.MarkTerminated(ctx, "mer-1"); err != nil {
					t.Fatal(err)
				}
			} else {
				mustApply(t, m, "mer-1", sig(domain.ActivityExited, "process-exited", "", ""))
			}
			if _, ok := m.flights["mer-1"]; ok {
				t.Fatal("approval identities leaked after teardown")
			}
		})
	}
}

func TestKimiApprovalDaemonRestartLosesInMemoryEvidence(t *testing.T) {
	m, st, messenger := newManager()
	seedKimi(st)
	mustApply(t, m, "mer-1", kimiApprovalSignal("permission-resolved", "a"))
	// The bounded tracker deliberately does not persist display/correlation
	// state. A replacement reducer cannot recognize callbacks seen only by its
	// predecessor; this is an explicit limit, not an ordering guarantee.
	restarted := New(st, messenger)
	mustApply(t, restarted, "mer-1", kimiApprovalSignal("permission-request", "a"))
	if got := stateOf(st, "mer-1"); got != domain.ActivityWaitingInput {
		t.Fatalf("unexpected cross-restart completion evidence: %s", got)
	}
	mustApply(t, restarted, "mer-1", kimiApprovalSignal("permission-resolved", "a"))
	if got := stateOf(st, "mer-1"); got != domain.ActivityActive {
		t.Fatalf("new reducer failed to correlate fresh callbacks: %s", got)
	}
}

func TestKimiApprovalSpawnResetsCacheAndFencesOldLaunch(t *testing.T) {
	m, st, _ := newManager()
	seedKimi(st)
	mustApply(t, m, "mer-1", kimiApprovalSignal("permission-resolved", "a"))
	if err := m.MarkSpawned(ctx, "mer-1", domain.SessionMetadata{RuntimeLaunchID: "new-launch"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := m.flights["mer-1"]; ok {
		t.Fatal("approval completion leaked into a fresh launch")
	}
	s := kimiApprovalSignal("permission-request", "a")
	s.LaunchID = "old-launch"
	mustApply(t, m, "mer-1", s)
	if got := stateOf(st, "mer-1"); got != domain.ActivityIdle {
		t.Fatalf("old launch changed fresh activity: %s", got)
	}
	s.LaunchID = "new-launch"
	mustApply(t, m, "mer-1", s)
	if got := stateOf(st, "mer-1"); got != domain.ActivityWaitingInput {
		t.Fatalf("fresh launch inherited old completion: %s", got)
	}
}

func TestNonKimiPermissionSignalsKeepExistingSemantics(t *testing.T) {
	m, st, _ := newManager()
	seedSignaled(st, "mer-1", domain.ActivityActive)
	// Even a key resembling a Kimi key cannot opt another harness in.
	mustApply(t, m, "mer-1", sig(domain.ActivityWaitingInput, "permission-request", "Bash", "kimi-approval:a"))
	mustApply(t, m, "mer-1", sig(domain.ActivityActive, "permission-resolved", "Bash", "kimi-approval:b"))
	if got := stateOf(st, "mer-1"); got != domain.ActivityActive {
		t.Fatalf("non-Kimi permission semantics changed: %s", got)
	}
}

// A later approval in the same session re-arms waiting_input: resolving one
// dialog with PermissionResult must not immunize the session against the next
// permission request. This is the id-less path; the correlated guard is
// covered by TestKimiPermissionResultWithAnotherApprovalPendingStaysWaiting.
func TestKimiActivityNextApprovalReArmsWaitingInput(t *testing.T) {
	m, st, _ := newManager()
	seedKimi(st)

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

// A PermissionResult correlates to its own scoped native approval identity:
// while another recorded approval is still pending,
// one dialog resolving must not flip the session to Working. This covers both
// concurrently open dialogs and a late, reordered result delivered after the
// next request (kimi's fire-and-forget hooks are independent processes, so
// delivery order is unguaranteed); the last result lifts waiting_input.
func TestKimiPermissionResultWithAnotherApprovalPendingStaysWaiting(t *testing.T) {
	m, st, _ := newManager()
	seedKimi(st)

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

// An id-less PermissionResult (older CLI, or a payload without approval id)
// cannot prove which approval it resolved, so while correlated approvals are
// tracked it must keep the session on waiting_input; only the last pending
// approval's own result lifts the wait. The legacy all-id-less path — no
// tracked approvals, unconditional clear — is pinned by
// TestKimiActivityNextApprovalReArmsWaitingInput above and the cline
// sequence test.
func TestKimiIdLessPermissionResultKeepsWaitingWhileCorrelatedApprovalsPending(t *testing.T) {
	m, st, _ := newManager()
	seedKimi(st)

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
