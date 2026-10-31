package coderapi

import "encoding/json"

// The wire types below mirror codersdk at Coder v2.37.9 (codersdk/chats.go),
// limited to the fields AO reads or sends. Unknown fields are ignored so a
// newer deployment's additions do not break decoding.

// BuildInfo is GET /api/v2/buildinfo.
type BuildInfo struct {
	Version string `json:"version"`
}

// User is GET /api/v2/users/me.
type User struct {
	ID              string   `json:"id"`
	Username        string   `json:"username"`
	Email           string   `json:"email"`
	Name            string   `json:"name"`
	OrganizationIDs []string `json:"organization_ids"`
}

// Organization is one entry of GET /api/v2/users/me/organizations.
type Organization struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	IsDefault   bool   `json:"is_default"`
}

// ChatModel is one admin-configured model config.
type ChatModel struct {
	ID               string   `json:"id"`
	Model            string   `json:"model"`
	DisplayName      string   `json:"display_name"`
	Enabled          bool     `json:"enabled"`
	IsDefault        bool     `json:"is_default"`
	ContextLimit     int64    `json:"context_limit"`
	ReasoningEfforts []string `json:"reasoning_efforts,omitempty"`
}

// Chat statuses.
const (
	ChatStatusWaiting        = "waiting"
	ChatStatusRunning        = "running"
	ChatStatusError          = "error"
	ChatStatusRequiresAction = "requires_action"
	ChatStatusInterrupting   = "interrupting"
)

// Chat is a Coder Agents conversation.
type Chat struct {
	ID             string            `json:"id"`
	OrganizationID string            `json:"organization_id"`
	OwnerID        string            `json:"owner_id"`
	WorkspaceID    *string           `json:"workspace_id,omitempty"`
	ParentChatID   *string           `json:"parent_chat_id,omitempty"`
	Status         string            `json:"status"`
	Archived       bool              `json:"archived"`
	Title          string            `json:"title"`
	Labels         map[string]string `json:"labels"`
	LastError      *ChatError        `json:"last_error,omitempty"`
}

// HasWorkspace reports whether Coder bound a workspace to the chat.
func (c Chat) HasWorkspace() bool { return c.WorkspaceID != nil && *c.WorkspaceID != "" }

// ChatInputPart is a user input part. AO sends text only.
type ChatInputPart struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// TextInput builds a single text content part.
func TextInput(text string) []ChatInputPart {
	return []ChatInputPart{{Type: "text", Text: text}}
}

// DynamicTool declares a client-executed tool (`unsafe_dynamic_tools`).
type DynamicTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// CreateChatRequest is POST /api/v2/chats.
type CreateChatRequest struct {
	OrganizationID     string            `json:"organization_id"`
	Content            []ChatInputPart   `json:"content"`
	SystemPrompt       string            `json:"system_prompt,omitempty"`
	ModelConfigID      string            `json:"model_config_id,omitempty"`
	ReasoningEffort    string            `json:"reasoning_effort,omitempty"`
	Labels             map[string]string `json:"labels,omitempty"`
	UnsafeDynamicTools []DynamicTool     `json:"unsafe_dynamic_tools,omitempty"`
	ClientType         string            `json:"client_type,omitempty"`
}

// CreateChatMessageRequest is POST /api/v2/chats/{chat}/messages.
type CreateChatMessageRequest struct {
	Content         []ChatInputPart `json:"content"`
	ModelConfigID   string          `json:"model_config_id,omitempty"`
	ReasoningEffort string          `json:"reasoning_effort,omitempty"`
	BusyBehavior    string          `json:"busy_behavior,omitempty"`
}

// CreateChatMessageResponse is the reply to CreateChatMessage.
type CreateChatMessageResponse struct {
	Message  *ChatMessage  `json:"message,omitempty"`
	Messages []ChatMessage `json:"messages,omitempty"`
	Queued   bool          `json:"queued"`
}

// ChatMessagesResponse is GET /api/v2/chats/{chat}/messages.
type ChatMessagesResponse struct {
	Messages []ChatMessage `json:"messages"`
	HasMore  bool          `json:"has_more"`
}

// Message roles.
const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleTool      = "tool"
)

// Message part types.
const (
	PartText       = "text"
	PartReasoning  = "reasoning"
	PartToolCall   = "tool-call"
	PartToolResult = "tool-result"
)

// ChatMessage is a persisted message.
type ChatMessage struct {
	ID      int64             `json:"id"`
	ChatID  string            `json:"chat_id"`
	Role    string            `json:"role"`
	Content []ChatMessagePart `json:"content,omitempty"`
	Usage   *ChatMessageUsage `json:"usage,omitempty"`
}

// ChatMessagePart is one structured chunk of a message.
type ChatMessagePart struct {
	Type             string          `json:"type"`
	Text             string          `json:"text"`
	ToolCallID       string          `json:"tool_call_id,omitempty"`
	ToolName         string          `json:"tool_name,omitempty"`
	Args             json.RawMessage `json:"args,omitempty"`
	ArgsDelta        string          `json:"args_delta,omitempty"`
	Result           json.RawMessage `json:"result,omitempty"`
	ResultDelta      string          `json:"result_delta,omitempty"`
	IsError          bool            `json:"is_error,omitempty"`
	ProviderExecuted bool            `json:"provider_executed,omitempty"`
}

// ChatMessageUsage is the token accounting on an assistant message.
type ChatMessageUsage struct {
	InputTokens     *int64 `json:"input_tokens,omitempty"`
	OutputTokens    *int64 `json:"output_tokens,omitempty"`
	TotalTokens     *int64 `json:"total_tokens,omitempty"`
	CacheReadTokens *int64 `json:"cache_read_tokens,omitempty"`
	ContextLimit    *int64 `json:"context_limit,omitempty"`
}

// Stream event types.
const (
	EventMessagePart    = "message_part"
	EventMessage        = "message"
	EventStatus         = "status"
	EventError          = "error"
	EventRetry          = "retry"
	EventActionRequired = "action_required"
	EventQueueUpdate    = "queue_update"
	EventPreviewReset   = "preview_reset"
	EventHistoryReset   = "history_reset"
)

// StreamEvent is one entry of a stream batch.
type StreamEvent struct {
	Type           string                `json:"type"`
	ChatID         string                `json:"chat_id"`
	Message        *ChatMessage          `json:"message,omitempty"`
	MessagePart    *StreamMessagePart    `json:"message_part,omitempty"`
	Status         *StreamStatus         `json:"status,omitempty"`
	Error          *ChatError            `json:"error,omitempty"`
	Retry          *StreamRetry          `json:"retry,omitempty"`
	ActionRequired *StreamActionRequired `json:"action_required,omitempty"`
}

// StreamMessagePart is a streamed, not yet persisted, message part.
type StreamMessagePart struct {
	Role string          `json:"role,omitempty"`
	Part ChatMessagePart `json:"part"`
}

// StreamStatus is a chat status change.
type StreamStatus struct {
	Status string `json:"status"`
}

// StreamRetry reports an automatic upstream retry.
type StreamRetry struct {
	Attempt int    `json:"attempt"`
	Error   string `json:"error"`
	Kind    string `json:"kind,omitempty"`
}

// StreamActionRequired lists dynamic tool calls the client must execute.
type StreamActionRequired struct {
	ToolCalls []ToolCall `json:"tool_calls"`
}

// ToolCall is one pending client-executed tool call. Args is a JSON string.
type ToolCall struct {
	ToolCallID string `json:"tool_call_id"`
	ToolName   string `json:"tool_name"`
	Args       string `json:"args"`
}

// ChatError is a terminal chat error.
type ChatError struct {
	Message    string `json:"message"`
	Detail     string `json:"detail,omitempty"`
	Kind       string `json:"kind,omitempty"`
	Retryable  bool   `json:"retryable"`
	StatusCode int    `json:"status_code,omitempty"`
}

// ToolResult answers one dynamic tool call. Output is JSON.
type ToolResult struct {
	ToolCallID string          `json:"tool_call_id"`
	Output     json.RawMessage `json:"output"`
	IsError    bool            `json:"is_error"`
}

// SubmitToolResultsRequest is POST /api/v2/chats/{chat}/tool-results.
type SubmitToolResultsRequest struct {
	Results []ToolResult `json:"results"`
}
