package codexappserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/codexappserver/codexproto"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var (
	_ ports.ChatRollbacker       = (*conversation)(nil)
	_ ports.ChatForker           = (*conversation)(nil)
	_ ports.ChatRenamer          = (*conversation)(nil)
	_ ports.ChatHistoryReader    = (*conversation)(nil)
	_ ports.ChatHistoryRefresher = (*conversation)(nil)
)

type providerRefusal struct{ err error }

func (e *providerRefusal) Error() string { return e.err.Error() }
func (e *providerRefusal) Unwrap() error { return e.err }

func (e *providerRefusal) ChatRefusal() bool { return true }

func refuse(format string, args ...any) error {
	return &providerRefusal{err: fmt.Errorf(format, args...)}
}

func asRefusal(err error) error {
	var rpcErr *rpcError
	if errors.As(err, &rpcErr) && rpcErr.Code == -32600 {
		return &providerRefusal{err: err}
	}
	return err
}

type providerTurn struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

func (c *conversation) ReadHistory(ctx context.Context) ([]ports.ChatEvent, error) {
	var resp codexproto.ThreadReadResponse
	includeTurns := true
	if err := c.conn.request(ctx, codexproto.MethodThreadRead, codexproto.ThreadReadParams{
		ThreadID:     c.threadID,
		IncludeTurns: &includeTurns,
	}, &resp); err != nil {
		return nil, asRefusal(fmt.Errorf("thread/read history: %w", err))
	}
	if resp.Thread.ID != c.threadID {
		return nil, fmt.Errorf("thread/read returned %q, requested %q", resp.Thread.ID, c.threadID)
	}
	c.mu.Lock()
	c.historyParentID = deref(resp.Thread.ForkedFromID)
	c.mu.Unlock()
	for _, turn := range resp.Thread.Turns {
		state := turnStateFrom(string(turn.Status))
		if state == domain.TurnStateRunning || state == domain.TurnStateQueued {
			return nil, fmt.Errorf("%w: Codex turn %s is %s",
				ports.ErrChatHistoryUnsettled, turn.ID, turn.Status)
		}
	}

	events := make([]ports.ChatEvent, 0, len(resp.Thread.Turns)*4)
	citations := newCitationFormatter()
	for _, turn := range resp.Thread.Turns {
		for _, item := range turn.Items {
			citations.observeItem(c.threadID, turn.ID, item)
		}
	}
	for _, turn := range resp.Thread.Turns {
		state := turnStateFrom(string(turn.Status))

		events = append(events, ports.ChatEvent{
			Kind:            ports.ChatEventTurnStarted,
			ProviderEventID: historyEventID(c.threadID, turn.ID, "started"),
			ProviderTurnID:  turn.ID,
		})

		for itemIndex, nativeItem := range turn.Items {
			item := nativeItem
			itemID := deref(item.ID)
			eventItemID := itemID
			if itemID == "" {
				eventItemID = fmt.Sprintf("item-%d", itemIndex)
				itemID = fmt.Sprintf("%s-%s", turn.ID, eventItemID)
				item.ID = &itemID
			}

			if item.Type == codexproto.ThreadItemTypeUserMessage {
				text := historicalUserText(item)
				if text == "" {
					continue
				}
				clientID := deref(item.ClientID)
				if clientID == "" {
					clientID = historyEventID(c.threadID, turn.ID, "user", eventItemID)
				}
				events = append(events, ports.ChatEvent{
					Kind:            ports.ChatEventUserMessageCompleted,
					ProviderEventID: historyEventID(c.threadID, turn.ID, "user", eventItemID),
					ProviderTurnID:  turn.ID,
					ProviderItemID:  itemID,
					ClientMessageID: clientID,
					Text:            text,
				})
				continue
			}

			params, err := json.Marshal(map[string]any{
				"threadId": c.threadID,
				"turnId":   turn.ID,
				"item":     item,
			})
			if err != nil {
				return nil, fmt.Errorf("encode history item %s: %w", itemID, err)
			}
			for eventIndex, event := range normalizeItem(params, true) {
				event, _ = citations.formatEvent(c.threadID, event)
				event.ProviderEventID = historyEventID(
					c.threadID, turn.ID, "item", eventItemID, string(event.Kind), fmt.Sprint(eventIndex))
				events = append(events, event)
			}
		}

		completed := ports.ChatEvent{
			Kind:            ports.ChatEventTurnCompleted,
			ProviderEventID: historyEventID(c.threadID, turn.ID, "completed"),
			ProviderTurnID:  turn.ID,
			TurnState:       state,
		}
		if turn.Error != nil {
			completed.Err = codexProviderFailure(*turn.Error)
		}
		events = append(events, completed)
	}
	for i := range events {
		events[i] = c.scopedEvent(events[i])
	}
	return events, nil
}

func (c *conversation) RefreshHistory(ctx context.Context) ([]ports.ChatEvent, error) {
	return c.ReadHistory(ctx)
}

func (c *conversation) InheritedHistory(ctx context.Context, ancestor domain.ConversationBranch, events []ports.ChatEvent) ([]ports.ChatEvent, error) {
	c.mu.Lock()
	parent := c.historyParentID
	c.mu.Unlock()
	seen := map[string]bool{c.threadID: true}
	for parent != "" && parent != ancestor.ProviderConversationID {
		if seen[parent] || len(seen) >= 64 {
			return nil, nil
		}
		seen[parent] = true
		var response codexproto.ThreadReadResponse
		if err := c.conn.request(ctx, codexproto.MethodThreadRead, codexproto.ThreadReadParams{ThreadID: parent}, &response); err != nil {
			return nil, ctx.Err()
		}
		if response.Thread.ID != parent {
			return nil, nil
		}
		parent = deref(response.Thread.ForkedFromID)
	}
	if parent == "" {
		return nil, nil
	}
	previous := conversation{}
	if ancestor.ProviderIDsScoped {
		previous.providerScopeID = ancestor.ProviderScopeID
	}
	mapped := append([]ports.ChatEvent(nil), events...)
	for i := range mapped {
		mapped[i].ProviderTurnID = previous.scopedID(c.nativeID(mapped[i].ProviderTurnID))
		mapped[i].ProviderItemID = previous.scopedID(c.nativeID(mapped[i].ProviderItemID))
	}
	return mapped, nil
}

func historyEventID(parts ...string) string {
	return "codex-history:" + strings.Join(parts, ":")
}

func historicalUserText(item codexproto.ThreadItem) string {
	var inputs []codexproto.UserInput
	if len(item.Content) > 0 && json.Unmarshal(item.Content, &inputs) == nil {
		parts := make([]string, 0, len(inputs))
		for _, input := range inputs {
			if text := strings.TrimSpace(deref(input.Text)); text != "" {
				parts = append(parts, text)
				continue
			}
			switch input.Type {
			case codexproto.UserInputTypeSkill:
				if name := strings.TrimSpace(deref(input.Name)); name != "" {
					parts = append(parts, "/"+name)
				} else {
					parts = append(parts, "[Skill]")
				}
			case codexproto.UserInputTypeMention:
				mention := firstNonEmpty(deref(input.Name), deref(input.Path))
				if mention == "" {
					parts = append(parts, "[Mention]")
				} else {
					parts = append(parts, "@"+mention)
				}
			case codexproto.UserInputTypeImage, codexproto.UserInputTypeLocalImage:
				parts = append(parts, "[Image]")
			case codexproto.UserInputTypeAudio, codexproto.UserInputTypeLocalAudio:
				parts = append(parts, "[Audio]")
			}
		}
		if text := strings.TrimSpace(strings.Join(parts, "\n")); text != "" {
			return text
		}
	}
	return strings.TrimSpace(deref(item.Text))
}

func (c *conversation) Rollback(ctx context.Context, providerTurnID string) error {
	providerTurnID = c.nativeID(providerTurnID)
	if strings.TrimSpace(providerTurnID) == "" {
		return errors.New("rollback needs a provider turn id")
	}

	c.sendMu.Lock()
	defer c.sendMu.Unlock()

	turns, err := c.readTurns(ctx)
	if err != nil {
		return err
	}

	discard := 0
	for i, turn := range turns {
		if turn.ID == providerTurnID {
			discard = len(turns) - i
			break
		}
	}
	if discard == 0 {
		return refuse("turn %q is not in the provider's history", providerTurnID)
	}

	if err := c.conn.request(ctx, "thread/rollback", map[string]any{
		"threadId": c.threadID,
		"numTurns": discard,
	}, nil); err != nil {
		var rpcErr *rpcError
		if errors.As(err, &rpcErr) && (rpcErr.Code == -32601 ||
			(rpcErr.Code == -32600 && strings.Contains(rpcErr.Message, "unknown variant `thread/rollback`"))) {
			if err := c.conn.request(ctx, "thread/revert", map[string]any{
				"threadId": c.threadID, "beforeTurnId": providerTurnID,
			}, nil); err != nil {
				return asRefusal(fmt.Errorf("thread/revert: %w", err))
			}
			return nil
		}
		return asRefusal(fmt.Errorf("thread/rollback: %w", err))
	}
	return nil
}

func (c *conversation) readTurns(ctx context.Context) ([]providerTurn, error) {
	var resp struct {
		Thread struct {
			Turns []providerTurn `json:"turns"`
		} `json:"thread"`
	}
	if err := c.conn.request(ctx, "thread/read", map[string]any{
		"threadId":     c.threadID,
		"includeTurns": true,
	}, &resp); err != nil {
		return nil, asRefusal(fmt.Errorf("thread/read: %w", err))
	}
	return resp.Thread.Turns, nil
}

func (c *conversation) Fork(ctx context.Context, lastProviderTurnID *string) (string, error) {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()

	params := codexproto.ThreadForkParams{ThreadID: c.threadID}
	if lastProviderTurnID != nil {
		anchor := c.nativeID(strings.TrimSpace(*lastProviderTurnID))
		if anchor == "" {
			return "", errors.New("fork anchor must not be blank")
		}
		params.LastTurnID = &anchor
	}
	var resp codexproto.ThreadForkResponse
	if err := c.conn.request(ctx, codexproto.MethodThreadFork, params, &resp); err != nil {
		return "", asRefusal(fmt.Errorf("thread/fork: %w", err))
	}
	if resp.Thread.ID == "" {
		return "", errors.New("thread/fork returned no thread id")
	}
	return resp.Thread.ID, nil
}

func (c *conversation) SetTitle(ctx context.Context, title string) error {
	trimmed := strings.TrimSpace(title)
	if trimmed == "" {
		return refuse("thread title must not be empty")
	}

	c.sendMu.Lock()
	defer c.sendMu.Unlock()

	if err := c.conn.request(ctx, "thread/name/set", map[string]any{
		"threadId": c.threadID,
		"name":     trimmed,
	}, nil); err != nil {
		return asRefusal(fmt.Errorf("thread/name/set: %w", err))
	}
	return nil
}

func (c *conversation) DeleteFork(ctx context.Context, providerConversationID string) error {
	id := strings.TrimSpace(providerConversationID)
	if id == "" || id == c.threadID {
		return errors.New("refusing to delete the active provider conversation")
	}
	err := c.conn.request(ctx, codexproto.MethodThreadDelete,
		codexproto.ThreadDeleteParams{ThreadID: id}, nil)
	var refusal *rpcError
	if errors.As(err, &refusal) && refusal.Code == -32600 && refusal.Message == "no rollout found for thread id "+id {
		return nil
	}
	return err
}
