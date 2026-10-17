package acp

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type qwenQuestion struct {
	Question    string
	Header      string
	Options     []qwenOption
	MultiSelect bool
}

type qwenOption struct {
	Label       string
	Description string
}

func isQwenUserQuestion(tool acpsdk.ToolCallUpdate) bool {
	if tool.Meta != nil {
		if tool.Meta["qwenInteractionKind"] == "user_question" || tool.Meta["toolName"] == "ask_user_question" {
			return true
		}
	}
	if tool.Kind != nil && string(*tool.Kind) == "ask_user_question" {
		return true
	}
	if raw, ok := tool.RawInput.(map[string]any); ok && raw["questions"] != nil {
		return tool.Title != nil && strings.Contains(strings.ToLower(*tool.Title), "question")
	}
	return false
}

func extractQwenQuestions(tool acpsdk.ToolCallUpdate) []qwenQuestion {
	candidates := []any{}
	if tool.Meta != nil {
		candidates = append(candidates, tool.Meta["qwenQuestions"], tool.Meta["questions"])
	}
	if raw, ok := tool.RawInput.(map[string]any); ok {
		candidates = append(candidates, raw["questions"])
	}
	for _, raw := range candidates {
		if qs := parseQwenQuestionsSlice(raw); len(qs) > 0 {
			return qs
		}
	}
	return nil
}

func parseQwenQuestionsSlice(raw any) []qwenQuestion {
	items, ok := toMapSlice(raw)
	if !ok {
		return nil
	}
	var questions []qwenQuestion
	for i, m := range items {
		qText := strings.TrimSpace(fmt.Sprint(m["question"]))
		if m["question"] == nil || qText == "" {
			continue
		}
		header := strings.TrimSpace(fmt.Sprint(m["header"]))
		if m["header"] == nil || header == "" {
			header = fmt.Sprintf("Question %d", i+1)
		}
		multiSelect, _ := m["multiSelect"].(bool)

		var options []qwenOption
		if rawOpts, ok := toMapSlice(m["options"]); ok {
			for _, opt := range rawOpts {
				label := strings.TrimSpace(fmt.Sprint(opt["label"]))
				if opt["label"] != nil && label != "" {
					desc, _ := opt["description"].(string)
					options = append(options, qwenOption{Label: label, Description: strings.TrimSpace(desc)})
				}
			}
		}

		if len(options) > 0 {
			questions = append(questions, qwenQuestion{
				Question:    qText,
				Header:      header,
				Options:     options,
				MultiSelect: multiSelect,
			})
		}
	}
	return questions
}

func toMapSlice(v any) ([]map[string]any, bool) {
	switch s := v.(type) {
	case []map[string]any:
		return s, true
	case []any:
		res := make([]map[string]any, 0, len(s))
		for _, item := range s {
			if m, ok := item.(map[string]any); ok {
				res = append(res, m)
			}
		}
		return res, len(res) > 0
	default:
		return nil, false
	}
}

func qwenQuestionsToChatInputRequest(tool acpsdk.ToolCallUpdate, questions []qwenQuestion) ports.ChatInputRequest {
	properties := make(map[string]any, len(questions)*2)
	for i, q := range questions {
		choices := make([]any, 0, len(q.Options))
		for _, opt := range q.Options {
			item := map[string]any{"const": opt.Label, "title": opt.Label}
			if opt.Description != "" {
				item["description"] = opt.Description
			}
			choices = append(choices, item)
		}

		prop := map[string]any{"title": q.Question}
		if q.Header != "" {
			prop["description"] = q.Header
		}
		if q.MultiSelect {
			prop["type"] = "array"
			prop["items"] = map[string]any{"anyOf": choices}
		} else {
			prop["type"] = "string"
			prop["oneOf"] = choices
		}

		properties[fmt.Sprintf("question_%d", i)] = prop
		properties[fmt.Sprintf("question_%d_custom", i)] = map[string]any{"type": "string", "title": "Other"}
	}

	title := "Please answer the following question(s):"
	if tool.Title != nil && strings.TrimSpace(*tool.Title) != "" {
		title = strings.TrimSpace(*tool.Title)
	}

	return ports.ChatInputRequest{
		Mode:    ports.ChatInputModeForm,
		Message: title,
		Schema:  map[string]any{"type": "object", "properties": properties},
	}
}

func qwenFormatAnswers(questions []qwenQuestion, content map[string]any) map[string]string {
	if len(content) == 0 {
		return nil
	}
	answers := make(map[string]string, len(questions))
	for i, q := range questions {
		key := strconv.Itoa(i)
		customVal, _ := content[fmt.Sprintf("question_%d_custom", i)].(string)
		customVal = strings.TrimSpace(customVal)

		if q.MultiSelect {
			var selected []string
			if raw, ok := content[fmt.Sprintf("question_%d", i)].([]any); ok {
				for _, item := range raw {
					if str, ok := item.(string); ok && strings.TrimSpace(str) != "" {
						selected = append(selected, strings.TrimSpace(str))
					}
				}
			}
			if customVal != "" {
				selected = append(selected, customVal)
			}
			if len(selected) > 0 {
				answers[key] = strings.Join(selected, ", ")
			}
		} else {
			choiceVal, _ := content[fmt.Sprintf("question_%d", i)].(string)
			if customVal != "" {
				answers[key] = customVal
			} else if choiceVal = strings.TrimSpace(choiceVal); choiceVal != "" {
				answers[key] = choiceVal
			}
		}
	}
	return answers
}

func (c *conversation) handleQwenUserQuestion(
	ctx context.Context,
	params acpsdk.RequestPermissionRequest,
) (acpsdk.RequestPermissionResponse, error) {
	questions := extractQwenQuestions(params.ToolCall)
	if len(questions) == 0 {
		return acpsdk.RequestPermissionResponse{Outcome: acpsdk.NewRequestPermissionOutcomeCancelled()}, nil
	}

	inputRequest := qwenQuestionsToChatInputRequest(params.ToolCall, questions)
	requestID := stableACPRequestID(params.Meta)
	if requestID == "" {
		requestID = string(params.ToolCall.ToolCallId)
	}

	response, err := c.requestInput(ctx, requestID, inputRequest)
	if err != nil || response.Action == ports.ChatInputActionCancel || response.Action == ports.ChatInputActionDecline {
		return acpsdk.RequestPermissionResponse{Outcome: acpsdk.NewRequestPermissionOutcomeCancelled()}, err
	}

	answers := qwenFormatAnswers(questions, response.Content)
	if len(answers) == 0 {
		return acpsdk.RequestPermissionResponse{Outcome: acpsdk.NewRequestPermissionOutcomeCancelled()}, nil
	}

	if c.legacyWire != nil {
		c.legacyWire.registerAnswersForToolCall(string(params.ToolCall.ToolCallId), answers)
	}

	selectedOption := "proceed_once"
	for _, opt := range params.Options {
		if opt.Kind == acpsdk.PermissionOptionKindAllowOnce {
			selectedOption = string(opt.OptionId)
			break
		}
	}

	return acpsdk.RequestPermissionResponse{
		Outcome: acpsdk.NewRequestPermissionOutcomeSelected(acpsdk.PermissionOptionId(selectedOption)),
	}, nil
}
