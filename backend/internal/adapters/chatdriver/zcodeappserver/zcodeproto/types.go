package zcodeproto

import "encoding/json"

// Every params object the server accepts is zod `.strict()`: an unknown key
// rejects the frame. Optional fields therefore carry omitempty so an unset
// value is absent rather than null or "".
//
// Provenance per type is in its doc comment: PROVEN names the testdata capture
// the shape was read from; STATIC names the bundle symbol it was read from and
// means the shape was never exercised live.

// Workspace identifies the session's working tree (`qXt.workspace`). PROVEN on
// session/create and session/resume with workspacePath == workspaceKey == the
// --cwd (p1-create-send-complete, p8-resume).
type Workspace struct {
	WorkspacePath string `json:"workspacePath"`
	WorkspaceKey  string `json:"workspaceKey"`
}

// ModelOptions is the options object of a ModelSelection. PROVEN
// (p7-models, p7-models-level-required): reasoningLevel is REQUIRED on session/setModel and on
// session/send.modelSelection — omitting it fails setModel with -32603
// "Reasoning level is required for <provider>/<model>" and fails a send's turn
// with turn.failed error.code invalid_model_request.
type ModelOptions struct {
	ReasoningLevel string `json:"reasoningLevel,omitempty"`
}

// ModelSelection names a provider model (`mhr.ref`, `QXt.modelSelection`,
// `iQt.model`). PROVEN (p7-models, p1-create-send-complete): provider ids are
// opaque strings that may contain ':' (account:zai-individual-coding-plan) or
// not (zai-api-ao); AO joins providerId and modelId with '/' and splits on the
// last one. The snapshot's settings.model.current carries Options too.
type ModelSelection struct {
	ProviderID string        `json:"providerId"`
	ModelID    string        `json:"modelId"`
	Options    *ModelOptions `json:"options,omitempty"`
}

// MCPServerConfig is one stdio tool server on session/create (`the` union,
// stdio member). PROVEN (p9-usage-read-mcp): {name, command, args, env:[{name,
// value}], isolation:"session"} is accepted and the server is spawned
// (process/mcpTelemetry session_startup). Passing the list REPLACES the
// operator's global config.mcp.servers for the session (DESIGN-zcode-context-trim.md).
// args and env are always sent, empty when AO has none: the strict schema
// rejects an entry that omits them ("mcpServers.0: Invalid input", observed
// live against zcode 0.16.9).
type MCPServerConfig struct {
	Name      string     `json:"name"`
	Command   string     `json:"command"`
	Args      []string   `json:"args"`
	Env       []EnvEntry `json:"env"`
	Isolation string     `json:"isolation,omitempty"`
}

// EnvEntry is one {name, value} pair in MCPServerConfig.Env. PROVEN (p9).
type EnvEntry struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// SessionCreateParams is session/create (`qXt`). PROVEN with workspace, mode,
// titleGenerationEnabled and mcpServers (p1-create-send-complete, p9). `model`
// is deliberately absent: sending one fails with "Provider Registry 中不存在
// Model" (HANDOFF.md); the model comes from the provider config's
// defaultModelSelection and is chosen per turn through modelSelection.
// persistence is STATIC (`LXt`) and never sent by the driver today.
type SessionCreateParams struct {
	Workspace              Workspace         `json:"workspace"`
	Mode                   string            `json:"mode,omitempty"`
	MCPServers             []MCPServerConfig `json:"mcpServers,omitempty"`
	TitleGenerationEnabled *bool             `json:"titleGenerationEnabled,omitempty"`
	Persistence            string            `json:"persistence,omitempty"`
}

// SessionSendParams is session/send (`QXt`). PROVEN with sessionId, content,
// inputId (echoed on turn.started.payload.inputId, p1) and modelSelection
// (p7-models). expectedRevision is never sent (DESIGN 3.6).
type SessionSendParams struct {
	SessionID      string          `json:"sessionId"`
	Content        string          `json:"content"`
	InputID        string          `json:"inputId,omitempty"`
	ModelSelection *ModelSelection `json:"modelSelection,omitempty"`
}

// SessionSendResult is the session/send acknowledgement (`Lhc`). PROVEN
// (p1): {accepted:true, sessionId, stateRevision}.
type SessionSendResult struct {
	SessionID     string `json:"sessionId"`
	Accepted      bool   `json:"accepted"`
	StateRevision int64  `json:"stateRevision"`
}

// SessionStopParams is session/stop (`rQt`). PROVEN (p4-stop): returns {} both
// during a turn (which then settles turn.completed resultType:"cancelled") and
// while idle, so the driver raises ErrChatNoActiveTurn itself.
type SessionStopParams struct {
	SessionID string `json:"sessionId"`
}

// SessionSubscribeParams is session/subscribe (`$Xt`). PROVEN (p1).
type SessionSubscribeParams struct {
	SessionID    string `json:"sessionId"`
	DeliveryKind string `json:"deliveryKind"`
}

// SessionSubscribeResult is the subscribe acknowledgement (`_ci`). PROVEN
// (p1, p8): {eventSeq, events:[], sessionId}; eventSeq continues after a
// resume in a new process.
type SessionSubscribeResult struct {
	SessionID string `json:"sessionId,omitempty"`
	EventSeq  int64  `json:"eventSeq,omitempty"`
}

// SessionReadParams is session/read (`mci`). PROVEN with sessionId and
// messageLimit (p7 limit 1, p9 limit 10). The params schema (`KXt`, strict)
// declares messageLimit as int().positive().optional(): 0 is refused with
// -32602, so a caller that wants no messages sends 1, never 0. deliveryKind
// is STATIC.
type SessionReadParams struct {
	SessionID    string `json:"sessionId"`
	MessageLimit *int   `json:"messageLimit,omitempty"`
	DeliveryKind string `json:"deliveryKind,omitempty"`
}

// SessionResumeParams is session/resume (`WXt`). PROVEN (p8-resume): a new
// process returns the full snapshot with messages and the session keeps its
// memory; an unknown id fails with -32004 "Session not found: <id>".
type SessionResumeParams struct {
	SessionID string    `json:"sessionId"`
	Workspace Workspace `json:"workspace"`
}

// SessionSetModelParams is session/setModel (`iQt`). PROVEN (p7-models):
// returns the full snapshot and emits state.updated reason:"model_changed".
type SessionSetModelParams struct {
	SessionID string         `json:"sessionId"`
	Model     ModelSelection `json:"model"`
}

// SessionSetModeParams is session/setMode. STATIC; not sent by the driver.
type SessionSetModeParams struct {
	SessionID string `json:"sessionId"`
	Mode      string `json:"mode"`
}

// SessionUsageParams is session/usage (`GYi`). PROVEN (p9).
type SessionUsageParams struct {
	SessionID string `json:"sessionId"`
}

// SessionUsageResult is the session/usage result (`GYi`). PROVEN (p9):
// {sessionId, totalTokens, inputTokens, outputTokens, reasoningTokens,
// cacheCreationTokens, cacheReadTokens, modelRequestCount, modelErrorCount,
// inputBaselineBySource}.
type SessionUsageResult struct {
	SessionID           string `json:"sessionId,omitempty"`
	TotalTokens         int64  `json:"totalTokens"`
	InputTokens         int64  `json:"inputTokens"`
	OutputTokens        int64  `json:"outputTokens"`
	ReasoningTokens     int64  `json:"reasoningTokens,omitempty"`
	CacheCreationTokens int64  `json:"cacheCreationTokens,omitempty"`
	CacheReadTokens     int64  `json:"cacheReadTokens"`
	ModelRequestCount   int64  `json:"modelRequestCount,omitempty"`
	ModelErrorCount     int64  `json:"modelErrorCount,omitempty"`
}

// StateUpdatedParams is the state.updated notification (`Lhc`/`Xmc`). PROVEN
// (p1, p4, p7): reasons prompt_started (patch {status:"running"}, emitted
// BEFORE the session/send ack), prompt_completed, prompt_failed (also after a
// cancelled turn) and model_changed; the settlement patches carry the settings
// object (mode, model, permission, thoughtLevel).
type StateUpdatedParams struct {
	Type      string          `json:"type,omitempty"`
	Scope     string          `json:"scope,omitempty"`
	SessionID string          `json:"sessionId,omitempty"`
	Revision  int64           `json:"revision,omitempty"`
	Reason    string          `json:"reason,omitempty"`
	Patch     json.RawMessage `json:"patch,omitempty"`
}

// StateSettingsPatch is the part of a settlement/model_changed patch the
// driver reads: the model catalog. PROVEN (p7-models).
type StateSettingsPatch struct {
	Model *ModelSettings `json:"model,omitempty"`
}

// OperationEventParams is computer-use/operation-event (`Oai`). PROVEN (p1,
// p5): kinds turn-started/turn-completed/turn-failed carry turnId;
// tool-scheduled/tool-started carry toolCallId and toolName. Sent before the
// deliveryKind gate, so it arrives even without a subscription.
type OperationEventParams struct {
	EventID        string `json:"eventId,omitempty"`
	SequenceNumber int64  `json:"sequenceNumber,omitempty"`
	SessionID      string `json:"sessionId,omitempty"`
	Timestamp      int64  `json:"timestamp,omitempty"`
	Kind           string `json:"kind"`
	TurnID         string `json:"turnId,omitempty"`
	ToolCallID     string `json:"toolCallId,omitempty"`
	ToolName       string `json:"toolName,omitempty"`
}

// SessionEventParams is session/event (`zXt`, `dhc`). PROVEN (every p*
// fixture): {deliveryKind, eventId, payload, seq, sessionId, timestamp,
// traceId, turnId, type}. Only dispatched when the session is subscribed.
type SessionEventParams struct {
	EventID      string          `json:"eventId,omitempty"`
	SessionID    string          `json:"sessionId,omitempty"`
	TurnID       string          `json:"turnId,omitempty"`
	Seq          int64           `json:"seq,omitempty"`
	Timestamp    int64           `json:"timestamp,omitempty"`
	DeliveryKind string          `json:"deliveryKind,omitempty"`
	Type         string          `json:"type"`
	Payload      json.RawMessage `json:"payload,omitempty"`
}

// session/event type strings (`zXt` discriminated union). The first seven are
// PROVEN; userInput.resolved and session.closed are STATIC (a resolved user
// input arrives as permission.resolved decision:"modify", p6-user-input).
const (
	EventTurnStarted          = "turn.started"
	EventTurnCompleted        = "turn.completed"
	EventTurnFailed           = "turn.failed"
	EventModelStreaming       = "model.streaming"
	EventToolUpdated          = "tool.updated"
	EventPermissionRequested  = "permission.requested"
	EventPermissionResolved   = "permission.resolved"
	EventSessionTitleUpdated  = "session.titleUpdated"
	EventSessionUpdated       = "session.updated"
	EventStreamRecoveryUpdate = "streamRecovery.updated"
	EventPartDelta            = "part.delta"
	EventUserInputResolved    = "userInput.resolved"
	EventSessionClosed        = "session.closed"
)

// computer-use/operation-event kind strings (`Oai`). PROVEN except
// session-closed (STATIC).
const (
	OperationTurnStarted   = "turn-started"
	OperationTurnCompleted = "turn-completed"
	OperationTurnFailed    = "turn-failed"
	OperationToolScheduled = "tool-scheduled"
	OperationToolStarted   = "tool-started"
	OperationSessionClosed = "session-closed"
)

// state.updated reason strings (`Lhc`, `Xmc`). PROVEN.
const (
	ReasonPromptStarted   = "prompt_started"
	ReasonPromptCompleted = "prompt_completed"
	ReasonPromptFailed    = "prompt_failed"
	ReasonModelChanged    = "model_changed"
)

// TurnStartedPayload is session/event turn.started (`Ehr`). PROVEN (p1):
// inputId echoes the session/send inputId (U3), turnNumber increments across
// a resume (p8), messageId is the user message's id.
type TurnStartedPayload struct {
	InputID    string          `json:"inputId,omitempty"`
	TurnNumber int64           `json:"turnNumber,omitempty"`
	MessageID  string          `json:"messageId,omitempty"`
	Input      json.RawMessage `json:"input,omitempty"`
}

// TurnCompletedPayload is session/event turn.completed (`Dhr`). PROVEN (p1,
// p4, p5): response repeats the whole reply; resultType "success" or
// "cancelled" (after session/stop, with response "" and no usage); usage,
// tokenCount, toolCallCount, historyRoundCount, duration, inputId.
type TurnCompletedPayload struct {
	Response          string          `json:"response,omitempty"`
	ResultType        string          `json:"resultType,omitempty"`
	Usage             json.RawMessage `json:"usage,omitempty"`
	TokenCount        int64           `json:"tokenCount,omitempty"`
	ToolCallCount     int64           `json:"toolCallCount,omitempty"`
	HistoryRoundCount int64           `json:"historyRoundCount,omitempty"`
	Duration          int64           `json:"duration,omitempty"`
	InputID           string          `json:"inputId,omitempty"`
}

// TurnUsage is turn.completed.usage. PROVEN (p1, p5): {source:"provider",
// modelRequestCount, inputTokens, outputTokens, totalTokens, cacheReadTokens,
// cacheWriteTokens, reasoningTokens, webFetchRequests, webSearchRequests}.
type TurnUsage struct {
	Source            string `json:"source,omitempty"`
	ModelRequestCount int64  `json:"modelRequestCount,omitempty"`
	InputTokens       int64  `json:"inputTokens"`
	OutputTokens      int64  `json:"outputTokens"`
	TotalTokens       int64  `json:"totalTokens"`
	CacheReadTokens   int64  `json:"cacheReadTokens"`
	CacheWriteTokens  int64  `json:"cacheWriteTokens,omitempty"`
	ReasoningTokens   int64  `json:"reasoningTokens,omitempty"`
}

// ErrorAttribution is turn.failed.error.attribution. PROVEN
// (p1-turn-failed-provider): a provider error carries source:"provider",
// errorPhase, exceptionKind, providerId, modelId, providerKind, transport,
// statusCode, providerErrorCode, retryable; a configuration error carries only
// retryable (HANDOFF.md run 1).
type ErrorAttribution struct {
	Source            string `json:"source,omitempty"`
	Reason            string `json:"reason,omitempty"`
	ErrorPhase        string `json:"errorPhase,omitempty"`
	ExceptionKind     string `json:"exceptionKind,omitempty"`
	ProviderID        string `json:"providerId,omitempty"`
	ModelID           string `json:"modelId,omitempty"`
	ProviderKind      string `json:"providerKind,omitempty"`
	Transport         string `json:"transport,omitempty"`
	StatusCode        int    `json:"statusCode,omitempty"`
	ProviderErrorCode string `json:"providerErrorCode,omitempty"`
	Retryable         bool   `json:"retryable,omitempty"`
}

// ProtocolErrorDetail is the error object on turn.failed (`UXt`, `D3`).
// PROVEN (p1-turn-failed-provider, p7-models): {type, code, message, detail,
// underlyingErrorMessage, attribution, stack}. Codes seen: "1113" (provider),
// "CONFIGURATION_ERROR", "provider_not_found", "invalid_model_request".
type ProtocolErrorDetail struct {
	Type                   string            `json:"type,omitempty"`
	Code                   string            `json:"code,omitempty"`
	Message                string            `json:"message,omitempty"`
	Detail                 string            `json:"detail,omitempty"`
	UnderlyingErrorMessage string            `json:"underlyingErrorMessage,omitempty"`
	Attribution            *ErrorAttribution `json:"attribution,omitempty"`
}

// TurnFailedPayload is session/event turn.failed (`Ohr`). PROVEN:
// {error, turnPhase ("model_creation" | "processing_input"), inputId}.
type TurnFailedPayload struct {
	Error     ProtocolErrorDetail `json:"error"`
	TurnPhase string              `json:"turnPhase,omitempty"`
	InputID   string              `json:"inputId,omitempty"`
}

// ModelStreamingPayload is session/event model.streaming (`khr`). PROVEN (p1,
// p5): every kind carries assistantMessageId and done; text_delta and
// reasoning_delta carry delta (reasoning has no partId); tool_input_start
// carries toolCallId and toolName; tool_input_delta carries the JSON input
// text; tool_call carries toolCallId, toolName and the parsed input object.
// partId is STATIC (never seen).
type ModelStreamingPayload struct {
	Kind               string          `json:"kind"`
	Delta              string          `json:"delta,omitempty"`
	Done               bool            `json:"done,omitempty"`
	AssistantMessageID string          `json:"assistantMessageId,omitempty"`
	PartID             string          `json:"partId,omitempty"`
	ToolCallID         string          `json:"toolCallId,omitempty"`
	ToolName           string          `json:"toolName,omitempty"`
	Input              json.RawMessage `json:"input,omitempty"`
}

// model.streaming kind strings (`khr`). PROVEN except reasoning_start, which
// was never observed: reasoning arrives as a bare reasoning_delta stream.
const (
	StreamingTextDelta      = "text_delta"
	StreamingReasoningStart = "reasoning_start"
	StreamingReasoningDelta = "reasoning_delta"
	StreamingToolInputStart = "tool_input_start"
	StreamingToolInputDelta = "tool_input_delta"
	StreamingToolInputEnd   = "tool_input_end"
	StreamingToolCall       = "tool_call"
)

// PartDeltaPayload is session/event part.delta (`Lhr`). STATIC: never seen on
// the wire (model.streaming text_delta is the text source, U2); kept so a
// build that emits it does not double the text.
type PartDeltaPayload struct {
	MessageID string `json:"messageId,omitempty"`
	PartID    string `json:"partId,omitempty"`
	Field     string `json:"field,omitempty"`
	Delta     string `json:"delta,omitempty"`
}

// ToolResultPayload is tool.updated{kind:"result"}.result. PROVEN (p5, p6):
// {success, content, perf{totalMs, permissionWaitMs, detail{kind,
// command{exitCode, ...}}}, truncated, originalBytes, returnedBytes}.
type ToolResultPayload struct {
	Success *bool           `json:"success,omitempty"`
	Content string          `json:"content,omitempty"`
	Perf    json.RawMessage `json:"perf,omitempty"`
}

// ToolUpdatedPayload is session/event tool.updated (`jhr`), kind-discriminated
// with the optional fields flattened. PROVEN kinds (p5, p6): scheduled
// {toolCallId, toolName, assistantMessageId, inputOmitted:true,
// inputRef:"model_stream"} — the input is NOT on this frame, it arrives on
// model.streaming tool_call; started {toolCallId, toolName, readOnly,
// sideEffectScope}; result {toolCallId, result, duration} — no toolName;
// batch {toolCallIds, successCount, errorCount} — no toolCallId. progress and
// error are STATIC.
type ToolUpdatedPayload struct {
	Kind          string          `json:"kind"`
	ToolCallID    string          `json:"toolCallId,omitempty"`
	ToolCallIDs   []string        `json:"toolCallIds,omitempty"`
	ToolName      string          `json:"toolName,omitempty"`
	Input         json.RawMessage `json:"input,omitempty"`
	InputOmitted  bool            `json:"inputOmitted,omitempty"`
	OutputPreview string          `json:"outputPreview,omitempty"`
	StdoutTail    string          `json:"stdoutTail,omitempty"`
	StderrTail    string          `json:"stderrTail,omitempty"`
	Result        json.RawMessage `json:"result,omitempty"`
	Error         json.RawMessage `json:"error,omitempty"`
	Duration      int64           `json:"duration,omitempty"`
	SuccessCount  int64           `json:"successCount,omitempty"`
	ErrorCount    int64           `json:"errorCount,omitempty"`
}

// tool.updated kind strings (`jhr`). scheduled, started, result and batch are
// PROVEN; progress and error are STATIC.
const (
	ToolScheduled = "scheduled"
	ToolStarted   = "started"
	ToolProgress  = "progress"
	ToolResult    = "result"
	ToolError     = "error"
	ToolBatch     = "batch"
)

// PermissionRequestedPayload is session/event permission.requested (`Bhr`).
// PROVEN (p5, p6): it mirrors the interaction/requestPermission params
// (requestId, toolCallId, toolName, riskLevel, reason, input, options) and is
// also emitted for an AskUserQuestion user-input request.
type PermissionRequestedPayload struct {
	RequestID  string `json:"requestId,omitempty"`
	ToolCallID string `json:"toolCallId"`
	ToolName   string `json:"toolName,omitempty"`
}

// PermissionResolvedPayload is session/event permission.resolved (`Uhr`:
// `requestId: yn.optional(), toolCallId: yn`). PROVEN (p5, p6): both ids were
// present on every observed frame; decision is "allow"/"deny" for a
// permission reply and "modify" (with modifiedInput.answers) for an accepted
// user-input reply.
type PermissionResolvedPayload struct {
	RequestID     string          `json:"requestId,omitempty"`
	ToolCallID    string          `json:"toolCallId"`
	ToolName      string          `json:"toolName,omitempty"`
	Decision      string          `json:"decision,omitempty"`
	Reason        string          `json:"reason,omitempty"`
	ModifiedInput json.RawMessage `json:"modifiedInput,omitempty"`
}

// UserInputResolvedPayload is session/event userInput.resolved (`$hr`).
// STATIC: never observed; the server resolved the user-input request through
// permission.resolved instead (p6-user-input).
type UserInputResolvedPayload struct {
	RequestID string `json:"requestId,omitempty"`
	Action    string `json:"action,omitempty"`
}

// TitleUpdatedPayload is session/event session.titleUpdated (`Chr`). PROVEN
// (p1): {title, previousTitle, source:"first_input"} — the first input text
// becomes the title even with titleGenerationEnabled:false (no model call).
type TitleUpdatedPayload struct {
	Title         string `json:"title"`
	PreviousTitle string `json:"previousTitle,omitempty"`
	Source        string `json:"source,omitempty"`
}

// RequestRuntimePreferencesParams is the server request raised inside
// session/create (`KYi`). PROVEN (p1): {sessionId, scope:"runtime-materialization"};
// params.sessionId equals result.session.sessionId and is the id source.
type RequestRuntimePreferencesParams struct {
	SessionID string `json:"sessionId"`
	Scope     string `json:"scope,omitempty"`
}

// RequestRuntimePreferencesResult is the reply (`XXt`, strict). PROVEN (every
// fixture): both keys are sent; askUserQuestionAutoResolutionEnabled:false
// keeps the server from self-answering AskUserQuestion (an unanswered request
// stayed pending for 400 s, HANDOFF.md run 5 addenda).
type RequestRuntimePreferencesResult struct {
	NativeSearchEnhancementsEnabled      bool `json:"nativeSearchEnhancementsEnabled"`
	AskUserQuestionAutoResolutionEnabled bool `json:"askUserQuestionAutoResolutionEnabled"`
}

// PermissionOption is one entry of RequestPermissionParams.Options (`fXi`).
// PROVEN (p5): {kind:"allow_once"|"allow_always"|"deny", optionId, name,
// description?, response}. Response is echoed verbatim as the reply result.
type PermissionOption struct {
	OptionID    string          `json:"optionId"`
	Kind        string          `json:"kind,omitempty"`
	Name        string          `json:"name,omitempty"`
	Description string          `json:"description,omitempty"`
	Response    json.RawMessage `json:"response"`
}

// RequestPermissionParams is interaction/requestPermission (`fXi`). PROVEN
// (p5-approval-allow): {requestId:"perm_…", sessionId, turnId, toolCallId,
// toolName, riskLevel:"high", reason, input:{command, description}, options}.
// The server re-announces an unanswered request under fresh server-N ids at
// +1 s and +3 s, then every 10 s; a reply to any id resolves it and replies to
// the others are ignored (p5-approval-deny-every-id).
type RequestPermissionParams struct {
	RequestID  string             `json:"requestId"`
	SessionID  string             `json:"sessionId,omitempty"`
	TurnID     string             `json:"turnId,omitempty"`
	ToolCallID string             `json:"toolCallId"`
	ToolName   string             `json:"toolName,omitempty"`
	Reason     string             `json:"reason,omitempty"`
	RiskLevel  string             `json:"riskLevel,omitempty"`
	Input      json.RawMessage    `json:"input,omitempty"`
	Options    []PermissionOption `json:"options"`
}

// PermissionResponse is the shape of PermissionOption.Response (`_F`, strict).
// PROVEN (p5): {decision:"allow"|"deny", reason, permissionUpdates?:[{type:
// "addRules", behavior, rules:[{toolName, ruleContent}]}]}. AO reads it to
// classify the decision; it never rebuilds it.
type PermissionResponse struct {
	Decision          string          `json:"decision"`
	Reason            string          `json:"reason,omitempty"`
	ModifiedInput     json.RawMessage `json:"modifiedInput,omitempty"`
	PermissionUpdates json.RawMessage `json:"permissionUpdates,omitempty"`
}

// QuestionOption is one choice of a Question (`mXi`). PROVEN (p6): {label,
// value, description}.
type QuestionOption struct {
	Value       string `json:"value"`
	Label       string `json:"label,omitempty"`
	Description string `json:"description,omitempty"`
}

// Question is one entry of RequestUserInputParams.Questions (`mXi`). PROVEN
// (p6): {question, header, multiSelect, options}.
type Question struct {
	Question    string           `json:"question"`
	Header      string           `json:"header,omitempty"`
	Options     []QuestionOption `json:"options,omitempty"`
	MultiSelect bool             `json:"multiSelect,omitempty"`
}

// RequestUserInputParams is interaction/requestUserInput (`mXi`). PROVEN
// (p6-user-input): {requestId:"perm_…", sessionId, turnId, toolCallId,
// toolName:"AskUserQuestion", prompt, schema:{toolName}, questions, input}.
type RequestUserInputParams struct {
	RequestID  string          `json:"requestId"`
	SessionID  string          `json:"sessionId,omitempty"`
	TurnID     string          `json:"turnId,omitempty"`
	ToolCallID string          `json:"toolCallId,omitempty"`
	ToolName   string          `json:"toolName,omitempty"`
	Kind       string          `json:"kind,omitempty"`
	Prompt     string          `json:"prompt,omitempty"`
	Questions  []Question      `json:"questions,omitempty"`
	Schema     json.RawMessage `json:"schema,omitempty"`
}

// UserInputResponse is the reply to interaction/requestUserInput (`oDe`,
// strict). PROVEN (p6): {action:"accept", content:{answers:{"<question
// text>": "<value>"}}} was accepted and the turn continued with the answer.
// cancel is STATIC.
type UserInputResponse struct {
	Action  string         `json:"action"`
	Content map[string]any `json:"content,omitempty"`
	Reason  string         `json:"reason,omitempty"`
}

// RequestProviderRuntimeHeadersParams is interaction/requestProviderRuntimeHeaders
// (`hXi`). STATIC: never sent on an API-key provider (U0 closed, HANDOFF.md
// run 3); the OAuth account provider would need a host role AO does not play.
type RequestProviderRuntimeHeadersParams struct {
	SessionID     string          `json:"sessionId,omitempty"`
	ProviderID    string          `json:"providerId,omitempty"`
	Reason        string          `json:"reason,omitempty"`
	AccountAccess json.RawMessage `json:"accountAccess,omitempty"`
}

// RequestProviderRuntimeHeadersResult is the reply shape read off `Coi`. STATIC.
type RequestProviderRuntimeHeadersResult struct {
	HeadersApplied bool            `json:"headersApplied"`
	RequestAuth    json.RawMessage `json:"requestAuth,omitempty"`
}

// RequestOfficialMcpAuthHeadersParams is interaction/requestOfficialMcpAuthHeaders
// (`_Xi`). PROVEN (p1): {mcpKey, pluginId, requestId:"official-mcp-auth:<n>",
// targetOrigin, workspace}; ~90 arrive after create, one per official plugin
// MCP, independent of any turn. AO declines each with -32001 and no error
// frame follows. The params are kept for logging only.
type RequestOfficialMcpAuthHeadersParams struct {
	SessionID    string `json:"sessionId,omitempty"`
	MCPKey       string `json:"mcpKey,omitempty"`
	PluginID     string `json:"pluginId,omitempty"`
	RequestID    string `json:"requestId,omitempty"`
	TargetOrigin string `json:"targetOrigin,omitempty"`
}

// SubagentRuntimeConfigResult answers subagents/readRuntimeConfig (`Net`), which
// is never sent unless the client enabled it. STATIC.
type SubagentRuntimeConfigResult struct {
	Kind string `json:"kind"`
}

// ReasoningLevel is one entry of AvailableModel.Reasoning.Levels. PROVEN
// (p1, p7): {value, label}.
type ReasoningLevel struct {
	Value string `json:"value"`
	Label string `json:"label,omitempty"`
}

// ModelReasoning is the reasoning block of an AvailableModel (`mhr`). PROVEN
// (p1, p7): {levels:[{value,label}…], defaultLevel}.
type ModelReasoning struct {
	Levels       []ReasoningLevel `json:"levels,omitempty"`
	DefaultLevel string           `json:"defaultLevel,omitempty"`
}

// AvailableModel is one entry of Snapshot.Settings.Model.Available (`mhr`).
// PROVEN (p1, p7): {ref, label, providerLabel, contextWindow, maxOutputTokens,
// reasoning, properties}.
type AvailableModel struct {
	Ref             ModelSelection  `json:"ref"`
	Label           string          `json:"label,omitempty"`
	ProviderLabel   string          `json:"providerLabel,omitempty"`
	ContextWindow   int64           `json:"contextWindow,omitempty"`
	MaxOutputTokens int64           `json:"maxOutputTokens,omitempty"`
	Reasoning       *ModelReasoning `json:"reasoning,omitempty"`
}

// ModelSettings is Snapshot.Settings.Model (`mhr`). PROVEN (p1, p7):
// {available, current (with options.reasoningLevel), lastUsed}.
type ModelSettings struct {
	Current   *ModelSelection  `json:"current,omitempty"`
	Available []AvailableModel `json:"available,omitempty"`
	LastUsed  *ModelSelection  `json:"lastUsed,omitempty"`
}

// SnapshotSession is Snapshot.Session (`JQe`/`rDe`). PROVEN (p1): {createdAt,
// mode, model, traceId, sessionId, sessionKind, status, target, title,
// updatedAt, workspace}. The key is sessionId; there is no `id`.
type SnapshotSession struct {
	SessionID   string          `json:"sessionId"`
	Mode        string          `json:"mode,omitempty"`
	Status      string          `json:"status,omitempty"`
	Model       *ModelSelection `json:"model,omitempty"`
	SessionKind string          `json:"sessionKind,omitempty"`
	Title       string          `json:"title,omitempty"`
}

// SnapshotSettings is Snapshot.Settings. PROVEN (p1): {mode:{current},
// model, permission:{mode}, thoughtLevel:{available:[{label,value}], current,
// enabled}}.
type SnapshotSettings struct {
	Model        ModelSettings   `json:"model"`
	ThoughtLevel json.RawMessage `json:"thoughtLevel,omitempty"`
	Mode         json.RawMessage `json:"mode,omitempty"`
	Permission   json.RawMessage `json:"permission,omitempty"`
}

// SnapshotProjection is Snapshot.Projection (`Shr`). PROVEN (p1, p9):
// {activeToolCalls:[], backgroundJobs:[], contextUsed, contextWindow,
// currentTurnId?, mode, pendingPermissions:[], sessionId, status:"idle",
// totalTokenCount, turnCount}. currentTurnId stays set after the turn
// settles, so idleness is read from status.
type SnapshotProjection struct {
	Status             string            `json:"status,omitempty"`
	ContextUsed        int64             `json:"contextUsed,omitempty"`
	ContextWindow      int64             `json:"contextWindow,omitempty"`
	CurrentTurnID      string            `json:"currentTurnId,omitempty"`
	TurnCount          int64             `json:"turnCount,omitempty"`
	PendingPermissions []json.RawMessage `json:"pendingPermissions,omitempty"`
	ActiveToolCalls    []json.RawMessage `json:"activeToolCalls,omitempty"`
	BackgroundJobs     []json.RawMessage `json:"backgroundJobs,omitempty"`
}

// ContextUsage is Snapshot.Runtime.ContextUsage. PROVEN (p9): {used, size,
// cache{…}, cost}.
type ContextUsage struct {
	Used int64 `json:"used,omitempty"`
	Size int64 `json:"size,omitempty"`
}

// SnapshotRuntime is Snapshot.Runtime (`xZt`). PROVEN (p1, p9): {eventSeq,
// pendingRequestIds, stateRevision, deliveryKind (after subscribe),
// contextUsage (after a turn)}. activeTurnId is STATIC: never present on any
// captured snapshot, all of which were read while idle.
type SnapshotRuntime struct {
	EventSeq          int64         `json:"eventSeq,omitempty"`
	StateRevision     int64         `json:"stateRevision,omitempty"`
	DeliveryKind      string        `json:"deliveryKind,omitempty"`
	ActiveTurnID      string        `json:"activeTurnId,omitempty"`
	PendingRequestIDs []string      `json:"pendingRequestIds,omitempty"`
	ContextUsage      *ContextUsage `json:"contextUsage,omitempty"`
}

// MessageSemantics is Message.Info.Semantics. PROVEN (p9): origin
// ("real_user" | "agent_runtime"), kind ("user_prompt" | "system_reminder" |
// "assistant_response"), uiVisibility/providerVisibility/transcriptVisibility
// ("visible" | "hidden"). Hidden messages are runtime reminders the user
// never wrote and must not be imported as history.
type MessageSemantics struct {
	Origin               string `json:"origin,omitempty"`
	Kind                 string `json:"kind,omitempty"`
	UIVisibility         string `json:"uiVisibility,omitempty"`
	ProviderVisibility   string `json:"providerVisibility,omitempty"`
	TranscriptVisibility string `json:"transcriptVisibility,omitempty"`
}

// MessageTime is Message.Info.Time. PROVEN (p9): {created: unix ms}.
type MessageTime struct {
	Created int64 `json:"created,omitempty"`
}

// MessageInfo is Message.Info (`ZQe`). PROVEN (p8, p9): {agent, messageId,
// model, role, semantics, sessionId, time, tools, metadata?}.
type MessageInfo struct {
	Role      string           `json:"role,omitempty"`
	MessageID string           `json:"messageId,omitempty"`
	Semantics MessageSemantics `json:"semantics"`
	Time      MessageTime      `json:"time"`
	Model     *ModelSelection  `json:"model,omitempty"`
}

// Part is one entry of Message.Parts (`KQe`). PROVEN (p8, p9): {messageId,
// partId, sessionId, type, text?}; types seen are "text", "reasoning",
// "step-start" and "step-finish" (the last carries tokens). Tool parts are
// STATIC: no captured session/read followed a tool call.
type Part struct {
	PartID    string          `json:"partId,omitempty"`
	MessageID string          `json:"messageId,omitempty"`
	Type      string          `json:"type,omitempty"`
	Text      string          `json:"text,omitempty"`
	Metadata  json.RawMessage `json:"metadata,omitempty"`
}

// Part type strings. PROVEN except PartTool (STATIC).
const (
	PartText       = "text"
	PartReasoning  = "reasoning"
	PartStepStart  = "step-start"
	PartStepFinish = "step-finish"
	PartTool       = "tool"
)

// Message is one entry of Snapshot.Messages (`NLn`). PROVEN (p8, p9).
type Message struct {
	Info  MessageInfo `json:"info"`
	Parts []Part      `json:"parts,omitempty"`
}

// Snapshot is the full session snapshot returned by session/create,
// session/resume, session/read and session/setModel (`JQe`). PROVEN (p1, p7,
// p8, p9): keys messages, projection, protocol, runtime, session, settings,
// slashCommands, todos, todoGroups; the last three are not modelled.
type Snapshot struct {
	Protocol   json.RawMessage    `json:"protocol,omitempty"`
	Session    SnapshotSession    `json:"session"`
	Settings   SnapshotSettings   `json:"settings"`
	Projection SnapshotProjection `json:"projection"`
	Runtime    SnapshotRuntime    `json:"runtime"`
	Messages   []Message          `json:"messages,omitempty"`
}

// Projection status strings. PROVEN: "idle" on every captured snapshot;
// "running" is STATIC (`Shr`).
const (
	ProjectionIdle    = "idle"
	ProjectionRunning = "running"
)
