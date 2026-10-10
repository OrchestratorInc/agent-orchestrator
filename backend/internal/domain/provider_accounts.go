package domain

import (
	"hash/fnv"
	"strings"
	"time"
)

// ProviderAccount is AO's safe identity plus a private upstream credential
// reference. Empty CredentialRef is the durable signed-out fact.
type ProviderAccount struct {
	ID          string `json:"id"`
	Provider    string `json:"provider"`
	DisplayName string `json:"display_name,omitempty"`
	Email       string `json:"email"`
	Kind        string `json:"kind,omitempty"`
	// Global marks the account currently discovered from the provider's native
	// login. It is informational; routing still follows the saved primary.
	Global        bool   `json:"global,omitempty"`
	CredentialRef string `json:"credential_ref"`
	AuthID        string `json:"auth_id"`
}

// APIKey reports an account that is an API key and not a sign-in. The
// credential itself says so: the account helper keeps API keys in its
// configuration and sign-ins as files. Accounts recorded before the helper
// named the kind carry no kind at all.
func (a ProviderAccount) APIKey() bool {
	return a.Kind == "api_key" || strings.HasPrefix(a.CredentialRef, "config-index:") || strings.HasPrefix(a.CredentialRef, "config:")
}

// GeneratedProviderAccountName gives older accounts a stable friendly label
// without depending on provider profile APIs.
func GeneratedProviderAccountName(provider, id string) string {
	words := []string{"Cedar", "Maple", "Willow", "River", "Summit", "Harbor", "Meadow", "Pine", "Juniper", "Clover", "Ember", "Atlas"}
	h := fnv.New32a()
	_, _ = h.Write([]byte(strings.ToLower(provider + ":" + id)))
	word := words[int(h.Sum32())%len(words)]
	name := "Account"
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "codex":
		name = "Codex"
	case "claude":
		name = "Claude"
	}
	return word + " " + name
}

// ProviderAccountUsage is a safe, short-lived view of what the provider and
// the account helper report about one account: its limits, its resets, its
// plan, and its recent activity. It deliberately contains no credential or
// provider-private response data.
type ProviderAccountUsage struct {
	Status string `json:"status"`
	Plan   string `json:"plan,omitempty"`
	// PlanTier is the size of the plan where the provider sells several, such as "20x".
	PlanTier string `json:"plan_tier,omitempty"`
	// Windows lists the two general limits first, then every scoped limit.
	Windows []ProviderAccountUsageWindow `json:"windows,omitempty"`
	// ResetCredits counts unused usage-limit resets; nil when not reported.
	ResetCredits *int64 `json:"reset_credits,omitempty"`
	// Resets describes each unused reset when the provider itemizes them.
	Resets []ProviderAccountReset `json:"resets,omitempty"`
	// ResetUsable is true when the provider would accept a reset right now.
	ResetUsable bool `json:"reset_usable,omitempty"`
	// ResetBlockedUntil is when the provider next allows a reset, when it says.
	ResetBlockedUntil string                     `json:"reset_blocked_until,omitempty"`
	Credits           *ProviderAccountCredits    `json:"credits,omitempty"`
	ExtraUsage        *ProviderAccountExtraUsage `json:"extra_usage,omitempty"`
	RenewsAt          string                     `json:"renews_at,omitempty"`
	Organization      string                     `json:"organization,omitempty"`
	AddedAt           string                     `json:"added_at,omitempty"`
	RefreshedAt       string                     `json:"refreshed_at,omitempty"`
	// PausedUntil is set while the helper holds the account back after a
	// provider refusal; PausedReason is the helper's short code for why.
	PausedUntil  string `json:"paused_until,omitempty"`
	PausedReason string `json:"paused_reason,omitempty"`
	// SignInEnding is set when the saved sign-in still works but has stopped
	// renewing, so it will stop working; SignInEndsAt is when, if known.
	SignInEnding bool   `json:"sign_in_ending,omitempty"`
	SignInEndsAt string `json:"sign_in_ends_at,omitempty"`
	// Requests counts recent requests in fixed slices of time, oldest first.
	Requests []ProviderAccountRequests `json:"requests,omitempty"`
	// Tokens is the provider's own tally of the account's token use.
	Tokens    *ProviderAccountTokens `json:"tokens,omitempty"`
	CheckedAt time.Time              `json:"checked_at,omitempty"`
	Message   string                 `json:"message,omitempty"`
}

// Scopes of a usage window. The general limits have no scope.
const (
	ProviderUsageScopeCodeReview = "code_review"
	ProviderUsageScopeModel      = "model"
	ProviderUsageScopeApps       = "oauth_apps"
	ProviderUsageScopeCowork     = "cowork"
)

// ProviderAccountUsageWindow is one normalized quota window returned by the
// proxy's management API.
type ProviderAccountUsageWindow struct {
	// Name is the provider's name for a model-scoped limit.
	Name string `json:"name,omitempty"`
	// Scope says what the limit covers; empty for the account's general limits.
	Scope             string  `json:"scope,omitempty"`
	DurationSeconds   int64   `json:"duration_seconds,omitempty"`
	RemainingFraction float64 `json:"remaining_fraction"`
	ResetTime         string  `json:"reset_time,omitempty"`
}

// ProviderAccountReset is one unused allowance to clear the usage limits.
type ProviderAccountReset struct {
	Label     string `json:"label,omitempty"`
	Left      int64  `json:"left"`
	Total     int64  `json:"total"`
	ExpiresAt string `json:"expires_at,omitempty"`
}

// ProviderAccountCredits is a prepaid balance spent after the plan's limits.
type ProviderAccountCredits struct {
	Balance   string `json:"balance,omitempty"`
	Unlimited bool   `json:"unlimited,omitempty"`
}

// ProviderAccountExtraUsage is pay-as-you-go spending beyond the plan, in cents.
type ProviderAccountExtraUsage struct {
	Enabled    bool  `json:"enabled"`
	UsedCents  int64 `json:"used_cents"`
	LimitCents int64 `json:"limit_cents"`
}

// ProviderAccountRequests counts the requests of one slice of time.
type ProviderAccountRequests struct {
	Succeeded int64 `json:"succeeded"`
	Failed    int64 `json:"failed"`
}

// ProviderAccountTokens is the provider's own tally of an account's token use.
// A nil figure is one the provider did not report.
type ProviderAccountTokens struct {
	// LatestDay is the most recent day the provider has counted, as YYYY-MM-DD.
	LatestDay          string `json:"latest_day,omitempty"`
	LatestDayTokens    *int64 `json:"latest_day_tokens,omitempty"`
	Lifetime           *int64 `json:"lifetime,omitempty"`
	PeakDaily          *int64 `json:"peak_daily,omitempty"`
	LongestTurnSeconds *int64 `json:"longest_turn_seconds,omitempty"`
	CurrentStreakDays  *int64 `json:"current_streak_days,omitempty"`
	LongestStreakDays  *int64 `json:"longest_streak_days,omitempty"`
}

// Outcomes of using a reset. Only ProviderResetDone means one was spent;
// ProviderResetUnknown means the provider never confirmed either way.
const (
	ProviderResetDone    = "reset"
	ProviderResetNothing = "nothing_to_reset"
	ProviderResetNone    = "none_available"
	ProviderResetWait    = "wait"
	ProviderResetFailed  = "failed"
	ProviderResetUnknown = "unknown"
)

// ProviderPrimary records a provider default, including an empty default after its last logout.
type ProviderPrimary struct {
	Provider  string `json:"provider"`
	PrimaryID string `json:"primary_id"`
}

// ProviderSessionRoute binds one session ticket to a provider and account.
type ProviderSessionRoute struct {
	SessionID  SessionID `json:"session_id"`
	Provider   string    `json:"provider"`
	AccountID  string    `json:"account_id"`
	TicketHash string    `json:"ticket_hash"`
}

// ProviderAccountState contains only durable routing facts. A primary entry,
// even when empty, records deliberate adoption of managed routing.
// NativeProviderImport remembers an observed source even after local sign-out
// or removal, so refresh cannot undo an explicit account-manager action.
type NativeProviderImport struct {
	Fingerprint string `json:"fingerprint"`
	AccountID   string `json:"account_id"`
	// Email is whose login the source held. It decides which accounts are
	// marked Global, however and whenever they were added.
	Email string `json:"email,omitempty"`
}

type ProviderAccountState struct {
	NativeImports map[string]NativeProviderImport `json:"native_imports,omitempty"`
	// NativeKeyImports is the same record for the API key this computer's agent
	// is set up to use, kept apart because one provider can have both a
	// sign-in and a key.
	NativeKeyImports map[string]NativeProviderImport `json:"native_key_imports,omitempty"`
	Revision         int64                           `json:"revision"`
	Accounts         []ProviderAccount               `json:"accounts"`
	Primaries        []ProviderPrimary               `json:"primaries"`
	Routes           []ProviderSessionRoute          `json:"routes"`
}

// ProviderAccountIntent survives a lost helper acknowledgement or daemon exit.
// Effective facts commit after acknowledgement; credential cleanup completes
// before the intent is cleared and the operation reports success.
type ProviderAccountIntent struct {
	Next             ProviderAccountState `json:"next"`
	DeleteCredential string               `json:"delete_credential"`
	RequestBoundary  bool                 `json:"request_boundary,omitempty"`
}
