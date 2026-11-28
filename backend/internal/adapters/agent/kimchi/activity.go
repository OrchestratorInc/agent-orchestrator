package kimchi

import (
	"encoding/json"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/domain"
)

// DeriveActivityState maps a Kimchi hook event (and its native stdin payload)
// onto an AO activity state. Kimchi's native hook-adapter system emits
// events with the same names and payload formats that AO installs. The
// notification-type-to-state mapping mirrors claudecode/activity.go: kimchi
// emits the same notification_type names (permission_prompt from the
// permissions extension, agent_needs_input from the questionnaire
// extension) and the semantics are identical.
func DeriveActivityState(event string, payload []byte) (domain.ActivityState, bool) {
	switch event {
	case "user-prompt-submit":
		return domain.ActivityActive, true
	case "pre-tool-use", "post-tool-use", "post-tool-use-failure", "post-tool-use-fail":
		return domain.ActivityActive, true
	case "stop":
		return domain.ActivityIdle, true
	case "notification":
		return notificationState(payload)
	case "session-end":
		return sessionEndState(payload)
	default:
		return "", false
	}
}

// notificationState splits the notification types that mean "the agent is
// paused on the user" by what unblocks them, mirroring the claude-code
// adapter's semantics:
//   - idle_prompt: the agent finished its turn and sits idle at the prompt
//     awaiting the next instruction — that is Idle, not a blocking request.
//   - agent_needs_input: a request for user input that carries no tool
//     identity — waiting_input (automated nudges stay suppressed via
//     NeedsInput, user sends deliver). It must NOT map to blocked: without a
//     tool to correlate, the block could only lift at a turn boundary,
//     rejecting user sends long after the question was answered.
//   - permission_prompt: a pending permission decision (blocked — a stray
//     Enter could answer the dialog). Unlike claude-code, kimchi has no
//     separate permission-request hook, so this Notification event is the
//     only signal for a blocked state.
//   - agent_completed (fired by background sessions): the turn finished —
//     Stop semantics, idle but alive.
//
// Other types (auth_success, elicitation_*) carry no activity meaning, as
// does a malformed payload.
func notificationState(payload []byte) (domain.ActivityState, bool) {
	var p struct {
		NotificationType string `json:"notification_type"`
	}
	_ = json.Unmarshal(payload, &p)
	switch p.NotificationType {
	case "idle_prompt", "agent_completed":
		return domain.ActivityIdle, true
	case "agent_needs_input":
		return domain.ActivityWaitingInput, true
	case "permission_prompt":
		return domain.ActivityBlocked, true
	default:
		return "", false
	}
}

func sessionEndState(payload []byte) (domain.ActivityState, bool) {
	var p struct {
		Reason string `json:"reason"`
	}
	_ = json.Unmarshal(payload, &p)
	switch p.Reason {
	case "reload", "new", "resume", "fork":
		// Non-terminal: the native session is replaced or reloaded while the
		// AO session stays alive. Report nothing so the activity feed is
		// unaffected and the session continues.
		return "", false
	default:
		// quit and any absent/unknown reason are terminal.
		return domain.ActivityExited, true
	}
}
