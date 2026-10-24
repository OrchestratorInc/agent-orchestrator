package hermesacp

import (
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Hermes closes a durable turn that ended without a reply with its own
// assistant boundary row (agent/turn_failure_copy.py in Hermes v0.22.2, the
// FAILED_TURN_NOTICE / PARTIAL_FAILED_TURN_NOTICE /
// AUTOMATION_INTERRUPTED_NOTICE copy). The rows are display-only — Hermes
// strips them before every provider request — and AO never sees them live, so
// a session/load replay that carries them must not import the text as reply
// content.
var hermesFailedTurnNotices = []string{
	"Your request was not processed. Send it again if you still want me to carry it out.",
	"This turn did not complete. Some actions may already have run; verify their effects before resending.",
	"An automated delivery was interrupted and was not run again. Some actions may already have run; verify their effects.",
}

// reconcileReplay normalizes one Hermes session/load replay into the shape AO
// projected live. The caller hands over a fresh copy, so events may be
// filtered in place. Hermes' replay differs from its live stream in ways the
// generic reconciliation cannot absorb: the failed-turn boundary rows above
// come back as ordinary agent text on turns that never had a reply, which
// would duplicate a reply into the stored interrupted turn. The chat service
// rebuilds a reply from the replay's message.delta chunks (AppendAssistantDelta),
// so dropping only the item's completed event still lets the notice text
// reassemble from its deltas — every event carrying a notice item's provider
// item id must go. Reply text is also persisted stripped while AO stored the
// live untrimmed text; that side is handled by the whitespace-tolerant
// fingerprints in the chat service, since the stripped text cannot be restored
// here.
func reconcileReplay(events []ports.ChatEvent) []ports.ChatEvent {
	noticeItems := make(map[string]bool)
	for _, event := range events {
		if event.Kind == ports.ChatEventMessageCompleted && isFailedTurnNotice(event.Text) && event.ProviderItemID != "" {
			noticeItems[event.ProviderItemID] = true
		}
	}
	reconciled := events[:0]
	for _, event := range events {
		if noticeItems[event.ProviderItemID] {
			continue
		}
		if event.Kind == ports.ChatEventMessageCompleted && isFailedTurnNotice(event.Text) {
			continue
		}
		reconciled = append(reconciled, event)
	}
	return reconciled
}

func isFailedTurnNotice(text string) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return false
	}
	for _, notice := range hermesFailedTurnNotices {
		if trimmed == notice {
			return true
		}
	}
	return false
}
