package proxyhost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type codexUsageWindow struct {
	UsedPercent   *float64 `json:"used_percent"`
	WindowSeconds int64    `json:"limit_window_seconds"`
	ResetAfter    int64    `json:"reset_after_seconds"`
	ResetAt       int64    `json:"reset_at"`
}

type codexRateLimit struct {
	Primary      *codexUsageWindow `json:"primary_window"`
	Secondary    *codexUsageWindow `json:"secondary_window"`
	LimitReached bool              `json:"limit_reached"`
}

// The plan, the general limits, and the reset count are the core of the
// answer. Everything else is read separately so that a field the provider
// reshapes can only cost its own row, never the whole reading.
type codexUsageResponse struct {
	PlanType     string          `json:"plan_type"`
	RateLimit    *codexRateLimit `json:"rate_limit"`
	ResetCredits *struct {
		AvailableCount int64  `json:"available_count"`
		Applicable     *int64 `json:"applicable_available_count"`
	} `json:"rate_limit_reset_credits"`
}

type codexUsageExtras struct {
	CodeReview json.RawMessage `json:"code_review_rate_limit"`
	Additional json.RawMessage `json:"additional_rate_limits"`
	Credits    json.RawMessage `json:"credits"`
}

type claudeUsageWindow struct {
	Utilization *float64 `json:"utilization"`
	ResetAt     string   `json:"resets_at"`
}

type claudeUsageResponse struct {
	FiveHour *claudeUsageWindow `json:"five_hour"`
	SevenDay *claudeUsageWindow `json:"seven_day"`
}

type claudeUsageExtras struct {
	Opus       json.RawMessage `json:"seven_day_opus"`
	Sonnet     json.RawMessage `json:"seven_day_sonnet"`
	Apps       json.RawMessage `json:"seven_day_oauth_apps"`
	Cowork     json.RawMessage `json:"seven_day_cowork"`
	Limits     json.RawMessage `json:"limits"`
	ExtraUsage json.RawMessage `json:"extra_usage"`
}

const (
	fiveHourSeconds = 5 * 60 * 60
	weekSeconds     = 7 * 24 * 60 * 60
)

var (
	planTierPattern = regexp.MustCompile(`(\d+x)$`)
	balancePattern  = regexp.MustCompile(`^\d+(\.\d+)?$`)
)

// displayText bounds provider-written text before it reaches the screen.
func displayText(value string) string {
	value = strings.Join(strings.Fields(strings.Map(func(r rune) rune {
		if r < 0x20 || (r >= 0x7f && r <= 0x9f) {
			return ' '
		}
		return r
	}, value)), " ")
	if len([]rune(value)) > 120 {
		value = string([]rune(value)[:120])
	}
	return value
}

// instant normalizes a provider timestamp, given as text or as Unix seconds.
func instant(value any) string {
	switch v := value.(type) {
	case string:
		if parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(v)); err == nil {
			return parsed.UTC().Format(time.RFC3339)
		}
	case float64:
		if v > 1e12 {
			v /= 1000
		}
		if v > 0 {
			return time.Unix(int64(v), 0).UTC().Format(time.RFC3339)
		}
	}
	return ""
}

func future(value string, now time.Time) bool {
	parsed, err := time.Parse(time.RFC3339, value)
	return err == nil && parsed.After(now)
}

func codexWindows(limit *codexRateLimit, scope, name string, now time.Time) []domain.ProviderAccountUsageWindow {
	if limit == nil {
		return nil
	}
	var windows []domain.ProviderAccountUsageWindow
	for _, window := range []*codexUsageWindow{limit.Primary, limit.Secondary} {
		if window == nil || window.UsedPercent == nil || *window.UsedPercent < 0 || *window.UsedPercent > 100 {
			continue
		}
		reset := ""
		if window.ResetAt > 0 {
			reset = time.Unix(window.ResetAt, 0).UTC().Format(time.RFC3339)
		} else if window.ResetAfter > 0 {
			reset = now.UTC().Add(time.Duration(window.ResetAfter) * time.Second).Format(time.RFC3339)
		}
		windows = append(windows, domain.ProviderAccountUsageWindow{Name: name, Scope: scope, DurationSeconds: window.WindowSeconds, RemainingFraction: 1 - *window.UsedPercent/100, ResetTime: reset})
	}
	return windows
}

func codexUsage(raw []byte, now time.Time) (domain.ProviderAccountUsage, error) {
	var quota codexUsageResponse
	if err := json.Unmarshal(raw, &quota); err != nil {
		return domain.ProviderAccountUsage{}, err
	}
	usage := domain.ProviderAccountUsage{Status: "available", CheckedAt: now.UTC(), Plan: strings.TrimSpace(quota.PlanType)}
	usage.Windows = codexWindows(quota.RateLimit, "", "", now)
	if usage.Plan == "" && len(usage.Windows) == 0 {
		return domain.ProviderAccountUsage{}, errors.New("Codex usage response contained no quota data")
	}
	reached := quota.RateLimit != nil && quota.RateLimit.LimitReached
	for _, window := range usage.Windows {
		reached = reached || window.RemainingFraction <= 0
	}
	if quota.ResetCredits != nil {
		count := quota.ResetCredits.AvailableCount
		usage.ResetCredits = &count
		usage.ResetUsable = count > 0 && reached
		if quota.ResetCredits.Applicable != nil {
			usage.ResetUsable = *quota.ResetCredits.Applicable > 0
		}
	}
	var extras codexUsageExtras
	_ = json.Unmarshal(raw, &extras)
	var review codexRateLimit
	if json.Unmarshal(extras.CodeReview, &review) == nil {
		usage.Windows = append(usage.Windows, codexWindows(&review, domain.ProviderUsageScopeCodeReview, "", now)...)
	}
	var additional []struct {
		Name      string          `json:"limit_name"`
		RateLimit *codexRateLimit `json:"rate_limit"`
	}
	if json.Unmarshal(extras.Additional, &additional) == nil {
		for _, limit := range additional {
			if name := displayText(limit.Name); name != "" {
				usage.Windows = append(usage.Windows, codexWindows(limit.RateLimit, domain.ProviderUsageScopeModel, name, now)...)
			}
		}
	}
	var credits struct {
		Unlimited bool `json:"unlimited"`
		Balance   any  `json:"balance"`
	}
	if json.Unmarshal(extras.Credits, &credits) == nil {
		balance := strings.TrimSpace(fmt.Sprint(credits.Balance))
		if number, ok := credits.Balance.(float64); ok {
			balance = strings.TrimSuffix(strings.TrimRight(fmt.Sprintf("%.2f", number), "0"), ".")
		}
		if credits.Unlimited {
			usage.Credits = &domain.ProviderAccountCredits{Unlimited: true}
		} else if balancePattern.MatchString(balance) && strings.Trim(balance, "0.") != "" {
			usage.Credits = &domain.ProviderAccountCredits{Balance: balance}
		}
	}
	return usage, nil
}

func claudeWindow(raw json.RawMessage) *claudeUsageWindow {
	var window claudeUsageWindow
	if len(raw) == 0 || json.Unmarshal(raw, &window) != nil || window.Utilization == nil || *window.Utilization < 0 || *window.Utilization > 100 {
		return nil
	}
	return &window
}

func claudeUsage(raw []byte, now time.Time) (domain.ProviderAccountUsage, error) {
	var quota claudeUsageResponse
	if err := json.Unmarshal(raw, &quota); err != nil {
		return domain.ProviderAccountUsage{}, err
	}
	usage := domain.ProviderAccountUsage{Status: "available", CheckedAt: now.UTC()}
	add := func(seconds int64, scope, name string, window *claudeUsageWindow) {
		if window == nil || window.Utilization == nil || *window.Utilization < 0 || *window.Utilization > 100 {
			return
		}
		usage.Windows = append(usage.Windows, domain.ProviderAccountUsageWindow{Name: name, Scope: scope, DurationSeconds: seconds, RemainingFraction: 1 - *window.Utilization/100, ResetTime: window.ResetAt})
	}
	add(fiveHourSeconds, "", "", quota.FiveHour)
	add(weekSeconds, "", "", quota.SevenDay)
	if len(usage.Windows) == 0 {
		return domain.ProviderAccountUsage{}, errors.New("Claude usage response contained no quota data")
	}
	var extras claudeUsageExtras
	_ = json.Unmarshal(raw, &extras)
	add(weekSeconds, domain.ProviderUsageScopeModel, "Opus", claudeWindow(extras.Opus))
	add(weekSeconds, domain.ProviderUsageScopeModel, "Sonnet", claudeWindow(extras.Sonnet))
	// Newer model limits arrive as a list instead of a named field.
	var limits []struct {
		Kind    string   `json:"kind"`
		Percent *float64 `json:"percent"`
		ResetAt string   `json:"resets_at"`
		Scope   struct {
			Model struct {
				Name string `json:"display_name"`
			} `json:"model"`
		} `json:"scope"`
	}
	if json.Unmarshal(extras.Limits, &limits) == nil {
		for _, limit := range limits {
			name := displayText(limit.Scope.Model.Name)
			known := name == ""
			for _, window := range usage.Windows {
				known = known || strings.EqualFold(window.Name, name)
			}
			if limit.Kind == "weekly_scoped" && !known {
				add(weekSeconds, domain.ProviderUsageScopeModel, name, &claudeUsageWindow{Utilization: limit.Percent, ResetAt: limit.ResetAt})
			}
		}
	}
	add(weekSeconds, domain.ProviderUsageScopeCowork, "", claudeWindow(extras.Cowork))
	add(weekSeconds, domain.ProviderUsageScopeApps, "", claudeWindow(extras.Apps))
	var extra struct {
		Enabled bool     `json:"is_enabled"`
		Limit   *float64 `json:"monthly_limit"`
		Used    *float64 `json:"used_credits"`
	}
	if json.Unmarshal(extras.ExtraUsage, &extra) == nil && extra.Enabled {
		usage.ExtraUsage = &domain.ProviderAccountExtraUsage{Enabled: true}
		if extra.Used != nil && *extra.Used > 0 {
			usage.ExtraUsage.UsedCents = int64(*extra.Used)
		}
		if extra.Limit != nil && *extra.Limit > 0 {
			usage.ExtraUsage.LimitCents = int64(*extra.Limit)
		}
	}
	return usage, nil
}

type accountDetails struct {
	AddedAt      string `json:"added_at"`
	RefreshedAt  string `json:"refreshed_at"`
	PausedUntil  string `json:"paused_until"`
	PausedReason string `json:"paused_reason"`
	SignInEnding bool   `json:"sign_in_ending"`
	SignInEndsAt string `json:"sign_in_ends_at"`
	Requests     []struct {
		Success int64 `json:"success"`
		Failed  int64 `json:"failed"`
	} `json:"requests"`
	RenewsAt     any             `json:"renews_at"`
	ResetCredits json.RawMessage `json:"reset_credits"`
	ResetGrants  json.RawMessage `json:"reset_grants"`
	Profile      *struct {
		Plan         string `json:"plan"`
		Tier         string `json:"tier"`
		Organization string `json:"organization"`
	} `json:"profile"`
	TokenStats *struct {
		LatestDay       string `json:"latest_day"`
		LatestDayTokens *int64 `json:"latest_day_tokens"`
		Lifetime        *int64 `json:"lifetime_tokens"`
		PeakDaily       *int64 `json:"peak_daily_tokens"`
		LongestTurn     *int64 `json:"longest_turn_seconds"`
		CurrentStreak   *int64 `json:"current_streak_days"`
		LongestStreak   *int64 `json:"longest_streak_days"`
	} `json:"token_stats"`
}

var dayPattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// applyDetails adds what the helper knows beyond the usage reading. Every part
// is optional: an older helper, or a provider that says nothing, leaves the
// usage reading as it was.
func applyDetails(usage *domain.ProviderAccountUsage, raw []byte, now time.Time) {
	var details accountDetails
	if json.Unmarshal(raw, &details) != nil {
		return
	}
	usage.AddedAt, usage.RefreshedAt = instant(details.AddedAt), instant(details.RefreshedAt)
	if until := instant(details.PausedUntil); future(until, now) {
		usage.PausedUntil, usage.PausedReason = until, details.PausedReason
	}
	if details.SignInEnding {
		usage.SignInEnding = true
		if until := instant(details.SignInEndsAt); future(until, now) {
			usage.SignInEndsAt = until
		}
	}
	for _, bucket := range details.Requests {
		usage.Requests = append(usage.Requests, domain.ProviderAccountRequests{Succeeded: max(bucket.Success, 0), Failed: max(bucket.Failed, 0)})
	}
	usage.RenewsAt = instant(details.RenewsAt)
	if profile := details.Profile; profile != nil {
		if plan := strings.TrimSpace(profile.Plan); plan != "" {
			usage.Plan = plan
		}
		usage.PlanTier = planTierPattern.FindString(strings.TrimSpace(profile.Tier))
		// A personal organization is named after its owner's email.
		if name := displayText(profile.Organization); !strings.Contains(name, "@") {
			usage.Organization = name
		}
	}
	if stats := details.TokenStats; stats != nil {
		usage.Tokens = &domain.ProviderAccountTokens{Lifetime: stats.Lifetime, PeakDaily: stats.PeakDaily, LongestTurnSeconds: stats.LongestTurn, CurrentStreakDays: stats.CurrentStreak, LongestStreakDays: stats.LongestStreak}
		if dayPattern.MatchString(stats.LatestDay) && stats.LatestDayTokens != nil {
			usage.Tokens.LatestDay, usage.Tokens.LatestDayTokens = stats.LatestDay, stats.LatestDayTokens
		}
	}
	applyCodexResetCredits(usage, details.ResetCredits)
	applyClaudeResetGrants(usage, details.ResetGrants, now)
	sort.SliceStable(usage.Resets, func(i, j int) bool {
		a, b := usage.Resets[i].ExpiresAt, usage.Resets[j].ExpiresAt
		return a != "" && (b == "" || a < b)
	})
}

func applyCodexResetCredits(usage *domain.ProviderAccountUsage, raw json.RawMessage) {
	var credits struct {
		Credits []struct {
			Status    string `json:"status"`
			ResetType string `json:"reset_type"`
			Title     string `json:"title"`
			ExpiresAt any    `json:"expires_at"`
		} `json:"credits"`
		Available  *int64 `json:"available_count"`
		Applicable *int64 `json:"applicable_available_count"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &credits) != nil {
		return
	}
	for _, credit := range credits.Credits {
		if credit.Status == "available" && credit.ResetType == "codex_rate_limits" {
			usage.Resets = append(usage.Resets, domain.ProviderAccountReset{Label: displayText(credit.Title), Left: 1, Total: 1, ExpiresAt: instant(credit.ExpiresAt)})
		}
	}
	if usage.ResetCredits == nil && credits.Available != nil {
		usage.ResetCredits = credits.Available
	}
	if credits.Applicable != nil {
		usage.ResetUsable = *credits.Applicable > 0
	}
}

func applyClaudeResetGrants(usage *domain.ProviderAccountUsage, raw json.RawMessage, now time.Time) {
	var status struct {
		Eligible bool `json:"eligible"`
		AtLimit  bool `json:"at_limit"`
		Grants   []struct {
			Label            string `json:"label"`
			Total            int64  `json:"resets_total"`
			Left             int64  `json:"resets_left"`
			StartsAt         string `json:"starts_at"`
			EndsAt           string `json:"ends_at"`
			Paused           bool   `json:"paused"`
			UsableNow        bool   `json:"usable_now"`
			UseRequiresLimit *bool  `json:"use_requires_limit"`
		} `json:"grants"`
		CooldownUntil string `json:"cooldown_until"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &status) != nil || !status.Eligible {
		return
	}
	left := int64(0)
	usable := false
	for _, grant := range status.Grants {
		ended := grant.EndsAt != "" && !future(instant(grant.EndsAt), now)
		if grant.Left <= 0 || grant.Left > grant.Total || ended {
			continue
		}
		left += grant.Left
		needsLimit := grant.UseRequiresLimit == nil || *grant.UseRequiresLimit
		usable = usable || (!grant.Paused && grant.UsableNow && (!needsLimit || status.AtLimit) && !future(instant(grant.StartsAt), now))
		usage.Resets = append(usage.Resets, domain.ProviderAccountReset{Label: displayText(grant.Label), Left: grant.Left, Total: grant.Total, ExpiresAt: instant(grant.EndsAt)})
	}
	usage.ResetCredits = &left
	if until := instant(status.CooldownUntil); future(until, now) {
		usage.ResetBlockedUntil = until
		usable = false
	}
	usage.ResetUsable = usable
}

type accountCall struct {
	AuthID    string `json:"auth_id"`
	Provider  string `json:"provider"`
	RequestID string `json:"request_id,omitempty"`
}

func apiKeyAccount(credentialRef string) bool {
	return strings.HasPrefix(credentialRef, "config-index:")
}

// FetchAccountUsage asks the helper to use CLIProxy's native authenticated
// provider usage request. AO never handles tokens or credential indexes.
func (c *Client) FetchAccountUsage(ctx context.Context, provider, authID, credentialRef string) (domain.ProviderAccountUsage, error) {
	if strings.TrimSpace(authID) == "" || strings.TrimSpace(credentialRef) == "" || (provider != "codex" && provider != "claude") {
		return domain.ProviderAccountUsage{}, errors.New("account is not signed in")
	}
	if err := c.Ensure(ctx); err != nil {
		return domain.ProviderAccountUsage{}, err
	}
	request := accountCall{AuthID: authID, Provider: provider}
	details := make(chan json.RawMessage, 1)
	go func() {
		var raw json.RawMessage
		// Always answer, so the usage reading can never wait on this forever.
		defer func() { details <- raw }()
		// An older helper has no such route; the usage reading stands alone then.
		if err := c.call(ctx, http.MethodPost, "/ao/account-details", request, &raw, nil); err != nil {
			raw = nil
		}
	}()
	now := time.Now()
	// Providers report limits for subscriptions only.
	usage := domain.ProviderAccountUsage{Status: "unavailable", Message: "Usage unavailable", CheckedAt: now.UTC()}
	if !apiKeyAccount(credentialRef) {
		var raw json.RawMessage
		err := c.call(ctx, http.MethodPost, "/ao/account-usage", request, &raw, nil)
		if err == nil && provider == "claude" {
			usage, err = claudeUsage(raw, now)
		} else if err == nil {
			usage, err = codexUsage(raw, now)
		}
		if err != nil {
			return domain.ProviderAccountUsage{}, err
		}
	}
	if raw := <-details; len(raw) > 0 {
		applyDetails(&usage, raw, now)
	}
	return usage, nil
}

// UseAccountReset asks the provider, through the helper, to spend one reset.
func (c *Client) UseAccountReset(ctx context.Context, provider, authID, requestID string) (string, error) {
	if err := c.Ensure(ctx); err != nil {
		return "", err
	}
	var answer struct {
		Outcome string `json:"outcome"`
	}
	// The helper waits for the provider's answer; allow it longer than a read.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 60*time.Second)
	defer cancel()
	if err := c.actions().call(ctx, http.MethodPost, "/ao/account-reset", accountCall{AuthID: authID, Provider: provider, RequestID: requestID}, &answer, nil); err != nil {
		return "", actionError(err)
	}
	switch answer.Outcome {
	case domain.ProviderResetDone, domain.ProviderResetNothing, domain.ProviderResetNone, domain.ProviderResetWait, domain.ProviderResetFailed:
		return answer.Outcome, nil
	}
	return domain.ProviderResetUnknown, nil
}

// ResumeAccount clears the helper's hold on an account.
func (c *Client) ResumeAccount(ctx context.Context, provider, authID string) error {
	if err := c.Ensure(ctx); err != nil {
		return err
	}
	return actionError(c.call(ctx, http.MethodPost, "/ao/account-resume", accountCall{AuthID: authID, Provider: provider}, nil, nil))
}

// RefreshAccountSignIn renews the saved sign-in through CLIProxy.
func (c *Client) RefreshAccountSignIn(ctx context.Context, provider, authID string) error {
	if err := c.Ensure(ctx); err != nil {
		return err
	}
	return actionError(c.call(ctx, http.MethodPost, "/ao/account-refresh", accountCall{AuthID: authID, Provider: provider}, nil, nil))
}

// actions returns a client whose HTTP timeout fits a provider round trip that
// must not be abandoned once sent.
func (c *Client) actions() *Client {
	return &Client{root: c.root, binary: c.binary, state: c.state, http: &http.Client{Timeout: 60 * time.Second, Transport: c.http.Transport}}
}

// actionError names the cases a person can act on: a helper too old to know
// the action, and an account the helper no longer has.
func actionError(err error) error {
	var status statusError
	if errors.As(err, &status) {
		switch status.status {
		case http.StatusNotFound:
			return ports.ErrProviderAccountActionUnavailable
		case http.StatusBadRequest:
			return ports.ErrProviderAccountActionUnavailable
		}
	}
	return err
}
