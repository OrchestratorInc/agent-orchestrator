package proxyhost

import (
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

var usageNow = time.Date(2030, 1, 1, 12, 0, 0, 0, time.UTC)

func TestCodexUsageReadsEveryLimitAndTheCreditBalance(t *testing.T) {
	usage, err := codexUsage([]byte(`{
		"plan_type": "pro",
		"rate_limit": {"limit_reached": true, "primary_window": {"used_percent": 100, "limit_window_seconds": 18000, "reset_at": 1893502800}, "secondary_window": {"used_percent": 19, "limit_window_seconds": 604800}},
		"code_review_rate_limit": {"primary_window": {"used_percent": 25, "limit_window_seconds": 604800}},
		"additional_rate_limits": [{"limit_name": "GPT-5.3-Codex-Spark", "rate_limit": {"primary_window": {"used_percent": 0, "limit_window_seconds": 18000}}}],
		"credits": {"has_credits": true, "unlimited": false, "balance": "1240"},
		"rate_limit_reset_credits": {"available_count": 2}
	}`), usageNow)
	if err != nil {
		t.Fatal(err)
	}
	if usage.Plan != "pro" || len(usage.Windows) != 4 {
		t.Fatalf("plan %q, windows %+v", usage.Plan, usage.Windows)
	}
	if general := usage.Windows[0]; general.Scope != "" || general.RemainingFraction != 0 || general.ResetTime != "2030-01-01T13:00:00Z" {
		t.Fatalf("general limit = %+v", general)
	}
	if review := usage.Windows[2]; review.Scope != domain.ProviderUsageScopeCodeReview || review.RemainingFraction != 0.75 {
		t.Fatalf("code review limit = %+v", review)
	}
	if model := usage.Windows[3]; model.Scope != domain.ProviderUsageScopeModel || model.Name != "GPT-5.3-Codex-Spark" {
		t.Fatalf("model limit = %+v", model)
	}
	if usage.Credits == nil || usage.Credits.Balance != "1240" {
		t.Fatalf("credits = %+v", usage.Credits)
	}
	// A reached limit with a reset in hand is the one case a reset can be used.
	if usage.ResetCredits == nil || *usage.ResetCredits != 2 || !usage.ResetUsable {
		t.Fatalf("resets = %v usable %v", usage.ResetCredits, usage.ResetUsable)
	}
}

// A field the provider reshapes may cost its own row, never the whole reading.
func TestCodexUsageSurvivesAReshapedOptionalField(t *testing.T) {
	usage, err := codexUsage([]byte(`{
		"plan_type": "plus",
		"rate_limit": {"primary_window": {"used_percent": 30, "limit_window_seconds": 18000}},
		"additional_rate_limits": {"GPT-5.3-Codex-Spark": {"primary_window": {"used_percent": 0}}},
		"credits": "none",
		"rate_limit_reset_credits": {"available_count": 1, "applicable_available_count": 0}
	}`), usageNow)
	if err != nil {
		t.Fatal(err)
	}
	if len(usage.Windows) != 1 || usage.Credits != nil {
		t.Fatalf("usage = %+v", usage)
	}
	// The provider's own word on whether a reset applies wins over the guess.
	if usage.ResetUsable {
		t.Fatal("a reset the provider says does not apply was offered")
	}
}

func TestClaudeUsageReadsScopedLimitsAndExtraUsage(t *testing.T) {
	usage, err := claudeUsage([]byte(`{
		"five_hour": {"utilization": 100, "resets_at": "2030-01-01T14:15:00Z"},
		"seven_day": {"utilization": 42, "resets_at": "2030-01-05T00:00:00Z"},
		"seven_day_opus": {"utilization": 75, "resets_at": "2030-01-05T00:00:00Z"},
		"seven_day_sonnet": null,
		"seven_day_cowork": {"utilization": null, "resets_at": null},
		"limits": [
			{"kind": "weekly_scoped", "percent": 12, "scope": {"model": {"display_name": "Opus"}}},
			{"kind": "weekly_scoped", "percent": 29, "scope": {"model": {"display_name": "Fable"}}},
			{"kind": "session", "percent": 5, "scope": {"model": {"display_name": "Haiku"}}}
		],
		"extra_usage": {"is_enabled": true, "monthly_limit": 5000, "used_credits": 1820, "utilization": 36.4}
	}`), usageNow)
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, window := range usage.Windows {
		names = append(names, window.Scope+":"+window.Name)
	}
	// Opus is listed once, under its named field; limits without a figure are left out.
	want := []string{":", ":", "model:Opus", "model:Fable"}
	if len(names) != len(want) {
		t.Fatalf("windows = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("windows = %v, want %v", names, want)
		}
	}
	if usage.Windows[2].RemainingFraction != 0.25 {
		t.Fatalf("Opus = %+v", usage.Windows[2])
	}
	if usage.ExtraUsage == nil || usage.ExtraUsage.UsedCents != 1820 || usage.ExtraUsage.LimitCents != 5000 {
		t.Fatalf("extra usage = %+v", usage.ExtraUsage)
	}
}

func TestDetailsAddCodexResetsPlanEndAndActivity(t *testing.T) {
	count := int64(2)
	usage := domain.ProviderAccountUsage{Status: "available", Plan: "pro", ResetCredits: &count}
	applyDetails(&usage, []byte(`{
		"added_at": "2029-09-03T08:00:00Z", "refreshed_at": "2030-01-01T11:48:00Z",
		"paused_until": "2030-01-01T12:48:00Z", "paused_reason": "quota",
		"requests": [{"time": "11:40-11:50", "success": 9, "failed": 0}, {"time": "11:50-12:00", "success": 4, "failed": 3}],
		"renews_at": 1920844800,
		"token_stats": {"latest_day": "2030-01-01", "latest_day_tokens": 1240000, "lifetime_tokens": 142300000000, "peak_daily_tokens": 7000000000, "longest_turn_seconds": 158760, "current_streak_days": 6, "longest_streak_days": 23},
		"reset_credits": {"available_count": 2, "applicable_available_count": 1, "credits": [
			{"id": "b", "status": "available", "reset_type": "codex_rate_limits", "expires_at": "2030-02-04T00:00:00Z"},
			{"id": "a", "status": "available", "reset_type": "codex_rate_limits", "expires_at": "2030-01-21T00:00:00Z"},
			{"id": "c", "status": "redeemed", "reset_type": "codex_rate_limits", "expires_at": "2030-01-02T00:00:00Z"}
		]}
	}`), usageNow)
	if usage.AddedAt != "2029-09-03T08:00:00Z" || usage.RefreshedAt != "2030-01-01T11:48:00Z" || usage.RenewsAt != "2030-11-14T00:00:00Z" {
		t.Fatalf("dates = %q %q %q", usage.AddedAt, usage.RefreshedAt, usage.RenewsAt)
	}
	if usage.PausedUntil != "2030-01-01T12:48:00Z" || usage.PausedReason != "quota" {
		t.Fatalf("pause = %q %q", usage.PausedUntil, usage.PausedReason)
	}
	if len(usage.Requests) != 2 || usage.Requests[1] != (domain.ProviderAccountRequests{Succeeded: 4, Failed: 3}) {
		t.Fatalf("requests = %+v", usage.Requests)
	}
	if tokens := usage.Tokens; tokens == nil || tokens.LatestDay != "2030-01-01" || *tokens.LatestDayTokens != 1240000 || *tokens.Lifetime != 142300000000 || *tokens.PeakDaily != 7000000000 || *tokens.LongestTurnSeconds != 158760 || *tokens.CurrentStreakDays != 6 || *tokens.LongestStreakDays != 23 {
		t.Fatalf("tokens = %+v", usage.Tokens)
	}
	// Soonest to expire first; a spent credit is not listed.
	if len(usage.Resets) != 2 || usage.Resets[0].ExpiresAt != "2030-01-21T00:00:00Z" || !usage.ResetUsable {
		t.Fatalf("resets = %+v usable %v", usage.Resets, usage.ResetUsable)
	}
}

func TestDetailsAddClaudePlanAndResetGrants(t *testing.T) {
	grants := func(body string) domain.ProviderAccountUsage {
		usage := domain.ProviderAccountUsage{Status: "available"}
		applyDetails(&usage, []byte(`{"profile": {"plan": "max", "tier": "default_claude_max_20x", "organization": "alice@example.test's Organization"}, "reset_grants": `+body+`}`), usageNow)
		return usage
	}
	usage := grants(`{"eligible": true, "at_limit": true, "grants": [
		{"id": "g1", "label": "Launch\u0007 bonus", "resets_total": 3, "resets_left": 2, "ends_at": "2030-01-30T00:00:00Z", "usable_now": true},
		{"id": "g2", "resets_total": 1, "resets_left": 0, "usable_now": true},
		{"id": "g3", "resets_total": 1, "resets_left": 1, "ends_at": "2029-12-30T00:00:00Z", "usable_now": true}
	]}`)
	if usage.Plan != "max" || usage.PlanTier != "20x" {
		t.Fatalf("plan = %q %q", usage.Plan, usage.PlanTier)
	}
	// A personal organization is named after an email, which must stay off the page.
	if usage.Organization != "" {
		t.Fatalf("organization = %q", usage.Organization)
	}
	if usage.ResetCredits == nil || *usage.ResetCredits != 2 || len(usage.Resets) != 1 || usage.Resets[0].Label != "Launch bonus" || !usage.ResetUsable {
		t.Fatalf("resets = %v %+v usable %v", usage.ResetCredits, usage.Resets, usage.ResetUsable)
	}
	// Most grants can only be spent at the limit, and never during a cooldown.
	if grants(`{"eligible": true, "at_limit": false, "grants": [{"id": "g1", "resets_total": 1, "resets_left": 1, "usable_now": true}]}`).ResetUsable {
		t.Fatal("a reset was offered below the limit")
	}
	cooling := grants(`{"eligible": true, "at_limit": true, "cooldown_until": "2030-01-01T16:00:00Z", "grants": [{"id": "g1", "resets_total": 1, "resets_left": 1, "usable_now": true}]}`)
	if cooling.ResetUsable || cooling.ResetBlockedUntil != "2030-01-01T16:00:00Z" {
		t.Fatalf("cooldown: usable %v until %q", cooling.ResetUsable, cooling.ResetBlockedUntil)
	}
	if ineligible := grants(`{"eligible": false, "grants": [{"id": "g1", "resets_total": 1, "resets_left": 1, "usable_now": true}]}`); ineligible.ResetCredits != nil {
		t.Fatalf("an ineligible account was shown resets: %v", *ineligible.ResetCredits)
	}
}

// An older helper answers the details route with nothing usable.
func TestDetailsFromAnOlderHelperLeaveUsageAlone(t *testing.T) {
	usage := domain.ProviderAccountUsage{Status: "available", Plan: "pro"}
	applyDetails(&usage, []byte(`not json`), usageNow)
	if usage.Plan != "pro" || usage.Requests != nil || usage.Resets != nil {
		t.Fatalf("usage = %+v", usage)
	}
}

func TestDetailsCarryASignInThatHasStoppedRenewing(t *testing.T) {
	now := time.Date(2030, 1, 1, 12, 0, 0, 0, time.UTC)
	usage := domain.ProviderAccountUsage{Status: "available"}
	applyDetails(&usage, []byte(`{"sign_in_ending": true, "sign_in_ends_at": "2030-01-01T15:00:00Z"}`), now)
	if !usage.SignInEnding || usage.SignInEndsAt != "2030-01-01T15:00:00Z" {
		t.Fatalf("usage = %+v", usage)
	}
	// An end already past says nothing useful; the sign-in is still ending.
	usage = domain.ProviderAccountUsage{Status: "available"}
	applyDetails(&usage, []byte(`{"sign_in_ending": true, "sign_in_ends_at": "2030-01-01T09:00:00Z"}`), now)
	if !usage.SignInEnding || usage.SignInEndsAt != "" {
		t.Fatalf("usage = %+v", usage)
	}
	// An end alone, from a helper that did not say the sign-in is ending, is not shown.
	usage = domain.ProviderAccountUsage{Status: "available"}
	applyDetails(&usage, []byte(`{"sign_in_ends_at": "2030-01-01T15:00:00Z"}`), now)
	if usage.SignInEnding || usage.SignInEndsAt != "" {
		t.Fatalf("usage = %+v", usage)
	}
}
