package domain

import (
	"strings"
	"time"
)

// AgentAccountProvider names a provider whose subscription accounts AO can
// launch agents with. An account is a config folder of the provider's own CLI.
type AgentAccountProvider string

// Account providers and the harness each one launches.
const (
	AgentAccountProviderClaude AgentAccountProvider = "claude"
	AgentAccountProviderCodex  AgentAccountProvider = "codex"
)

// AgentAccountProviders lists every provider in display order.
var AgentAccountProviders = []AgentAccountProvider{AgentAccountProviderClaude, AgentAccountProviderCodex}

// AgentAccountProviderFor reports the account provider behind a harness, or
// false when the harness has no accounts.
func AgentAccountProviderFor(h AgentHarness) (AgentAccountProvider, bool) {
	switch h {
	case HarnessClaudeCode:
		return AgentAccountProviderClaude, true
	case HarnessCodex:
		return AgentAccountProviderCodex, true
	}
	return "", false
}

// DeviceAgentAccountID is the id of a provider's "This computer" account: the
// login the computer's own CLI uses, launched with its config folder untouched.
func DeviceAgentAccountID(p AgentAccountProvider) string { return "device-" + string(p) }

// AgentAccount is one subscription login AO can launch a provider's CLI with.
// Home is the CLI config folder (CLAUDE_CONFIG_DIR or CODEX_HOME); it is empty
// for the device account, whose folder is the CLI's own default.
type AgentAccount struct {
	ID        string
	Provider  AgentAccountProvider
	Home      string
	Email     string
	Plan      string
	CreatedAt time.Time
}

// Device reports the "This computer" account.
func (a AgentAccount) Device() bool { return strings.TrimSpace(a.Home) == "" }

// AgentAccountLimitWindow is one usage meter of an account, such as the
// five-hour session or the weekly limit.
type AgentAccountLimitWindow struct {
	ID          string     `json:"id"`
	Label       string     `json:"label"`
	UsedPercent float64    `json:"usedPercent" minimum:"0" maximum:"100"`
	ResetsAt    *time.Time `json:"resetsAt,omitempty"`
	// Scope names the model a meter limits, such as "opus" or a Codex model's
	// own bucket; empty for a meter every request draws on.
	Scope string `json:"-"`
}

// AgentAccountLimits is what the provider's CLI reports about an account's
// subscription limits. AO never reads the account's token to get it.
type AgentAccountLimits struct {
	Windows []AgentAccountLimitWindow `json:"windows"`
	// AutoResumeResetsAt is present only when native rolling limits identify an
	// unambiguous future deadline. Nil does not establish available capacity.
	AutoResumeResetsAt *time.Time `json:"-"`
	// ExtraUsage is Claude's paid usage beyond the plan, in the plan's currency.
	ExtraUsage *AgentAccountExtraUsage `json:"extraUsage,omitempty"`
	// ResetCredits counts Codex usage-limit resets the account can spend.
	ResetCredits *int64 `json:"resetCredits,omitempty" minimum:"0"`
	// Activity is the provider's own count of the account's use everywhere,
	// not only in AO. Codex reports it; Claude does not.
	Activity  *AgentAccountActivity `json:"activity,omitempty"`
	FetchedAt time.Time             `json:"fetchedAt"`
}

// HandoffMaxUsedPercent is the fullest a relevant meter may be for an account
// to receive a session moved off a usage limit, so the retry is not refused at once.
const HandoffMaxUsedPercent = 90

// HandoffHeadroom is the percent left in the fullest meter a request for model
// draws on, and whether the reading verifies room for it. Every request draws
// on unscoped meters; a scoped one counts when it and model name each other
// ("opus" and "claude-opus-5"). An unknown model
// counts every Claude meter, as the default may be any of them, and only the
// overall Codex bucket, as Codex model buckets belong to non-default models. A
// reading with no relevant meter verifies nothing.
func (l AgentAccountLimits) HandoffHeadroom(p AgentAccountProvider, model string) (float64, bool) {
	model = normalizeLimitScope(model)
	fullest, relevant := 0.0, false
	for _, w := range l.Windows {
		scope := normalizeLimitScope(w.Scope)
		switch {
		case scope == "":
		case model == "" && p == AgentAccountProviderClaude:
		case model != "" && (strings.Contains(model, scope) || strings.Contains(scope, model)):
		default:
			continue
		}
		relevant = true
		fullest = max(fullest, w.UsedPercent)
	}
	if !relevant || fullest >= HandoffMaxUsedPercent {
		return 0, false
	}
	return 100 - fullest, true
}

func normalizeLimitScope(s string) string {
	return strings.NewReplacer(" ", "-", "_", "-").Replace(strings.ToLower(strings.TrimSpace(s)))
}

// AgentAccountActivity is the provider's own record of an account's use.
type AgentAccountActivity struct {
	LatestDayTokens    *int64  `json:"latestDayTokens,omitempty"`
	LatestDay          *string `json:"latestDay,omitempty"`
	LifetimeTokens     *int64  `json:"lifetimeTokens,omitempty"`
	PeakDailyTokens    *int64  `json:"peakDailyTokens,omitempty"`
	LongestTurnSeconds *int64  `json:"longestTurnSeconds,omitempty"`
	CurrentStreakDays  *int64  `json:"currentStreakDays,omitempty"`
}

// AgentAccountExtraUsage is spend beyond the plan's included usage, in major
// units of Currency.
type AgentAccountExtraUsage struct {
	Enabled      bool     `json:"enabled"`
	UsedCredits  *float64 `json:"usedCredits,omitempty"`
	MonthlyLimit *float64 `json:"monthlyLimit,omitempty"`
	Currency     string   `json:"currency,omitempty"`
}

// AgentAccountUsage is one account's model usage on one UTC day, from AO's own
// reading of the session logs.
type AgentAccountUsage struct {
	AccountID         string
	Day               string
	Model             string
	InputTokens       int64
	CachedInputTokens int64
	OutputTokens      int64
	CostNanos         int64
}

// AgentAccountIdentityCheck is who a config folder is signed in to, as the
// provider's CLI reports it.
type AgentAccountIdentityCheck struct {
	// SignedIn is a subscription sign-in, the only kind an account can be.
	SignedIn bool
	// LoggedIn is any sign-in the CLI can launch with, an API key included.
	LoggedIn bool
	Email    string
	Plan     string
}
