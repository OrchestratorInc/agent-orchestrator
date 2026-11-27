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

// AgentAccountKind says how an account signs its CLI in.
type AgentAccountKind string

// Account kinds. A subscription account is the CLI's own sign-in, and a setup
// token account a subscription signed in with a saved `claude setup-token`
// token. An API key account launches the CLI with a saved key, and a metered
// account is the CLI's own sign-in to API billing (Anthropic Console, an
// OpenAI API key); both bill per token and have no usage limits to read or
// move a session at.
const (
	AgentAccountKindSubscription AgentAccountKind = "subscription"
	AgentAccountKindSetupToken   AgentAccountKind = "setup_token"
	AgentAccountKindAPIKey       AgentAccountKind = "api_key"
	AgentAccountKindMetered      AgentAccountKind = "metered"
)

// AgentAccount is one login AO can launch a provider's CLI with. Home is the
// CLI config folder (CLAUDE_CONFIG_DIR or CODEX_HOME); it is empty for the
// device account, whose folder is the CLI's own default. Name and KeyHint (the
// key's last four characters) are set only on an API key account.
type AgentAccount struct {
	ID       string
	Provider AgentAccountProvider
	Home     string
	Email    string
	Plan     string
	Kind     AgentAccountKind
	Name     string
	KeyHint  string
	// Color is the palette id the app tints the account with. Every account of
	// a provider gets its own; empty only until the service assigns one.
	Color     string
	CreatedAt time.Time
}

// AgentAccountLabel is the name and color the user gave an account.
type AgentAccountLabel struct {
	Name  string
	Color string
}

// AgentAccountAutoColors is the order new accounts take colors in: the first
// one no other account of the provider uses.
var AgentAccountAutoColors = []string{
	AgentAccountColorBlue, AgentAccountColorViolet, AgentAccountColorTeal, AgentAccountColorGreen,
	AgentAccountColorAmber, AgentAccountColorPink, AgentAccountColorOrange, AgentAccountColorRed,
}

// Account colors: the closed set of palette ids an account can carry. The app
// maps each id to its own swatch, so the backend never stores a raw color value.
const (
	AgentAccountColorRed    = "red"
	AgentAccountColorOrange = "orange"
	AgentAccountColorAmber  = "amber"
	AgentAccountColorGreen  = "green"
	AgentAccountColorTeal   = "teal"
	AgentAccountColorBlue   = "blue"
	AgentAccountColorViolet = "violet"
	AgentAccountColorPink   = "pink"
)

// AgentAccountColors lists every palette id in display order.
var AgentAccountColors = []string{
	AgentAccountColorRed, AgentAccountColorOrange, AgentAccountColorAmber, AgentAccountColorGreen,
	AgentAccountColorTeal, AgentAccountColorBlue, AgentAccountColorViolet, AgentAccountColorPink,
}

// ValidAgentAccountColor reports whether color is a palette id. The empty
// string is not one: it is how a caller asks for the first free color.
func ValidAgentAccountColor(color string) bool {
	for _, c := range AgentAccountColors {
		if c == color {
			return true
		}
	}
	return false
}

// Device reports the "This computer" account. It has no saved account row;
// its name and color are saved apart, by account id.
func (a AgentAccount) Device() bool { return strings.TrimSpace(a.Home) == "" }

// APIKey reports an account that launches its CLI with a saved API key.
func (a AgentAccount) APIKey() bool { return a.Kind == AgentAccountKindAPIKey }

// SetupToken reports a subscription account signed in with a saved setup token.
func (a AgentAccount) SetupToken() bool { return a.Kind == AgentAccountKindSetupToken }

// PerToken reports an account billed per token: it has no usage limits, is
// never the fallback for a signed-out default, and no session moves to or from
// it at a usage limit.
func (a AgentAccount) PerToken() bool {
	return a.Kind == AgentAccountKindAPIKey || a.Kind == AgentAccountKindMetered
}

// AgentAccountProblemKind says why an account's identity or limits could not
// be read, so the app can say it plainly instead of showing the CLI's error.
type AgentAccountProblemKind string

// Problem kinds. Slow, rate limited and unavailable pass on their own; a
// missing CLI and another login need the user.
const (
	// AgentAccountProblemSlow: the CLI did not answer in time.
	AgentAccountProblemSlow AgentAccountProblemKind = "slow"
	// AgentAccountProblemRateLimited: the provider refused the check for now.
	AgentAccountProblemRateLimited AgentAccountProblemKind = "rate_limited"
	// AgentAccountProblemUnavailable: any other failed check.
	AgentAccountProblemUnavailable AgentAccountProblemKind = "unavailable"
	// AgentAccountProblemCLIMissing: the provider's CLI is not installed.
	AgentAccountProblemCLIMissing AgentAccountProblemKind = "cli_missing"
	// AgentAccountProblemOtherLogin: the folder is signed in as another login.
	AgentAccountProblemOtherLogin AgentAccountProblemKind = "other_login"
	// AgentAccountProblemOtherOrg: the folder is signed in to another organization.
	AgentAccountProblemOtherOrg AgentAccountProblemKind = "other_org"
)

// Transient reports a problem the next check may not have.
func (k AgentAccountProblemKind) Transient() bool {
	return k == AgentAccountProblemSlow || k == AgentAccountProblemRateLimited || k == AgentAccountProblemUnavailable
}

// AgentAccountLimitWindow is one usage meter of an account, such as the
// five-hour session or the weekly limit.
type AgentAccountLimitWindow struct {
	ID              string     `json:"id"`
	Label           string     `json:"label"`
	UsedPercent     float64    `json:"usedPercent" minimum:"0" maximum:"100"`
	ResetsAt        *time.Time `json:"resetsAt,omitempty"`
	DurationMinutes int        `json:"durationMinutes,omitempty" minimum:"0"`
	Scope           string     `json:"-"`
}

// AgentLimitReading is one limit window's value as read at one moment.
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

// AgentAccountLimits is what the provider's CLI reports about an account's
// subscription limits. AO never reads the account's token to get it.
type AgentAccountLimits struct {
	Windows []AgentAccountLimitWindow `json:"windows"`
	// AutoResumeResetsAt is present only when native rolling limits identify an
	// unambiguous future deadline. Nil does not establish available capacity.
	AutoResumeResetsAt *time.Time `json:"-"`
	// Incomplete is set when the provider reported a window without a valid
	// utilization, or a limit state Windows does not show, so Windows alone
	// cannot establish available capacity.
	Incomplete bool `json:"-"`
	// ReachedScopes are the meter scopes the provider reports as exhausted.
	ReachedScopes []string `json:"-"`
	// ExtraUsage is Claude's paid usage beyond the plan, in the plan's currency.
	ExtraUsage *AgentAccountExtraUsage `json:"extraUsage,omitempty"`
	// ResetCredits counts Codex usage-limit resets the account can spend.
	ResetCredits *int64 `json:"resetCredits,omitempty" minimum:"0"`
	// Activity is the provider's own count of the account's use everywhere,
	// not only in AO. Codex reports it; Claude does not.
	Activity  *AgentAccountActivity `json:"activity,omitempty"`
	FetchedAt time.Time             `json:"fetchedAt"`
}

// HandoffHeadroom reports the room left in the meters a request for model draws on.
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
	OrgID    string
	OrgName  string
}
