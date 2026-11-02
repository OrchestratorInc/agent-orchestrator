package coderagents

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/coderagents/coderapi"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/commanddetail"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// ReadHistory returns the native snapshot captured before the stream cursor
// advances. AO reconciles these stable events against its durable projection
// before consuming live events; importing an unchanged snapshot is idempotent.
func (c *conversation) ReadHistory(ctx context.Context) ([]ports.ChatEvent, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return slices.Clone(c.history), nil
}

func historyID(key string, parts ...string) string {
	return "coder-agents:" + key + ":" + strings.Join(parts, ":")
}

// replayHistory projects persisted messages, never executing their tools. Coder
// exposes no native turn ID/outcome per message, so user record IDs delimit
// turns and old outcomes remain recovered rather than being guessed successful.
func replayHistory(key string, chat coderapi.Chat, messages []coderapi.ChatMessage) ([]ports.ChatEvent, string) {
	var events []ports.ChatEvent
	var turnID string
	calls := map[string]coderapi.ChatMessagePart{}
	appendEvent := func(id string, event ports.ChatEvent) {
		event.ProviderConversationID = key
		event.ProviderTurnID = turnID
		event.ProviderEventID = historyID(key, id)
		events = append(events, event)
	}
	finish := func(state domain.TurnState, err error) {
		appendEvent(turnID+":completed", ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, TurnState: state, Err: err})
	}
	for i := len(messages) - 1; i >= 0; i-- {
		message := messages[i]
		id := strconv.FormatInt(message.ID, 10)
		itemID := historyID(key, "message", id)
		if message.Role == coderapi.RoleUser || turnID == "" {
			if turnID != "" {
				finish(domain.TurnStateRecovered, nil)
			}
			turnID = historyID(key, "turn", id)
			appendEvent(turnID+":started", ports.ChatEvent{Kind: ports.ChatEventTurnStarted})
		}
		var text, reasoning strings.Builder
		for index, part := range message.Content {
			partID := fmt.Sprintf("message:%s:part:%d", id, index)
			switch part.Type {
			case coderapi.PartText:
				text.WriteString(part.Text)
			case coderapi.PartReasoning:
				reasoning.WriteString(part.Text)
			case coderapi.PartToolCall:
				if part.ToolCallID == "" {
					continue
				}
				calls[part.ToolCallID] = part
				kind, summary, detail := toolPresentation(part.ToolName, string(part.Args))
				appendEvent(partID+":tool-started", ports.ChatEvent{
					Kind: ports.ChatEventActivityStarted, ProviderItemID: toolItemID(part.ToolCallID),
					ActivityKind: kind, ActivityStatus: domain.ActivityStatusRunning, Summary: summary, Detail: detail,
				})
			case coderapi.PartToolResult:
				if part.ToolCallID == "" {
					continue
				}
				call := calls[part.ToolCallID]
				name := call.ToolName
				if name == "" {
					name = part.ToolName
				}
				kind, summary, rawDetail := toolPresentation(name, string(call.Args))
				detail := map[string]any{}
				_ = json.Unmarshal(rawDetail, &detail)
				detail["output"] = commanddetail.TruncatedJSON(part.Result)
				encoded, _ := json.Marshal(detail)
				status := domain.ActivityStatusCompleted
				if part.IsError {
					status = domain.ActivityStatusFailed
				}
				appendEvent(partID+":tool-completed", ports.ChatEvent{
					Kind: ports.ChatEventActivityCompleted, ProviderItemID: toolItemID(part.ToolCallID),
					ActivityKind: kind, ActivityStatus: status, Summary: summary, Detail: encoded,
				})
			}
		}
		switch message.Role {
		case coderapi.RoleUser:
			if text.Len() > 0 {
				appendEvent("message:"+id+":user", ports.ChatEvent{
					Kind: ports.ChatEventUserMessageCompleted, ProviderItemID: itemID,
					NativeUserMessageID: id, ClientMessageID: historyID(key, "user", id), Text: text.String(),
				})
			}
		case coderapi.RoleAssistant:
			if reasoning.Len() > 0 {
				appendEvent("message:"+id+":reasoning", ports.ChatEvent{
					Kind: ports.ChatEventActivityCompleted, ProviderItemID: itemID + ":reasoning",
					ActivityKind: domain.ActivityKindReasoning, ActivityStatus: domain.ActivityStatusCompleted,
					Summary: "Reasoning", Text: reasoning.String(),
				})
			}
			if text.Len() > 0 {
				appendEvent("message:"+id+":assistant", ports.ChatEvent{Kind: ports.ChatEventMessageCompleted, ProviderItemID: itemID, Text: text.String()})
			}
			if message.Usage != nil {
				appendEvent("message:"+id+":usage", ports.ChatEvent{Kind: ports.ChatEventUsage, Usage: usage(message.Usage)})
			}
		}
	}
	switch chat.Status {
	case coderapi.ChatStatusRunning, coderapi.ChatStatusRequiresAction, coderapi.ChatStatusInterrupting:
		if turnID == "" {
			turnID = historyID(key, "turn", "pending")
		}
		return events, turnID
	case coderapi.ChatStatusError:
		if turnID != "" {
			var err error
			if chat.LastError != nil {
				err = chatError(chat.LastError)
			}
			finish(domain.TurnStateFailed, err)
		}
	default:
		if turnID != "" {
			finish(domain.TurnStateRecovered, nil)
		}
	}
	return events, ""
}
