package domain

import "time"

const (
	BudgetQualityExact     = "EXACT"
	BudgetQualityEstimated = "ESTIMATED"
	BudgetQualityUnknown   = "UNKNOWN"
)

// BudgetUsageEntry 按 canonical event 归因一次；三层预算是该事件的查询投影。
// 原始 Provider payload 和机器路径不进入预算账本。
type BudgetUsageEntry struct {
	EventID, BindingID, Revision int64
	SourceEventKey               string
	SessionID                    SessionID
	AttemptID                    WorkflowAttemptID
	WorkflowID                   WorkflowID
	TaskID                       WorkflowTaskID
	OccurredAt                   *time.Time
	Currency, Quality            string
	CostNanos                    *int64
	PriceVersion                 string
	BillingProviderID            string
	BillingProviderSource        string
	MeasurementKind              UsageMeasurementKind
	Tokens                       UsageTokenMetrics
}

// BudgetUsageCorrection 保存修订前后完整非秘密事实，不将修正再计为另一笔费用。
type BudgetUsageCorrection struct {
	Key                        string
	EventID, Revision          int64
	PreviousRevision           int64
	OldCostNanos, NewCostNanos *int64
	Reason                     string
	PreviousSnapshot, Snapshot string
	CreatedAt                  time.Time
}

// BudgetSnapshot 的 nil 表示不能确认；KnownCostNanos 始终只是已知费用下界。
type BudgetSnapshot struct {
	Policy           BudgetPolicy
	Window           string
	KnownCostNanos   int64
	CostNanos        *int64
	ReservedNanos    *int64
	RemainingNanos   *int64
	Quality          string
	Tokens           UsageTokenMetrics
	EventCount       int64
	AlertThreshold   int64
	ReconcilePending bool
}
