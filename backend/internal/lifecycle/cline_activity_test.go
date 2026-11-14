package lifecycle

import (
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/cline"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestToolPrecedence_ClineApprovalThenPreToolUsePersistsActive(t *testing.T) {
	m, st, _ := newManager()
	seedSignaled(st, "mer-1", domain.ActivityActive)

	mustApply(t, m, "mer-1", sig(domain.ActivityWaitingInput, "terminal-waiting-input", "", ""))
	if got := stateOf(st, "mer-1"); got != domain.ActivityWaitingInput {
		t.Fatalf("setup: state after observed approval = %q, want waiting_input", got)
	}

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

func TestToolPrecedence_ToolEventCannotClearObservedApproval(t *testing.T) {
	m, st, _ := newManager()
	seedSignaled(st, "mer-1", domain.ActivityActive)
	mustApply(t, m, "mer-1", sig(domain.ActivityWaitingInput, "terminal-waiting-input", "", ""))

	mustApply(t, m, "mer-1", sig(domain.ActivityActive, "pre-tool-use", "run_commands", "cline-tool-1"))
	if got := stateOf(st, "mer-1"); got != domain.ActivityWaitingInput {
		t.Fatalf("state after pre-tool-use = %q, want waiting_input (tool events cannot clear it)", got)
	}
}
