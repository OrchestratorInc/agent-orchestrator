package chat

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const maxExcerptSegments = 32

// Shape validation also runs before provisioning/queueing, without trusting any
// client rendering of a source. Empty legacy slots keep old clients compatible.
func validateExcerptShape(ref ports.ChatExcerptReference) error {
	if strings.TrimSpace(ref.Text) == "" || len(ref.Text) > maxExcerptTextBytes || ref.Revision < 0 {
		return ErrExcerptInvalid
	}
	if ref.Segments == nil {
		if ref.MessageID == "" {
			return ErrExcerptInvalid
		}
		return nil
	}
	if ref.MessageID != "" || ref.Revision != 0 || len(ref.Segments) == 0 || len(ref.Segments) > maxExcerptSegments {
		return ErrExcerptInvalid
	}
	parts := make([]string, 0, len(ref.Segments))
	for _, part := range ref.Segments {
		if part.SourceID == "" || part.Revision < 0 || strings.TrimSpace(part.Text) == "" {
			return ErrExcerptInvalid
		}
		switch part.Kind {
		case "message":
			if part.Field != "" {
				return ErrExcerptInvalid
			}
		case "command":
			switch part.Field {
			case "command", "output", "terminalInput", "explanation":
			default:
				return ErrExcerptInvalid
			}
		default:
			return ErrExcerptInvalid
		}
		parts = append(parts, part.Text)
	}
	if strings.Join(parts, "\n\n") != ref.Text {
		return ErrExcerptInvalid
	}
	return nil
}

// All segments must belong to the same completed turn. A caller cannot use an
// activity ID from another conversation, a stale output, or a fabricated label.
func resolveExcerptSegments(rows ConversationRows, ref ports.ChatExcerptReference) (domain.ConversationTurn, error) {
	return resolveExcerptSegmentsWithAnchorError(rows, ref, ErrExcerptStale)
}

func resolveExcerptSegmentsWithAnchorError(rows ConversationRows, ref ports.ChatExcerptReference, anchorError error) (domain.ConversationTurn, error) {
	if err := validateExcerptShape(ref); err != nil {
		return domain.ConversationTurn{}, err
	}
	var turnID string
	for _, part := range ref.Segments {
		var found bool
		var sourceTurn string
		if part.Kind == "message" {
			for _, message := range rows.Messages {
				if message.ID != part.SourceID {
					continue
				}
				if message.ConversationID != ref.ConversationID || message.Revision != part.Revision || message.Streaming ||
					(message.Role != domain.MessageRoleUser && message.Role != domain.MessageRoleAssistant) {
					return domain.ConversationTurn{}, ErrExcerptStale
				}
				if !sourceContainsExcerptSelection(message, part.Text) {
					return domain.ConversationTurn{}, ErrExcerptInvalid
				}
				found, sourceTurn = true, message.TurnID
				break
			}
		} else {
			for _, activity := range rows.Activities {
				if activity.ID != part.SourceID {
					continue
				}
				if activity.ConversationID != ref.ConversationID || activity.Revision != part.Revision || activity.Kind != domain.ActivityKindCommand ||
					(activity.Status != domain.ActivityStatusCompleted && activity.Status != domain.ActivityStatusFailed) {
					return domain.ConversationTurn{}, ErrExcerptStale
				}
				text := commandExcerptField(activity, part.Field)
				if strings.TrimSpace(text) == "" || normalizeExcerptWhitespace(part.Text) == "" || !strings.Contains(normalizeExcerptWhitespace(text), normalizeExcerptWhitespace(part.Text)) {
					return domain.ConversationTurn{}, ErrExcerptInvalid
				}
				found, sourceTurn = true, activity.TurnID
				break
			}
		}
		if !found || sourceTurn == "" {
			return domain.ConversationTurn{}, ErrExcerptStale
		}
		if turnID != "" && turnID != sourceTurn {
			return domain.ConversationTurn{}, ErrExcerptInvalid
		}
		turnID = sourceTurn
	}
	for _, turn := range rows.Turns {
		if turn.ID == turnID && turn.ConversationID == ref.ConversationID && turn.State == domain.TurnStateCompleted && turn.RolledBackAt == nil {
			return turn, nil
		}
	}
	return domain.ConversationTurn{}, anchorError
}

func commandExcerptField(activity domain.ConversationActivity, field string) string {
	var detail map[string]json.RawMessage
	_ = json.Unmarshal(activity.Detail, &detail)
	if detail == nil {
		detail = map[string]json.RawMessage{}
	}
	// The HTTP timeline projects accumulated streams over aggregate detail.
	if activity.CommandOutput != "" {
		detail["output"], _ = json.Marshal(activity.CommandOutput)
	}
	if activity.StreamedText != "" {
		detail["terminalInput"], _ = json.Marshal(activity.StreamedText)
	}
	var text string
	if field == "explanation" {
		raw := detail["reason"]
		if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			raw = detail["text"]
		}
		if json.Unmarshal(raw, &text) != nil {
			return ""
		}
	} else if json.Unmarshal(detail[field], &text) != nil {
		return ""
	}
	// Only text that can be checked against durable bytes is attachable. Do not
	// reproduce the browser's JSON, ANSI, carriage-return or caret rendering.
	for _, ch := range text {
		if (ch < 32 && ch != '\n' && ch != '\t') || ch == 127 {
			return ""
		}
	}
	return text
}

func hydrateSegmentedExcerpt(ctxRows ConversationRows, ref ports.ChatExcerptReference) (ports.ChatContent, error) {
	turn, err := resolveExcerptSegments(ctxRows, ref)
	if err != nil {
		return ports.ChatContent{}, err
	}
	var user, assistant []string
	for _, message := range ctxRows.Messages {
		if message.TurnID != turn.ID || message.Streaming {
			continue
		}
		if message.Role == domain.MessageRoleUser {
			user = append(user, message.Text)
		}
		if message.Role == domain.MessageRoleAssistant {
			assistant = append(assistant, message.Text)
		}
	}
	if len(user) == 0 {
		return ports.ChatContent{}, ErrExcerptStale
	}
	// A command-only finished turn need not have an assistant prose message.
	identity, _ := json.Marshal(ref.Segments)
	return ports.ChatContent{Type: "excerpt", URI: ports.ChatExcerptResourceURIPrefix + fmt.Sprintf("selection/%x", sha256.Sum256(identity)),
		Name: "selected turn content", MIMEType: "text/plain", Text: ref.Text,
		Excerpt: &ports.ChatExcerptContext{Reference: ref, SelectedText: ref.Text,
			UserMessage: truncateExcerptContext(strings.Join(user, "\n\n")), AssistantMessage: truncateExcerptContext(strings.Join(assistant, "\n\n"))}}, nil
}

func segmentedExcerptError(err error) error {
	return fmt.Errorf("%w: selected message or command changed; reselect it", err)
}
