package domain

import "time"

// UsageReportEvent is one timestamped usage event as the usage report reads
// it, with the session it belongs to. Cache-write counts are nil when the
// stored provider object did not report them.
type UsageReportEvent struct {
	CreatedAt          time.Time
	ProviderID         UsageProviderID
	BillingProviderID  string
	ModelID            string
	ServiceTier        string
	Tokens             UsageTokenMetrics
	CacheWriteTokens   *int64
	CacheWrite5mTokens *int64
	CacheWrite1hTokens *int64
	Costs              UsageEventCosts
	Harness            AgentHarness
	SessionID          SessionID
	ProjectID          ProjectID
	SessionTitle       string
}

// UsageReport is token and estimated-cost usage over a window, bucketed by
// day or hour in the reader's time zone.
type UsageReport struct {
	Days       int
	TimeZone   string
	Resolution string
	Since      time.Time
	Until      time.Time
	// Periods lists every bucket in the window, oldest first: "2006-01-02" by
	// day, "2006-01-02T15:00" by hour.
	Periods  []string
	Rows     []UsageReportRow
	Sessions []UsageReportSession
}

// UsageReportRow is one period's usage for one harness, model and speed tier.
//
// The token counts are disjoint: InputTokens is uncached input that was not
// written to the cache. Costs cover only the priced events. The four category
// costs add up to CostNanos; PremiumNanos and CacheSavingsNanos are views of
// it, never added again.
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

// UsageReportSession is one session's usage over the report window.
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
