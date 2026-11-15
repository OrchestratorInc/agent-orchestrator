package acp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"
	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/commanddetail"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/persistenthost"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func (c *conversation) ReadTextFile(context.Context, acpsdk.ReadTextFileRequest) (acpsdk.ReadTextFileResponse, error) {
	return acpsdk.ReadTextFileResponse{}, errClientCapability
}

func (c *conversation) WriteTextFile(context.Context, acpsdk.WriteTextFileRequest) (acpsdk.WriteTextFileResponse, error) {
	return acpsdk.WriteTextFileResponse{}, errClientCapability
}

func (c *conversation) CreateTerminal(context.Context, acpsdk.CreateTerminalRequest) (acpsdk.CreateTerminalResponse, error) {
	return acpsdk.CreateTerminalResponse{}, errClientCapability
}

func (c *conversation) KillTerminal(context.Context, acpsdk.KillTerminalRequest) (acpsdk.KillTerminalResponse, error) {
	return acpsdk.KillTerminalResponse{}, errClientCapability
}

func (c *conversation) TerminalOutput(context.Context, acpsdk.TerminalOutputRequest) (acpsdk.TerminalOutputResponse, error) {
	return acpsdk.TerminalOutputResponse{}, errClientCapability
}

func (c *conversation) ReleaseTerminal(context.Context, acpsdk.ReleaseTerminalRequest) (acpsdk.ReleaseTerminalResponse, error) {
	return acpsdk.ReleaseTerminalResponse{}, errClientCapability
}

func (c *conversation) WaitForTerminalExit(context.Context, acpsdk.WaitForTerminalExitRequest) (acpsdk.WaitForTerminalExitResponse, error) {
	return acpsdk.WaitForTerminalExitResponse{}, errClientCapability
}

func (c *conversation) RequestPermission(
	ctx context.Context,
	params acpsdk.RequestPermissionRequest,
) (acpsdk.RequestPermissionResponse, error) {
	if len(params.Options) == 0 {
		return acpsdk.RequestPermissionResponse{Outcome: acpsdk.NewRequestPermissionOutcomeCancelled()}, nil
	}
	if isQwenUserQuestion(params.ToolCall) {
		return c.handleQwenUserQuestion(ctx, params)
	}
	c.mu.Lock()
	policy, mode := c.permissionFor, c.permissionMode
	c.mu.Unlock()
	if policy != nil {
		if selected, handled := policy(mode, params); handled {
			if selected == CancelPermission {
				return acpsdk.RequestPermissionResponse{Outcome: acpsdk.NewRequestPermissionOutcomeCancelled()}, nil
			}
			for _, option := range params.Options {
				if option.OptionId == selected {
					return acpsdk.RequestPermissionResponse{
						Outcome: acpsdk.NewRequestPermissionOutcomeSelected(selected),
					}, nil
				}
			}
			return acpsdk.RequestPermissionResponse{}, fmt.Errorf(
				"provider permission policy selected unoffered option %q", selected)
		}
	}
	decisions := make([]ports.ChatDecisionOption, 0, len(params.Options))
	for _, option := range params.Options {
		id := string(option.OptionId)
		raw, _ := json.Marshal(option)
		decisions = append(decisions, ports.ChatDecisionOption{
			ID: id, Label: option.Name, Kind: ports.ChatDecisionKind(option.Kind), Raw: raw,
		})
	}
	summary := "Permission required"
	if params.ToolCall.Title != nil && strings.TrimSpace(*params.ToolCall.Title) != "" {
		summary = *params.ToolCall.Title
	}
	selected, err := c.requestApproval(ctx, stableACPRequestID(params.Meta), ClientApprovalRequest{
		Summary: summary, ActivityKind: activityKindFromTool(pointerValue(params.ToolCall.Kind)),
		Detail:    approvalToolDetail(params.ToolCall, activityKindFromTool(pointerValue(params.ToolCall.Kind))),
		Decisions: decisions,
	})
	if err != nil || selected == "" {
		return acpsdk.RequestPermissionResponse{Outcome: acpsdk.NewRequestPermissionOutcomeCancelled()}, err
	}
	return acpsdk.RequestPermissionResponse{
		Outcome: acpsdk.NewRequestPermissionOutcomeSelected(acpsdk.PermissionOptionId(selected)),
	}, nil
}

func (c *conversation) RequestApproval(
	ctx context.Context,
	params ClientApprovalRequest,
) (string, error) {
	return c.requestApproval(ctx, "", params)
}

func (c *conversation) requestApproval(
	ctx context.Context,
	requestID string,
	params ClientApprovalRequest,
) (string, error) {
	if len(params.Decisions) == 0 {
		return "", nil
	}
	if requestID == "" {
		requestID = uuid.NewString()
	}
	options := make(map[string]json.RawMessage, len(params.Decisions))
	for _, option := range params.Decisions {
		options[option.ID] = append(json.RawMessage(nil), option.Raw...)
	}
	request := &parkedPermission{
		options: options, result: make(chan string, 1), ready: make(chan struct{}),
	}

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return "", nil
	}
	accepted, hasAccepted := c.accepted[requestID]
	if hasAccepted && accepted.Kind == persistentInteractionApproval {
		delete(c.accepted, requestID)
	} else {
		c.pending[requestID] = request
	}
	turnID := c.activeTurn
	c.mu.Unlock()
	c.emit(ports.ChatEvent{
		Kind:           ports.ChatEventApprovalRequested,
		ProviderTurnID: turnID,
		ProviderItemID: requestID,
		ActivityKind:   params.ActivityKind,
		ActivityStatus: domain.ActivityStatusPending,
		Summary:        params.Summary,
		Detail:         params.Detail,
		RequestID:      requestID,
		Decisions:      params.Decisions,
	})
	close(request.ready)
	if hasAccepted && accepted.Kind == persistentInteractionApproval {
		c.emit(persistentInteractionEvent(accepted))
		request.result <- accepted.Decision.ID
	}

	timer := timeAfter(approvalWait)
	select {
	case selected := <-request.result:
		return selected, nil
	case <-ctx.Done():
		c.discardPermission(requestID)
		if !c.providerDetaching() {
			c.emit(ports.ChatEvent{Kind: ports.ChatEventApprovalResolved, RequestID: requestID})
		}
		return "", nil
	case <-timer:
		c.discardPermission(requestID)
		c.emit(ports.ChatEvent{Kind: ports.ChatEventApprovalResolved, RequestID: requestID})
		return "", nil
	}
}

func approvalToolDetail(tool acpsdk.ToolCallUpdate, activityKind domain.ActivityKind) []byte {
	detail := map[string]any{
		"protocol": "acp", "toolKind": pointerValue(tool.Kind),
		"subjectKind": string(activityKind),
	}
	if tool.RawInput != nil {
		detail["input"] = cappedPayload(tool.RawInput)
	}
	if claude := nestedMap(tool.Meta, "claudeCode"); claude != nil {
		copyDetail(detail, claude, "toolName", "providerToolName")
	}
	encoded, err := json.Marshal(detail)
	if err != nil {
		return nil
	}
	return encoded
}

func (c *conversation) UnstableCreateElicitation(
	ctx context.Context,
	params acpsdk.UnstableCreateElicitationRequest,
) (acpsdk.UnstableCreateElicitationResponse, error) {
	request := ports.ChatInputRequest{}
	switch {
	case params.Form != nil:
		request.Mode = ports.ChatInputModeForm
		request.Message = params.Form.Message
		schema, err := schemaMap(params.Form.RequestedSchema)
		if err != nil {
			return acpsdk.NewUnstableCreateElicitationResponseCancel(), err
		}
		request.Schema = schema
	case params.Url != nil:
		request.Mode = ports.ChatInputModeURL
		request.Message = params.Url.Message
		request.URL = params.Url.Url
		request.ElicitationID = string(params.Url.ElicitationId)
	default:
		return acpsdk.NewUnstableCreateElicitationResponseCancel(), errors.New("ACP elicitation has no mode")
	}

	requestID := ""
	if params.Form != nil {
		requestID = stableACPRequestID(params.Form.Meta)
	} else if params.Url != nil {
		requestID = stableACPRequestID(params.Url.Meta)
	}
	response, err := c.requestInput(ctx, requestID, request)
	if err != nil {
		return acpsdk.NewUnstableCreateElicitationResponseCancel(), err
	}
	return acpInputResponse(response), nil
}

func (c *conversation) RequestInput(
	ctx context.Context,
	request ports.ChatInputRequest,
) (ports.ChatInputResponse, error) {
	return c.requestInput(ctx, "", request)
}

func (c *conversation) requestInput(
	ctx context.Context,
	requestID string,
	request ports.ChatInputRequest,
) (ports.ChatInputResponse, error) {
	if requestID == "" {
		requestID = uuid.NewString()
	}
	parked := &parkedInput{
		request: request, result: make(chan ports.ChatInputResponse, 1), ready: make(chan struct{}),
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return ports.ChatInputResponse{Action: ports.ChatInputActionCancel}, nil
	}
	accepted, hasAccepted := c.accepted[requestID]
	if hasAccepted && accepted.Kind == persistentInteractionInput {
		delete(c.accepted, requestID)
	} else {
		c.pendingInputs[requestID] = parked
	}
	turnID := c.activeTurn
	c.mu.Unlock()

	c.emit(ports.ChatEvent{
		Kind: ports.ChatEventInputRequested, ProviderTurnID: turnID,
		RequestID: requestID, Input: &request, Summary: request.Message,
	})
	close(parked.ready)
	if hasAccepted && accepted.Kind == persistentInteractionInput {
		c.emit(persistentInteractionEvent(accepted))
		parked.result <- *accepted.Input
	}

	timer := timeAfter(approvalWait)
	select {
	case response := <-parked.result:
		return response, nil
	case <-ctx.Done():
		c.discardInput(requestID)
		if !c.providerDetaching() {
			c.emit(ports.ChatEvent{Kind: ports.ChatEventInputResolved, RequestID: requestID})
		}
		return ports.ChatInputResponse{Action: ports.ChatInputActionCancel}, nil
	case <-timer:
		c.discardInput(requestID)
		c.emit(ports.ChatEvent{Kind: ports.ChatEventInputResolved, RequestID: requestID})
		return ports.ChatInputResponse{Action: ports.ChatInputActionCancel}, nil
	}
}

func (c *conversation) providerDetaching() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.closed && c.proc != nil && c.proc.terminate != nil && c.conn != nil {
		select {
		case <-c.conn.Done():
			return true
		default:
		}
	}
	return c.detaching
}

func stableACPRequestID(meta map[string]any) string {
	requestID, _ := meta[persistenthost.ACPRequestIDMetaKey].(string)
	return strings.TrimSpace(requestID)
}

func (c *conversation) UpdatePlan(plan *domain.ConversationPlan) {
	c.mu.Lock()
	turnID := c.activeTurn
	c.mu.Unlock()
	c.emit(ports.ChatEvent{Kind: ports.ChatEventPlanUpdated, ProviderTurnID: turnID, Plan: plan})
}

func (c *conversation) ResolveInput(
	ctx context.Context,
	requestID string,
	response ports.ChatInputResponse,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	request, ok := c.pendingInputs[requestID]
	if !ok {
		c.mu.Unlock()
		return ports.ErrChatRequestNotPending
	}
	if err := validateInputResponse(request.request, response); err != nil {
		c.mu.Unlock()
		return err
	}
	delete(c.pendingInputs, requestID)
	c.mu.Unlock()
	command := persistentInteractionCommand{
		RequestID: requestID, Kind: persistentInteractionInput, Input: &response,
	}
	eventID, err := c.recordPersistentInteraction(ctx, command)
	if err != nil {
		c.mu.Lock()
		if !c.closed {
			c.pendingInputs[requestID] = request
		}
		c.mu.Unlock()
		return err
	}
	command.EventID = eventID

	c.emit(persistentInteractionEvent(command))
	request.result <- response
	return nil
}

func (c *conversation) UnstableCompleteElicitation(
	context.Context,
	acpsdk.UnstableCompleteElicitationNotification,
) error {
	return nil
}

func (c *conversation) UnstableConnectMcp(
	context.Context,
	acpsdk.UnstableConnectMcpRequest,
) (acpsdk.UnstableConnectMcpResponse, error) {
	return acpsdk.UnstableConnectMcpResponse{}, errClientCapability
}

func (c *conversation) UnstableDisconnectMcp(
	context.Context,
	acpsdk.UnstableDisconnectMcpRequest,
) (acpsdk.UnstableDisconnectMcpResponse, error) {
	return acpsdk.UnstableDisconnectMcpResponse{}, errClientCapability
}

func schemaMap(schema acpsdk.UnstableElicitationSchema) (map[string]any, error) {
	encoded, err := json.Marshal(schema)
	if err != nil {
		return nil, fmt.Errorf("encode ACP elicitation schema: %w", err)
	}
	var out map[string]any
	if err := json.Unmarshal(encoded, &out); err != nil {
		return nil, fmt.Errorf("normalize ACP elicitation schema: %w", err)
	}
	return out, nil
}

func acpInputResponse(response ports.ChatInputResponse) acpsdk.UnstableCreateElicitationResponse {
	switch response.Action {
	case ports.ChatInputActionAccept:
		result := acpsdk.NewUnstableCreateElicitationResponseAccept()
		result.Accept.Content = response.Content
		return result
	case ports.ChatInputActionDecline:
		return acpsdk.NewUnstableCreateElicitationResponseDecline()
	default:
		return acpsdk.NewUnstableCreateElicitationResponseCancel()
	}
}

func validateInputResponse(request ports.ChatInputRequest, response ports.ChatInputResponse) error {
	switch response.Action {
	case ports.ChatInputActionDecline, ports.ChatInputActionCancel:
		return nil
	case ports.ChatInputActionAccept:
		if request.Mode == ports.ChatInputModeURL {
			return nil
		}
	default:
		return fmt.Errorf("%w: unsupported input action %q", ports.ErrChatDecisionNotOffered, response.Action)
	}
	if request.Mode != ports.ChatInputModeForm {
		return fmt.Errorf("%w: unsupported input mode %q", ports.ErrChatDecisionNotOffered, request.Mode)
	}
	if err := validateFormContent(request.Schema, response.Content); err != nil {
		return fmt.Errorf("%w: %s", ports.ErrChatDecisionNotOffered, err.Error())
	}
	return nil
}

func validateFormContent(schema, content map[string]any) error {
	properties, _ := schema["properties"].(map[string]any)
	customAnswers := formCustomAnswers(properties)
	for _, name := range formRequired(schema["required"]) {
		if name == "" {
			continue
		}
		if _, present := content[name]; present {
			continue
		}
		if custom, ok := customAnswers[name]; ok {
			if text, _ := content[custom].(string); strings.TrimSpace(text) != "" {
				continue
			}
		}
		return fmt.Errorf("required input %q is missing", name)
	}
	for name, value := range content {
		rawProperty, known := properties[name]
		if !known {
			return fmt.Errorf("input %q is not in the requested schema", name)
		}
		property, ok := rawProperty.(map[string]any)
		if !ok {
			return fmt.Errorf("input %q has an invalid requested schema", name)
		}
		if err := validateFormValue(value, property); err != nil {
			return fmt.Errorf("input %q %s", name, err.Error())
		}
	}
	return nil
}

func validateFormValue(value any, property map[string]any) error {
	typeName, _ := property["type"].(string)
	switch typeName {
	case "string", "":
		text, ok := value.(string)
		if !ok {
			return errors.New("must be a string")
		}
		if !formOptionOffered(text, property) {
			return errors.New("is not one of the offered values")
		}
	case "number", "integer":
		numeric, ok := number(value)
		if !ok || math.IsNaN(numeric) || math.IsInf(numeric, 0) {
			return errors.New("must be a finite number")
		}
		if typeName == "integer" && math.Trunc(numeric) != numeric {
			return errors.New("must be an integer")
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return errors.New("must be a boolean")
		}
	case "array":
		values, ok := value.([]any)
		if !ok {
			return errors.New("must be an array")
		}
		items, _ := property["items"].(map[string]any)
		for _, item := range values {
			text, ok := item.(string)
			if !ok || !formOptionOffered(text, items) {
				return errors.New("contains a value that was not offered")
			}
		}
	default:
		return fmt.Errorf("uses unsupported type %q", typeName)
	}
	return nil
}

const askUserQuestionCustomAnswerMetaKey = "_askUserQuestionCustomAnswer"

func formCustomAnswers(properties map[string]any) map[string]string {
	var out map[string]string
	for name, raw := range properties {
		property, _ := raw.(map[string]any)
		meta, _ := property["_meta"].(map[string]any)
		marker, _ := meta[askUserQuestionCustomAnswerMetaKey].(map[string]any)
		question, _ := marker["questionId"].(string)
		if marker["isCustomAnswer"] != true || question == "" || question == name {
			continue
		}
		if _, known := properties[question]; !known {
			continue
		}
		if out == nil {
			out = map[string]string{}
		}
		out[question] = name
	}
	return out
}

func formRequired(value any) []string {
	switch values := value.(type) {
	case []string:
		return values
	case []any:
		out := make([]string, 0, len(values))
		for _, value := range values {
			if text, ok := value.(string); ok {
				out = append(out, text)
			}
		}
		return out
	default:
		return nil
	}
}

func formOptionOffered(value string, schema map[string]any) bool {
	var options []any
	if candidates, ok := schema["oneOf"].([]any); ok {
		options = candidates
	} else if candidates, ok := schema["anyOf"].([]any); ok {
		options = candidates
	} else if candidates, ok := schema["enum"].([]any); ok {
		for _, candidate := range candidates {
			if candidate == value {
				return true
			}
		}
		return len(candidates) == 0
	}
	if len(options) == 0 {
		return true
	}
	for _, raw := range options {
		option, _ := raw.(map[string]any)
		if option["const"] == value {
			return true
		}
	}
	return false
}

func (c *conversation) discardInput(requestID string) {
	c.mu.Lock()
	delete(c.pendingInputs, requestID)
	c.mu.Unlock()
}

var timeAfter = time.After

func (c *conversation) discardPermission(requestID string) {
	c.mu.Lock()
	delete(c.pending, requestID)
	c.mu.Unlock()
}

func (c *conversation) SessionUpdate(_ context.Context, params acpsdk.SessionNotification) error {
	if c.captureReplayUpdate(params) {
		return nil
	}
	return c.processUpdate(params)
}

func (c *conversation) handleNotification(method string, params json.RawMessage) bool {
	var err error
	switch method {
	case acpsdk.ClientMethodSessionUpdate:
		var update acpsdk.SessionNotification
		if err = json.Unmarshal(params, &update); err == nil {
			err = update.Validate()
		}
		if err == nil {
			err = c.SessionUpdate(context.Background(), update)
		}
	case persistenthost.ACPPromptResultMethod, persistenthost.ACPPromptHandoffMethod,
		persistenthost.ACPInteractionCommandMethod:
		_, err = c.HandleExtensionMethod(context.Background(), method, params)
	case claudeSDKMessageMethod:
		err = c.handleClaudeSDKMessage(params)
	default:
		return false
	}
	if err != nil {
		c.log.Error("failed to handle ACP notification", "method", method, "err", err)
	}
	return true
}

const claudeSDKMessageMethod = "_claude/sdkMessage"

func (c *conversation) handleClaudeSDKMessage(params json.RawMessage) error {
	var payload struct {
		SessionID string `json:"sessionId"`
		Message   struct {
			Type    string `json:"type"`
			Subtype string `json:"subtype"`
			State   string `json:"state"`
		} `json:"message"`
		Meta map[string]any `json:"_meta"`
	}
	if err := json.Unmarshal(params, &payload); err != nil {
		return err
	}
	if payload.Message.Type != "system" || payload.Message.Subtype != "session_state_changed" {
		return nil
	}
	var status domain.ThreadStatus
	switch payload.Message.State {
	case "running":
		status = domain.ThreadStatusActive
	case "idle":
		status = domain.ThreadStatusIdle
	default:
		return nil
	}
	c.mu.Lock()
	sessionID := c.sessionID
	c.mu.Unlock()
	if sessionID == "" || payload.SessionID != sessionID {
		return nil
	}
	c.historyMu.Lock()
	loadingHistory := c.history != nil
	c.historyMu.Unlock()
	if loadingHistory {
		return nil
	}
	event := ports.ChatEvent{Kind: ports.ChatEventProviderState, ThreadState: &ports.ChatThreadState{Status: status}}
	if eventID := stableACPEventID(payload.Meta); eventID != "" {
		event.ProviderEventID = eventID + ":0"
	}
	c.emit(event)
	return nil
}

func (c *conversation) processUpdate(params acpsdk.SessionNotification) error {
	if c.prepareHistoryUpdate(params.Update) {
		return nil
	}
	c.mu.Lock()
	sessionID := c.sessionID
	turnID := c.activeTurn
	c.mu.Unlock()
	if sessionID != "" && string(params.SessionId) != sessionID {
		return fmt.Errorf("ACP update for unexpected session %q", params.SessionId)
	}
	sourceID := stableACPEventID(params.Meta)
	sourceIndex := 0
	emit := func(event ports.ChatEvent) {
		if sourceID != "" {
			event.ProviderEventID = fmt.Sprintf("%s:%d", sourceID, sourceIndex)
			sourceIndex++
		}
		c.emit(event)
	}

	update := params.Update
	providerOutputResumed := update.AgentMessageChunk != nil ||
		update.AgentThoughtChunk != nil ||
		update.ToolCall != nil ||
		update.Plan != nil
	if providerOutputResumed {
		c.completeProviderFailure(turnID, emit)
	}
	switch {
	case update.AgentMessageChunk != nil:
		if c.steerAcknowledgement(turnID, contentText(update.AgentMessageChunk.Content)) {
			break
		}
		c.mu.Lock()
		isCompacting := c.compactingTurnID != "" && c.compactingTurnID == turnID
		segment := c.steerSegment
		c.mu.Unlock()
		if isCompacting {
			if delta := contentText(update.AgentMessageChunk.Content); delta != "" {
				c.mu.Lock()
				c.compactionSummary += delta
				c.mu.Unlock()
			}
			break
		}
		parentID := c.providerItemID(parentToolUseID(update.AgentMessageChunk.Meta))
		if parentID != "" {
			segment = 0
		}
		id := c.providerItemID(segmentedMessageID(update.AgentMessageChunk.MessageId, "assistant", turnID, segment))
		if delta := contentText(update.AgentMessageChunk.Content); delta != "" {
			if parentID != "" {
				c.mu.Lock()
				item, existed := c.nestedMessages[id]
				item.text += delta
				item.parentID = parentID
				c.nestedMessages[id] = item
				c.mu.Unlock()
				detail, _ := json.Marshal(map[string]any{"parentProviderItemId": parentID, "nestedAgent": true})
				if !existed {
					emit(ports.ChatEvent{Kind: ports.ChatEventActivityStarted, ProviderTurnID: turnID,
						ProviderItemID: id, ActivityKind: domain.ActivityKindMCPTool,
						ActivityStatus: domain.ActivityStatusRunning, Summary: "Subagent response", Detail: detail})
				}
				emit(ports.ChatEvent{Kind: ports.ChatEventActivityText, ProviderTurnID: turnID,
					ProviderItemID: id, Delta: delta})
				break
			}
			c.mu.Lock()
			c.messages[id] += delta
			c.mu.Unlock()
			emit(ports.ChatEvent{Kind: ports.ChatEventMessageDelta, ProviderTurnID: turnID, ProviderItemID: id, Delta: delta})
		}
	case update.AgentThoughtChunk != nil:
		c.mu.Lock()
		isCompacting := c.compactingTurnID != "" && c.compactingTurnID == turnID
		segment := c.steerSegment
		c.mu.Unlock()
		if isCompacting {
			break
		}
		id := c.providerItemID(segmentedMessageID(update.AgentThoughtChunk.MessageId, "thought", turnID, segment))
		if delta := contentText(update.AgentThoughtChunk.Content); delta != "" {
			c.mu.Lock()
			_, existed := c.thoughts[id]
			c.thoughts[id] += delta
			c.mu.Unlock()
			if !existed {
				emit(ports.ChatEvent{Kind: ports.ChatEventActivityStarted, ProviderTurnID: turnID,
					ProviderItemID: id, ActivityKind: domain.ActivityKindReasoning,
					ActivityStatus: domain.ActivityStatusRunning, Summary: "Reasoning"})
			}
			emit(ports.ChatEvent{Kind: ports.ChatEventReasoningDelta, ProviderTurnID: turnID, ProviderItemID: id, Delta: delta})
		}
	case update.ToolCall != nil:
		tool := &toolState{
			id: string(update.ToolCall.ToolCallId), title: update.ToolCall.Title,
			kind: update.ToolCall.Kind, status: update.ToolCall.Status,
			locations: update.ToolCall.Locations, content: update.ToolCall.Content,
			rawInput: update.ToolCall.RawInput, rawOutput: update.ToolCall.RawOutput,
			meta: cloneMeta(update.ToolCall.Meta),
		}
		c.mu.Lock()
		c.tools[tool.id] = tool
		c.mu.Unlock()
		emit(c.toolEvent(turnID, tool, toolTerminal(tool.status)))
		c.emitDiffs(turnID, tool.id, tool.content, emit)
	case update.ToolCallUpdate != nil:
		tool := c.mergeToolUpdate(update.ToolCallUpdate)
		if delta := terminalOutput(update.ToolCallUpdate.Meta); delta != "" {
			emit(ports.ChatEvent{Kind: ports.ChatEventCommandOutputDelta, ProviderTurnID: turnID,
				ProviderItemID: c.providerItemID(tool.id), Delta: delta})
		}
		emit(c.toolEvent(turnID, tool, toolTerminal(tool.status)))
		c.emitDiffs(turnID, tool.id, tool.content, emit)
	case update.Plan != nil:
		plan := normalizePlan(update.Plan.Entries)
		plan.SpansTurns = c.plansSpanTurns
		emit(ports.ChatEvent{Kind: ports.ChatEventPlanUpdated, ProviderTurnID: turnID, Plan: plan})
	case update.SessionInfoUpdate != nil:
		if update.SessionInfoUpdate.Title != nil {
			emit(ports.ChatEvent{Kind: ports.ChatEventThreadRenamed, Title: *update.SessionInfoUpdate.Title})
		}
		if turnID != "" {
			if event, ok := c.sessionFailureEvent(turnID, sourceID, update.SessionInfoUpdate.Meta); ok {
				emit(event)
			}
			c.trackCompactionStatus(turnID, update.SessionInfoUpdate.Meta)
		}
	case update.ConfigOptionUpdate != nil:
		c.replaceConfigOptions(update.ConfigOptionUpdate.ConfigOptions)
	case update.AvailableCommandsUpdate != nil:
		c.replaceAvailableCommands(update.AvailableCommandsUpdate.AvailableCommands)
	case update.UsageUpdate != nil:
		c.trackContext(int64(update.UsageUpdate.Used), int64(update.UsageUpdate.Size))
		usage := &ports.ChatUsage{
			ContextUsed: int64(update.UsageUpdate.Used), ContextWindow: int64(update.UsageUpdate.Size),
			ContextKnown: true,
		}
		if update.UsageUpdate.Cost != nil {
			cost := update.UsageUpdate.Cost.Amount
			usage.Cost = &cost
			usage.Currency = update.UsageUpdate.Cost.Currency
		}
		emit(ports.ChatEvent{Kind: ports.ChatEventUsage, Usage: usage})
		if limits := claudeRateLimits(update.UsageUpdate.Meta); limits != nil {
			emit(ports.ChatEvent{Kind: ports.ChatEventRateLimits, RateLimits: limits})
		}
		c.trackClaudeRateLimit(update.UsageUpdate.Meta, time.Now())
	}
	return nil
}

func stableACPEventID(meta map[string]any) string {
	eventID, _ := meta[persistenthost.ACPEventIDMetaKey].(string)
	return strings.TrimSpace(eventID)
}

func contentText(content acpsdk.ContentBlock) string {
	if content.Text == nil {
		return ""
	}
	return content.Text.Text
}

func segmentedMessageID(id *string, prefix, turnID string, segment int) string {
	if id != nil && *id != "" {
		return *id
	}
	if segment > 0 {
		return fmt.Sprintf("%s-%s-%d", prefix, turnID, segment)
	}
	return prefix + "-" + turnID
}

func (c *conversation) mergeToolUpdate(update *acpsdk.SessionToolCallUpdate) *toolState {
	id := string(update.ToolCallId)
	c.mu.Lock()
	defer c.mu.Unlock()
	tool := c.tools[id]
	if tool == nil {
		tool = &toolState{id: id}
		c.tools[id] = tool
	}
	if update.Title != nil {
		tool.title = *update.Title
	}
	if update.Kind != nil {
		tool.kind = *update.Kind
	}
	if update.Status != nil {
		tool.status = *update.Status
	}
	if update.Locations != nil {
		tool.locations = update.Locations
	}
	if update.Content != nil {
		tool.content = update.Content
	}
	if update.RawInput != nil {
		tool.rawInput = update.RawInput
	}
	if update.RawOutput != nil {
		tool.rawOutput = update.RawOutput
	}
	tool.meta = mergeMeta(tool.meta, update.Meta)
	if delta := terminalOutput(update.Meta); delta != "" {
		tool.terminalOutput += delta
	}
	snapshot := *tool
	return &snapshot
}

func (c *conversation) toolEvent(turnID string, tool *toolState, completed bool) ports.ChatEvent {
	output := toolOutputText(withoutImageData(tool.rawOutput))
	if tool.terminalOutput != "" {
		output = tool.terminalOutput
	}
	activityKind := activityKindFromTool(tool.kind)
	output, outputCut := commanddetail.TailChars(output, commanddetail.MaxOutputChars)
	var content any = contentWithoutImageData(tool.content)
	if activityKind != domain.ActivityKindMCPTool {
		content = cappedPayload(content)
	}
	detailMap := map[string]any{
		"protocol": "acp", "toolKind": tool.kind, "locations": tool.locations,
		"input": cappedPayload(tool.rawInput), "output": output, "content": content,
	}
	if outputCut {
		detailMap["outputTruncated"] = true
	}
	if c.harness == domain.HarnessHermes {
		delete(detailMap, "input")
		if input, err := json.Marshal(tool.rawInput); err == nil && string(input) != "null" {
			detailMap["arguments"] = cappedPayload(tool.rawInput)
		}
		if len(tool.locations) == 0 {
			delete(detailMap, "locations")
		}
		if len(tool.content) == 0 {
			delete(detailMap, "content")
		}
	}
	if activityKind == domain.ActivityKindFileChange {
		files := make([]map[string]any, 0)
		for _, item := range tool.content {
			if item.Diff == nil {
				continue
			}
			stats := diffFileFromSnapshot(item.Diff.Path, item.Diff.OldText, item.Diff.NewText)
			patch := acpFilePatch(item.Diff.Path, item.Diff.OldText, item.Diff.NewText)
			file := map[string]any{
				"path": item.Diff.Path, "status": stats.Status,
				"additions": stats.Additions, "deletions": stats.Deletions,
				"patch": patch, "oldText": item.Diff.OldText, "newText": item.Diff.NewText,
			}
			if len(patch) > commanddetail.MaxPatchChars {
				capped, cut := cappedACPPatch(item.Diff.Path, item.Diff.OldText, item.Diff.NewText)
				file["patch"] = capped
				if cut {
					file["patchTruncated"] = true
				}
				delete(file, "oldText")
				delete(file, "newText")
			}
			files = append(files, file)
		}
		if len(files) > 0 {
			detailMap["files"] = files
		}
	}
	if claude := nestedMap(tool.meta, "claudeCode"); claude != nil {
		copyDetail(detailMap, claude, "toolName", "providerToolName")
		if parentID, ok := claude["parentToolUseId"].(string); ok && parentID != "" {
			detailMap["parentProviderItemId"] = c.providerItemID(parentID)
		}
		copyDetail(detailMap, claude, "subagent", "nestedAgent")
		copyDetail(detailMap, claude, "subagentType", "subagentType")
		copyDetail(detailMap, claude, "subagentRetry", "subagentRetry")
		if title, ok := claude["title"].(string); ok && strings.TrimSpace(title) != "" {
			detailMap["providerTitle"] = title
		}
	}
	if terminal := nestedMap(tool.meta, "terminal_info"); terminal != nil {
		copyDetail(detailMap, terminal, "terminal_id", "terminalId")
	}
	if terminal := nestedMap(tool.meta, "terminal_exit"); terminal != nil {
		copyDetail(detailMap, terminal, "exit_code", "exitCode")
		copyDetail(detailMap, terminal, "signal", "signal")
	}
	if activityKind == domain.ActivityKindCommand {
		if _, exists := detailMap["exitCode"]; !exists {
			if raw, ok := tool.rawOutput.(map[string]any); ok {
				if exit, ok := number(nestedMap(raw, "metadata")["exit"]); ok &&
					!math.IsNaN(exit) && !math.IsInf(exit, 0) && math.Trunc(exit) == exit {
					detailMap["exitCode"] = exit
				}
			}
		}
		if rawCommand := rawCommandFromInput(tool.rawInput); rawCommand != "" {
			detailMap["command"] = commanddetail.UnwrapShell(rawCommand)
			detailMap["rawCommand"] = rawCommand
		}
	}
	detail, _ := json.Marshal(detailMap)
	status := activityStatusFromTool(tool.status)
	if completed && activityKind == domain.ActivityKindCommand && status == domain.ActivityStatusCompleted {
		if exit, ok := number(detailMap["exitCode"]); ok && exit != 0 {
			status = domain.ActivityStatusFailed
		}
	}
	kind := ports.ChatEventActivityStarted
	if completed {
		kind = ports.ChatEventActivityCompleted
	}
	summary := strings.TrimSpace(tool.title)
	if summary == "" {
		summary = "Agent tool"
	}
	return ports.ChatEvent{
		Kind: kind, ProviderTurnID: turnID, ProviderItemID: c.providerItemID(tool.id),
		ActivityKind: activityKind, ActivityStatus: status,
		Summary: summary, Detail: detail,
	}
}

func acpFilePatch(path string, oldText *string, newText string) string {
	oldLines, newLines := acpPatchLines(oldText, newText)
	return acpPatch(path, oldLines, newLines)
}

func acpPatchLines(oldText *string, newText string) (oldLines, newLines []string) {
	oldLines = []string{}
	if oldText != nil {
		oldLines = strings.Split(strings.ReplaceAll(*oldText, "\r\n", "\n"), "\n")
	}
	return oldLines, strings.Split(strings.ReplaceAll(newText, "\r\n", "\n"), "\n")
}

func cappedACPPatch(path string, oldText *string, newText string) (string, bool) {
	oldLines, newLines := acpPatchLines(oldText, newText)
	for len(oldLines) > 0 && len(newLines) > 0 && oldLines[0] == newLines[0] {
		oldLines, newLines = oldLines[1:], newLines[1:]
	}
	for len(oldLines) > 0 && len(newLines) > 0 && oldLines[len(oldLines)-1] == newLines[len(newLines)-1] {
		oldLines, newLines = oldLines[:len(oldLines)-1], newLines[:len(newLines)-1]
	}
	if len(path) > commanddetail.MaxPatchChars/4 {
		path = commanddetail.Head(path, commanddetail.MaxPatchChars/4-3) + "..."
	}
	if patch := acpPatch(path, oldLines, newLines); len(patch) <= commanddetail.MaxPatchChars {
		return patch, false
	}
	budget := commanddetail.MaxPatchChars - len(acpPatch(path, nil, nil))
	oldBudget := max(min(patchLinesSize(oldLines), budget/2), budget-patchLinesSize(newLines))
	return acpPatch(path, keepPatchLines(oldLines, oldBudget), keepPatchLines(newLines, budget-oldBudget)), true
}

func patchLinesSize(lines []string) int {
	size := 0
	for _, line := range lines {
		size += len(line) + 2
	}
	return size
}

func keepPatchLines(lines []string, budget int) []string {
	kept := 0
	for _, line := range lines {
		if len(line)+2 > budget {
			break
		}
		budget -= len(line) + 2
		kept++
	}
	if kept == 0 && len(lines) > 0 && budget > 2 {
		return []string{commanddetail.Head(lines[0], budget-2)}
	}
	return lines[:kept]
}

func acpPatch(path string, oldLines, newLines []string) string {
	lines := make([]string, 0, 2+len(oldLines)+len(newLines))
	lines = append(lines, "--- "+path, "+++ "+path)
	for _, line := range oldLines {
		lines = append(lines, "-"+line)
	}
	for _, line := range newLines {
		lines = append(lines, "+"+line)
	}
	return strings.Join(lines, "\n")
}

func cappedPayload(raw any) any {
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	return commanddetail.TruncatedJSON(encoded)
}

func imageStub(mimeType any, data string) map[string]any {
	return map[string]any{"type": "image", "mimeType": mimeType, "bytes": len(data)}
}

func contentWithoutImageData(content []acpsdk.ToolCallContent) []any {
	if content == nil {
		return nil
	}
	out := make([]any, len(content))
	for i, item := range content {
		if item.Content != nil && item.Content.Content.Image != nil {
			image := item.Content.Content.Image
			out[i] = map[string]any{"type": "content", "content": imageStub(image.MimeType, image.Data)}
			continue
		}
		out[i] = item
	}
	return out
}

func withoutImageData(value any) any {
	switch v := value.(type) {
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = withoutImageData(item)
		}
		return out
	case map[string]any:
		if v["type"] == "image" {
			if data, ok := v["data"].(string); ok {
				return imageStub(v["mimeType"], data)
			}
			if source, ok := v["source"].(map[string]any); ok {
				if data, ok := source["data"].(string); ok {
					return imageStub(source["media_type"], data)
				}
			}
		}
		out := make(map[string]any, len(v))
		for key, item := range v {
			out[key] = withoutImageData(item)
		}
		return out
	}
	return value
}

func toolOutputText(raw any) string {
	switch value := raw.(type) {
	case nil:
		return ""
	case string:
		return value
	case map[string]any:
		for _, key := range []string{"output", "text", "error"} {
			if text := toolOutputText(value[key]); text != "" {
				return text
			}
		}
		if text := toolOutputText(value["metadata"]); text != "" {
			return text
		}
	}

	encoded, err := json.Marshal(raw)
	if err != nil || string(encoded) == "null" {
		return ""
	}
	return string(encoded)
}

func rawCommandFromInput(raw any) string {
	value, ok := raw.(map[string]any)
	if !ok {
		return ""
	}
	for _, key := range []string{"command", "cmd"} {
		if text, ok := value[key].(string); ok && strings.TrimSpace(text) != "" {
			return text
		}
	}
	return ""
}

func parentToolUseID(meta map[string]any) string {
	claude := nestedMap(meta, "claudeCode")
	if claude == nil {
		return ""
	}
	value, _ := claude["parentToolUseId"].(string)
	return value
}

func terminalOutput(meta map[string]any) string {
	terminal := nestedMap(meta, "terminal_output")
	if terminal == nil {
		return ""
	}
	value, _ := terminal["data"].(string)
	return value
}

func nestedMap(meta map[string]any, key string) map[string]any {
	if meta == nil {
		return nil
	}
	value, _ := meta[key].(map[string]any)
	return value
}

func (c *conversation) sessionFailureEvent(
	turnID, sourceID string,
	meta map[string]any,
) (ports.ChatEvent, bool) {
	failure := sessionFailure(meta)
	if failure == nil {
		return ports.ChatEvent{}, false
	}
	title, _ := failure["title"].(string)
	title = strings.TrimSpace(title)

	detailMap := map[string]any{"event": "provider.failure"}
	for _, key := range []string{"category", "severity"} {
		if value, ok := failure[key].(string); ok && strings.TrimSpace(value) != "" {
			detailMap[key] = strings.TrimSpace(value)
		}
	}
	if details, ok := failure["details"].(string); ok && strings.TrimSpace(details) != "" {
		detailMap["text"] = strings.TrimSpace(details)
	}
	if revision, ok := number(failure["revision"]); ok && revision >= 0 {
		detailMap["revision"] = revision
	}
	detail, _ := json.Marshal(detailMap)
	event := ports.ChatEvent{
		Kind:           ports.ChatEventActivityStarted,
		ProviderTurnID: turnID,
		ActivityKind:   domain.ActivityKindSystem,
		ActivityStatus: domain.ActivityStatusRunning,
		Summary:        title,
		Detail:         detail,
	}
	c.mu.Lock()
	if c.providerFailure != nil && c.providerFailure.ProviderTurnID == turnID {
		event.ProviderItemID = c.providerFailure.ProviderItemID
	} else {
		if sourceID == "" {
			sourceID = uuid.NewString()
		}
		event.ProviderItemID = c.providerItemID("session-failure:" + sourceID)
	}
	c.providerFailure = &event
	c.mu.Unlock()
	return event, true
}

func (c *conversation) trackCompactionStatus(turnID string, meta map[string]any) {
	compaction := nestedMap(meta, "opencode/compaction")
	if compaction == nil {
		return
	}
	failure := ""
	switch status, _ := compaction["status"].(string); status {
	case "started", "completed":
	case "failed":
		failure, _ = nestedMap(compaction, "error")["message"].(string)
		if failure = strings.TrimSpace(failure); failure == "" {
			failure = "Compaction failed"
		}
	default:
		return
	}
	c.mu.Lock()
	if c.compactingTurnID == turnID {
		c.compactionFailure = failure
	}
	c.mu.Unlock()
}

func sessionFailure(meta map[string]any) map[string]any {
	air := nestedMap(nestedMap(meta, "jetbrains"), "air")
	version, versionOK := number(air["version"])
	failure := nestedMap(air, "sessionFailure")
	id, _ := failure["id"].(string)
	title, _ := failure["title"].(string)
	if !versionOK || version < 1 || strings.TrimSpace(id) == "" || strings.TrimSpace(title) == "" {
		return nil
	}
	return failure
}

func (c *conversation) completeProviderFailure(turnID string, emit func(ports.ChatEvent)) {
	c.mu.Lock()
	if c.providerFailure == nil || c.providerFailure.ProviderTurnID != turnID {
		c.mu.Unlock()
		return
	}
	event := *c.providerFailure
	c.providerFailure = nil
	c.mu.Unlock()

	event.Kind = ports.ChatEventActivityCompleted
	event.ActivityStatus = domain.ActivityStatusCompleted
	emit(event)
}

func cloneMeta(meta map[string]any) map[string]any {
	return mergeMeta(nil, meta)
}

func mergeMeta(existing, update map[string]any) map[string]any {
	if len(existing) == 0 && len(update) == 0 {
		return nil
	}
	out := make(map[string]any, len(existing)+len(update))
	for key, value := range existing {
		out[key] = value
	}
	for key, value := range update {
		if current, ok := out[key].(map[string]any); ok {
			if incoming, ok := value.(map[string]any); ok {
				out[key] = mergeMeta(current, incoming)
				continue
			}
		}
		out[key] = value
	}
	return out
}

func copyDetail(target, source map[string]any, sourceKey, targetKey string) {
	if value, ok := source[sourceKey]; ok {
		target[targetKey] = value
	}
}

func claudeRateLimits(meta map[string]any) *ports.ChatRateLimits {
	value := nestedMap(meta, "_claude/rateLimit")
	if value == nil {
		return nil
	}
	limits := &ports.ChatRateLimits{PrimaryUsedPercent: -1, SecondaryUsedPercent: -1}
	if utilization, ok := number(value["utilization"]); ok {
		limits.PrimaryUsedPercent = utilization * 100
	}
	if resetsAt, ok := number(value["resetsAt"]); ok {
		remaining := int64(resetsAt) - time.Now().Unix()
		if remaining < 0 {
			remaining = 0
		}
		limits.PrimaryResetsInSeconds = remaining
	}
	if kind, ok := value["rateLimitType"].(string); ok {
		limits.PlanLabel = strings.ReplaceAll(kind, "_", " ")
	}
	return limits
}

const claudeUsageLimitMaxWait = 31 * 24 * time.Hour

func (c *conversation) trackClaudeRateLimit(meta map[string]any, now time.Time) {
	info := nestedMap(meta, "_claude/rateLimit")
	if info == nil {
		return
	}
	kind, _ := info["rateLimitType"].(string)
	if kind == "" {
		kind = "unknown"
	}
	status, _ := info["status"].(string)
	overageStatus, _ := info["overageStatus"].(string)
	usingOverage, _ := info["isUsingOverage"].(bool)
	overageInUse, _ := info["overageInUse"].(bool)
	overage := overageStatus == "allowed" || overageStatus == "allowed_warning" || usingOverage || overageInUse
	c.mu.Lock()
	defer c.mu.Unlock()
	switch {
	case status == "rejected" && !overage:
		var reset time.Time
		if seconds, ok := number(info["resetsAt"]); ok && seconds > 0 {
			if at := time.Unix(int64(seconds), 0).UTC(); at.Sub(now) <= claudeUsageLimitMaxWait {
				reset = at
			}
		}
		if c.claudeRejectedLimits == nil {
			c.claudeRejectedLimits = make(map[string]time.Time)
		}
		c.claudeRejectedLimits[kind] = reset
	case status == "allowed" || status == "allowed_warning" || overage:
		delete(c.claudeRejectedLimits, kind)
	}
}

func (c *conversation) claudeUsageLimitReset(now time.Time) (time.Time, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for kind, reset := range c.claudeRejectedLimits {
		if !reset.IsZero() && !reset.After(now) {
			delete(c.claudeRejectedLimits, kind)
		}
	}
	var latest time.Time
	for _, reset := range c.claudeRejectedLimits {
		if reset.IsZero() {
			return time.Time{}, false
		}
		if reset.After(latest) {
			latest = reset
		}
	}
	return latest, !latest.IsZero()
}

func number(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	default:
		return 0, false
	}
}

func activityKindFromTool(kind acpsdk.ToolKind) domain.ActivityKind {
	switch kind {
	case acpsdk.ToolKindExecute:
		return domain.ActivityKindCommand
	case acpsdk.ToolKindEdit, acpsdk.ToolKindDelete, acpsdk.ToolKindMove:
		return domain.ActivityKindFileChange
	case acpsdk.ToolKindThink:
		return domain.ActivityKindReasoning
	default:
		return domain.ActivityKindMCPTool
	}
}

func activityStatusFromTool(status acpsdk.ToolCallStatus) domain.ActivityStatus {
	switch status {
	case acpsdk.ToolCallStatusCompleted:
		return domain.ActivityStatusCompleted
	case acpsdk.ToolCallStatusFailed:
		return domain.ActivityStatusFailed
	case acpsdk.ToolCallStatusPending:
		return domain.ActivityStatusPending
	default:
		return domain.ActivityStatusRunning
	}
}

func toolTerminal(status acpsdk.ToolCallStatus) bool {
	return status == acpsdk.ToolCallStatusCompleted || status == acpsdk.ToolCallStatusFailed
}

func (c *conversation) emitDiffs(
	turnID, toolID string,
	content []acpsdk.ToolCallContent,
	emit func(ports.ChatEvent),
) {
	byPath := make(map[string]ports.ChatDiffFile)
	var pathOrder []string
	for _, item := range content {
		if item.Diff == nil {
			continue
		}
		path := item.Diff.Path
		if _, exists := byPath[path]; !exists {
			pathOrder = append(pathOrder, path)
		}
		byPath[path] = diffFileFromSnapshot(path, item.Diff.OldText, item.Diff.NewText)
	}
	if len(byPath) == 0 {
		return
	}
	files := make([]ports.ChatDiffFile, 0, len(pathOrder))
	for _, path := range pathOrder {
		files = append(files, byPath[path])
	}
	c.mu.Lock()
	if c.turnDiffTurnID != turnID || c.turnDiffs == nil {
		c.turnDiffs = &turnDiffAccumulator{}
		c.turnDiffTurnID = turnID
	}
	c.turnDiffs.replaceTool(toolID, files)
	aggregated := c.turnDiffs.aggregate()
	c.mu.Unlock()
	emit(ports.ChatEvent{Kind: ports.ChatEventTurnDiff, ProviderTurnID: turnID, Diff: &ports.ChatTurnDiff{Files: aggregated}})
}

func normalizePlan(entries []acpsdk.PlanEntry) *domain.ConversationPlan {
	plan := &domain.ConversationPlan{Steps: make([]domain.ConversationPlanStep, 0, len(entries))}
	for _, entry := range entries {
		status := domain.PlanStepPending
		switch entry.Status {
		case acpsdk.PlanEntryStatusInProgress:
			status = domain.PlanStepInProgress
		case acpsdk.PlanEntryStatusCompleted:
			status = domain.PlanStepCompleted
		}
		plan.Steps = append(plan.Steps, domain.ConversationPlanStep{Text: entry.Content, Status: status})
	}
	return plan
}

func pointerValue[T any](value *T) T {
	if value == nil {
		var zero T
		return zero
	}
	return *value
}
