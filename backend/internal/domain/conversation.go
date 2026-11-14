package domain

import (
	"errors"
	"time"
)

type ConversationOwnerKind string

const (
	ConversationOwnerSession ConversationOwnerKind = "session"
	ConversationOwnerReview  ConversationOwnerKind = "review"
)

type ConversationOwner struct {
	Kind ConversationOwnerKind `json:"kind"`
	ID   string                `json:"id"`
}

func SessionConversationOwner(id SessionID) ConversationOwner {
	return ConversationOwner{Kind: ConversationOwnerSession, ID: string(id)}
}

func ReviewConversationOwner(id string) ConversationOwner {
	return ConversationOwner{Kind: ConversationOwnerReview, ID: id}
}

type ConversationScope string

const (
	ConversationScopeSession ConversationScope = "session"
	ConversationScopeProject ConversationScope = "project"
	ConversationScopeReview  ConversationScope = "review"
)

func ConversationContextResetProviderItemID(session SessionID) string {
	return "ao-context-reset:" + string(session)
}

type TurnState string

const (
	TurnStateQueued      TurnState = "queued"
	TurnStateRunning     TurnState = "running"
	TurnStateCompleted   TurnState = "completed"
	TurnStateRecovered   TurnState = "recovered"
	TurnStateInterrupted TurnState = "interrupted"
	TurnStateFailed      TurnState = "failed"
	TurnStateCancelled   TurnState = "cancelled"
)

func (s TurnState) Terminal() bool {
	switch s {
	case TurnStateCompleted, TurnStateRecovered, TurnStateInterrupted, TurnStateFailed, TurnStateCancelled:
		return true
	default:
		return false
	}
}

type MessageRole string

const (
	MessageRoleUser      MessageRole = "user"
	MessageRoleAssistant MessageRole = "assistant"
)

type MessageOrigin string

const (
	MessageOriginHuman      MessageOrigin = "human"
	MessageOriginAutomation MessageOrigin = "automation"
	MessageOriginDaemon     MessageOrigin = "daemon"
	MessageOriginProvider   MessageOrigin = "provider"
)

type ActivityKind string

const (
	ActivityKindCommand    ActivityKind = "command"
	ActivityKindFileChange ActivityKind = "file_change"
	ActivityKindPlan       ActivityKind = "plan"
	ActivityKindReasoning  ActivityKind = "reasoning"
	ActivityKindApproval   ActivityKind = "approval"
	ActivityKindUsage      ActivityKind = "usage"
	ActivityKindError      ActivityKind = "error"
	ActivityKindSystem     ActivityKind = "system"
	ActivityKindMCPTool    ActivityKind = "mcp_tool"
	ActivityKindAutoReview ActivityKind = "auto_review"
	ActivityKindUserInput  ActivityKind = "user_input"
	ActivityKindAOAction   ActivityKind = "ao_action"
)

type ActivityStatus string

const (
	ActivityStatusRunning   ActivityStatus = "running"
	ActivityStatusCompleted ActivityStatus = "completed"
	ActivityStatusRecovered ActivityStatus = "recovered"
	ActivityStatusFailed    ActivityStatus = "failed"
	ActivityStatusCancelled ActivityStatus = "cancelled"
	ActivityStatusPending   ActivityStatus = "pending"
	ActivityStatusResolved  ActivityStatus = "resolved"
)

type ConversationRecord struct {
	ID             string                    `json:"id"`
	Scope          ConversationScope         `json:"scope"`
	ProjectID      ProjectID                 `json:"projectId"`
	SessionID      SessionID                 `json:"sessionId,omitempty"`
	ReviewID       string                    `json:"reviewId,omitempty"`
	ActiveBranchID string                    `json:"activeBranchId,omitempty"`
	LatestSequence int64                     `json:"latestSequence"`
	Settings       ConversationSettings      `json:"settings"`
	Usage          *ConversationUsage        `json:"usage,omitempty"`
	RateLimits     *ConversationRateLimits   `json:"rateLimits,omitempty"`
	CompactedAt    *time.Time                `json:"compactedAt,omitempty"`
	ProviderTitle  string                    `json:"providerTitle,omitempty"`
	ModelReroute   *ConversationModelReroute `json:"modelReroute,omitempty"`
	Account        *ConversationAccount      `json:"account,omitempty"`
	ThreadState    *ConversationThreadState  `json:"threadState,omitempty"`
	MCPServers     []ConversationMCPServer   `json:"mcpServers,omitempty"`
	AppliedTitle   string                    `json:"-"`
	CreatedAt      time.Time                 `json:"createdAt"`
	UpdatedAt      time.Time                 `json:"updatedAt"`
}

type ConversationBranchStrategy string

const (
	ConversationBranchStrategyNative             ConversationBranchStrategy = "native"
	ConversationBranchStrategyApproximateContext ConversationBranchStrategy = "approximate_context"
)

func NormalizeConversationBranchStrategy(strategy ConversationBranchStrategy) ConversationBranchStrategy {
	if strategy == "" {
		return ConversationBranchStrategyNative
	}
	return strategy
}

type ConversationBranch struct {
	ID                     string                     `json:"id"`
	ConversationID         string                     `json:"conversationId"`
	SessionID              SessionID                  `json:"sessionId"`
	ReviewID               string                     `json:"reviewId,omitempty"`
	ProviderConversationID string                     `json:"-"`
	ParentBranchID         string                     `json:"parentBranchId,omitempty"`
	ForkAfterTurnID        string                     `json:"forkAfterTurnId,omitempty"`
	ReplacedTurnID         string                     `json:"replacedTurnId,omitempty"`
	ReplacementTurnID      string                     `json:"replacementTurnId,omitempty"`
	ForkAfterSequence      int64                      `json:"-"`
	Strategy               ConversationBranchStrategy `json:"strategy,omitempty"`
	ReplayCutoffSequence   int64                      `json:"-"`
	ReplayTruncated        bool                       `json:"replayTruncated,omitempty"`
	ProviderBindingID      string                     `json:"-"`
	ProviderScopeID        string                     `json:"-"`
	ProviderIDsScoped      bool                       `json:"-"`
	Active                 bool                       `json:"active"`
	CreatedAt              time.Time                  `json:"createdAt"`

	HandoffNote string `json:"-"`
}

type ConversationBranchPoint struct {
	TurnID           string `json:"turnId"`
	Position         int    `json:"position"`
	Total            int    `json:"total"`
	PreviousBranchID string `json:"previousBranchId,omitempty"`
	NextBranchID     string `json:"nextBranchId,omitempty"`
}

type ConversationEditAnchor struct {
	ConversationID              string
	SourceBranchID              string
	ReplacedTurnID              string
	PreviousProviderTurnID      string
	ForkAfterSequence           int64
	ReplayFloorSequence         int64
	HasPriorContext             bool
	OriginalDeliveryContentJSON string
	RetryActiveBranch           bool
}

type ConversationUsage struct {
	ContextUsed   int64    `json:"contextUsed"`
	ContextWindow int64    `json:"contextWindow"`
	InputTokens   int64    `json:"inputTokens"`
	OutputTokens  int64    `json:"outputTokens"`
	CachedTokens  int64    `json:"cachedTokens"`
	TotalTokens   int64    `json:"totalTokens"`
	Cost          *float64 `json:"cost,omitempty"`
	Currency      string   `json:"currency,omitempty"`
}

func (u ConversationUsage) ContextFraction() float64 {
	if u.ContextWindow <= 0 || u.ContextUsed < 0 {
		return -1
	}
	return float64(u.ContextUsed) / float64(u.ContextWindow)
}

type ConversationRateLimits struct {
	PrimaryUsedPercent       float64 `json:"primaryUsedPercent"`
	SecondaryUsedPercent     float64 `json:"secondaryUsedPercent"`
	PrimaryResetsInSeconds   int64   `json:"primaryResetsInSeconds,omitempty"`
	SecondaryResetsInSeconds int64   `json:"secondaryResetsInSeconds,omitempty"`
	PlanLabel                string  `json:"planLabel,omitempty"`
}

func (l ConversationRateLimits) WorstUsedPercent() float64 {
	worst := l.PrimaryUsedPercent
	if l.SecondaryUsedPercent > worst {
		worst = l.SecondaryUsedPercent
	}
	if worst < 0 {
		return -1
	}
	return worst
}

type ConversationModelReroute struct {
	FromModel      string    `json:"fromModel"`
	ToModel        string    `json:"toModel"`
	Reason         string    `json:"reason,omitempty"`
	ProviderTurnID string    `json:"providerTurnId,omitempty"`
	At             time.Time `json:"at"`
}

type ConversationAccount struct {
	AuthenticationState   string     `json:"authenticationState,omitempty"`
	AuthVerifiedAt        *time.Time `json:"authVerifiedAt,omitempty"`
	LastAuthFailureAt     *time.Time `json:"lastAuthFailureAt,omitempty"`
	LastAuthFailureReason string     `json:"lastAuthFailureReason,omitempty"`
	AuthFailureID         string     `json:"authFailureId,omitempty"`
	AuthChangedAt         *time.Time `json:"authChangedAt,omitempty"`
	AuthMode              string     `json:"authMode,omitempty"`
	PlanLabel             string     `json:"planLabel,omitempty"`
	ReauthRequiredAt      *time.Time `json:"reauthRequiredAt,omitempty"`
	ReauthReason          string     `json:"reauthReason,omitempty"`
}

func (a *ConversationAccount) RequireLogin(reason, failureID string, at time.Time) {
	a.AuthenticationState = "required"
	a.ReauthRequiredAt = &at
	a.ReauthReason = reason
	a.LastAuthFailureAt = &at
	a.LastAuthFailureReason = reason
	a.AuthFailureID = failureID
	a.AuthVerifiedAt = nil
}

type ThreadStatus string

const (
	ThreadStatusActive      ThreadStatus = "active"
	ThreadStatusIdle        ThreadStatus = "idle"
	ThreadStatusNotLoaded   ThreadStatus = "not_loaded"
	ThreadStatusSystemError ThreadStatus = "system_error"
	ThreadStatusClosed      ThreadStatus = "closed"
)

type ConversationThreadState struct {
	Status     ThreadStatus `json:"status,omitempty"`
	WaitingOn  []string     `json:"waitingOn,omitempty"`
	ArchivedAt *time.Time   `json:"archivedAt,omitempty"`
	ClosedAt   *time.Time   `json:"closedAt,omitempty"`
	UpdatedAt  time.Time    `json:"updatedAt"`
}

type ConversationMCPServer struct {
	Name          string `json:"name"`
	Status        string `json:"status"`
	Error         string `json:"error,omitempty"`
	FailureReason string `json:"failureReason,omitempty"`
}

type PlanStepStatus string

const (
	PlanStepPending    PlanStepStatus = "pending"
	PlanStepInProgress PlanStepStatus = "in_progress"
	PlanStepCompleted  PlanStepStatus = "completed"
)

type ConversationPlan struct {
	Explanation string                 `json:"explanation,omitempty"`
	Steps       []ConversationPlanStep `json:"steps"`
	SpansTurns  bool                   `json:"spansTurns,omitempty"`
	Superseded  bool                   `json:"superseded,omitempty"`
}

type ConversationPlanStep struct {
	Text   string         `json:"text"`
	Status PlanStepStatus `json:"status"`
}

type ConversationSettings struct {
	Model             string         `json:"model,omitempty"`
	ReasoningEffort   string         `json:"reasoningEffort,omitempty"`
	FastMode          bool           `json:"fastMode,omitempty"`
	PlanMode          bool           `json:"planMode,omitempty"`
	ApprovalMode      PermissionMode `json:"approvalMode,omitempty"`
	OpenCodeMode      string         `json:"openCodeMode,omitempty"`
	AutoCompactWindow string         `json:"autoCompactWindow,omitempty"`
}

type ConversationTurn struct {
	ID                 string                     `json:"id"`
	ConversationID     string                     `json:"conversationId"`
	BranchID           string                     `json:"-"`
	HandledBySessionID SessionID                  `json:"handledBySessionId"`
	HandledByReviewID  string                     `json:"handledByReviewId,omitempty"`
	ProviderTurnID     string                     `json:"providerTurnId,omitempty"`
	ProviderInputText  string                     `json:"-"`
	RetryOfTurnID      string                     `json:"retryOfTurnId,omitempty"`
	HasRetryAttempt    bool                       `json:"hasRetryAttempt,omitempty"`
	HasHumanPrompt     bool                       `json:"hasHumanPrompt,omitempty"`
	State              TurnState                  `json:"state"`
	ErrorMessage       string                     `json:"errorMessage,omitempty"`
	FailureCode        ConversationFailureCode    `json:"failureCode,omitempty"`
	UsageLimit         *ConversationUsageLimit    `json:"usageLimit,omitempty"`
	RequestedAt        time.Time                  `json:"requestedAt"`
	StartedAt          *time.Time                 `json:"startedAt,omitempty"`
	CompletedAt        *time.Time                 `json:"completedAt,omitempty"`
	RolledBackAt       *time.Time                 `json:"rolledBackAt,omitempty"`
	Diff               *ConversationTurnDiff      `json:"diff,omitempty"`
	Plan               *ConversationPlan          `json:"plan,omitempty"`
	Artifacts          *ConversationTurnArtifacts `json:"artifacts,omitempty"`
}

type ConversationTurnArtifacts struct {
	Files []ConversationTurnArtifact `json:"files"`
}

type ConversationTurnArtifact struct {
	Path   string `json:"path"`
	Status string `json:"status"`
}

const (
	TurnArtifactAdded    = "added"
	TurnArtifactModified = "modified"
)

type ConversationFailureCode string

const (
	ConversationFailureProviderKeyMissing ConversationFailureCode = "provider_key_missing"
	ConversationFailureAuthRequired       ConversationFailureCode = "auth_required"
)

type ConversationTurnDiff struct {
	Files     []ConversationDiffFile `json:"files"`
	Truncated bool                   `json:"truncated,omitempty"`
}

const ChatDiffTruncatedSummary = "diff file list truncated"

type ConversationDiffFile struct {
	Path         string     `json:"path"`
	Additions    int        `json:"additions"`
	Deletions    int        `json:"deletions"`
	Status       string     `json:"status"`
	OldPath      string     `json:"oldPath,omitempty"`
	RolledBackAt *time.Time `json:"rolledBackAt,omitempty"`
}

type QueuedTurn struct {
	TurnID              string
	Text                string
	ClientMessageID     string
	Origin              MessageOrigin
	AuthoredByUser      bool
	SenderSessionID     string
	SenderProjectID     string
	SenderDisplayName   string
	DeliveryContentJSON string
}

type RetryPrompt struct {
	Continuation        bool
	Text                string
	ProviderInputText   string
	Origin              MessageOrigin
	DeliveryContentJSON string
	ActiveLineage       bool
}

type ConversationMessage struct {
	InteractionAt       time.Time     `json:"-"`
	ID                  string        `json:"id"`
	ConversationID      string        `json:"conversationId"`
	TurnID              string        `json:"turnId,omitempty"`
	Sequence            int64         `json:"sequence"`
	Revision            int64         `json:"revision"`
	Role                MessageRole   `json:"role"`
	Origin              MessageOrigin `json:"origin"`
	Text                string        `json:"text"`
	Continuation        bool          `json:"continuation,omitempty"`
	AuthoredByUser      bool          `json:"-"`
	SpawnOpeningPrompt  bool          `json:"-"`
	Streaming           bool          `json:"streaming"`
	ProviderItemID      string        `json:"providerItemId,omitempty"`
	ClientMessageID     string        `json:"clientMessageId,omitempty"`
	ClientPayloadHash   string        `json:"-"`
	DeliveryContentJSON string        `json:"-"`
	SenderSessionID     string        `json:"senderSessionId,omitempty"`
	SenderProjectID     string        `json:"senderProjectId,omitempty"`
	SenderDisplayName   string        `json:"senderDisplayName,omitempty"`
	CreatedAt           time.Time     `json:"createdAt"`
	UpdatedAt           time.Time     `json:"updatedAt"`
}

type ConversationActivity struct {
	ID                     string         `json:"id"`
	ConversationID         string         `json:"conversationId"`
	TurnID                 string         `json:"turnId,omitempty"`
	Sequence               int64          `json:"sequence"`
	Revision               int64          `json:"revision"`
	Kind                   ActivityKind   `json:"kind"`
	Status                 ActivityStatus `json:"status"`
	Summary                string         `json:"summary"`
	Detail                 []byte         `json:"detail,omitempty"`
	RequestID              string         `json:"requestId,omitempty"`
	ProviderItemID         string         `json:"providerItemId,omitempty"`
	CreatedAt              time.Time      `json:"createdAt"`
	UpdatedAt              time.Time      `json:"updatedAt"`
	CommandOutput          string         `json:"commandOutput,omitempty"`
	CommandOutputTruncated bool           `json:"commandOutputTruncated,omitempty"`
	StreamedText           string         `json:"streamedText,omitempty"`
	StreamedTextTruncated  bool           `json:"streamedTextTruncated,omitempty"`
}

var ErrNoConversation = errors.New("session has no conversation")

var ErrNoQueuedTurn = errors.New("no queued turn")

var ErrSessionNotProvisioning = errors.New("session is not provisioning")

var ErrConversationRebound = errors.New("conversation is no longer bound to the session")

var ErrNoConversationTurn = errors.New("conversation turn not found")

var ErrClientMessageConflict = errors.New("client message id belongs to a different message")

var ErrConversationAutoResumeNotDue = errors.New("usage-limit reset time has not arrived")

var ErrNoConversationBranch = errors.New("conversation branch not found")

type ConversationQueuedEditDelivery struct {
	ClientMessageID string
	RequestHash     string
}

type ConversationEditDelivery struct {
	ConversationID      string
	ClientMessageID     string
	RequestJSON         string
	ProviderWorkStarted bool
	State               ConversationEditDeliveryState
	SourceBranchID      string
	ActiveBranchID      string
	Turn                ConversationTurn
	RejectionKind       ConversationEditRejectionKind
	RejectionMessage    string
	CreatedAt           time.Time
	SettledAt           *time.Time
}

type ConversationEditDeliveryState string

const (
	ConversationEditReserved ConversationEditDeliveryState = "reserved"
	ConversationEditAccepted ConversationEditDeliveryState = "accepted"
	ConversationEditRejected ConversationEditDeliveryState = "rejected"
)

type ConversationEditRejectionKind string

const (
	ConversationEditRejectedInvalid             ConversationEditRejectionKind = "invalid_turn"
	ConversationEditRejectedMissingTurn         ConversationEditRejectionKind = "missing_turn"
	ConversationEditRejectedUnsupported         ConversationEditRejectionKind = "unsupported"
	ConversationEditRejectedBusy                ConversationEditRejectionKind = "busy"
	ConversationEditRejectedInterfaceTransition ConversationEditRejectionKind = "interface_transition"
	ConversationEditRejectedByProvider          ConversationEditRejectionKind = "provider_refused"
	ConversationEditRejectedProviderFailure     ConversationEditRejectionKind = "provider_failure"
)

type ConversationSteerDelivery struct {
	ConversationID   string
	ClientMessageID  string
	RequestJSON      string
	State            ConversationSteerDeliveryState
	ProviderTurnID   string
	ActivityID       string
	RejectionKind    ConversationSteerRejectionKind
	RejectionMessage string
	CreatedAt        time.Time
	SettledAt        *time.Time
}

type ConversationSteerDeliveryState string

const (
	ConversationSteerReserved ConversationSteerDeliveryState = "reserved"
	ConversationSteerAccepted ConversationSteerDeliveryState = "accepted"
	ConversationSteerRejected ConversationSteerDeliveryState = "rejected"
)

type ConversationSteerRejectionKind string

const (
	ConversationSteerRejectedNoActiveTurn        ConversationSteerRejectionKind = "no_active_turn"
	ConversationSteerRejectedUnsupported         ConversationSteerRejectionKind = "unsupported"
	ConversationSteerRejectedTurnNotSteerable    ConversationSteerRejectionKind = "turn_not_steerable"
	ConversationSteerRejectedContentUnsupported  ConversationSteerRejectionKind = "content_unsupported"
	ConversationSteerRejectedByProvider          ConversationSteerRejectionKind = "provider_refused"
	ConversationSteerRejectedInterfaceTransition ConversationSteerRejectionKind = "interface_transition"
)

type ConversationBatchedDelivery struct {
	TurnID              string `json:"turnId"`
	DeliveredWithTurnID string `json:"deliveredWithTurnId"`
	SteerActivityID     string `json:"steerActivityId,omitempty"`
}
