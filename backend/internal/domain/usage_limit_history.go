package domain

import "time"

// UsageLimitHistory is what AO's own limit readings show about each login's
// windows over a period: how full each five-hour and weekly window got.
type UsageLimitHistory struct {
	Since time.Time `json:"since"`
	Until time.Time `json:"until"`
	// FirstReadingAt is the oldest reading in the period; nil when there is none.
	FirstReadingAt *time.Time        `json:"firstReadingAt,omitempty"`
	Logins         []UsageLimitLogin `json:"logins"`
}

// UsageLimitLogin is one subscription login. Two accounts signed in to the
// same login share its limits, so their readings are one login.
type UsageLimitLogin struct {
	Provider AgentAccountProvider `json:"provider" enum:"claude,codex"`
	Email    string               `json:"email,omitempty"`
	// AccountID is the account that read this login most recently.
	AccountID string           `json:"accountId"`
	Windows   []UsageLimitSpan `json:"windows"`
}

// Usage limit span states.
const (
	// UsageLimitSpanFinal: read within minutes of its reset, so the peak is its end.
	UsageLimitSpanFinal = "final"
	// UsageLimitSpanPartial: readings stopped well before the reset, so the
	// window got at least this full.
	UsageLimitSpanPartial = "partial"
	// UsageLimitSpanOpen: the window has not reset yet.
	UsageLimitSpanOpen = "open"
)

// UsageLimitSpan is one window of one limit, from the readings that share its
// reset time.
type UsageLimitSpan struct {
	WindowID        string `json:"windowId"`
	Label           string `json:"label"`
	DurationMinutes int    `json:"durationMinutes,omitempty" minimum:"0"`
	// StartsAt is the reset less the window's length; nil when the length is unknown.
	StartsAt        *time.Time `json:"startsAt,omitempty"`
	ResetsAt        time.Time  `json:"resetsAt"`
	PeakUsedPercent float64    `json:"peakUsedPercent" minimum:"0" maximum:"100"`
	FirstReadAt     time.Time  `json:"firstReadAt"`
	LastReadAt      time.Time  `json:"lastReadAt"`
	Readings        int        `json:"readings" minimum:"1"`
	State           string     `json:"state" enum:"final,partial,open"`
	// Points is the last reading of each hour, for windows of a day or longer.
	Points []UsageLimitPoint `json:"points,omitempty"`
}

// UsageLimitPoint is one reading of a window.
type UsageLimitPoint struct {
	At          time.Time `json:"at"`
	UsedPercent float64   `json:"usedPercent" minimum:"0" maximum:"100"`
}
