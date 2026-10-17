package codexappserver

import (
	"context"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/codexappserver/codexproto"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var _ ports.ChatLiveReconnectActivator = (*conversation)(nil)

type liveTurnRecovery struct {
	turnID string
	events []ports.ChatEvent
}

// ActivateLiveReconnect repairs a terminal notification delivered to the old
// daemon's socket but lost before its projection committed. Active or unknown
// native work remains owned by the restored busy controller.
func (c *conversation) ActivateLiveReconnect(ctx context.Context, providerTurnID string, reconcile func([]ports.ChatEvent) []ports.ChatEvent) error {
	if !c.ReconnectedLive() || providerTurnID == "" {
		return nil
	}
	includeTurns := true
	var response codexproto.ThreadReadResponse
	readCtx, cancel := context.WithTimeout(ctx, handshakeTimeout)
	defer cancel()
	if err := c.conn.request(readCtx, codexproto.MethodThreadRead, codexproto.ThreadReadParams{
		ThreadID: c.threadID, IncludeTurns: &includeTurns,
	}, &response); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		c.log.Warn("live Codex turn recovery unavailable; preserving running work", "error", err)
		return nil
	}
	if response.Thread.ID != c.threadID {
		return fmt.Errorf("%w: thread/read returned %q, requested %q", ports.ErrChatRecoveryInconclusive, response.Thread.ID, c.threadID)
	}
	nativeTurnID := c.nativeID(providerTurnID)
	for _, turn := range response.Thread.Turns {
		if turn.ID != nativeTurnID {
			continue
		}
		switch turn.Status {
		case codexproto.TurnStatusCompleted, codexproto.TurnStatusFailed, codexproto.TurnStatusInterrupted:
		default:
			return nil
		}
		citations := newCitationFormatter()
		for _, item := range turn.Items {
			citations.observeItem(c.threadID, turn.ID, item)
		}
		events, err := c.historyTurnEvents(turn, citations)
		if err != nil {
			return err
		}
		repair := liveTurnRecovery{turnID: nativeTurnID}
		for _, event := range events {
			// Dispatch and its user message are already durable. Restore only
			// the provider output and terminal fact that could have been lost.
			if event.Kind == ports.ChatEventTurnStarted || event.Kind == ports.ChatEventUserMessageCompleted {
				continue
			}
			event.ProviderConversationID = c.threadID
			repair.events = append(repair.events, event)
		}
		if reconcile != nil {
			for i, event := range repair.events {
				repair.events[i] = c.scopedEvent(event)
			}
			repair.events = reconcile(repair.events)
			// The common pump consumes native IDs and scopes exactly once at emit.
			for i := range repair.events {
				event := &repair.events[i]
				event.ProviderTurnID = c.nativeID(event.ProviderTurnID)
				event.ProviderItemID = c.nativeID(event.ProviderItemID)
				event.ProviderEventID = c.nativeID(event.ProviderEventID)
				event.RequestID = c.nativeID(event.RequestID)
			}
		}
		// The pump serializes recovery with live output. Returning on admission
		// lets the service start projection even for a large recovered turn.
		select {
		case c.liveRecovery <- repair:
			return nil
		case <-c.pumpDone:
			return ErrConnClosed
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}
