package ports

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

var (
	ErrChatUnsupported               = errors.New("chat mode unsupported for harness")
	ErrChatDriverUnavailable         = errors.New("chat driver unavailable")
	ErrChatDriverIncompatible        = errors.New("chat driver incompatible")
	ErrChatAuthRequired              = errors.New("chat driver requires authentication")
	ErrChatResumeFailed              = errors.New("chat conversation resume failed")
	ErrChatRecoveryInconclusive      = errors.New("chat conversation recovery is inconclusive")
	ErrChatHostNotRunning            = errors.New("chat provider host is not running")
	ErrChatProviderSessionLost       = errors.New("chat provider no longer holds the conversation's session")
	ErrChatNoActiveTurn              = errors.New("no active turn to interrupt")
	ErrChatRequestNotPending         = errors.New("chat request is not pending")
	ErrChatDecisionNotOffered        = errors.New("chat decision was not offered for this request")
	ErrChatConfigOptionInvalid       = errors.New("chat config option value is invalid")
	ErrChatPermissionModeUnsupported = errors.New("chat permission mode is unsupported")
	ErrChatHistoryUnsettled          = errors.New("chat conversation history is not settled")
	ErrChatHistoryUnavailable        = errors.New("chat conversation history replay is unavailable")
	ErrChatCapabilityUnavailable     = errors.New("chat model capability unavailable")
	ErrChatHistoryLoadFailed         = errors.New("chat conversation history load failed")

	ErrChatPromptStalled = errors.New("chat provider stopped responding")
)

type ChatHistoryMismatchDimension string

const (
	ChatHistoryMismatchUntrustedUserText      ChatHistoryMismatchDimension = "untrusted_user_text"
	ChatHistoryMismatchUntrustedAssistantText ChatHistoryMismatchDimension = "untrusted_assistant_text"
	ChatHistoryMismatchTrustedUserText        ChatHistoryMismatchDimension = "trusted_user_text"
	ChatHistoryMismatchTrustedTurn            ChatHistoryMismatchDimension = "trusted_turn_boundary"
	ChatHistoryMismatchTrustedAssistantText   ChatHistoryMismatchDimension = "trusted_assistant_text"
	ChatHistoryMismatchNativeIdentity         ChatHistoryMismatchDimension = "native_identity"
	ChatHistoryMismatchAOHighWater            ChatHistoryMismatchDimension = "ao_high_water"
	ChatHistoryMismatchUnsettledBoundary      ChatHistoryMismatchDimension = "unsettled_turn_boundary"
)

type ChatHistoryUnsettledError struct {
	Dimensions []ChatHistoryMismatchDimension
}

func (e *ChatHistoryUnsettledError) Error() string {
	return fmt.Sprintf("%s: checkpoint mismatches %v", ErrChatHistoryUnsettled, e.Dimensions)
}

func (e *ChatHistoryUnsettledError) Unwrap() error { return ErrChatHistoryUnsettled }

func ChatHistoryMismatchDimensions(err error) []ChatHistoryMismatchDimension {
	var mismatch *ChatHistoryUnsettledError
	if !errors.As(err, &mismatch) {
		return nil
	}
	return append([]ChatHistoryMismatchDimension(nil), mismatch.Dimensions...)
}

func ChatHistoryMismatchOnlyUntrustedText(err error) bool {
	dimensions := ChatHistoryMismatchDimensions(err)
	if len(dimensions) == 0 {
		return false
	}
	for _, dimension := range dimensions {
		if dimension != ChatHistoryMismatchUntrustedUserText &&
			dimension != ChatHistoryMismatchUntrustedAssistantText {
			return false
		}
	}
	return true
}

type ChatCapabilityError struct {
	Harness                domain.AgentHarness
	Missing                []ChatCapability
	AllowedPermissionModes []PermissionMode
}

func (e *ChatCapabilityError) Error() string {
	return fmt.Sprintf("%s: %s lacks %v", ErrChatUnsupported, e.Harness, e.Missing)
}

func (e *ChatCapabilityError) Unwrap() error { return ErrChatUnsupported }

type ChatCapability string

const (
	ChatCapabilityStreaming       ChatCapability = "streaming"
	ChatCapabilityTools           ChatCapability = "tools"
	ChatCapabilityApprovals       ChatCapability = "approvals"
	ChatCapabilityInterrupt       ChatCapability = "interrupt"
	ChatCapabilitySteer           ChatCapability = "steer"
	ChatCapabilityResume          ChatCapability = "resume"
	ChatCapabilityHistory         ChatCapability = "history"
	ChatCapabilityUsage           ChatCapability = "usage"
	ChatCapabilityDiffs           ChatCapability = "diffs"
	ChatCapabilityPlans           ChatCapability = "plans"
	ChatCapabilityPlanMode        ChatCapability = "plan_mode"
	ChatCapabilityModels          ChatCapability = "models"
	ChatCapabilityConfigOptions   ChatCapability = "config_options"
	ChatCapabilityCompaction      ChatCapability = "compaction"
	ChatCapabilityRollback        ChatCapability = "rollback"
	ChatCapabilityFork            ChatCapability = "fork"
	ChatCapabilityForkCurrent     ChatCapability = "fork_current"
	ChatCapabilityPromptReplay    ChatCapability = "prompt_replay"
	ChatCapabilityRename          ChatCapability = "rename"
	ChatCapabilitySkills          ChatCapability = "skills"
	ChatCapabilityRateLimits      ChatCapability = "rate_limits"
	ChatCapabilityInteractive     ChatCapability = "user_input"
	ChatCapabilityMCPReload       ChatCapability = "mcp_reload"
	ChatCapabilityImages          ChatCapability = "images"
	ChatCapabilityEmbeddedContext ChatCapability = "embedded_context"
	ChatCapabilityResourceLinks   ChatCapability = "resource_links"
	ChatCapabilityElicitation     ChatCapability = "elicitation"
	ChatCapabilityNestedAgents    ChatCapability = "nested_agents"
	ChatCapabilityTerminalOutput  ChatCapability = "terminal_output"
)

type ChatCapabilities map[ChatCapability]bool

func (c ChatCapabilities) Has(capability ChatCapability) bool { return c[capability] }

var chatProductionFloor = []ChatCapability{
	ChatCapabilityStreaming,
	ChatCapabilityApprovals,
	ChatCapabilityInterrupt,
	ChatCapabilityResume,
}

func MissingProductionCapabilities(caps ChatCapabilities) []ChatCapability {
	var missing []ChatCapability
	for _, want := range chatProductionFloor {
		if !caps.Has(want) {
			missing = append(missing, want)
		}
	}
	return missing
}

func MissingCapabilitiesForPermissions(caps ChatCapabilities, permissions PermissionMode) []ChatCapability {
	missing := MissingProductionCapabilities(caps)
	if NormalizePermissionMode(permissions) != PermissionModeBypassPermissions {
		return missing
	}
	out := missing[:0]
	for _, capability := range missing {
		if capability != ChatCapabilityApprovals {
			out = append(out, capability)
		}
	}
	return out
}

var PermissionModesStrictestFirst = []PermissionMode{
	PermissionModeDefault, PermissionModeAcceptEdits, PermissionModeAuto, PermissionModeBypassPermissions,
}

type ChatStartConfig struct {
	SessionID             domain.SessionID
	DataDir               string
	WorkspacePath         string
	Env                   map[string]string
	PrepareEnv            func(context.Context) (map[string]string, error)
	AcquireForkSource     func(context.Context) (copyCtx context.Context, release func(), err error)
	RecordFork            func(context.Context, string) error
	Model                 string
	Effort                string
	Permissions           PermissionMode
	ReadOnly              bool
	DisableNetwork        bool
	DisableTools          bool
	SystemPrompt          string
	ProviderScopeID       string
	ProviderIDsScoped     bool
	Ephemeral             bool
	AdditionalDirectories []string
	MCPServers            []ChatMCPServerConfig
}

type ChatResumeConfig struct {
	ReconnectOnly          bool
	ProviderIDsScoped      bool
	SessionID              domain.SessionID
	ProviderConversationID string
	FreshIfMissing         bool
	DataDir                string
	WorkspacePath          string
	Env                    map[string]string
	PrepareEnv             func(context.Context) (map[string]string, error)
	Model                  string
	Effort                 string
	Permissions            PermissionMode
	ReadOnly               bool
	SystemPrompt           string
	HasPriorTurns          bool
	ProviderScopeID        string
	AdditionalDirectories  []string
	MCPServers             []ChatMCPServerConfig
}

type ChatMCPServerConfig struct {
	Name    string
	Type    string
	Command string
	Args    []string
	Env     map[string]string
	URL     string
	Headers map[string]string
}

const ChatInternalReplayResourceURI = "ao://conversation/edit-replay"

const ChatExcerptResourceURIPrefix = "ao://conversation-excerpt/"

type ChatExcerptReference struct {
	Snapshot       *domain.ChatExcerptSnapshot `json:"snapshot,omitempty"`
	ID             string                      `json:"id,omitempty"`
	Order          int64                       `json:"order,omitempty"`
	ConversationID string                      `json:"conversationId"`
	MessageID      string                      `json:"messageId"`
	Revision       int64                       `json:"revision"`
	Text           string                      `json:"text"`
	Comment        string                      `json:"comment,omitempty"`
}

type ChatExcerptContext struct {
	Reference        ChatExcerptReference `json:"reference"`
	SelectedText     string               `json:"selectedText"`
	UserMessage      string               `json:"userMessage"`
	AssistantMessage string               `json:"assistantMessage"`
}

type ChatContent struct {
	Type          string              `json:"type"`
	Data          string              `json:"data,omitempty"`
	MIMEType      string              `json:"mimeType,omitempty"`
	URI           string              `json:"uri,omitempty"`
	Name          string              `json:"name,omitempty"`
	Text          string              `json:"text,omitempty"`
	Excerpt       *ChatExcerptContext `json:"excerpt,omitempty"`
	FileReference *ChatFileReference  `json:"fileReference,omitempty"`
	Internal      bool                `json:"internal,omitempty"`
}

func IsInternalReplayContent(content ChatContent) bool {
	return content.Internal && content.Type == "resource" && content.URI == ChatInternalReplayResourceURI
}

type ChatUserMessage struct {
	InteractionAt      time.Time
	Text               string
	SpawnOpeningPrompt bool
	SenderSessionID    string
	SenderProjectID    string
	SenderDisplayName  string
	Content            []ChatContent
	ClientMessageID    string
	ClientPayloadHash  string
	Excerpts           []ChatExcerptReference
	Origin             domain.MessageOrigin
	AuthoredByUser     bool
	Settings           ChatTurnSettings
	Continuation       bool
}

type MessageDeliveryOptions struct {
	InteractionAt   time.Time
	SenderSessionID string
	AuthoredByUser  bool
}

type ChatTurnSettings struct {
	Model    string
	Effort   string
	Approval PermissionMode
	Fast     bool
	Plan     bool
}

func (s ChatTurnSettings) IsZero() bool {
	return s.Model == "" && s.Effort == "" && s.Approval == ""
}

type ChatModel struct {
	Inputs        []string
	ID            string
	DisplayName   string
	Description   string
	Default       bool
	Efforts       []string
	DefaultEffort string
	FastTier      string
}

type ChatModelLister interface {
	ListModels(ctx context.Context) ([]ChatModel, error)
}

type ChatConfigOptionType string

const (
	ChatConfigOptionSelect  ChatConfigOptionType = "select"
	ChatConfigOptionBoolean ChatConfigOptionType = "boolean"
)

type ChatConfigOptionValue struct {
	Select  string
	Boolean *bool
}

type ChatConfigOptionChoice struct {
	PermissionMode PermissionMode
	Value          string
	Name           string
	Description    string
	Group          string
	GroupName      string
}

type ChatConfigOption struct {
	ID          string
	Name        string
	Description string
	Category    string
	Type        ChatConfigOptionType
	Current     ChatConfigOptionValue
	Choices     []ChatConfigOptionChoice
}

type ChatConfigOptionController interface {
	ListConfigOptions(ctx context.Context) ([]ChatConfigOption, error)
	SetConfigOption(ctx context.Context, id string, value ChatConfigOptionValue) ([]ChatConfigOption, error)
}

type ChatUsage struct {
	InputTokens   int64
	OutputTokens  int64
	CachedTokens  int64
	TotalTokens   int64
	ContextUsed   int64
	ContextWindow int64
	ContextKnown  bool
	TotalsKnown   bool
	Cost          *float64
	Currency      string
}

type ChatRateLimits struct {
	PrimaryUsedPercent       float64
	SecondaryUsedPercent     float64
	PrimaryResetsInSeconds   int64
	SecondaryResetsInSeconds int64
	PlanLabel                string
	CodexCapacity            *CodexCapacityObservation
}

type ChatTurnDiff struct {
	Files []ChatDiffFile
}

type ChatDiffFile struct {
	Path      string
	Additions int
	Deletions int
	Status    string
	OldPath   string
	Patch     string
}

type ChatModelReroute struct {
	FromModel string
	ToModel   string
	Reason    string
}

type ChatAccount struct {
	AuthMode        string
	PlanLabel       string
	ReauthRequired  bool
	ReauthRecovered bool
	ReauthReason    string
}

type ChatThreadState struct {
	Status    domain.ThreadStatus
	WaitingOn []string
	Archived  *bool
	Closed    bool
}

type ChatMCPServer struct {
	Name          string
	Status        string
	Error         string
	FailureReason string
}

type ChatMCPReloadResult struct {
	Servers       []ChatMCPServer
	Authoritative bool
}

type ChatSkill struct {
	Name        string
	DisplayName string
	Description string
	InputHint   string
	Source      string
}

type ChatCompactionResult struct {
	TokensBefore int64
	TokensAfter  int64
}

type (
	ChatCompactor interface {
		Compact(ctx context.Context) (ChatCompactionResult, error)
	}
	ChatRollbacker interface {
		Rollback(ctx context.Context, providerTurnID string) error
	}
	ChatForker interface {
		Fork(ctx context.Context, lastProviderTurnID *string) (providerConversationID string, err error)
	}
	ChatIsolatedForker interface {
		ForkIntoHost(ctx context.Context, sourceProviderConversationID, lastProviderTurnID string, cfg ChatStartConfig) (ChatConversation, error)
	}
	ChatWorkspaceForker interface {
		ForkIntoWorkspace(ctx context.Context, sourceProviderConversationID, sourceWorkspace, upToMessageID string, cfg ChatStartConfig) (providerConversationID string, err error)
	}
	ChatNativeTurnID interface {
		NativeTurnID(providerTurnID string) string
	}
	ChatForkDeleter interface {
		DeleteFork(ctx context.Context, providerConversationID string) error
	}
	ChatInheritedHistory interface {
		InheritedHistory(ctx context.Context, ancestor domain.ConversationBranch, events []ChatEvent) ([]ChatEvent, error)
	}
	ChatRenamer interface {
		SetTitle(ctx context.Context, title string) error
	}
	ChatSkillLister interface {
		ListSkills(ctx context.Context) ([]ChatSkill, error)
	}
	ChatSandboxNetwork interface {
		SandboxAllowsNetwork(turnMode PermissionMode) bool
	}
	ChatUsageReporter interface {
		ReadRateLimits(ctx context.Context) (ChatRateLimits, error)
	}
	ChatMCPReloader interface {
		ReloadMCPServers(ctx context.Context) (ChatMCPReloadResult, error)
	}
)

type ChatTurnRef struct {
	ProviderTurnID string
}

type ChatDeferredTurnStarter interface {
	StartDeferredTurn(providerTurnID string) error
	DiscardDeferredTurn(providerTurnID string)
}

type ChatDecisionOption struct {
	ID    string
	Label string
	Kind  ChatDecisionKind
	Raw   []byte
}

type ChatDecisionKind string

const (
	ChatDecisionAllowOnce    ChatDecisionKind = "allow_once"
	ChatDecisionAllowAlways  ChatDecisionKind = "allow_always"
	ChatDecisionRejectOnce   ChatDecisionKind = "reject_once"
	ChatDecisionRejectAlways ChatDecisionKind = "reject_always"
)

type ChatDecision struct {
	ID  string
	Raw []byte
}

type ChatInputAction string

const (
	ChatInputActionAccept  ChatInputAction = "accept"
	ChatInputActionDecline ChatInputAction = "decline"
	ChatInputActionCancel  ChatInputAction = "cancel"
)

func (a ChatInputAction) Valid() bool {
	return a == ChatInputActionAccept || a == ChatInputActionDecline || a == ChatInputActionCancel
}

type ChatInputMode string

const (
	ChatInputModeForm ChatInputMode = "form"
	ChatInputModeURL  ChatInputMode = "url"
)

func (m ChatInputMode) Valid() bool {
	return m == ChatInputModeForm || m == ChatInputModeURL
}

type ChatInputResponse struct {
	Action  ChatInputAction
	Content map[string]any
}

type ChatInputRequest struct {
	Mode          ChatInputMode
	Message       string
	Schema        map[string]any
	URL           string
	ElicitationID string
}

type ChatInputResponder interface {
	ResolveInput(ctx context.Context, requestID string, response ChatInputResponse) error
}

type ChatEventKind string

const (
	ChatEventTurnStarted          ChatEventKind = "turn.started"
	ChatEventTurnCompleted        ChatEventKind = "turn.completed"
	ChatEventUserMessageCompleted ChatEventKind = "message.user.completed"
	ChatEventMessageDelta         ChatEventKind = "message.delta"
	ChatEventMessageCompleted     ChatEventKind = "message.completed"
	ChatEventActivityStarted      ChatEventKind = "activity.started"
	ChatEventActivityCompleted    ChatEventKind = "activity.completed"
	ChatEventApprovalRequested    ChatEventKind = "approval.requested"
	ChatEventApprovalResolved     ChatEventKind = "approval.resolved"
	ChatEventInputRequested       ChatEventKind = "input.requested"
	ChatEventInputResolved        ChatEventKind = "input.resolved"
	ChatEventControllerState      ChatEventKind = "controller.state"
	ChatEventError                ChatEventKind = "error"

	ChatEventCommandOutputDelta ChatEventKind = "command.output.delta"
	ChatEventTurnDiff           ChatEventKind = "turn.diff"
	ChatEventCompacted          ChatEventKind = "thread.compacted"
	ChatEventUsage              ChatEventKind = "usage"
	ChatEventRateLimits         ChatEventKind = "account.rateLimits"
	ChatEventThreadRenamed      ChatEventKind = "thread.renamed"
	ChatEventPlanUpdated        ChatEventKind = "turn.plan"

	ChatEventReasoningDelta ChatEventKind = "reasoning.delta"
	ChatEventCommandInput   ChatEventKind = "command.input"
	ChatEventActivityText   ChatEventKind = "activity.text"
	ChatEventModelRerouted  ChatEventKind = "model.rerouted"
	ChatEventAccountChanged ChatEventKind = "account.changed"
	ChatEventThreadState    ChatEventKind = "thread.state"
	ChatEventProviderState  ChatEventKind = "provider.state"
	ChatEventMCPServers     ChatEventKind = "mcp.servers"
)

type ChatControllerState string

const (
	ChatControllerConnecting ChatControllerState = "connecting"
	ChatControllerReady      ChatControllerState = "ready"
	ChatControllerBusy       ChatControllerState = "busy"
	ChatControllerRecovering ChatControllerState = "recovering"
	ChatControllerHibernated ChatControllerState = "hibernated"
	ChatControllerStopped    ChatControllerState = "stopped"
)

type chatProviderFailure struct {
	message string
	cause   error
}

func NewChatProviderFailure(title, detail string, cause error) error {
	title = strings.TrimSpace(title)
	detail = strings.TrimSpace(detail)
	if title == "" {
		title, detail = detail, ""
	}
	if title == "" {
		title = "Provider error"
	}
	if detail == title {
		detail = ""
	}
	if detail != "" {
		title += "\n\n" + detail
	}
	return &chatProviderFailure{message: title, cause: cause}
}

func (f *chatProviderFailure) Error() string { return f.message }
func (f *chatProviderFailure) Unwrap() error { return f.cause }

type ChatEvent struct {
	Kind                ChatEventKind
	NativeUserMessageID string
	NativeTurnID        string
	ProviderEventID     string

	ProviderTurnID         string
	ProviderConversationID string
	ProviderItemID         string
	ProviderItemAliases    []string
	ClientMessageID        string

	TurnState          domain.TurnState
	FailureCode        domain.ConversationFailureCode
	UsageLimitResetsAt time.Time

	Delta string
	Text  string

	ActivityKind   domain.ActivityKind
	ActivityStatus domain.ActivityStatus
	Summary        string
	Detail         []byte

	RequestID string
	Decisions []ChatDecisionOption
	Input     *ChatInputRequest

	ControllerState ChatControllerState

	Usage      *ChatUsage
	RateLimits *ChatRateLimits
	Diff       *ChatTurnDiff
	Title      string

	Plan        *domain.ConversationPlan
	Reroute     *ChatModelReroute
	Account     *ChatAccount
	ThreadState *ChatThreadState
	MCPServers  []ChatMCPServer

	Err error
}

type ChatInstallChecker interface {
	CheckChatInstall(ctx context.Context) error
}

type ChatPermissionModeChecker interface {
	SupportsPermissionMode(mode PermissionMode) bool
}

type ChatDriver interface {
	Harness() domain.AgentHarness
	Probe(ctx context.Context) (ChatCapabilities, error)
	Start(ctx context.Context, cfg ChatStartConfig) (ChatConversation, error)
	Resume(ctx context.Context, cfg ChatResumeConfig) (ChatConversation, error)
}

type ChatEnvironmentValidator interface {
	ValidateEnvironment(ctx context.Context, env map[string]string) error
}

type ChatDriverReconnector interface {
	Reconnect(context.Context, ChatResumeConfig) (ChatConversation, error)
}

type ChatConversation interface {
	ProviderConversationID() string
	Capabilities() ChatCapabilities
	SendTurn(ctx context.Context, msg ChatUserMessage) (ChatTurnRef, error)
	Interrupt(ctx context.Context, providerTurnID string) error
	ResolveRequest(ctx context.Context, requestID string, decision ChatDecision) error
	Events() <-chan ChatEvent
	Close() error
}

type ChatProviderPreserver interface {
	PreservesProviderOnClose() bool
}

type ChatProviderTerminator interface {
	Terminate() error
}

type ChatProviderHibernator interface {
	CanHibernate(ctx context.Context) (bool, error)
	Hibernate() error
}

type ChatLiveReconnector interface {
	ReconnectedLive() bool
}

type ChatLiveReconnectActivator interface {
	ActivateLiveReconnect(ctx context.Context, providerTurnID string) error
}

type ChatProviderEventAcknowledger interface {
	AcknowledgeProviderEvent(ctx context.Context, providerEventID string) error
}

type ChatHistoryReader interface {
	ReadHistory(ctx context.Context) ([]ChatEvent, error)
}

type ChatHistoryRefresher interface {
	ChatHistoryReader
	RefreshHistory(ctx context.Context) ([]ChatEvent, error)
}

type ChatDriverRegistry interface {
	Driver(harness domain.AgentHarness) (ChatDriver, error)
	SupportsChat(harness domain.AgentHarness) bool
}
