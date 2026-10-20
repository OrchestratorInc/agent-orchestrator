package copilot

import (
	"encoding/json"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// DeriveActivityState maps a Copilot hook sub-command onto an AO activity state.
//
// Generic preToolUse is ordinary tool activity, not proof a human must approve.
// Input needed comes from notification types that mean Copilot surfaced a
// permission or elicitation UI. permission-request is installed only so AO can
// return {} and fall through to Copilot's native Yes/No — it carries no
// activity signal (the hook fires before auto-allow, so treating it as
// waiting_input would false-positive).
//
// postToolUse maps to ordinary post-tool-use (active), not permission-resolved:
// the latter is a turn boundary and would clear sticky waiting_input when a
// sibling tool finishes while a prompt is still open. The lifecycle clears
// Copilot waiting_input when pre-tool-use follows a notification-opened
// prompt, or on an explicit user/turn boundary (user-prompt-submit, stop).
// Stale async permission/elicitation notifications after that clear are
// suppressed in the lifecycle reducer, not here — this deriver only maps
// payload shape onto a state.
func DeriveActivityState(event string, payload []byte) (domain.ActivityState, bool) {
	switch event {
	case "session-start", "user-prompt-submit", "pre-tool-use", "post-tool-use", "post-tool-use-failure":
		return domain.ActivityActive, true
	case "stop":
		return domain.ActivityIdle, true
	case "permission-request":
		return "", false
	case "notification":
		return notificationState(payload)
	default:
		return "", false
	}
}

func notificationState(payload []byte) (domain.ActivityState, bool) {
	var p struct {
		NotificationType      string `json:"notification_type"`
		NotificationTypeCamel string `json:"notificationType"`
	}
	_ = json.Unmarshal(payload, &p)
	typ := p.NotificationType
	if typ == "" {
		typ = p.NotificationTypeCamel
	}
	switch typ {
	case "permission_prompt", "elicitation_dialog":
		return domain.ActivityWaitingInput, true
	default:
		return "", false
	}
}
