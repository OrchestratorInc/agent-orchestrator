package droid

import (
	"encoding/json"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/domain"
)

// DeriveActivityState maps a Droid hook event (and its native stdin payload)
// onto an AO activity state. The bool is false when the event carries no
// activity signal — e.g. SessionStart (metadata only) or a SessionEnd reason
// that doesn't actually end the AO session — in which case the caller reports
// nothing.
//
// event is the AO hook sub-command name installed in droidManagedHooks
// ("user-prompt-submit", "stop", "notification", "session-end", ...), NOT the
// native Droid event name. Keeping this beside hooks.go means the events AO
// installs and what they mean live in one place.
//
// Droid's payload shapes differ from Claude Code's in one way that matters here:
// the Notification payload carries no notification_type discriminator (it only
// has a free-form message), but Droid only fires Notification when it needs a
// permission decision or has been idle awaiting input for 60s. AO cannot tell
// which, so every Notification maps to waiting_input: it suppresses automated
// nudges (NeedsInput) in both cases, and blocked is reserved for harnesses that
// can clear it mid-turn via the pre/post-tool-use trio — droid installs no tool
// hooks, so a blocked state would linger until the turn boundary and reject
// user sends at a safely idle prompt.
func DeriveActivityState(event string, payload []byte) (domain.ActivityState, bool) {
	switch event {
	case "user-prompt-submit":
		return domain.ActivityActive, true
	case "stop":
		// End of a turn: the agent is idle but alive (not exited). A following
		// Notification upgrades this to the sticky waiting_input.
		return domain.ActivityIdle, true
	case "notification":
		return domain.ActivityWaitingInput, true
	case "session-end":
		return sessionEndState(payload)
	default:
		return "", false
	}
}

// sessionEndState reports exited for reasons that actually end the session.
// "clear" keeps the same AO session alive (a new native session continues in
// the worktree), so it reports nothing. Any other reason — logout,
// prompt_input_exit, other, or an absent/unknown reason on a SessionEnd that did
// fire — is treated as a real exit. SessionEnd is not guaranteed on crash, so
// the reaper remains the backstop; both paths guard on IsTerminated, so
// whichever lands first wins.
func sessionEndState(payload []byte) (domain.ActivityState, bool) {
	var p struct {
		Reason string `json:"reason"`
	}
	_ = json.Unmarshal(payload, &p)
	switch p.Reason {
	case "clear":
		return "", false
	default:
		return domain.ActivityExited, true
	}
}
