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
// item id must go. A partial turn (text, then a tool call, then
// PARTIAL_FAILED_TURN_NOTICE) replays its text and the notice under one
// id-less item, so the completed text ends with the notice instead of being
// one: that item keeps its text with the notice cut off, carried by a single
// delta so no chunk of the notice survives. Reply text is also persisted
// stripped while AO stored the live untrimmed text; that side is handled by
// the whitespace-tolerant fingerprints in the chat service, since the stripped
// text cannot be restored here.
func reconcileReplay(events []ports.ChatEvent) []ports.ChatEvent {
	noticeItems := make(map[string]bool)
	trimmedItems := make(map[string]string)
	for _, event := range events {
		if event.Kind != ports.ChatEventMessageCompleted || event.ProviderItemID == "" {
			continue
		}
		if isFailedTurnNotice(event.Text) {
			noticeItems[event.ProviderItemID] = true
		} else if reply, cut := cutFailedTurnNotice(event.Text); cut {
			trimmedItems[event.ProviderItemID] = reply
		}
	}
	reconciled := events[:0]
	replacedDelta := make(map[string]bool)
	for _, event := range events {
		if noticeItems[event.ProviderItemID] {
			continue
		}
		if event.Kind == ports.ChatEventMessageCompleted && isFailedTurnNotice(event.Text) {
			continue
		}
		if reply, ok := trimmedItems[event.ProviderItemID]; ok {
			switch event.Kind {
			case ports.ChatEventMessageDelta:
				if replacedDelta[event.ProviderItemID] {
					continue
				}
				replacedDelta[event.ProviderItemID] = true
				event.Delta = reply
			case ports.ChatEventMessageCompleted:
				event.Text = reply
			}
		} else if event.Kind == ports.ChatEventMessageCompleted {
			if reply, cut := cutFailedTurnNotice(event.Text); cut {
				event.Text = reply
			}
		}
		reconciled = append(reconciled, event)
	}
	return reconciled
}

// cutFailedTurnNotice returns text without a trailing boundary notice, and
// whether one was cut. Text that is only a notice is isFailedTurnNotice's case.
func cutFailedTurnNotice(text string) (string, bool) {
	trimmed := strings.TrimSpace(text)
	for _, notice := range hermesFailedTurnNotices {
		if reply, found := strings.CutSuffix(trimmed, notice); found {
			if reply = strings.TrimSpace(reply); reply != "" {
				return reply, true
			}
		}
	}
	return text, false
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
