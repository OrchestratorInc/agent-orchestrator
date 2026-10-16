package lifecycle

import (
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// End-to-end Copilot B1 (#6280) activity sequence through the lifecycle reducer.
func TestCopilotB1_CompleteActivityFlows(t *testing.T) {
	t.Run("ordinary tool never needs input", func(t *testing.T) {
		m, st, _ := newManager()
		seedSignaled(st, "c1", domain.ActivityIdle)

		mustApply(t, m, "c1", sig(domain.ActivityActive, "session-start", "", ""))
		mustApply(t, m, "c1", sig(domain.ActivityActive, "user-prompt-submit", "", ""))
		mustApply(t, m, "c1", sig(domain.ActivityActive, "pre-tool-use", "bash", ""))
		if got := stateOf(st, "c1"); got != domain.ActivityActive {
			t.Fatalf("after pre-tool-use = %q, want active (not waiting_input)", got)
		}
		mustApply(t, m, "c1", sig(domain.ActivityActive, "post-tool-use", "bash", ""))
		if got := stateOf(st, "c1"); got != domain.ActivityActive {
			t.Fatalf("after post-tool-use = %q, want active", got)
		}
		mustApply(t, m, "c1", sig(domain.ActivityIdle, "stop", "", ""))
		if got := stateOf(st, "c1"); got != domain.ActivityIdle {
			t.Fatalf("after stop = %q, want idle", got)
		}
	})

	t.Run("permission prompt then approve then clear", func(t *testing.T) {
		m, st, _ := newManager()
		seedSignaled(st, "c2", domain.ActivityActive)

		mustApply(t, m, "c2", sig(domain.ActivityWaitingInput, "notification", "", ""))
		if got := stateOf(st, "c2"); got != domain.ActivityWaitingInput {
			t.Fatalf("after permission_prompt = %q, want waiting_input", got)
		}
		// Sibling/background post must not clear while the prompt is open.
		mustApply(t, m, "c2", sig(domain.ActivityActive, "post-tool-use", "read", "toolu_bg"))
		if got := stateOf(st, "c2"); got != domain.ActivityWaitingInput {
			t.Fatalf("sibling post must not demote waiting_input: %q", got)
		}
		// Gated tool starting after the user answered clears waiting_input.
		mustApply(t, m, "c2", sig(domain.ActivityActive, "pre-tool-use", "bash", ""))
		if got := stateOf(st, "c2"); got != domain.ActivityActive {
			t.Fatalf("after approve/pre-tool-use = %q, want active", got)
		}
	})

	t.Run("elicitation clears on next prompt", func(t *testing.T) {
		m, st, _ := newManager()
		seedSignaled(st, "c3", domain.ActivityActive)

		mustApply(t, m, "c3", sig(domain.ActivityWaitingInput, "notification", "", ""))
		mustApply(t, m, "c3", sig(domain.ActivityActive, "user-prompt-submit", "", ""))
		if got := stateOf(st, "c3"); got != domain.ActivityActive {
			t.Fatalf("after user answer = %q, want active", got)
		}
	})

	t.Run("late permission_prompt after clear is suppressed", func(t *testing.T) {
		m, st, _ := newManager()
		seedSignaled(st, "c4", domain.ActivityActive)

		mustApply(t, m, "c4", sig(domain.ActivityWaitingInput, "notification", "", ""))
		mustApply(t, m, "c4", sig(domain.ActivityActive, "pre-tool-use", "bash", ""))
		if got := stateOf(st, "c4"); got != domain.ActivityActive {
			t.Fatalf("after clear = %q, want active", got)
		}
		mustApply(t, m, "c4", sig(domain.ActivityWaitingInput, "notification", "", ""))
		if got := stateOf(st, "c4"); got != domain.ActivityActive {
			t.Fatalf("late notification = %q, want active (stale suppress)", got)
		}
	})

	t.Run("late permission_prompt after stop is suppressed", func(t *testing.T) {
		m, st, _ := newManager()
		seedSignaled(st, "c5", domain.ActivityActive)

		mustApply(t, m, "c5", sig(domain.ActivityWaitingInput, "notification", "", ""))
		mustApply(t, m, "c5", sig(domain.ActivityIdle, "stop", "", ""))
		if got := stateOf(st, "c5"); got != domain.ActivityIdle {
			t.Fatalf("after stop = %q, want idle", got)
		}
		mustApply(t, m, "c5", sig(domain.ActivityWaitingInput, "notification", "", ""))
		if got := stateOf(st, "c5"); got != domain.ActivityIdle {
			t.Fatalf("late notification after stop = %q, want idle", got)
		}
	})

	t.Run("fresh user turn accepts permission notifications again", func(t *testing.T) {
		m, st, _ := newManager()
		seedSignaled(st, "c6", domain.ActivityActive)

		mustApply(t, m, "c6", sig(domain.ActivityWaitingInput, "notification", "", ""))
		mustApply(t, m, "c6", sig(domain.ActivityActive, "pre-tool-use", "bash", ""))
		mustApply(t, m, "c6", sig(domain.ActivityWaitingInput, "notification", "", ""))
		if got := stateOf(st, "c6"); got != domain.ActivityActive {
			t.Fatalf("stale after clear = %q, want active", got)
		}
		mustApply(t, m, "c6", sig(domain.ActivityActive, "user-prompt-submit", "", ""))
		mustApply(t, m, "c6", sig(domain.ActivityWaitingInput, "notification", "", ""))
		if got := stateOf(st, "c6"); got != domain.ActivityWaitingInput {
			t.Fatalf("new-turn notification = %q, want waiting_input", got)
		}
	})
}
