package domain

import "time"

type UsageReportEvent struct {
	CreatedAt          time.Time
	ProviderID         UsageProviderID
	BillingProviderID  string
	ModelID            string
	ServiceTier        string
	Tokens             UsageTokenMetrics
	CacheWriteTokens   *int64
	CacheWrite1hTokens *int64
	Costs              UsageEventCosts
	Harness            AgentHarness
	SessionID          SessionID
	ProjectID          ProjectID
	SessionTitle       string
}

type UsageReport struct {
	Days       int
	TimeZone   string
	Resolution string
	Since      time.Time
	Until      time.Time
	Periods    []string
	Rows       []UsageReportRow
	Sessions   []UsageReportSession
}

type UsageReportRow struct {
	Period              string
	Harness             AgentHarness
	ModelID             string
	Speed               string
	Events              int64
	PricedEvents        int64
	InputTokens         int64
	CacheReadTokens     int64
	CacheWriteTokens    int64
	OutputTokens        int64
	InputCostNanos      int64
	CacheReadCostNanos  int64
	CacheWriteCostNanos int64
	OutputCostNanos     int64
	CostNanos           int64
	PremiumNanos        int64
	CacheSavingsNanos   int64
}

type UsageReportSession struct {
	SessionID    SessionID
	ProjectID    ProjectID
	Harness      AgentHarness
	Title        string
	Events       int64
	PricedEvents int64
	Tokens       int64
	CostNanos    int64
	LastUsedAt   time.Time
}
