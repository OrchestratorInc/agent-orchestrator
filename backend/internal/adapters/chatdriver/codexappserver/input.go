package codexappserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/codexappserver/codexproto"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var _ ports.ChatInputResponder = (*conversation)(nil)

type parkedInput struct {
	params   codexproto.ToolRequestUserInputParams
	custom   map[string]string
	result   chan inputResult
	resolved bool
}

type inputResult struct {
	reply userInputReply
	err   error
}

type userInputAnswer struct {
	Answers []string `json:"answers"`
}

type userInputReply struct {
	Answers map[string]userInputAnswer `json:"answers"`
}

func parseUserInput(raw json.RawMessage) (*parkedInput, ports.ChatInputRequest, error) {
	var params codexproto.ToolRequestUserInputParams
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(&params); err != nil {
		return nil, ports.ChatInputRequest{}, errors.New("unsupported or malformed request_user_input payload")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, ports.ChatInputRequest{}, errors.New("malformed request_user_input payload")
	}
	if params.ThreadID == "" || params.TurnID == "" || params.ItemID == "" || len(params.Questions) == 0 {
		return nil, ports.ChatInputRequest{}, errors.New("request_user_input requires thread, turn, item and questions")
	}
	ids := make(map[string]bool, len(params.Questions))
	for _, question := range params.Questions {
		if question.IsSecret != nil && *question.IsSecret {
			return nil, ports.ChatInputRequest{}, errors.New("secret request_user_input is unsupported: answers cannot be safely stored in chat history")
		}
		if strings.TrimSpace(question.ID) == "" || strings.TrimSpace(question.Question) == "" || ids[question.ID] {
			return nil, ports.ChatInputRequest{}, errors.New("request_user_input requires unique nonempty question ids and question text")
		}
		ids[question.ID] = true
	}
	parked := &parkedInput{params: params, custom: make(map[string]string), result: make(chan inputResult, 1)}
	properties := make(map[string]any)
	required := make([]string, 0, len(params.Questions))
	for index, question := range params.Questions {
		title := question.Header
		if strings.TrimSpace(title) == "" {
			title = question.Question
		}
		property := map[string]any{"type": "string", "title": title, "description": question.Question}
		if len(question.Options) > 0 {
			options := make([]any, 0, len(question.Options))
			labels := make(map[string]bool, len(question.Options))
			for _, option := range question.Options {
				if strings.TrimSpace(option.Label) == "" || labels[option.Label] {
					return nil, ports.ChatInputRequest{}, errors.New("request_user_input requires unique nonempty option labels")
				}
				labels[option.Label] = true
				options = append(options, map[string]any{"const": option.Label, "title": option.Label, "description": option.Description})
			}
			property["oneOf"] = options
		}
		properties[question.ID] = property
		if len(question.Options) > 0 && question.IsOther != nil && *question.IsOther {
			customID := fmt.Sprintf("codex_other_%d", index)
			for ids[customID] {
				customID += "_"
			}
			ids[customID] = true
			parked.custom[question.ID] = customID
			properties[customID] = map[string]any{
				"type": "string", "title": title + " — other answer or additional details",
				"description": question.Question,
			}
		} else {
			required = append(required, question.ID)
		}
	}
	return parked, ports.ChatInputRequest{
		Mode: ports.ChatInputModeForm, Message: params.Questions[0].Question,
		Schema: map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false},
	}, nil
}

func (c *conversation) requestUserInput(ctx context.Context, req serverRequest) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if c.disableNetwork {
		return nil, errors.New("background task does not permit approval or input requests")
	}
	requestID := rawID(req.ID)
	if requestID == "" {
		return nil, errors.New("server request carried no id")
	}
	parked, input, err := parseUserInput(req.Params)
	if err != nil {
		c.log.Warn("refusing invalid request_user_input payload", "request_id", requestID, "error", err)
		return nil, err
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, errConversationClosed
	}
	if c.pendingInputs[requestID] != nil || c.pending[requestID] != nil {
		c.mu.Unlock()
		return nil, errors.New("server request id is already pending")
	}
	if c.pendingInputs == nil {
		c.pendingInputs = make(map[string]*parkedInput)
	}
	c.pendingInputs[requestID] = parked
	c.mu.Unlock()
	c.emit(ports.ChatEvent{
		Kind: ports.ChatEventInputRequested, RequestID: requestID, ProviderItemID: requestID,
		ProviderTurnID: parked.params.TurnID, ActivityKind: domain.ActivityKindUserInput,
		ActivityStatus: domain.ActivityStatusPending, Summary: input.Message, Input: &input,
	})
	wait := approvalWait
	if ms := parked.params.AutoResolutionMs; ms != nil {
		milliseconds := *ms
		if milliseconds < uint64(approvalWait/time.Millisecond) {
			wait = time.Duration(milliseconds) * time.Millisecond
		}
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case result := <-parked.result:
		return result.reply, result.err
	case <-timer.C:
		err = errors.New("input request timed out without an answer")
	case <-ctx.Done():
		err = ctx.Err()
	}
	c.mu.Lock()
	if c.pendingInputs[requestID] != parked || parked.resolved {
		c.mu.Unlock()
		result := <-parked.result
		return result.reply, result.err
	}
	delete(c.pendingInputs, requestID)
	c.mu.Unlock()
	c.emit(ports.ChatEvent{Kind: ports.ChatEventInputResolved, RequestID: requestID, Detail: []byte(`{"action":"cancel"}`)})
	return nil, err
}

// ResolveInput validates and answers one pending Codex question form.
func (c *conversation) ResolveInput(ctx context.Context, requestID string, response ports.ChatInputResponse) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	requestID = c.nativeID(requestID)
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return errConversationClosed
	}
	parked := c.pendingInputs[requestID]
	if parked == nil || parked.resolved {
		c.mu.Unlock()
		return ports.ErrChatRequestNotPending
	}
	reply, err := parked.reply(response)
	if err != nil {
		c.mu.Unlock()
		return fmt.Errorf("%w: %w", ports.ErrChatDecisionNotOffered, err)
	}
	detail, err := json.Marshal(map[string]any{"action": response.Action, "content": response.Content})
	if err != nil {
		c.mu.Unlock()
		return fmt.Errorf("encode input response: %w", err)
	}
	parked.resolved = true
	c.mu.Unlock()
	c.emit(ports.ChatEvent{
		Kind: ports.ChatEventInputResolved, RequestID: requestID, ProviderItemID: requestID,
		ProviderTurnID: parked.params.TurnID, ActivityKind: domain.ActivityKindUserInput,
		ActivityStatus: domain.ActivityStatusResolved, Detail: detail,
	})
	parked.result <- inputResult{reply: reply}
	return nil
}

func (p *parkedInput) reply(response ports.ChatInputResponse) (userInputReply, error) {
	reply := userInputReply{Answers: make(map[string]userInputAnswer)}
	switch response.Action {
	case ports.ChatInputActionCancel, ports.ChatInputActionDecline:
		if len(response.Content) != 0 {
			return reply, errors.New("cancelled or declined input must not contain answers")
		}
		return reply, nil
	case ports.ChatInputActionAccept:
	default:
		return reply, errors.New("unsupported input action")
	}
	known := make(map[string]bool)
	for _, question := range p.params.Questions {
		known[question.ID] = true
		answer := userInputAnswer{Answers: []string{}}
		if value, present := response.Content[question.ID]; present {
			text, ok := value.(string)
			if !ok {
				return reply, errors.New("answers must be strings")
			}
			if strings.TrimSpace(text) != "" {
				if len(question.Options) > 0 {
					offered := false
					for _, option := range question.Options {
						offered = offered || text == option.Label
					}
					if !offered {
						return reply, errors.New("answer is not an offered option")
					}
				} else {
					text = "user_note: " + text
				}
				answer.Answers = append(answer.Answers, text)
			}
		}
		if customID := p.custom[question.ID]; customID != "" {
			known[customID] = true
			if value, present := response.Content[customID]; present {
				text, ok := value.(string)
				if !ok {
					return reply, errors.New("other answers must be strings")
				}
				if strings.TrimSpace(text) != "" {
					answer.Answers = append(answer.Answers, "user_note: "+text)
				}
			}
		}
		if len(answer.Answers) == 0 {
			return reply, errors.New("each question requires an answer")
		}
		reply.Answers[question.ID] = answer
	}
	for name := range response.Content {
		if !known[name] {
			return reply, errors.New("answer contains an unknown question")
		}
	}
	return reply, nil
}
