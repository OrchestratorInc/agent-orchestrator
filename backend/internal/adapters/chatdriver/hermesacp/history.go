package hermesacp

import (
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var hermesFailedTurnNotices = []string{
	"Your request was not processed. Send it again if you still want me to carry it out.",
	"This turn did not complete. Some actions may already have run; verify their effects before resending.",
	"An automated delivery was interrupted and was not run again. Some actions may already have run; verify their effects.",
}

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
