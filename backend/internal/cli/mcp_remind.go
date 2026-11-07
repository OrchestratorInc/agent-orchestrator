package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// The reminder tools give an agent that calls AO over MCP the same "wake me
// later" set as ao remind: schedule, list, and cancel follow-ups into its own
// session.

const mcpRemindGuidance = `Do not schedule check-ins for things that already report back to you, such as worker reports or PR events. ` +
	`Space repeated checks further apart, and stop after a few with no change.`

var mcpRemindTools = []map[string]any{
	{
		"name":  "remind",
		"title": "Schedule a follow-up",
		"description": "Schedule a one-shot follow-up into this session, instead of keeping your turn open and polling. " +
			"When it is due, AO delivers message back into this same conversation as a new turn; a running turn is never interrupted. " +
			"Pass either at (RFC 3339) or delay_minutes. Returns the reminder id, which remind_cancel takes. " + mcpRemindGuidance,
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"message": map[string]any{
					"type": "string", "minLength": 1, "maxLength": 4000,
					"description": "What to tell yourself, written assuming the context you are in now.",
				},
				"at": map[string]any{
					"type": "string", "description": "When to deliver it, as an RFC 3339 time. At least 1 minute and at most 30 days ahead.",
				},
				"delay_minutes": map[string]any{
					"type": "integer", "minimum": 1, "maximum": 43200, "description": "Minutes from now to deliver it, instead of at.",
				},
				"name": map[string]any{
					"type": "string", "maxLength": 120, "description": "Short label shown on the session.",
				},
			},
			"required": []string{"message"},
		},
		"annotations": map[string]any{
			"title": "Schedule a follow-up", "readOnlyHint": false, "destructiveHint": false, "idempotentHint": false, "openWorldHint": false,
		},
	},
	{
		"name":        "remind_list",
		"title":       "List follow-ups",
		"description": "List this session's pending follow-ups, soonest first.",
		"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
		"annotations": map[string]any{
			"title": "List follow-ups", "readOnlyHint": true, "destructiveHint": false, "idempotentHint": true, "openWorldHint": false,
		},
	},
	{
		"name":        "remind_cancel",
		"title":       "Cancel a follow-up",
		"description": "Cancel one of this session's pending follow-ups by id.",
		"inputSchema": map[string]any{
			"type":       "object",
			"properties": map[string]any{"id": map[string]any{"type": "string", "minLength": 1, "description": "The reminder id remind returned."}},
			"required":   []string{"id"},
		},
		"annotations": map[string]any{
			"title": "Cancel a follow-up", "readOnlyHint": false, "destructiveHint": true, "idempotentHint": true, "openWorldHint": false,
		},
	},
}

func isMCPRemindTool(name string) bool {
	return name == "remind" || name == "remind_list" || name == "remind_cancel"
}

func (c *commandContext) callMCPRemind(ctx context.Context, name string, raw json.RawMessage) map[string]any {
	sessionID, err := remindSessionID()
	if err != nil {
		return mcpToolError(err)
	}
	var args struct {
		Message      string `json:"message"`
		At           string `json:"at"`
		DelayMinutes int    `json:"delay_minutes"`
		Name         string `json:"name"`
		ID           string `json:"id"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &args); err != nil {
			return mcpToolError(fmt.Errorf("invalid arguments: %w", err))
		}
	}
	switch name {
	case "remind":
		if strings.TrimSpace(args.Message) == "" {
			return mcpToolError(fmt.Errorf("message is required"))
		}
		if (args.At == "") == (args.DelayMinutes == 0) {
			return mcpToolError(fmt.Errorf("pass exactly one of at or delay_minutes"))
		}
		dueAt := c.deps.Now().Add(time.Duration(args.DelayMinutes) * time.Minute)
		if args.At != "" {
			dueAt, err = time.Parse(time.RFC3339, args.At)
			if err != nil {
				return mcpToolError(fmt.Errorf("at must be an RFC 3339 time: %w", err))
			}
		}
		response, err := c.createReminder(ctx, reminderCreateDTO{SessionID: sessionID, Message: args.Message, Name: args.Name, DueAt: dueAt})
		if err != nil {
			return mcpToolError(err)
		}
		return map[string]any{"content": []any{mcpText(fmt.Sprintf(
			"Scheduled %s for %s. It will arrive in this conversation as a new turn; end your turn now. Cancel it with remind_cancel.",
			response.Reminder.ID, response.Reminder.DueAt.UTC().Format(time.RFC3339)))}}
	case "remind_list":
		response, err := c.listReminders(ctx, sessionID)
		if err != nil {
			return mcpToolError(err)
		}
		var out bytes.Buffer
		if err := writeReminderTable(&out, response.Reminders); err != nil {
			return mcpToolError(err)
		}
		return map[string]any{"content": []any{mcpText(out.String())}}
	default:
		if strings.TrimSpace(args.ID) == "" {
			return mcpToolError(fmt.Errorf("id is required"))
		}
		response, err := c.cancelReminder(ctx, args.ID, sessionID)
		if err != nil {
			return mcpToolError(err)
		}
		return map[string]any{"content": []any{mcpText("Cancelled " + response.Reminder.ID + ".")}}
	}
}
