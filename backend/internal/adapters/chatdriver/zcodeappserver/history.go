package zcodeappserver

import (
	"context"
	"fmt"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/zcodeappserver/zcodeproto"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Asserted so a refactor cannot silently drop the history import: the service
// feature-detects the interface, and a missing method would read as "the
// provider cannot do this" with nothing anywhere to notice.
var (
	_ ports.ChatHistoryReader    = (*conversation)(nil)
	_ ports.ChatHistoryRefresher = (*conversation)(nil)
)

// historyMessageLimit is the session/read messageLimit for a full import. The
// param is PROVEN with explicit limits (1 and 10); its absence is STATIC, so a
// large explicit limit stays on the proven shape.
const historyMessageLimit = 10000

// historyEventPrefix namespaces history event ids (DESIGN 2.3).
const historyEventPrefix = "zcode-history:"

// ReadHistory returns a settled, provider-neutral replay of the native zcode
// session through session/read (PROVEN, p9-usage-read-mcp.ndjson,
// p8-resume.ndjson). It is how a daemon-restart Resume and a TUI -> Chat
// handoff make the turns the provider already holds visible in AO's timeline;
// resuming alone preserves model context but does not re-emit old events.
//
// The native message list has no turn ids. A turn is every visible user
// prompt and the assistant messages that follow it, keyed by the prompt's
// messageId, so the same read always produces the same ProviderEventIDs.
// Hidden messages (runtime system reminders the user never wrote,
// semantics.uiVisibility:"hidden") are skipped. Tool parts are STATIC (no
// captured read followed a tool call) and are skipped rather than guessed.
func (c *conversation) ReadHistory(ctx context.Context) ([]ports.ChatEvent, error) {
	snapshot, err := c.readSnapshot(ctx, historyMessageLimit)
	if err != nil {
		return nil, fmt.Errorf("read zcode history: %w", err)
	}
	sessionID := c.ProviderConversationID()
	if snapshot.Session.SessionID != "" && snapshot.Session.SessionID != sessionID {
		return nil, fmt.Errorf("session/read returned %q, requested %q", snapshot.Session.SessionID, sessionID)
	}
	if snapshot.Projection.Status == zcodeproto.ProjectionRunning || c.turns.active() != "" {
		return nil, fmt.Errorf("%w: zcode session %s has a running turn", ports.ErrChatHistoryUnsettled, sessionID)
	}
	return historyEvents(sessionID, snapshot.Messages), nil
}

// RefreshHistory performs another authoritative session/read. zcode exposes a
// repeatable native read rather than a replay captured during resume, so
// bounded settle polling can observe provider progress here.
func (c *conversation) RefreshHistory(ctx context.Context) ([]ports.ChatEvent, error) {
	return c.ReadHistory(ctx)
}

// historyEvents projects the native message list onto AO events, oldest first.
func historyEvents(sessionID string, messages []zcodeproto.Message) []ports.ChatEvent {
	events := make([]ports.ChatEvent, 0, len(messages)*3)
	turn := ""
	closeTurn := func() {
		if turn == "" {
			return
		}
		events = append(events, ports.ChatEvent{
			Kind:            ports.ChatEventTurnCompleted,
			ProviderEventID: historyEventID(sessionID, turn, "turn.completed"),
			ProviderTurnID:  turn,
			TurnState:       domain.TurnStateCompleted,
		})
		turn = ""
	}
	openTurn := func(messageID string) {
		closeTurn()
		turn = messageID
		events = append(events, ports.ChatEvent{
			Kind:            ports.ChatEventTurnStarted,
			ProviderEventID: historyEventID(sessionID, turn, "turn.started"),
			ProviderTurnID:  turn,
		})
	}
	for _, message := range messages {
		info := message.Info
		if info.MessageID == "" || info.Semantics.UIVisibility == "hidden" {
			continue
		}
		switch info.Role {
		case "user":
			text := joinedText(message.Parts, zcodeproto.PartText)
			if text == "" {
				continue
			}
			openTurn(info.MessageID)
			events = append(events, ports.ChatEvent{
				Kind:            ports.ChatEventUserMessageCompleted,
				ProviderEventID: historyEventID(sessionID, turn, "user", info.MessageID),
				ProviderTurnID:  turn,
				ProviderItemID:  info.MessageID,
				ClientMessageID: historyEventID(sessionID, turn, "user", info.MessageID),
				Text:            text,
			})
		case "assistant":
			if turn == "" {
				// An assistant message with no preceding prompt (a continuation
				// the provider started on its own): give it a turn of its own so
				// its text is not lost.
				openTurn(info.MessageID)
			}
			if reasoning := joinedText(message.Parts, zcodeproto.PartReasoning); reasoning != "" {
				itemID := reasoningItem(info.MessageID, turn)
				events = append(events,
					ports.ChatEvent{
						Kind:            ports.ChatEventActivityStarted,
						ProviderEventID: historyEventID(sessionID, turn, "reasoning", info.MessageID, "started"),
						ProviderTurnID:  turn,
						ProviderItemID:  itemID,
						ActivityKind:    domain.ActivityKindReasoning,
						ActivityStatus:  domain.ActivityStatusRunning,
						Summary:         "Reasoning",
					},
					ports.ChatEvent{
						Kind:            ports.ChatEventReasoningDelta,
						ProviderEventID: historyEventID(sessionID, turn, "reasoning", info.MessageID, "delta"),
						ProviderTurnID:  turn,
						ProviderItemID:  itemID,
						Delta:           reasoning,
					},
					ports.ChatEvent{
						Kind:            ports.ChatEventActivityCompleted,
						ProviderEventID: historyEventID(sessionID, turn, "reasoning", info.MessageID, "completed"),
						ProviderTurnID:  turn,
						ProviderItemID:  itemID,
						ActivityKind:    domain.ActivityKindReasoning,
						ActivityStatus:  domain.ActivityStatusCompleted,
						Summary:         "Reasoning",
					})
			}
			if text := joinedText(message.Parts, zcodeproto.PartText); text != "" {
				// Keyed by the assistant messageId, the same item the live
				// text_delta stream accumulates under (assistantMessageId), so a
				// history import after a live turn lands on the same row.
				events = append(events, ports.ChatEvent{
					Kind:            ports.ChatEventMessageCompleted,
					ProviderEventID: historyEventID(sessionID, turn, "text", info.MessageID),
					ProviderTurnID:  turn,
					ProviderItemID:  info.MessageID,
					Text:            text,
				})
			}
		}
	}
	closeTurn()
	return events
}

// joinedText concatenates the text of every part of one type, in order.
func joinedText(parts []zcodeproto.Part, partType string) string {
	var chunks []string
	for _, part := range parts {
		if part.Type == partType && strings.TrimSpace(part.Text) != "" {
			chunks = append(chunks, part.Text)
		}
	}
	return strings.Join(chunks, "\n\n")
}

// historyEventID builds a stable identity for a history event
// ("zcode-history:<sessionId>:<turn>:<parts…>", DESIGN 2.3).
func historyEventID(sessionID, turn string, parts ...string) string {
	return historyEventPrefix + sessionID + ":" + turn + ":" + strings.Join(parts, ":")
}
