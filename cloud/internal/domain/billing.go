package domain

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// PlanInternal marks an organization that is exempt from billing (the AO team,
// design partners). Set it directly on ao_organizations.plan.
const PlanInternal = "internal"

// Billing modes. Only self-serve organizations are subject to plans, usage
// limits and the usage enforcer.
const (
	// BillingModeOff: Stripe billing is not configured on the deployment, or
	// does not apply to the organization (no billed provider is offered).
	BillingModeOff = "off"
	// BillingModeContract: the organization pays under a contract, is on the
	// internal plan, or is grandfathered.
	BillingModeContract = "contract"
	// BillingModeSelfServe: the organization subscribes to a plan through
	// Stripe Checkout.
	BillingModeSelfServe = "self_serve"
)

// UsageWeek is the length of the weekly usage window.
const UsageWeek = 7 * 24 * time.Hour

// PlanLimits are a billing plan's limits. They are Stripe product metadata,
// cached on the organization when its subscription changes, so a limit is
// changed in the Stripe dashboard without a deploy.
type PlanLimits struct {
	Plan string `json:"plan"`
	// MaxActiveSandboxes counts every session that has a sandbox, running or
	// paused, orchestrators included: the plan's total VMs.
	MaxActiveSandboxes int `json:"maxActiveSandboxes"`
	// MaxRunningSandboxes bounds how many of them run at once.
	MaxRunningSandboxes int `json:"maxRunningSandboxes"`
	// OrchestratorSlots of MaxRunningSandboxes are reserved for orchestrators;
	// workers may run in the rest.
	OrchestratorSlots    int     `json:"orchestratorSlots"`
	WindowHours          int     `json:"windowHours"`
	WindowSessionHours   float64 `json:"windowSessionHours"`
	WeeklySessionHours   float64 `json:"weeklySessionHours"`
	ManualResetsPerMonth int     `json:"manualResetsPerMonth"`
	// Features are the paywalled features the plan includes (Stripe product
	// metadata ao_features, a comma list).
	Features []string `json:"features,omitempty"`
}

// HasFeature reports whether the plan includes feature.
func (l PlanLimits) HasFeature(feature string) bool {
	for _, included := range l.Features {
		if included == feature {
			return true
		}
	}
	return false
}

// Stripe product metadata keys for PlanLimits.
const (
	MetadataPlan                 = "ao_plan"
	MetadataMaxActiveSandboxes   = "ao_max_active_sandboxes"
	MetadataMaxRunningSandboxes  = "ao_max_running_sandboxes"
	MetadataOrchestratorSlots    = "ao_orchestrator_slots"
	MetadataWindowHours          = "ao_window_hours"
	MetadataWindowSessionHours   = "ao_window_session_hours"
	MetadataWeeklySessionHours   = "ao_weekly_session_hours"
	MetadataManualResetsPerMonth = "ao_manual_resets_per_month"
	// MetadataFeatures is optional: a comma list of paywalled features.
	MetadataFeatures = "ao_features"
)

// ParseFeatureList reads a comma list of feature names, lowercased, trimmed
// and deduplicated, in order.
func ParseFeatureList(raw string) []string {
	var features []string
	seen := map[string]bool{}
	for _, part := range strings.Split(raw, ",") {
		feature := strings.ToLower(strings.TrimSpace(part))
		if feature == "" || seen[feature] {
			continue
		}
		seen[feature] = true
		features = append(features, feature)
	}
	return features
}

// ParsePlanLimits reads PlanLimits from Stripe product metadata. Every key is
// required: a product missing one is a dashboard mistake, and guessing a
// limit would either overcharge or give compute away.
func ParsePlanLimits(metadata map[string]string) (PlanLimits, error) {
	var limits PlanLimits
	var problems []string
	limits.Plan = strings.TrimSpace(metadata[MetadataPlan])
	if limits.Plan == "" {
		problems = append(problems, MetadataPlan+" is missing")
	}
	intValue := func(key string, min int, target *int) {
		raw := strings.TrimSpace(metadata[key])
		value, err := strconv.Atoi(raw)
		if err != nil || value < min {
			problems = append(problems, fmt.Sprintf("%s must be an integer >= %d (got %q)", key, min, raw))
			return
		}
		*target = value
	}
	floatValue := func(key string, target *float64) {
		raw := strings.TrimSpace(metadata[key])
		value, err := strconv.ParseFloat(raw, 64)
		if err != nil || value <= 0 {
			problems = append(problems, fmt.Sprintf("%s must be a number > 0 (got %q)", key, raw))
			return
		}
		*target = value
	}
	intValue(MetadataMaxActiveSandboxes, 1, &limits.MaxActiveSandboxes)
	// Optional: plans set up before running caps existed run all their VMs.
	if strings.TrimSpace(metadata[MetadataMaxRunningSandboxes]) == "" {
		limits.MaxRunningSandboxes = limits.MaxActiveSandboxes
	} else {
		intValue(MetadataMaxRunningSandboxes, 1, &limits.MaxRunningSandboxes)
	}
	intValue(MetadataOrchestratorSlots, 0, &limits.OrchestratorSlots)
	intValue(MetadataWindowHours, 1, &limits.WindowHours)
	floatValue(MetadataWindowSessionHours, &limits.WindowSessionHours)
	floatValue(MetadataWeeklySessionHours, &limits.WeeklySessionHours)
	intValue(MetadataManualResetsPerMonth, 0, &limits.ManualResetsPerMonth)
	limits.Features = ParseFeatureList(metadata[MetadataFeatures])
	if len(problems) == 0 && limits.MaxRunningSandboxes > limits.MaxActiveSandboxes {
		problems = append(problems, MetadataMaxRunningSandboxes+" exceeds "+MetadataMaxActiveSandboxes)
	}
	if len(problems) == 0 && limits.OrchestratorSlots > limits.MaxRunningSandboxes {
		problems = append(problems, MetadataOrchestratorSlots+" exceeds "+MetadataMaxRunningSandboxes)
	}
	if len(problems) > 0 {
		return PlanLimits{}, errors.New("invalid plan metadata: " + strings.Join(problems, "; "))
	}
	return limits, nil
}

// WorkerSlots is how many non-orchestrator sessions may run at once.
func (l PlanLimits) WorkerSlots() int {
	return l.RunningSlots() - l.OrchestratorSlots
}

// RunningSlots is how many sessions may run at once.
func (l PlanLimits) RunningSlots() int {
	if l.MaxRunningSandboxes == 0 {
		return l.MaxActiveSandboxes
	}
	return l.MaxRunningSandboxes
}

// WindowMinutes and WeeklyMinutes are the limits in session-minutes, the unit
// the usage ledger records.
func (l PlanLimits) WindowMinutes() int { return int(l.WindowSessionHours * 60) }
func (l PlanLimits) WeeklyMinutes() int { return int(l.WeeklySessionHours * 60) }

// SubscriptionEntitled reports whether a subscription in status may use paid
// features: active or trialing, or past_due within the grace period (a card
// retry is still under way and Stripe has not given up).
func SubscriptionEntitled(status string, pastDueSince *time.Time, grace time.Duration, now time.Time) bool {
	switch status {
	case "active", "trialing":
		return true
	case "past_due":
		return pastDueSince == nil || now.Sub(*pastDueSince) < grace
	}
	return false
}

// UsageWeekBounds returns the current weekly window. Weeks repeat every seven
// days from anchor. A manual reset inside the current week starts the
// window afresh at the reset; the next automatic reset stays on the anchor's
// schedule, so a reset never moves the day the week renews.
func UsageWeekBounds(anchor time.Time, lastReset *time.Time, now time.Time) (start, resetsAt time.Time) {
	if now.Before(anchor) {
		return anchor, anchor.Add(UsageWeek)
	}
	weeks := now.Sub(anchor) / UsageWeek
	start = anchor.Add(weeks * UsageWeek)
	resetsAt = start.Add(UsageWeek)
	if lastReset != nil && lastReset.After(start) && !lastReset.After(now) {
		start = *lastReset
	}
	return start, resetsAt
}

// ResetPeriodStart is when the manual-reset allowance last renewed: the
// subscription's billing period, or the calendar month when Stripe has not
// reported one.
func ResetPeriodStart(periodStart *time.Time, now time.Time) time.Time {
	if periodStart != nil && !periodStart.After(now) {
		return *periodStart
	}
	return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
}

// OrgBilling is an organization's billing state.
type OrgBilling struct {
	OrgID                string
	Plan                 string
	StripeCustomerID     string
	StripeSubscriptionID string
	SubscriptionStatus   string
	Limits               *PlanLimits
	PeriodStart          *time.Time
	PeriodEnd            *time.Time
	UsageWeekAnchor      *time.Time
	PastDueSince         *time.Time
	CreatedAt            time.Time
	// BillingMode is the organization's billing as last recorded from WorkOS
	// (OrgBillingContract, BillingModeSelfServe, or empty when never seen).
	// Background work, which has no principal, decides on it.
	BillingMode string
	// SubscriptionCreated is when the current subscription was created in
	// Stripe, to keep the newest when an organization ends up with two.
	SubscriptionCreated *time.Time
	// PendingCheckout is the open Checkout session started for the
	// organization, if any.
	PendingCheckout *PendingCheckout
}

// PendingCheckout is a Stripe Checkout session that is open for an
// organization. A second Checkout is not started while it is open, so one
// organization cannot end up with two subscriptions.
type PendingCheckout struct {
	ID        string    `json:"id"`
	URL       string    `json:"url"`
	PriceID   string    `json:"priceId"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// Open reports whether the session can still be completed at now, with a
// minute's margin so a link handed out is not about to expire.
func (p *PendingCheckout) Open(now time.Time) bool {
	return p != nil && p.ID != "" && p.ExpiresAt.After(now.Add(time.Minute))
}

// Exempt reports whether billing does not apply to the organization.
func (b OrgBilling) Exempt() bool { return b.Plan == PlanInternal }

// UsageSummary is what an organization has used against its limits.
type UsageSummary struct {
	WindowUsedMinutes    int        `json:"windowUsedMinutes"`
	WindowLimitMinutes   int        `json:"windowLimitMinutes"`
	WindowHours          int        `json:"windowHours"`
	WindowResetsAt       *time.Time `json:"windowResetsAt,omitempty"`
	WeeklyUsedMinutes    int        `json:"weeklyUsedMinutes"`
	WeeklyLimitMinutes   int        `json:"weeklyLimitMinutes"`
	WeeklyResetsAt       *time.Time `json:"weeklyResetsAt,omitempty"`
	ManualResetsAllowed  int        `json:"manualResetsAllowed"`
	ManualResetsUsed     int        `json:"manualResetsUsed"`
	ManualResetsRenewAt  *time.Time `json:"manualResetsRenewAt,omitempty"`
	ActiveOrchestrators  int        `json:"activeOrchestrators"`
	ActiveWorkers        int        `json:"activeWorkers"`
	RunningOrchestrators int        `json:"runningOrchestrators"`
	RunningWorkers       int        `json:"runningWorkers"`
}

// BillingSummary is the billing state the desktop app shows.
type BillingSummary struct {
	// Enabled means Stripe billing is configured on the deployment. Whether
	// plans apply to this organization is BillingMode.
	Enabled bool `json:"enabled"`
	// BillingMode is one of the BillingMode constants; the app shows plans
	// only for BillingModeSelfServe.
	BillingMode        string        `json:"billingMode"`
	Exempt             bool          `json:"exempt"`
	Plan               string        `json:"plan"`
	SubscriptionStatus string        `json:"subscriptionStatus,omitempty"`
	Entitled           bool          `json:"entitled"`
	CurrentPeriodEnd   *time.Time    `json:"currentPeriodEnd,omitempty"`
	Limits             *PlanLimits   `json:"limits,omitempty"`
	Usage              *UsageSummary `json:"usage,omitempty"`
	// Features are the paywalled features the organization's plan includes.
	Features []string `json:"features"`
}
