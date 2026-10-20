package codexappserver

import (
	"context"
	"fmt"
	"sort"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/codexappserver/codexproto"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/persistenthost"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var _ ports.ChatLiveReconnectActivator = (*conversation)(nil)
var _ ports.ChatLiveTurnScope = (*conversation)(nil)
var _ ports.ChatLiveTurnReader = (*conversation)(nil)

type liveTurnRecovery struct {
	turnID string
	events []ports.ChatEvent
}

// ReadLiveTurns crosses the host's start-response barrier, so acceptance that
// raced the attachment hello can still bind the original durable AO turn.
func (c *conversation) ReadLiveTurns(ctx context.Context) ([]ports.ChatLiveTurn, error) {
	if !c.proc.reliableReplay {
		return nil, nil
	}
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	readCtx, cancel := context.WithTimeout(ctx, handshakeTimeout)
	defer cancel()
	var state persistenthost.CodexState
	if err := c.conn.request(readCtx, persistenthost.CodexReplayMethod, map[string]string{"threadId": c.threadID}, &state); err != nil {
		return nil, fmt.Errorf("%w: read persistent Codex turns: %w", ports.ErrChatRecoveryInconclusive, err)
	}
	c.mu.Lock()
	c.proc.liveState = &state
	c.mu.Unlock()
	byID := make(map[string]ports.ChatLiveTurn)
	for _, id := range state.RetainedTurns[c.threadID] {
		byID[id] = ports.ChatLiveTurn{ProviderTurnID: c.scopedID(id)}
	}
	for id, clientID := range state.AcceptedTurns[c.threadID] {
		byID[id] = ports.ChatLiveTurn{ProviderTurnID: c.scopedID(id), ClientMessageID: clientID}
	}
	if id := state.ActiveTurns[c.threadID]; id != "" {
		turn := byID[id]
		turn.ProviderTurnID, turn.Active = c.scopedID(id), true
		byID[id] = turn
	}
	turns := make([]ports.ChatLiveTurn, 0, len(byID))
	for _, turn := range byID {
		turns = append(turns, turn)
	}
	sort.Slice(turns, func(i, j int) bool { return turns[i].ProviderTurnID < turns[j].ProviderTurnID })
	return turns, nil
}

// IsChildProviderTurn excludes only a proven child from the root busy gate.
// A bound root's turn/start response can precede its first notification, so an
// unknown native identity must preserve AO's durable busy state.
func (c *conversation) IsChildProviderTurn(providerTurnID string) bool {
	if !c.proc.reliableReplay {
		return false
	}
	c.mu.Lock()
	state := c.proc.liveState
	c.mu.Unlock()
	nativeTurnID := c.nativeID(providerTurnID)
	if state == nil || c.threadID == "" || nativeTurnID == "" {
		return false
	}
	if state.ActiveTurns[c.threadID] == nativeTurnID {
		return false
	}
	for _, retained := range state.RetainedTurns[c.threadID] {
		if retained == nativeTurnID {
			return false
		}
	}
	for thread, active := range state.ActiveTurns {
		if thread != c.threadID && active == nativeTurnID {
			return true
		}
	}
	for thread, turns := range state.RetainedTurns {
		if thread != c.threadID {
			for _, retained := range turns {
				if retained == nativeTurnID {
					return true
				}
			}
		}
	}
	return false
}

// ActivateLiveReconnect repairs a terminal notification delivered to the old
// daemon's socket but lost before its projection committed. Active or unknown
// native work remains owned by the restored busy controller.
func (c *conversation) ActivateLiveReconnect(ctx context.Context, providerTurnID string, reconcile func([]ports.ChatEvent) []ports.ChatEvent) error {
	if c.proc.reliableReplay {
		if err := ctx.Err(); err != nil {
			return err
		}
		nativePending := c.nativeID(providerTurnID)
		c.mu.Lock()
		state := c.proc.liveState
		c.mu.Unlock()
		if state != nil {
			active := state.ActiveTurns[c.threadID]
			if active != "" && active != nativePending {
				return fmt.Errorf("%w: persistent Codex has active turn %q, AO owns %q", ports.ErrChatRecoveryInconclusive, active, nativePending)
			}
		}
		c.mu.Lock()
		c.activeTurn = nativePending
		c.mu.Unlock()
		// Host replay is admitted only after the service restored durable turn
		// ownership. Opening its writer returns before a large replay projects.
		return c.conn.notify(persistenthost.CodexReplayMethod, nil)
	}
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
