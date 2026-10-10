package host

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
)

const (
	codexResetCreditsURL = "https://chatgpt.com/backend-api/wham/rate-limit-reset-credits"
	codexResetConsumeURL = "https://chatgpt.com/backend-api/wham/rate-limit-reset-credits/consume"
	// The route Codex's own client reads its account summary from.
	codexProfileURL      = "https://chatgpt.com/backend-api/wham/profiles/me"
	claudeProfileURL     = "https://api.anthropic.com/api/oauth/profile"
	claudeResetGrantsURL = "https://api.anthropic.com/api/oauth/usage?cedar_ember=1&skip_spend=1"
	claudeAPIOrigin      = "https://api.anthropic.com"
	// Anthropic's name for its reset-grant programme.
	claudeResetProgram = "cedar_ember"

	accountDetailTimeout = 4 * time.Second
	// The provider may take this long to confirm a spent reset.
	accountResetTimeout = 25 * time.Second
)

var (
	resetRequestIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	claudeGrantIDPattern  = regexp.MustCompile(`^[a-z0-9_-]{1,40}$`)
	organizationPattern   = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
)

// Outcomes of using a reset. Only resetOutcomeReset means one was spent;
// resetOutcomeUnknown means the provider never confirmed either way.
const (
	resetOutcomeReset   = "reset"
	resetOutcomeNothing = "nothing_to_reset"
	resetOutcomeNone    = "none_available"
	resetOutcomeWait    = "wait"
	resetOutcomeFailed  = "failed"
	resetOutcomeUnknown = "unknown"
)

type accountRequest struct {
	AuthID    string `json:"auth_id"`
	Provider  string `json:"provider"`
	RequestID string `json:"request_id"`
}

func boundAccount(c *gin.Context, manager *coreauth.Manager) (*coreauth.Auth, accountRequest, bool) {
	var body accountRequest
	if err := c.ShouldBindJSON(&body); err != nil || strings.TrimSpace(body.AuthID) == "" || (body.Provider != "codex" && body.Provider != "claude") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid account request"})
		return nil, body, false
	}
	auth, ok := manager.GetByID(strings.TrimSpace(body.AuthID))
	if !ok || auth.Provider != body.Provider {
		c.JSON(http.StatusNotFound, gin.H{"error": "provider account not found"})
		return nil, body, false
	}
	return auth, body, true
}

// providerJSON sends one authenticated provider request through CLIProxy.
func providerJSON(ctx context.Context, manager *coreauth.Manager, auth *coreauth.Auth, method, url string, body []byte) (int, []byte, error) {
	headers := http.Header{"Accept": {"application/json"}}
	if auth.Provider == "claude" {
		// Anthropic rejects subscription tokens without this header.
		headers.Set("anthropic-beta", "oauth-2025-04-20")
	}
	if body != nil {
		headers.Set("Content-Type", "application/json")
	}
	req, err := manager.NewHttpRequest(ctx, auth, method, url, body, headers)
	if err != nil {
		return 0, nil, err
	}
	resp, err := manager.HttpRequest(ctx, auth, req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode, data, err
}

// providerRead returns a provider's JSON answer, or nil when it gave none.
func providerRead(ctx context.Context, manager *coreauth.Manager, auth *coreauth.Auth, url string) json.RawMessage {
	ctx, cancel := context.WithTimeout(ctx, accountDetailTimeout)
	defer cancel()
	status, data, err := providerJSON(ctx, manager, auth, http.MethodGet, url, nil)
	if err != nil || status < http.StatusOK || status >= http.StatusMultipleChoices || !json.Valid(data) {
		return nil
	}
	return data
}

// accountPause reports how long CLIProxy is holding an account back after a
// provider refusal. Sign-in failures and unsupported models are not pauses.
func accountPause(auth *coreauth.Auth, now time.Time) (time.Time, string) {
	var until time.Time
	reason := ""
	for _, view := range coreauth.CooldownSnapshotForAuth(auth, now) {
		switch view.Reason {
		case "model_not_supported", "unauthorized", "invalid_grant":
			continue
		}
		if view.Scope == "credential" {
			return view.RetryAt, view.Reason
		}
		if view.RetryAt.After(until) {
			until, reason = view.RetryAt, view.Reason
		}
	}
	return until, reason
}

// renewalGrace is how far past the moment CLIProxy should have renewed a
// sign-in AO waits before saying it is not renewing. CLIProxy renews within
// seconds of that moment and retries a failure every five minutes, so half an
// hour is several refused attempts and not a slow one.
const renewalGrace = 30 * time.Minute

// signInEnding reports a saved sign-in that has stopped renewing while it still
// works, and when it stops working, if that is known.
//
// CLIProxy renews a sign-in some hours before its token runs out. When the
// provider refuses the renewal, CLIProxy keeps using the token it has for as
// long as it lasts and records nothing an account list can read: the account
// looks healthy, then fails all at once. The lasting evidence is the token
// itself: it is well inside the window in which it should have been replaced.
// The error CLIProxy keeps is no use alone, because the next request that
// succeeds clears it.
func signInEnding(auth *coreauth.Auth, now time.Time) (bool, time.Time) {
	if auth == nil || auth.Disabled || auth.Attributes["api_key"] != "" {
		return false, time.Time{}
	}
	lead := coreauth.ProviderRefreshLead(auth.Provider, auth.Runtime)
	if lead == nil || *lead <= renewalGrace {
		return false, time.Time{}
	}
	// A failure that is not a refusal, such as the provider being out of
	// reach, may pass by itself.
	if failure := auth.LastError; failure != nil && !renewalRefused(failure) {
		return false, time.Time{}
	}
	if expiry, known := auth.ExpirationTime(); known && !expiry.IsZero() {
		if expiry.Sub(now) <= *lead-renewalGrace {
			return true, expiry
		}
		return false, time.Time{}
	}
	// With no expiry to go by, CLIProxy renews one lead after the last renewal.
	if renewed := signInRefreshedAt(auth); !renewed.IsZero() && now.Sub(renewed) >= *lead+renewalGrace {
		return true, time.Time{}
	}
	return false, time.Time{}
}

func renewalRefused(failure *coreauth.Error) bool {
	switch failure.HTTPStatus {
	case http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden:
		return true
	}
	return strings.EqualFold(failure.Code, "unauthorized") || strings.Contains(strings.ToLower(failure.Message), "invalid_grant")
}

// signInRefreshedAt is when the saved sign-in was last renewed. CLIProxy keeps
// the time in memory once it has refreshed; before that, the saved credential
// carries the time of its last refresh.
func signInRefreshedAt(auth *coreauth.Auth) time.Time {
	if !auth.LastRefreshedAt.IsZero() {
		return auth.LastRefreshedAt
	}
	for _, key := range []string{"last_refresh", "lastRefresh", "last_refreshed_at", "lastRefreshedAt"} {
		switch value := auth.Metadata[key].(type) {
		case string:
			if parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(value)); err == nil {
				return parsed
			}
		case float64:
			if value > 0 {
				return time.Unix(int64(value), 0)
			}
		}
	}
	return time.Time{}
}

// codexSubscriptionEnd reads the plan's end date from the saved sign-in token.
func codexSubscriptionEnd(auth *coreauth.Auth) any {
	token, _ := auth.Metadata["id_token"].(string)
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil
	}
	var claims struct {
		Auth struct {
			Until any `json:"chatgpt_subscription_active_until"`
		} `json:"https://api.openai.com/auth"`
	}
	if json.Unmarshal(payload, &claims) != nil {
		return nil
	}
	return claims.Auth.Until
}

// codexTokenStats keeps the provider's token tally from the Codex profile and
// leaves the person's name and username behind.
func codexTokenStats(raw json.RawMessage) gin.H {
	var profile struct {
		Stats struct {
			Lifetime      *float64 `json:"lifetime_tokens"`
			PeakDaily     *float64 `json:"peak_daily_tokens"`
			LongestTurn   *float64 `json:"longest_running_turn_sec"`
			CurrentStreak *float64 `json:"current_streak_days"`
			LongestStreak *float64 `json:"longest_streak_days"`
			Daily         []struct {
				StartDate string  `json:"start_date"`
				Tokens    float64 `json:"tokens"`
			} `json:"daily_usage_buckets"`
		} `json:"stats"`
	}
	if raw == nil || json.Unmarshal(raw, &profile) != nil {
		return nil
	}
	stats := profile.Stats
	out := gin.H{}
	for key, value := range map[string]*float64{"lifetime_tokens": stats.Lifetime, "peak_daily_tokens": stats.PeakDaily, "longest_turn_seconds": stats.LongestTurn, "current_streak_days": stats.CurrentStreak, "longest_streak_days": stats.LongestStreak} {
		if value != nil && *value >= 0 {
			out[key] = int64(*value)
		}
	}
	// The most recent day the provider has counted.
	latest := ""
	for _, day := range stats.Daily {
		if day.StartDate > latest && day.Tokens >= 0 {
			latest = day.StartDate
			out["latest_day"], out["latest_day_tokens"] = day.StartDate, int64(day.Tokens)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

type claudeProfile struct {
	Account struct {
		HasMax *bool `json:"has_claude_max"`
		HasPro *bool `json:"has_claude_pro"`
	} `json:"account"`
	Organization struct {
		UUID               string `json:"uuid"`
		Name               string `json:"name"`
		Type               string `json:"organization_type"`
		RateLimitTier      string `json:"rate_limit_tier"`
		SubscriptionStatus string `json:"subscription_status"`
	} `json:"organization"`
}

// claudePlan keeps the plan facts from the profile and nothing that identifies the person.
func claudePlan(raw json.RawMessage) gin.H {
	var profile claudeProfile
	if raw == nil || json.Unmarshal(raw, &profile) != nil {
		return nil
	}
	plan := ""
	switch {
	case profile.Organization.Type == "claude_team" && profile.Organization.SubscriptionStatus == "active":
		plan = "team"
	case profile.Account.HasMax != nil && *profile.Account.HasMax:
		plan = "max"
	case profile.Account.HasPro != nil && *profile.Account.HasPro:
		plan = "pro"
	case profile.Account.HasMax != nil && profile.Account.HasPro != nil:
		plan = "free"
	}
	out := gin.H{"plan": plan, "tier": profile.Organization.RateLimitTier}
	// A personal organization is named after its owner; only a shared one is named here.
	if profile.Organization.Type == "claude_team" || profile.Organization.Type == "claude_enterprise" {
		out["organization"] = profile.Organization.Name
	}
	return out
}

type claudeGrant struct {
	ID               string `json:"id"`
	Left             int64  `json:"resets_left"`
	StartsAt         string `json:"starts_at"`
	EndsAt           string `json:"ends_at"`
	Paused           bool   `json:"paused"`
	UsableNow        bool   `json:"usable_now"`
	UseRequiresLimit *bool  `json:"use_requires_limit"`
}

type claudeGrantStatus struct {
	Eligible      bool          `json:"eligible"`
	AtLimit       bool          `json:"at_limit"`
	Grants        []claudeGrant `json:"grants"`
	NextGrantID   string        `json:"next_grant_id"`
	CooldownUntil string        `json:"cooldown_until"`
}

func after(value string, now time.Time) bool {
	parsed, err := time.Parse(time.RFC3339, value)
	return err == nil && parsed.After(now)
}

// spendable reports whether the provider would accept this grant now. A grant
// that does not say it is usable is never offered.
func (g claudeGrant) spendable(atLimit bool, now time.Time) bool {
	needsLimit := g.UseRequiresLimit == nil || *g.UseRequiresLimit
	return claudeGrantIDPattern.MatchString(g.ID) && g.Left > 0 && !g.Paused && g.UsableNow &&
		(!needsLimit || atLimit) && !after(g.StartsAt, now) && (g.EndsAt == "" || after(g.EndsAt, now))
}

func readClaudeGrants(ctx context.Context, manager *coreauth.Manager, auth *coreauth.Auth) (json.RawMessage, claudeGrantStatus, bool) {
	var envelope struct {
		Grants json.RawMessage `json:"cedar_ember"`
	}
	var status claudeGrantStatus
	raw := providerRead(ctx, manager, auth, claudeResetGrantsURL)
	if raw == nil || json.Unmarshal(raw, &envelope) != nil || len(envelope.Grants) == 0 || json.Unmarshal(envelope.Grants, &status) != nil {
		return nil, status, false
	}
	return envelope.Grants, status, true
}

// useClaudeReset spends one reset grant. It reads the grant list first so it
// never sends a claim the provider has already said it would refuse.
func useClaudeReset(ctx context.Context, manager *coreauth.Manager, auth *coreauth.Auth, requestID string) string {
	var profile claudeProfile
	raw := providerRead(ctx, manager, auth, claudeProfileURL)
	if raw == nil || json.Unmarshal(raw, &profile) != nil || !organizationPattern.MatchString(profile.Organization.UUID) {
		return resetOutcomeFailed
	}
	_, status, ok := readClaudeGrants(ctx, manager, auth)
	if !ok {
		return resetOutcomeFailed
	}
	now := time.Now()
	if !status.Eligible {
		return resetOutcomeNone
	}
	if after(status.CooldownUntil, now) {
		return resetOutcomeWait
	}
	sort.Slice(status.Grants, func(i, j int) bool { return status.Grants[i].ID < status.Grants[j].ID })
	grantID, left := "", int64(0)
	for _, grant := range status.Grants {
		left += grant.Left
		if grant.spendable(status.AtLimit, now) && (grantID == "" || grant.ID == status.NextGrantID) {
			grantID = grant.ID
		}
	}
	if grantID == "" {
		if left > 0 && !status.AtLimit {
			return resetOutcomeNothing
		}
		return resetOutcomeNone
	}
	body, _ := json.Marshal(gin.H{"program": claudeResetProgram, "grant_id": grantID, "request_id": requestID})
	ctx, cancel := context.WithTimeout(ctx, accountResetTimeout)
	defer cancel()
	code, data, err := providerJSON(ctx, manager, auth, http.MethodPost, claudeAPIOrigin+"/api/organizations/"+strings.ToLower(profile.Organization.UUID)+"/reset_rate_limits", body)
	if err != nil {
		// The claim may have reached the provider before the failure.
		return resetOutcomeUnknown
	}
	switch {
	case code == http.StatusTooManyRequests:
		return resetOutcomeWait
	case code == http.StatusUnauthorized || code == http.StatusForbidden:
		return resetOutcomeFailed
	case code < http.StatusOK || code >= http.StatusMultipleChoices:
		return resetOutcomeUnknown
	}
	var answer struct {
		Result string `json:"result"`
	}
	_ = json.Unmarshal(bytes.TrimSpace(data), &answer)
	switch answer.Result {
	case "reset", "already_used":
		return resetOutcomeReset
	case "not_limited":
		return resetOutcomeNothing
	case "cooldown":
		return resetOutcomeWait
	case "ineligible":
		return resetOutcomeNone
	case "unavailable":
		return resetOutcomeFailed
	}
	return resetOutcomeUnknown
}

// useCodexReset spends one reset credit; the provider picks which.
func useCodexReset(ctx context.Context, manager *coreauth.Manager, auth *coreauth.Auth, requestID string) string {
	body, _ := json.Marshal(gin.H{"redeem_request_id": requestID})
	ctx, cancel := context.WithTimeout(ctx, accountResetTimeout)
	defer cancel()
	code, data, err := providerJSON(ctx, manager, auth, http.MethodPost, codexResetConsumeURL, body)
	if err != nil {
		return resetOutcomeUnknown
	}
	switch {
	case code == http.StatusTooManyRequests:
		return resetOutcomeWait
	case code >= http.StatusInternalServerError:
		return resetOutcomeUnknown
	case code < http.StatusOK || code >= http.StatusMultipleChoices:
		return resetOutcomeFailed
	}
	var answer struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(bytes.TrimSpace(data), &answer)
	switch answer.Code {
	case "reset", "already_redeemed":
		return resetOutcomeReset
	case "nothing_to_reset":
		return resetOutcomeNothing
	case "no_credit":
		return resetOutcomeNone
	}
	return resetOutcomeUnknown
}

// configureAccountDetails adds what Account Manager shows beyond usage, and
// the account actions that run as one background request.
func configureAccountDetails(engine *gin.Engine, manager *coreauth.Manager) {
	engine.POST("/ao/account-details", func(c *gin.Context) {
		auth, body, ok := boundAccount(c, manager)
		if !ok {
			return
		}
		now := time.Now()
		out := gin.H{"requests": auth.RecentRequestsSnapshot(now)}
		if !auth.CreatedAt.IsZero() {
			out["added_at"] = auth.CreatedAt.UTC()
		}
		if refreshed := signInRefreshedAt(auth); !refreshed.IsZero() {
			out["refreshed_at"] = refreshed.UTC()
		}
		if until, reason := accountPause(auth, now); until.After(now) {
			out["paused_until"], out["paused_reason"] = until.UTC(), reason
		}
		if ending, until := signInEnding(auth, now); ending {
			out["sign_in_ending"] = true
			if until.After(now) {
				out["sign_in_ends_at"] = until.UTC()
			}
		}
		if auth.Attributes["api_key"] != "" {
			c.JSON(http.StatusOK, out)
			return
		}
		if body.Provider == "codex" {
			if until := codexSubscriptionEnd(auth); until != nil {
				out["renews_at"] = until
			}
			tokens := make(chan gin.H, 1)
			go func() { tokens <- codexTokenStats(providerRead(c.Request.Context(), manager, auth, codexProfileURL)) }()
			if credits := providerRead(c.Request.Context(), manager, auth, codexResetCreditsURL); credits != nil {
				out["reset_credits"] = credits
			}
			if stats := <-tokens; stats != nil {
				out["token_stats"] = stats
			}
			c.JSON(http.StatusOK, out)
			return
		}
		profile := make(chan gin.H, 1)
		go func() { profile <- claudePlan(providerRead(c.Request.Context(), manager, auth, claudeProfileURL)) }()
		if grants, _, found := readClaudeGrants(c.Request.Context(), manager, auth); found {
			out["reset_grants"] = grants
		}
		if plan := <-profile; plan != nil {
			out["profile"] = plan
		}
		c.JSON(http.StatusOK, out)
	})
	engine.POST("/ao/account-reset", func(c *gin.Context) {
		auth, body, ok := boundAccount(c, manager)
		if !ok {
			return
		}
		if !resetRequestIDPattern.MatchString(body.RequestID) || auth.Attributes["api_key"] != "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid account reset request"})
			return
		}
		// Detached from the caller: once a reset is sent, its answer must be read.
		ctx := context.WithoutCancel(c.Request.Context())
		outcome := ""
		if body.Provider == "claude" {
			outcome = useClaudeReset(ctx, manager, auth, body.RequestID)
		} else {
			outcome = useCodexReset(ctx, manager, auth, body.RequestID)
		}
		if outcome == resetOutcomeReset {
			// The limit is gone at the provider; stop holding the account back here.
			_, _, _ = manager.ResetQuota(ctx, auth.ID)
		}
		c.JSON(http.StatusOK, gin.H{"outcome": outcome})
	})
	engine.POST("/ao/account-resume", func(c *gin.Context) {
		auth, _, ok := boundAccount(c, manager)
		if !ok {
			return
		}
		if _, _, err := manager.ResetQuota(c.Request.Context(), auth.ID); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "account could not be resumed"})
			return
		}
		c.Status(http.StatusNoContent)
	})
	engine.POST("/ao/account-refresh", func(c *gin.Context) {
		auth, _, ok := boundAccount(c, manager)
		if !ok {
			return
		}
		if auth.Attributes["api_key"] != "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "API keys have no sign-in to refresh"})
			return
		}
		if _, err := manager.ForceRefreshAuth(c.Request.Context(), auth.ID); err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": "sign-in could not be refreshed"})
			return
		}
		c.Status(http.StatusNoContent)
	})
}
