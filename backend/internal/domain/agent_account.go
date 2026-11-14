package domain

import (
	"strings"
	"time"
)

type AgentAccountProvider string

const (
	AgentAccountProviderClaude AgentAccountProvider = "claude"
	AgentAccountProviderCodex  AgentAccountProvider = "codex"
)

var AgentAccountProviders = []AgentAccountProvider{AgentAccountProviderClaude, AgentAccountProviderCodex}

func AgentAccountProviderFor(h AgentHarness) (AgentAccountProvider, bool) {
	switch h {
	case HarnessClaudeCode:
		return AgentAccountProviderClaude, true
	case HarnessCodex:
		return AgentAccountProviderCodex, true
	}
	return "", false
}

func DeviceAgentAccountID(p AgentAccountProvider) string { return "device-" + string(p) }

type AgentAccount struct {
	ID        string
	Provider  AgentAccountProvider
	Home      string
	Email     string
	Plan      string
	CreatedAt time.Time
}

func (a AgentAccount) Device() bool { return strings.TrimSpace(a.Home) == "" }

type AgentAccountProblemKind string

const (
	AgentAccountProblemSlow        AgentAccountProblemKind = "slow"
	AgentAccountProblemRateLimited AgentAccountProblemKind = "rate_limited"
	AgentAccountProblemUnavailable AgentAccountProblemKind = "unavailable"
	AgentAccountProblemCLIMissing  AgentAccountProblemKind = "cli_missing"
	AgentAccountProblemOtherLogin  AgentAccountProblemKind = "other_login"
	AgentAccountProblemOtherOrg    AgentAccountProblemKind = "other_org"
)

func (k AgentAccountProblemKind) Transient() bool {
	return k == AgentAccountProblemSlow || k == AgentAccountProblemRateLimited || k == AgentAccountProblemUnavailable
}

type AgentAccountLimitWindow struct {
	ID              string     `json:"id"`
	Label           string     `json:"label"`
	UsedPercent     float64    `json:"usedPercent" minimum:"0" maximum:"100"`
	ResetsAt        *time.Time `json:"resetsAt,omitempty"`
	DurationMinutes int        `json:"durationMinutes,omitempty" minimum:"0"`
	Scope           string     `json:"-"`
}

type AgentLimitReading struct {
	AccountID       string
	Provider        AgentAccountProvider
	Email           string
	Plan            string
	WindowID        string
	Label           string
	DurationMinutes int
	UsedPercent     float64
	ResetsAt        *time.Time
	ReadAt          time.Time
}

type AgentAccountLimits struct {
	Windows            []AgentAccountLimitWindow `json:"windows"`
	AutoResumeResetsAt *time.Time                `json:"-"`
	ReachedScopes      []string                  `json:"-"`
	ExtraUsage         *AgentAccountExtraUsage   `json:"extraUsage,omitempty"`
	ResetCredits       *int64                    `json:"resetCredits,omitempty" minimum:"0"`
	Activity           *AgentAccountActivity     `json:"activity,omitempty"`
	FetchedAt          time.Time                 `json:"fetchedAt"`
}

func (l AgentAccountLimits) HandoffHeadroom(p AgentAccountProvider, model string) (float64, bool) {
	model = normalizeLimitScope(model)
	fullest, relevant := 0.0, false
	meters := append([]AgentAccountLimitWindow{}, l.Windows...)
	for _, scope := range l.ReachedScopes {
		meters = append(meters, AgentAccountLimitWindow{Scope: scope, UsedPercent: 100})
	}
	for _, w := range meters {
		scope := normalizeLimitScope(w.Scope)
		switch {
		case scope == "":
		case model == "":
		case model != "" && (strings.Contains(model, scope) || p == AgentAccountProviderClaude && strings.Contains(scope, model)):
		default:
			continue
		}
		relevant = true
		fullest = max(fullest, w.UsedPercent)
	}
	if !relevant || fullest >= 100 {
		return 0, false
	}
	return 100 - fullest, true
}

func normalizeLimitScope(s string) string {
	return strings.NewReplacer(" ", "-", "_", "-").Replace(strings.ToLower(strings.TrimSpace(s)))
}

type AgentAccountActivity struct {
	LatestDayTokens    *int64  `json:"latestDayTokens,omitempty"`
	LatestDay          *string `json:"latestDay,omitempty"`
	LifetimeTokens     *int64  `json:"lifetimeTokens,omitempty"`
	PeakDailyTokens    *int64  `json:"peakDailyTokens,omitempty"`
	LongestTurnSeconds *int64  `json:"longestTurnSeconds,omitempty"`
	CurrentStreakDays  *int64  `json:"currentStreakDays,omitempty"`
}

type AgentAccountExtraUsage struct {
	Enabled      bool     `json:"enabled"`
	UsedCredits  *float64 `json:"usedCredits,omitempty"`
	MonthlyLimit *float64 `json:"monthlyLimit,omitempty"`
	Currency     string   `json:"currency,omitempty"`
}

type AgentAccountUsage struct {
	AccountID         string
	Day               string
	Model             string
	InputTokens       int64
	CachedInputTokens int64
	OutputTokens      int64
	CostNanos         int64
}

type AgentAccountIdentityCheck struct {
	SignedIn bool
	LoggedIn bool
	Email    string
	Plan     string
	OrgID    string
	OrgName  string
}
