package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
)

// Agent-facing workflow collaboration tools.
//
// The identity question is the whole design. An agent must be able to speak as the
// workflow attempt it IS, and must not be able to speak as any other one. So:
//
//   - the session id, workflow id, attempt id, and capability come ONLY from this
//     process's own environment, which the daemon sets on the MCP subprocess when it
//     starts a workflow session;
//   - no tool schema declares workflowId, attemptId, lease, capability, or a sender
//     field, so a model cannot express a forged identity even if it tries;
//   - a session without those variables gets a readable "not a workflow attempt"
//     result and makes no HTTP call at all.
//
// No tool result, error string, or log line ever contains the capability or the
// lease. A daemon error is reported as its status code plus the envelope's public
// code/message, which the daemon already guarantees carries no secret.

// Workflow MCP env var names. They mirror sessionmanager's constants; the CLI is a
// separate binary boundary and deliberately does not import the daemon's internals.
const (
	envMCPSessionID   = "AO_SESSION_ID"
	envMCPWorkflowID  = "AO_WORKFLOW_ID" // #nosec G101 -- 常量是环境变量名 AO_WORKFLOW_ID，不包含工作流凭据。
	envMCPTaskID      = "AO_TASK_ID"
	envMCPAttemptID   = "AO_ATTEMPT_ID"
	envMCPCapability  = "AO_BROWSER_CAPABILITY" //nolint:gosec // Environment variable name, not a value.
	envMCPRunFileHint = "AO_RUN_FILE"           //nolint:unused // documented for readers of this file
)

// Identity headers the daemon's principal resolver reads. Named here rather than
// imported so the CLI keeps its one-way dependency on the daemon's HTTP contract.
const (
	headerAgentCapability = "X-AO-Agent-Capability"
	headerSessionID       = "X-AO-Session-Id"
	headerAttemptID       = "X-AO-Attempt-Id"
)

// errNotWorkflowAttempt is the readable answer for a session that is not a workflow
// attempt. It is a tool result, not a protocol error: the agent should see it and
// stop, not retry.
var errNotWorkflowAttempt = errors.New(
	"this session is not a workflow attempt: workflow collaboration tools are unavailable here")

// workflowToolIdentity 携带待服务端验证的 Session capability 和一致性标识。
// Workflow/Attempt 标识不能替代服务端从 Session 读取的持久身份。
type workflowToolIdentity struct {
	sessionID  string
	workflowID string
	attemptID  string
	capability string
}

// workflowIdentityFromEnv reads the identity this process holds.
//
// Session、Workflow、Attempt 和 capability 缺一不可；不完整身份在本地拒绝。
// 旧 lease 环境不读取，内部租约不进入 CLI 身份或 HTTP 请求。
func workflowIdentityFromEnv(getenv func(string) string) (workflowToolIdentity, bool) {
	identity := workflowToolIdentity{
		sessionID:  strings.TrimSpace(getenv(envMCPSessionID)),
		workflowID: strings.TrimSpace(getenv(envMCPWorkflowID)),
		attemptID:  strings.TrimSpace(getenv(envMCPAttemptID)),
		capability: strings.TrimSpace(getenv(envMCPCapability)),
	}
	return identity, identity.sessionID != "" && identity.workflowID != "" &&
		identity.attemptID != "" && identity.capability != ""
}

// headers 将已通过本地完整性检查的 Session 凭据与非秘密标识写入请求。
func (i workflowToolIdentity) headers() map[string]string {
	return map[string]string{
		"Content-Type":        "application/json",
		headerAgentCapability: i.capability,
		headerSessionID:       i.sessionID,
		headerAttemptID:       i.attemptID,
	}
}

// workflowMessagesPath is the workflow-scoped messages route.
func (i workflowToolIdentity) workflowPath(suffix string) string {
	return "/api/v1/workflows/" + url.PathEscape(i.workflowID) + suffix
}

// --- schemas -----------------------------------------------------------------

// workflowToolSchemas are the five tools. None of them declares an identity field.
var workflowToolSchemas = []map[string]any{
	{
		"name":  "workflow_message_send",
		"title": "Send workflow message",
		"description": "Send a collaboration message to this workflow, optionally addressed to another task. " +
			"Your identity and attempt are supplied by the session; you cannot and need not name them. " +
			"Pass a stable clientMessageId so a retry is deduplicated instead of posting twice.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"kind": map[string]any{
					"type": "string", "enum": []string{"progress", "question", "result", "handoff", "reference"},
					"description": "What kind of message this is.",
				},
				"body": map[string]any{
					"type": "string", "maxLength": 4096,
					"description": "The message text. Omit only when payloadRef carries the content.",
				},
				"toTaskId": map[string]any{
					"type": "string", "maxLength": 200,
					"description": "Optional target task id. Omit to broadcast to the workflow.",
				},
				"payloadRef": map[string]any{
					"type": "string", "maxLength": 512,
					"description": "Optional opaque artifact id. Never a filesystem path.",
				},
				"clientMessageId": map[string]any{
					"type": "string", "minLength": 1, "maxLength": 200,
					"description": "Caller-chosen idempotency key. Reusing it with the same content returns the same message.",
				},
			},
			"required": []string{"kind", "clientMessageId"},
		},
		"annotations": map[string]any{
			"title": "Send workflow message", "readOnlyHint": false, "destructiveHint": false,
			"idempotentHint": true, "openWorldHint": false,
		},
	},
	{
		"name":        "workflow_messages_list",
		"title":       "List workflow messages",
		"description": "List this workflow's collaboration messages, oldest first.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"limit":  map[string]any{"type": "integer", "minimum": 1, "maximum": 100, "description": "Page size, 1-100; defaults to 50."},
				"offset": map[string]any{"type": "integer", "minimum": 0, "description": "Number of messages to skip."},
			},
		},
		"annotations": map[string]any{
			"title": "List workflow messages", "readOnlyHint": true, "destructiveHint": false,
			"idempotentHint": true, "openWorldHint": false,
		},
	},
	{
		"name":  "workflow_task_request_create",
		"title": "Raise a workflow task request",
		"description": "Raise a request for work or resources (more tasks, a dependency, more repositories, " +
			"more concurrency, a larger budget). The owner decides; this records the request under your identity.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"requestId": map[string]any{
					"type": "string", "minLength": 1, "maxLength": 200,
					"description": "Caller-chosen stable id for this request.",
				},
				"requestKind": map[string]any{
					"type":        "string",
					"enum":        []string{"create_task", "add_dependency", "expand_repos", "increase_concurrency", "increase_budget"},
					"description": "What is being requested.",
				},
				"payload": map[string]any{
					"type": "object", "description": "Opaque request body; the owner's decision path interprets it.",
				},
			},
			"required": []string{"requestId", "requestKind"},
		},
		"annotations": map[string]any{
			"title": "Raise a workflow task request", "readOnlyHint": false, "destructiveHint": false,
			"idempotentHint": true, "openWorldHint": false,
		},
	},
	{
		"name":        "workflow_task_request_get",
		"title":       "Read one workflow task request",
		"description": "Read one task request of this workflow, including its decision state.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"requestId": map[string]any{"type": "string", "minLength": 1, "maxLength": 200},
			},
			"required": []string{"requestId"},
		},
		"annotations": map[string]any{
			"title": "Read one workflow task request", "readOnlyHint": true, "destructiveHint": false,
			"idempotentHint": true, "openWorldHint": false,
		},
	},
	{
		"name":        "workflow_task_requests_list",
		"title":       "List workflow task requests",
		"description": "List this workflow's task requests with their decision states.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"limit":  map[string]any{"type": "integer", "minimum": 1, "maximum": 100, "description": "Page size, 1-100; defaults to 50."},
				"offset": map[string]any{"type": "integer", "minimum": 0, "description": "Number of requests to skip."},
			},
		},
		"annotations": map[string]any{
			"title": "List workflow task requests", "readOnlyHint": true, "destructiveHint": false,
			"idempotentHint": true, "openWorldHint": false,
		},
	},
}

// workflowToolNames is the set of tool names this file dispatches.
func isWorkflowMCPTool(name string) bool {
	for _, tool := range workflowToolSchemas {
		if tool["name"] == name {
			return true
		}
	}
	return false
}

// workflowMCPTools returns the tools this session may offer: all five for a workflow
// attempt, none otherwise. A tool list that advertised them to a non-workflow
// session would be advertising five guaranteed refusals.
func workflowMCPTools(getenv func(string) string) []map[string]any {
	if _, ok := workflowIdentityFromEnv(getenv); !ok {
		return nil
	}
	return workflowToolSchemas
}

// --- argument shapes ---------------------------------------------------------

type workflowMessageSendArgs struct {
	Kind            string `json:"kind"`
	Body            string `json:"body"`
	ToTaskID        string `json:"toTaskId"`
	PayloadRef      string `json:"payloadRef"`
	ClientMessageID string `json:"clientMessageId"`
}

type workflowListArgs struct {
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
}

type workflowTaskRequestCreateArgs struct {
	RequestID   string          `json:"requestId"`
	RequestKind string          `json:"requestKind"`
	Payload     json.RawMessage `json:"payload"`
}

type workflowTaskRequestGetArgs struct {
	RequestID string `json:"requestId"`
}

// --- call implementations ----------------------------------------------------

// callWorkflowTool dispatches one of the five tools. Every path returns a tool
// result (never a protocol error), so the agent reads the outcome.
func (c *commandContext) callWorkflowTool(ctx context.Context, name string, raw json.RawMessage) map[string]any {
	identity, ok := workflowIdentityFromEnv(os.Getenv)
	if !ok {
		return mcpToolError(errNotWorkflowAttempt)
	}
	switch name {
	case "workflow_message_send":
		return c.workflowMessageSend(ctx, identity, raw)
	case "workflow_messages_list":
		return c.workflowMessagesList(ctx, identity, raw)
	case "workflow_task_request_create":
		return c.workflowTaskRequestCreate(ctx, identity, raw)
	case "workflow_task_request_get":
		return c.workflowTaskRequestGet(ctx, identity, raw)
	case "workflow_task_requests_list":
		return c.workflowTaskRequestsList(ctx, identity, raw)
	}
	return mcpToolError(fmt.Errorf("unknown tool %q", name))
}

func (c *commandContext) workflowMessageSend(
	ctx context.Context,
	identity workflowToolIdentity,
	raw json.RawMessage,
) map[string]any {
	var args workflowMessageSendArgs
	if err := decodeWorkflowArgs(raw, &args); err != nil {
		return mcpToolError(err)
	}
	if strings.TrimSpace(args.Kind) == "" || strings.TrimSpace(args.ClientMessageID) == "" {
		return mcpToolError(errors.New("kind and clientMessageId are required"))
	}
	body := map[string]any{"kind": args.Kind, "clientMessageId": args.ClientMessageID}
	if args.Body != "" {
		body["body"] = args.Body
	}
	if args.ToTaskID != "" {
		body["toTaskId"] = args.ToTaskID
	}
	if args.PayloadRef != "" {
		body["payloadRef"] = args.PayloadRef
	}
	var response struct {
		Message struct {
			ID              string `json:"id"`
			WorkflowID      string `json:"workflowId"`
			FromTaskID      string `json:"fromTaskId"`
			ToTaskID        string `json:"toTaskId"`
			SenderKind      string `json:"senderKind"`
			Kind            string `json:"kind"`
			DeliveryState   string `json:"deliveryState"`
			ClientMessageID string `json:"clientMessageId"`
		} `json:"message"`
		Created bool `json:"created"`
	}
	if err := c.doWorkflowCall(ctx, http.MethodPost, identity.workflowPath("/messages"), body, identity, &response); err != nil {
		return mcpToolError(err)
	}
	// The identity the daemon recorded is echoed back so the agent can see which
	// attempt it spoke as, but the response carries no token of any kind.
	return mcpJSONResult(map[string]any{
		"id":              response.Message.ID,
		"workflowId":      response.Message.WorkflowID,
		"fromTaskId":      response.Message.FromTaskID,
		"toTaskId":        response.Message.ToTaskID,
		"senderKind":      response.Message.SenderKind,
		"kind":            response.Message.Kind,
		"deliveryState":   response.Message.DeliveryState,
		"clientMessageId": response.Message.ClientMessageID,
		"created":         response.Created,
	})
}

func (c *commandContext) workflowMessagesList(
	ctx context.Context,
	identity workflowToolIdentity,
	raw json.RawMessage,
) map[string]any {
	var args workflowListArgs
	if err := decodeWorkflowArgs(raw, &args); err != nil {
		return mcpToolError(err)
	}
	path, err := workflowListPath(identity.workflowPath("/messages"), args)
	if err != nil {
		return mcpToolError(err)
	}
	var response struct {
		Messages []struct {
			ID              string `json:"id"`
			FromTaskID      string `json:"fromTaskId"`
			ToTaskID        string `json:"toTaskId"`
			SenderKind      string `json:"senderKind"`
			Kind            string `json:"kind"`
			DeliveryState   string `json:"deliveryState"`
			Body            string `json:"body"`
			ClientMessageID string `json:"clientMessageId"`
			CreatedAt       string `json:"createdAt"`
		} `json:"messages"`
		NextCursor string `json:"nextCursor"`
	}
	if err := c.doWorkflowCall(ctx, http.MethodGet, path, nil, identity, &response); err != nil {
		return mcpToolError(err)
	}
	messages := make([]map[string]any, 0, len(response.Messages))
	for _, message := range response.Messages {
		messages = append(messages, map[string]any{
			"id": message.ID, "fromTaskId": message.FromTaskID, "toTaskId": message.ToTaskID,
			"senderKind": message.SenderKind, "kind": message.Kind,
			"deliveryState": message.DeliveryState, "body": message.Body,
			"clientMessageId": message.ClientMessageID, "createdAt": message.CreatedAt,
		})
	}
	return mcpJSONResult(map[string]any{"messages": messages, "nextCursor": response.NextCursor})
}

func (c *commandContext) workflowTaskRequestCreate(
	ctx context.Context,
	identity workflowToolIdentity,
	raw json.RawMessage,
) map[string]any {
	var args workflowTaskRequestCreateArgs
	if err := decodeWorkflowArgs(raw, &args); err != nil {
		return mcpToolError(err)
	}
	if strings.TrimSpace(args.RequestID) == "" || strings.TrimSpace(args.RequestKind) == "" {
		return mcpToolError(errors.New("requestId and requestKind are required"))
	}
	body := map[string]any{"requestId": args.RequestID, "requestKind": args.RequestKind}
	if len(args.Payload) > 0 {
		body["payload"] = args.Payload
	}
	var response struct {
		Request workflowTaskRequestDTO `json:"request"`
		Created bool                   `json:"created"`
	}
	if err := c.doWorkflowCall(ctx, http.MethodPost, identity.workflowPath("/task-requests"), body, identity, &response); err != nil {
		return mcpToolError(err)
	}
	result := response.Request.asMap()
	result["created"] = response.Created
	return mcpJSONResult(result)
}

func (c *commandContext) workflowTaskRequestGet(
	ctx context.Context,
	identity workflowToolIdentity,
	raw json.RawMessage,
) map[string]any {
	var args workflowTaskRequestGetArgs
	if err := decodeWorkflowArgs(raw, &args); err != nil {
		return mcpToolError(err)
	}
	if strings.TrimSpace(args.RequestID) == "" {
		return mcpToolError(errors.New("requestId is required"))
	}
	var response struct {
		Request workflowTaskRequestDTO `json:"request"`
	}
	path := identity.workflowPath("/task-requests/" + url.PathEscape(args.RequestID))
	if err := c.doWorkflowCall(ctx, http.MethodGet, path, nil, identity, &response); err != nil {
		return mcpToolError(err)
	}
	return mcpJSONResult(response.Request.asMap())
}

func (c *commandContext) workflowTaskRequestsList(
	ctx context.Context,
	identity workflowToolIdentity,
	raw json.RawMessage,
) map[string]any {
	var args workflowListArgs
	if err := decodeWorkflowArgs(raw, &args); err != nil {
		return mcpToolError(err)
	}
	path, err := workflowListPath(identity.workflowPath("/task-requests"), args)
	if err != nil {
		return mcpToolError(err)
	}
	var response struct {
		Requests   []workflowTaskRequestDTO `json:"requests"`
		NextCursor string                   `json:"nextCursor"`
	}
	if err := c.doWorkflowCall(ctx, http.MethodGet, path, nil, identity, &response); err != nil {
		return mcpToolError(err)
	}
	requests := make([]map[string]any, 0, len(response.Requests))
	for _, request := range response.Requests {
		requests = append(requests, request.asMap())
	}
	return mcpJSONResult(map[string]any{"requests": requests, "nextCursor": response.NextCursor})
}

// workflowTaskRequestDTO mirrors the daemon's task-request shape. It is a hand
// mirror, matching how this CLI already carries its own DTOs.
type workflowTaskRequestDTO struct {
	ID               string `json:"id"`
	WorkflowID       string `json:"workflowId"`
	RequesterTaskID  string `json:"requesterTaskId"`
	RequestKind      string `json:"requestKind"`
	RequestSHA       string `json:"requestSha"`
	State            string `json:"state"`
	DecidedByKind    string `json:"decidedByKind"`
	DecidedByRef     string `json:"decidedByRef"`
	Reason           string `json:"reason"`
	WorkflowRevision int64  `json:"workflowRevision"`
	Revision         int64  `json:"revision"`
	// Materialization and ReasonCode are present when the daemon's answer carries them
	// (the decide route always does). They are reported verbatim, never inferred: a
	// pending approval means no task exists yet, and a tool result must not word that
	// as success.
	Materialization string `json:"materialization"`
	ReasonCode      string `json:"reasonCode"`
}

func (r workflowTaskRequestDTO) asMap() map[string]any {
	return map[string]any{
		"id": r.ID, "workflowId": r.WorkflowID, "requesterTaskId": r.RequesterTaskID,
		"requestKind": r.RequestKind, "requestSha": r.RequestSHA, "state": r.State,
		"decidedByKind": r.DecidedByKind, "decidedByRef": r.DecidedByRef,
		"reason": r.Reason, "workflowRevision": r.WorkflowRevision, "revision": r.Revision,
		"materialization": r.materialization(), "reasonCode": r.ReasonCode,
		"note": r.note(),
	}
}

// materialization reports what happened to the requested graph change, derived from
// the state ONLY when the daemon did not say. An approval with no materialization
// field is "pending": this daemon records approvals without creating tasks, and
// claiming otherwise would be a lie the UI could render.
func (r workflowTaskRequestDTO) materialization() string {
	if r.Materialization != "" {
		return r.Materialization
	}
	switch r.State {
	case "approved":
		return "pending"
	case "rejected":
		return "not_applicable"
	default:
		return ""
	}
}

// note is the plain-language consequence, so no reader has to interpret a status.
func (r workflowTaskRequestDTO) note() string {
	switch r.materialization() {
	case "pending":
		return "Approved. No task has been created yet."
	case "not_applicable":
		return "Rejected. Nothing was created."
	case "materialized":
		return "Approved and the task exists."
	default:
		return "Waiting for an owner decision. Nothing has been created."
	}
}

// --- transport ---------------------------------------------------------------

// doWorkflowCall performs one authenticated workflow API call.
//
// The error it returns is already safe to show: a daemon refusal becomes the status
// code plus the envelope's public code and message, which never include the
// capability or the lease. Transport errors keep their wrapped cause text, which is
// about the connection, not the credential.
func (c *commandContext) doWorkflowCall(
	ctx context.Context,
	method, path string,
	body any,
	identity workflowToolIdentity,
	out any,
) error {
	err := c.doJSONPathWithHeaders(ctx, method, path, body, out, identity.headers())
	if err == nil {
		return nil
	}
	return workflowCallError(err)
}

// workflowCallError renders a daemon refusal without disclosing any credential.
func workflowCallError(err error) error {
	var response apiResponseError
	if errors.As(err, &response) {
		detail := response.ErrorBody.Message
		if response.ErrorBody.Code != "" {
			detail = strings.TrimSpace(detail + " (" + response.ErrorBody.Code + ")")
		}
		if detail == "" {
			return fmt.Errorf("workflow call failed with HTTP %d", response.StatusCode)
		}
		return fmt.Errorf("workflow call failed with HTTP %d: %s", response.StatusCode, detail)
	}
	var unavailable daemonUnavailableError
	if errors.As(err, &unavailable) {
		return errors.New(unavailable.message)
	}
	// Anything else is a decode/transport problem; its text names the route and
	// never a header value.
	return fmt.Errorf("workflow call failed: %s", err.Error())
}

// --- helpers -----------------------------------------------------------------

func decodeWorkflowArgs(raw json.RawMessage, out any) error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("invalid arguments: %w", err)
	}
	return nil
}

// workflowListPath appends the validated page arguments.
func workflowListPath(base string, args workflowListArgs) (string, error) {
	if args.Limit < 0 || args.Limit > 100 {
		return "", errors.New("limit must be between 1 and 100")
	}
	if args.Offset < 0 {
		return "", errors.New("offset cannot be negative")
	}
	query := url.Values{}
	if args.Limit > 0 {
		query.Set("limit", strconv.Itoa(args.Limit))
	}
	if args.Offset > 0 {
		query.Set("offset", strconv.Itoa(args.Offset))
	}
	if len(query) == 0 {
		return base, nil
	}
	return base + "?" + query.Encode(), nil
}

// mcpJSONResult renders a structured result as one JSON text block, which is what an
// agent reads.
func mcpJSONResult(payload map[string]any) map[string]any {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return mcpToolError(err)
	}
	return map[string]any{"content": []any{mcpText(string(encoded))}}
}
