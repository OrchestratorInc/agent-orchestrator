package domain

import (
	"errors"
	"slices"
	"time"
)

type UsageSourceKind string

const (
	UsageSourceClaudeMain        UsageSourceKind = "claude_main"
	UsageSourceClaudeSubagent    UsageSourceKind = "claude_subagent"
	UsageSourceCodexRollout      UsageSourceKind = "codex_rollout"
	UsageSourceKimiWire          UsageSourceKind = "kimi_wire"
	UsageSourcePiSession         UsageSourceKind = "pi_session"
	UsageSourceQwenMonthly       UsageSourceKind = "qwen_monthly"
	UsageSourceOMPSession        UsageSourceKind = "omp_session"
	UsageSourceKimchiSession     UsageSourceKind = "kimchi_session"
	UsageSourcePrimeAgentSession UsageSourceKind = "prime_agent_session"
	UsageSourceGooseLedger       UsageSourceKind = "goose_ledger"
	UsageSourceOpenCodeDB        UsageSourceKind = "opencode_db"
	UsageSourceKiloDB            UsageSourceKind = "kilo_db"
	UsageSourceGrokUsage         UsageSourceKind = "grok_usage"
	UsageSourceDeepSeekSession   UsageSourceKind = "deepseek_session"
	UsageSourceAgyConversation   UsageSourceKind = "agy_conversation"
)

var AllUsageSourceKinds = []UsageSourceKind{
	UsageSourceClaudeMain,
	UsageSourceClaudeSubagent,
	UsageSourceCodexRollout,
	UsageSourceKimiWire,
	UsageSourcePiSession,
	UsageSourceQwenMonthly,
	UsageSourceOMPSession,
	UsageSourceKimchiSession,
	UsageSourcePrimeAgentSession,
	UsageSourceGooseLedger,
	UsageSourceOpenCodeDB,
	UsageSourceKiloDB,
	UsageSourceGrokUsage,
	UsageSourceDeepSeekSession,
	UsageSourceAgyConversation,
}

func (k UsageSourceKind) IsKnown() bool {
	return slices.Contains(AllUsageSourceKinds, k)
}

type UsageBindingState string

const (
	UsageBindingDiscovering UsageBindingState = "discovering"
	UsageBindingActive      UsageBindingState = "active"
	UsageBindingFinalizing  UsageBindingState = "finalizing"
	UsageBindingComplete    UsageBindingState = "complete"
	UsageBindingPartial     UsageBindingState = "partial"
)

type UsageSourceState string

const (
	UsageSourcePending  UsageSourceState = "pending"
	UsageSourceActive   UsageSourceState = "active"
	UsageSourceComplete UsageSourceState = "complete"
	UsageSourceError    UsageSourceState = "error"
)

const (
	UsageErrorSourceDiscoveryPending      = "source_discovery_pending"
	UsageErrorArtifactPathRejected        = "artifact_path_rejected"
	UsageErrorArtifactMissing             = "artifact_missing"
	UsageErrorArtifactReplaced            = "artifact_replaced"
	UsageErrorSourceReadFailed            = "source_read_failed"
	UsageErrorRecordTooLarge              = "record_too_large"
	UsageErrorMalformedJSONL              = "malformed_jsonl"
	UsageErrorUnsupportedSourceFormat     = "unsupported_source_format"
	UsageErrorSourceEventConflict         = "source_event_conflict"
	UsageErrorNonMonotonicCumulativeUsage = "non_monotonic_cumulative_usage"
	UsageErrorInvalidParserState          = "invalid_parser_state"
	UsageErrorUnresolvedSpawnCall         = "unresolved_spawn_call"
	UsageErrorCodexSourceBudgetExceeded   = "codex_source_budget_exceeded"
)

var (
	ErrUsageSourceOffsetConflict   = errors.New("usage source cursor offset conflict")
	ErrUsageSourceRevisionConflict = errors.New("usage source revision conflict")
	ErrUsageSourceEventConflict    = errors.New("usage source event conflict")
)

type UsageBindingRecord struct {
	ID             int64
	SessionID      SessionID
	Harness        AgentHarness
	NativeRootID   string
	InitialModelID string
	ProviderHint   string
	State          UsageBindingState
	LastErrorCode  string
	UpdatedAt      time.Time
}

type UsageSourceRecord struct {
	ID              int64
	BindingID       int64
	Kind            UsageSourceKind
	NativeSessionID string
	SubagentID      string
	ArtifactPath    string
	FileIdentity    string
	Generation      int64
	ByteOffset      int64
	ParserStateJSON string
	State           UsageSourceState
	FailureCount    int64
	AnomalyCount    int64
	NextRetryAt     *time.Time
	LastErrorCode   string
	UpdatedAt       time.Time
}

type UsageSourceContext struct {
	Source         UsageSourceRecord
	SessionID      SessionID
	NativeRootID   string
	InitialModelID string
	ProviderHint   string
	BindingState   UsageBindingState
}

type UsageProviderID string

const (
	UsageProviderOpenAI    UsageProviderID = "openai"
	UsageProviderAnthropic UsageProviderID = "anthropic"
)

type UsageMeasurementKind string

const (
	UsageMeasurementNativeReported UsageMeasurementKind = "native_reported"
	UsageMeasurementAOEstimated    UsageMeasurementKind = "ao_estimated"
	UsageMeasurementMixed          UsageMeasurementKind = "mixed"
	UsageMeasurementUnknown        UsageMeasurementKind = "unknown"
)

type UsageBillingProviderSource string

const (
	UsageBillingProviderObserved UsageBillingProviderSource = "observed"
	UsageBillingProviderInferred UsageBillingProviderSource = "inferred"
)

func ObservedBillingProviderSource(billingProviderID string) UsageBillingProviderSource {
	if billingProviderID == "" {
		return ""
	}
	return UsageBillingProviderObserved
}

type UsageTokenMetrics struct {
	InputTokens         *int64
	CachedInputTokens   *int64
	UncachedInputTokens *int64
	OutputTokens        *int64
}

type UsageEventCosts struct {
	InputCostNanos       *int64
	CachedInputCostNanos *int64
	OutputCostNanos      *int64
	EstimatedCostNanos   *int64
	PricingVersion       string
}

type ModelUsageEvent struct {
	ProviderID            UsageProviderID
	BillingProviderID     string
	BillingProviderSource UsageBillingProviderSource
	ModelID               string
	MeasurementKind       UsageMeasurementKind
	Tokens                UsageTokenMetrics
	ProviderUsageJSON     string
	ServiceTier           string
	ProviderMessageID     string
	Costs                 UsageEventCosts
	CreatedAt             time.Time
	SourceEventKey        string
}

type UsagePriceOverride struct {
	ModelID                              string
	Input, Output, CacheRead, CacheWrite string
	PriceAs                              string
}

type UsageCostCandidate struct {
	ID                 int64
	BindingID          int64
	ProviderID         UsageProviderID
	BillingProviderID  string
	ModelID            string
	MeasurementKind    UsageMeasurementKind
	Tokens             UsageTokenMetrics
	ProviderUsageJSON  string
	ServiceTier        string
	PricingVersion     string
	SourceEventKey     string
	EstimatedCostNanos *int64
}

type UsageCostUpdate struct {
	Candidate UsageCostCandidate
	Costs     UsageEventCosts
}

type LegacyUsageEvent struct {
	ID                    int64
	BindingID             int64
	UsageSourceID         int64
	ProviderID            UsageProviderID
	BillingProviderID     string
	BillingProviderSource UsageBillingProviderSource
	ModelID               string
	MeasurementKind       UsageMeasurementKind
	Tokens                UsageTokenMetrics
	ProviderUsageJSON     string
	PricingVersion        string
	SourceEventKey        string
}

type LegacyUsageRepair struct {
	Candidate               LegacyUsageEvent
	ExpectedFileIdentity    string
	ExpectedByteOffset      int64
	ExpectedParserStateJSON string
	ExpectedSourceUpdatedAt time.Time
	BillingProviderID       string
	BillingProviderSource   UsageBillingProviderSource
	ProviderUsageJSON       string
	ServiceTier             string
	Costs                   UsageEventCosts
}

type EstimatedCostCoverage string

const (
	EstimatedCostCoverageComplete EstimatedCostCoverage = "complete"
	EstimatedCostCoveragePartial  EstimatedCostCoverage = "partial"
)

type EstimatedCostProviderAttribution string

const (
	EstimatedCostProviderAttributionObserved EstimatedCostProviderAttribution = "observed"
	EstimatedCostProviderAttributionInferred EstimatedCostProviderAttribution = "inferred"
	EstimatedCostProviderAttributionMixed    EstimatedCostProviderAttribution = "mixed"
)

type EstimatedCost struct {
	TotalNanos          int64
	InputNanos          *int64
	CachedInputNanos    *int64
	OutputNanos         *int64
	Coverage            EstimatedCostCoverage
	ProviderAttribution EstimatedCostProviderAttribution
}

type UsageCostAggregate struct {
	EventCount             int64
	PricedEventCount       int64
	PricedTotalNanos       int64
	ObservedCostEventCount int64
	InferredCostEventCount int64

	KnownInputCount               int64
	KnownInputNanos               int64
	UnpricedKnownInputNanos       int64
	KnownCachedInputCount         int64
	KnownCachedInputNanos         int64
	UnpricedKnownCachedInputNanos int64
	KnownOutputCount              int64
	KnownOutputNanos              int64
	UnpricedKnownOutputNanos      int64
}

type UsageModelAggregate struct {
	Harness AgentHarness
	ModelID string
	Tokens  UsageTokenMetrics
	Cost    UsageCostAggregate
}

type CompactSessionUsageAggregate struct {
	SessionID       SessionID
	ProcessedTokens *int64
	Incomplete      bool
	Cost            UsageCostAggregate
}

type CompactSessionUsage struct {
	SessionID       SessionID
	ProcessedTokens *int64
	Incomplete      bool
	EstimatedCost   *EstimatedCost
}

type SourceCursorState struct {
	ByteOffset      int64
	State           UsageSourceState
	ParserStateJSON string
	FailureCount    int64
	AnomalyCount    int64
	NextRetryAt     *time.Time
	LastErrorCode   string
	UpdatedAt       time.Time
}

type SessionMemoryProcess struct {
	PID        int
	PPID       int
	RSSBytes   uint64
	CPUPercent float64
	Command    string
}

type SessionMemory struct {
	SessionID    SessionID
	RSSBytes     uint64
	ProcessCount int
	CPUPercent   float64
	SampledAt    time.Time
	Processes    []SessionMemoryProcess
}

type SystemMemory struct {
	TotalBytes      uint64
	AvailableBytes  uint64
	SwapTotalBytes  uint64
	SwapUsedBytes   uint64
	SwapBytesPerSec float64
	CPUCount        int
	Load1           float64
	CPUPercent      float64
	CPUMeasured     bool
	PressureRaw     float64
	PressureSource  string
}

type AppMemory struct {
	RSSBytes     uint64
	ProcessCount int
	CPUPercent   float64
	CPUMeasured  bool
	Own          SessionMemory
	Reviewers    []ReviewerMemory
}

type ReviewerMemory struct {
	ReviewID  string
	SessionID SessionID
	Harness   ReviewerHarness
	Memory    SessionMemory
}

type SessionStep struct {
	ToolUseID string
	Tool      string
	StartedAt time.Time
	EndedAt   time.Time
	Failed    bool
}
