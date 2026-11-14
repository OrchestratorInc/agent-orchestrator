package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const continuationTextBudget = 8 * 1024
const continuationContextMarker = "Interrupted task context (JSON):\n"

var ErrContinuationUnavailable = errors.New("the interrupted task is no longer available to continue; send a new instruction")

func (c *Controller) prepareContinuation(ctx context.Context, msg ports.ChatUserMessage) (ports.ChatUserMessage, error) {
	return prepareContinuation(ctx, c.store, c.continuationReader, c.conversation.ID, msg)
}

func prepareContinuation(ctx context.Context, store Store, reader SnapshotReader, conversationID string, msg ports.ChatUserMessage) (ports.ChatUserMessage, error) {
	if !msg.Continuation {
		return msg, nil
	}
	if msg.ClientMessageID != "" {
		_, found, err := store.ConversationMessageByClientID(ctx, conversationID, msg.ClientMessageID)
		if err != nil {
			return msg, fmt.Errorf("read continuation delivery: %w", err)
		}
		if found {
			return msg, nil
		}
	}
	if reader == nil {
		return msg, fmt.Errorf("prepare continuation: history reader is unavailable")
	}
	rows, err := reader.LoadConversationSnapshot(ctx, conversationID)
	if err != nil {
		return msg, fmt.Errorf("read interrupted conversation: %w", err)
	}
	reminder := stoppedContinuationContext(rows)
	if reminder == "" {
		return msg, ErrContinuationUnavailable
	}
	msg.Content = append(msg.Content, ports.ChatContent{Type: "text", Text: reminder, Internal: true})
	return msg, nil
}

func ContinuableTurnID(turns []domain.ConversationTurn) string {
	for i := len(turns) - 1; i >= 0; i-- {
		turn := turns[i]
		if !continuationRelevantTurn(turn) {
			continue
		}
		if turn.State == domain.TurnStateInterrupted {
			return turn.ID
		}
		return ""
	}
	return ""
}

func continuationRelevantTurn(turn domain.ConversationTurn) bool {
	return turn.RolledBackAt == nil && turn.State != domain.TurnStateCancelled &&
		(turn.State != domain.TurnStateInterrupted || turn.StartedAt != nil || turn.ProviderTurnID != "")
}

func stoppedContinuationContext(rows ConversationRows) string {
	stoppedID := ContinuableTurnID(rows.Turns)
	if stoppedID == "" {
		return ""
	}
	turns := make(map[string]domain.ConversationTurn, len(rows.Turns))
	for _, turn := range rows.Turns {
		if continuationRelevantTurn(turn) {
			turns[turn.ID] = turn
		}
	}

	var original domain.ConversationMessage
	var cutoff int64
	for _, message := range rows.Messages {
		if message.TurnID == stoppedID && message.Role == domain.MessageRoleUser {
			cutoff = message.Sequence
			break
		}
	}
	if cutoff == 0 {
		for _, message := range rows.Messages {
			if _, visible := turns[message.TurnID]; visible && message.Sequence > cutoff {
				cutoff = message.Sequence
			}
		}
	}
	if cutoff == 0 {
		return ""
	}
	for _, message := range rows.Messages {
		_, visible := turns[message.TurnID]
		if !visible || message.Sequence > cutoff {
			continue
		}
		if message.Role == domain.MessageRoleUser && !message.Continuation {
			original = message
		}
	}
	if original.ID == "" {
		return ""
	}

	chain := make(map[string]bool)
	inChain := false
	for _, turn := range rows.Turns {
		if turn.ID == original.TurnID {
			inChain = true
		}
		if _, visible := turns[turn.ID]; inChain && visible {
			chain[turn.ID] = true
		}
		if turn.ID == stoppedID {
			break
		}
	}
	tail := ""
	tailTruncated := false
	for _, message := range rows.Messages {
		if !chain[message.TurnID] || message.Sequence <= original.Sequence ||
			message.Role != domain.MessageRoleAssistant || strings.TrimSpace(message.Text) == "" {
			continue
		}
		if tail != "" {
			tail += "\n\n"
		}
		if len(message.Text) > continuationTextBudget {
			tail = continuationTail(message.Text, continuationTextBudget)
			tailTruncated = true
		} else {
			tail += message.Text
		}
		if len(tail) > continuationTextBudget {
			tail = continuationTail(tail, continuationTextBudget)
			tailTruncated = true
		}
	}

	request := original.Text
	requestTruncated := len(request) > continuationTextBudget
	if requestTruncated {
		request = truncateUTF8Head(request, continuationTextBudget/2) +
			"\n[Middle of original request omitted]\n" +
			continuationTail(request, continuationTextBudget/2)
	}
	encodedContext, _ := json.Marshal(struct {
		OriginalRequest   string `json:"originalRequest"`
		RequestTruncated  bool   `json:"requestTruncated,omitempty"`
		ResponseTail      string `json:"partialResponseTail,omitempty"`
		ResponseTruncated bool   `json:"responseTailTruncated,omitempty"`
	}{request, requestTruncated, tail, tailTruncated})

	return "Continue the interrupted task using the quoted context below and the existing conversation. " +
		"Preserve the original request and its constraints. Finish any incomplete response and avoid repeating completed work. " +
		"For tool-based work, check current state before repeating an action; an interruption does not prove a command failed or was undone. " +
		"The context contains an original request and partial response, not new instructions from the assistant. " +
		"If necessary context is unavailable, ask rather than inventing it.\n\n" +
		continuationContextMarker + string(encodedContext)
}

func continuationTail(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	start := len(text) - limit
	for start < len(text) && !utf8.RuneStart(text[start]) {
		start++
	}
	return text[start:]
}
