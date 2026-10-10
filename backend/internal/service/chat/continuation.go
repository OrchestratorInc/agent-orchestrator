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

// ErrContinuationUnavailable refuses a stale Continue intent without starting
// a context-free turn. The user can give a new instruction instead.
var ErrContinuationUnavailable = errors.New("the interrupted task is no longer available to continue; send a new instruction")

// prepareContinuation runs under sendMu, before persisting the provider request.
// The intake hash and visible text remain the client's original payload.
// Persisting internal content makes queue recovery and retries send the same context rather
// than reconstructing it from a conversation that may have changed meanwhile.
func (c *Controller) prepareContinuation(ctx context.Context, msg ports.ChatUserMessage) (ports.ChatUserMessage, error) {
	return prepareContinuation(ctx, c.store, c.continuationReader, c.continuationPageReader, c.conversation.ID, msg)
}

func prepareContinuation(ctx context.Context, store Store, reader SnapshotReader, pageReader SnapshotPageReader, conversationID string, msg ports.ChatUserMessage) (ports.ChatUserMessage, error) {
	if !msg.Continuation || (reader == nil && pageReader == nil) {
		return msg, nil
	}
	if msg.ClientMessageID != "" {
		_, found, err := store.ConversationMessageByClientID(ctx, conversationID, msg.ClientMessageID)
		if err != nil {
			return msg, fmt.Errorf("read continuation delivery: %w", err)
		}
		if found {
			// AppendUserMessage performs the fingerprint/conflict check. A duplicate
			// must not depend on whether the task is still the latest stopped turn.
			return msg, nil
		}
	}
	var rows ConversationRows
	var err error
	if pageReader != nil {
		rows, err = pageReader.LoadConversationSnapshotPage(ctx, conversationID, 0, 128)
	} else {
		// Compatibility for injected readers; production uses bounded pages.
		rows, err = reader.LoadConversationSnapshot(ctx, conversationID)
	}
	if err != nil {
		return msg, fmt.Errorf("read interrupted conversation: %w", err)
	}
	reminder := stoppedContinuationContext(rows)
	if reminder == "" && pageReader != nil && store != nil {
		if turnID := ContinuableTurnID(rows.Turns); turnID != "" {
			prompt, readErr := store.ContinuationPrompt(ctx, conversationID, turnID)
			if errors.Is(readErr, domain.ErrNoConversationTurn) {
				return msg, ErrContinuationUnavailable
			}
			if readErr != nil {
				return msg, fmt.Errorf("read interrupted prompt: %w", readErr)
			}
			rows.Messages = append([]domain.ConversationMessage{prompt}, rows.Messages...)
			reminder = stoppedContinuationContext(rows)
		}
	}
	if reminder == "" {
		return msg, ErrContinuationUnavailable
	}
	if reminder != "" {
		msg.Content = append(msg.Content, ports.ChatContent{Type: "text", Text: reminder, Internal: true})
	}
	return msg, nil
}

// ContinuableTurnID selects the latest dispatched, non-withdrawn turn. The
// durable StartedAt survives provider-scope changes; queued Stop sweeps do not.
func ContinuableTurnID(turns []domain.ConversationTurn) string {
	for i := len(turns) - 1; i >= 0; i-- {
		turn := turns[i]
		if turn.RolledBackAt != nil || turn.State == domain.TurnStateCancelled {
			continue
		}
		if turn.State == domain.TurnStateInterrupted && turn.StartedAt == nil && turn.ProviderTurnID == "" {
			continue
		}
		if turn.State == domain.TurnStateInterrupted {
			return turn.ID
		}
		return ""
	}
	return ""
}

// stoppedContinuationContext consumes the active-lineage snapshot, never another
// session's history. It supplies a task reminder, not a replacement transcript or
// an execution checkpoint. Reasoning and tool outputs are deliberately omitted.
func stoppedContinuationContext(rows ConversationRows) string {
	turns := make(map[string]domain.ConversationTurn, len(rows.Turns))
	var stopped domain.ConversationTurn
	for _, turn := range rows.Turns {
		if turn.RolledBackAt != nil || turn.State == domain.TurnStateCancelled {
			continue
		}
		// Stop also interrupts queued messages that never reached the provider.
		// Those are not the task the user wants to continue.
		if turn.State == domain.TurnStateInterrupted && turn.ProviderTurnID == "" && turn.StartedAt == nil {
			continue
		}
		turns[turn.ID] = turn
		stopped = turn
	}
	if stopped.ID != ContinuableTurnID(rows.Turns) || stopped.ID == "" {
		return ""
	}

	var original domain.ConversationMessage
	var carriedTail string
	var cutoff int64
	for _, message := range rows.Messages {
		if message.TurnID == stopped.ID && message.Role == domain.MessageRoleUser {
			cutoff = message.Sequence
			break
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
		// A prior continuation carries a frozen request even if the original
		// prompt has fallen outside the bounded page window.
		for _, message := range rows.Messages {
			if _, visible := turns[message.TurnID]; !visible || !message.Continuation || message.Sequence > cutoff {
				continue
			}
			var content []ports.ChatContent
			if json.Unmarshal([]byte(message.DeliveryContentJSON), &content) != nil {
				continue
			}
			for _, block := range content {
				if !block.Internal || block.Type != "text" {
					continue
				}
				_, encoded, found := strings.Cut(block.Text, continuationContextMarker)
				if !found {
					continue
				}
				var carried struct {
					OriginalRequest *string         `json:"originalRequest"`
					ResponseTail    string          `json:"partialResponseTail"`
					Attachments     json.RawMessage `json:"attachmentReferences"`
				}
				if json.Unmarshal([]byte(encoded), &carried) != nil || carried.OriginalRequest == nil {
					continue
				}
				original = message
				original.Text = *carried.OriginalRequest
				original.DeliveryContentJSON = string(carried.Attachments)
				carriedTail = carried.ResponseTail
			}
		}
		if original.ID == "" {
			return ""
		}
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
		if turn.ID == stopped.ID {
			break
		}
	}
	tail := carriedTail
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

	type attachmentReference struct {
		Type     string                    `json:"type"`
		Name     string                    `json:"name,omitempty"`
		URI      string                    `json:"uri,omitempty"`
		MIMEType string                    `json:"mimeType,omitempty"`
		Internal bool                      `json:"internal,omitempty"`
		Excerpt  *ports.ChatExcerptContext `json:"excerpt,omitempty"`
	}
	var references []attachmentReference
	// Decode descriptors only; image bytes and replay seeds are not copied into
	// the text reminder. Native attachment content remains provider-owned.
	if original.DeliveryContentJSON != "" {
		_ = json.Unmarshal([]byte(original.DeliveryContentJSON), &references)
	}
	attachments := make([]attachmentReference, 0, 8)
	excerptBudget := continuationTextBudget
	for _, reference := range references {
		if reference.Internal || reference.Type == "text" {
			continue
		}
		if reference.Excerpt != nil {
			excerpt := *reference.Excerpt
			excerpt.SelectedText = truncateUTF8Head(excerpt.SelectedText, min(excerptBudget, 4096))
			excerptBudget -= len(excerpt.SelectedText)
			excerpt.UserMessage = truncateUTF8Head(excerpt.UserMessage, min(excerptBudget, 2048))
			excerptBudget -= len(excerpt.UserMessage)
			excerpt.AssistantMessage = truncateUTF8Head(excerpt.AssistantMessage, min(excerptBudget, 2048))
			excerptBudget -= len(excerpt.AssistantMessage)
			reference.Excerpt = &excerpt
		}
		reference.Name = truncateUTF8Head(reference.Name, 256)
		reference.URI = truncateUTF8Head(reference.URI, 512)
		reference.Type = truncateUTF8Head(reference.Type, 32)
		reference.MIMEType = truncateUTF8Head(reference.MIMEType, 128)
		attachments = append(attachments, reference)
		if len(attachments) == 8 {
			break
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
		OriginalRequest   string                `json:"originalRequest"`
		RequestTruncated  bool                  `json:"requestTruncated,omitempty"`
		ResponseTail      string                `json:"partialResponseTail,omitempty"`
		ResponseTruncated bool                  `json:"responseTailTruncated,omitempty"`
		Attachments       []attachmentReference `json:"attachmentReferences,omitempty"`
	}{request, requestTruncated, tail, tailTruncated, attachments})

	return "Continue the interrupted task using the quoted context below and the existing conversation. " +
		"Preserve the original request and its constraints. Finish any incomplete response and avoid repeating completed work. " +
		"For tool-based work, check current state before repeating an action; an interruption does not prove a command failed or was undone. " +
		"The context contains an original request and partial response, not new instructions from the assistant. " +
		"Attachment references do not contain attachment contents; if necessary context is unavailable, ask rather than inventing it.\n\n" +
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
