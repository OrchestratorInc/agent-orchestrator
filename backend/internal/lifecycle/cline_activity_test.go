package lifecycle

import (
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/cline"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// TestToolPrecedence_ClineApprovalThenPreToolUsePersistsActive covers the
// sequence behind #6414 end to end: the activity observer writes waiting_input
// while Cline's approval dialog is on screen, the user approves, and Cline
// fires PreToolUse. Lifecycle precedence suppresses plain tool events against
// sticky waiting_input, so the hook installs as permission-resolved — a turn
// boundary — and the session must persist active rather than stay on Input
// needed with no hook left to clear it.
func TestToolPrecedence_ClineApprovalThenPreToolUsePersistsActive(t *testing.T) {
	m, st, _ := newManager()
	seedSignaled(st, "mer-1", domain.ActivityActive)

	// What the activity observer reports for a live approval dialog.
	mustApply(t, m, "mer-1", sig(domain.ActivityWaitingInput, "terminal-waiting-input", "", ""))
	if got := stateOf(st, "mer-1"); got != domain.ActivityWaitingInput {
		t.Fatalf("setup: state after observed approval = %q, want waiting_input", got)
	}

	// The Cline PreToolUse hook path: `ao hooks cline permission-resolved`.
	event := "permission-resolved"
	state, ok := cline.DeriveActivityState(event, nil)
	if !ok || state != domain.ActivityActive {
		t.Fatalf("cline.DeriveActivityState(%q) = (%q, %v), want (%q, true)", event, state, ok, domain.ActivityActive)
	}
	mustApply(t, m, "mer-1", sig(state, event, "run_commands", "cline-tool-1"))
	if got := stateOf(st, "mer-1"); got != domain.ActivityActive {
		t.Fatalf("state after Cline PreToolUse = %q, want active", got)
	}
}

// TestToolPrecedence_ToolEventCannotClearObservedApproval pins why the Cline
// hook does not install as a plain tool event: a pre-tool-use signal is
// suppressed against sticky waiting_input, so the approved session would stay
// on Input needed until an unrelated terminal marker or turn boundary arrived.
func TestToolPrecedence_ToolEventCannotClearObservedApproval(t *testing.T) {
	m, st, _ := newManager()
	seedSignaled(st, "mer-1", domain.ActivityActive)
	mustApply(t, m, "mer-1", sig(domain.ActivityWaitingInput, "terminal-waiting-input", "", ""))

	mustApply(t, m, "mer-1", sig(domain.ActivityActive, "pre-tool-use", "run_commands", "cline-tool-1"))
	if got := stateOf(st, "mer-1"); got != domain.ActivityWaitingInput {
		t.Fatalf("state after pre-tool-use = %q, want waiting_input (tool events cannot clear it)", got)
	}
}
