package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/sandbox"
)

// ErrPlanRequired rejects paid work for an organization without an entitled
// subscription.
var ErrPlanRequired = errors.New("an active plan is required")

// ErrNoResetsLeft rejects a manual usage reset once the plan's allowance for
// the billing period is spent.
var ErrNoResetsLeft = errors.New("no manual usage resets left this billing period")

// Plan limit codes carried by PlanLimitError.
const (
	LimitOrchestratorSlots = "ORCHESTRATOR_SLOTS_FULL"
	LimitWorkerSlots       = "WORKER_SLOTS_FULL"
	LimitTotalSessions     = "SESSION_LIMIT_REACHED"
	LimitWindow            = "WINDOW_LIMIT_REACHED"
	LimitWeekly            = "WEEKLY_LIMIT_REACHED"
)

// PlanLimitError rejects work that would exceed one of the plan's limits.
type PlanLimitError struct {
	Code     string
	Plan     string
	Limit    int
	Used     int
	ResetsAt *time.Time
}

func (e *PlanLimitError) Error() string {
	return fmt.Sprintf("plan limit %s: %d of %d", e.Code, e.Used, e.Limit)
}

// billingPolicy is how session creation treats billing. The zero value turns
// enforcement off, which is what local and self-hosted control planes get.
type billingPolicy struct {
	enforced     bool
	pastDueGrace time.Duration
	// exemptBefore grandfathers organizations created before it, so turning
	// billing on does not block the people already using a deployment.
	exemptBefore time.Time
}

// EnableBilling turns plan enforcement on for session creation. Organizations
// created before exemptBefore (zero: none) are exempt, like plan internal.
func (s *Store) EnableBilling(pastDueGrace time.Duration, exemptBefore time.Time) {
	s.billing = billingPolicy{enforced: true, pastDueGrace: pastDueGrace, exemptBefore: exemptBefore}
}

// exempt reports whether billing does not apply to the organization. A
// grandfathered organization that subscribes opts in: from then on its plan
// applies like anyone else's.
func (p billingPolicy) exempt(billing domain.OrgBilling) bool {
	if billing.Exempt() {
		return true
	}
	return billing.StripeSubscriptionID == "" && !p.exemptBefore.IsZero() && billing.CreatedAt.Before(p.exemptBefore)
}

// sessionProvider is the sandbox provider a new session gets.
func sessionProvider(requested string) string {
	if provider := strings.ToLower(strings.TrimSpace(requested)); provider != "" {
		return provider
	}
	return sandbox.DefaultProvider
}

// billedProvider reports whether sandboxes on provider count against a plan.
// Only Freestyle is billed; Coder and Docker sandboxes run on capacity the
// deployment already pays for.
func billedProvider(provider string) bool { return provider == sandbox.ProviderFreestyle }

// BillingEnforced reports whether plan enforcement is on.
func (s *Store) BillingEnforced() bool { return s.billing.enforced }

func orgBillingTx(ctx context.Context, tx pgx.Tx, orgID string) (domain.OrgBilling, error) {
	billing := domain.OrgBilling{OrgID: orgID}
	var customer, subscription, status *string
	var limits []byte
	err := tx.QueryRow(ctx,
		`SELECT plan, stripe_customer_id, stripe_subscription_id, subscription_status, plan_limits,
			billing_period_start, billing_period_end, usage_week_anchor, past_due_since, created_at
		FROM ao_organizations WHERE id = $1`,
		orgID,
	).Scan(&billing.Plan, &customer, &subscription, &status, &limits,
		&billing.PeriodStart, &billing.PeriodEnd, &billing.UsageWeekAnchor, &billing.PastDueSince, &billing.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.OrgBilling{}, ErrNotFound
	}
	if err != nil {
		return domain.OrgBilling{}, err
	}
	if customer != nil {
		billing.StripeCustomerID = *customer
	}
	if subscription != nil {
		billing.StripeSubscriptionID = *subscription
	}
	if status != nil {
		billing.SubscriptionStatus = *status
	}
	if len(limits) > 0 {
		var parsed domain.PlanLimits
		if err := json.Unmarshal(limits, &parsed); err != nil {
			return domain.OrgBilling{}, fmt.Errorf("decode plan limits: %w", err)
		}
		billing.Limits = &parsed
	}
	return billing, nil
}

// OrgBilling returns an organization's billing state to a member.
func (s *Store) OrgBilling(ctx context.Context, principal domain.Principal, orgID string) (domain.OrgBilling, error) {
	var billing domain.OrgBilling
	err := s.withTenant(ctx, principal, orgID, func(tx pgx.Tx) error {
		var err error
		billing, err = orgBillingTx(ctx, tx, orgID)
		return err
	})
	return billing, err
}

// RequireOrgAdmin returns ErrForbidden unless the principal administers the
// organization.
func (s *Store) RequireOrgAdmin(ctx context.Context, principal domain.Principal, orgID string) error {
	return s.withTenant(ctx, principal, orgID, func(tx pgx.Tx) error {
		return requireOrgAdmin(ctx, tx, orgID, principal.UserID)
	})
}

// OrgDisplayName returns an organization's name for its Stripe customer.
func (s *Store) OrgDisplayName(ctx context.Context, orgID string) (string, error) {
	var name string
	err := s.withOrg(ctx, orgID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT display_name FROM ao_organizations WHERE id = $1`, orgID).Scan(&name)
	})
	return name, err
}

// LinkStripeCustomer records the organization's Stripe customer. Linking the
// same pair again is a no-op; a different customer for an organization that
// already has one is refused, so a webhook can never move an organization.
func (s *Store) LinkStripeCustomer(ctx context.Context, orgID, customerID string) error {
	return s.withOrg(ctx, orgID, func(tx pgx.Tx) error {
		var existing *string
		if err := tx.QueryRow(ctx,
			`SELECT stripe_customer_id FROM ao_organizations WHERE id = $1 FOR UPDATE`, orgID,
		).Scan(&existing); err != nil {
			return err
		}
		if existing != nil && *existing != customerID {
			return fmt.Errorf("organization %s already has Stripe customer %s: %w", orgID, *existing, ErrConflict)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO ao_stripe_customers (stripe_customer_id, org_id) VALUES ($1, $2)
			ON CONFLICT (stripe_customer_id) DO NOTHING`, customerID, orgID,
		); err != nil {
			return normalizeConstraintError(err)
		}
		_, err := tx.Exec(ctx, `UPDATE ao_organizations SET stripe_customer_id = $2, updated_at = now() WHERE id = $1`,
			orgID, customerID)
		return err
	})
}

// OrgForStripeCustomer finds the organization a Stripe customer belongs to.
func (s *Store) OrgForStripeCustomer(ctx context.Context, customerID string) (string, bool, error) {
	var orgID string
	err := s.withService(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT org_id FROM ao_stripe_customers WHERE stripe_customer_id = $1`, customerID,
		).Scan(&orgID)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	return orgID, err == nil, err
}

// SubscriptionUpdate is a subscription's current state as read from Stripe.
type SubscriptionUpdate struct {
	SubscriptionID string
	Status         string
	Limits         *domain.PlanLimits
	PeriodStart    *time.Time
	PeriodEnd      *time.Time
	CancelAt       *time.Time
}

// ApplySubscription stores the subscription's current state on the
// organization. It is idempotent, so replayed or reordered webhooks that each
// re-read Stripe converge on the same row. An internal (exempt) plan is never
// overwritten.
func (s *Store) ApplySubscription(ctx context.Context, orgID string, update SubscriptionUpdate) error {
	var limits []byte
	plan := "free"
	if update.Limits != nil {
		encoded, err := json.Marshal(update.Limits)
		if err != nil {
			return err
		}
		limits = encoded
		plan = update.Limits.Plan
	}
	entitled := update.Status == "active" || update.Status == "trialing" || update.Status == "past_due"
	if !entitled {
		plan = "free"
	}
	return s.withOrg(ctx, orgID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`UPDATE ao_organizations SET
				stripe_subscription_id = $2,
				subscription_status = $3,
				plan_limits = coalesce($4::jsonb, plan_limits),
				plan = CASE WHEN plan = $5 THEN plan ELSE $6 END,
				billing_period_start = $7,
				billing_period_end = $8,
				subscription_cancel_at = $10,
				usage_week_anchor = CASE WHEN $9 THEN coalesce(usage_week_anchor, now()) ELSE usage_week_anchor END,
				past_due_since = CASE
					WHEN $3 = 'past_due' THEN coalesce(past_due_since, now())
					ELSE NULL
				END,
				updated_at = now()
			WHERE id = $1`,
			orgID, update.SubscriptionID, update.Status, limits, domain.PlanInternal, plan,
			update.PeriodStart, update.PeriodEnd, entitled, update.CancelAt,
		)
		return err
	})
}

// StripeEventSeen reports whether a webhook event was already processed.
func (s *Store) StripeEventSeen(ctx context.Context, eventID string) (bool, error) {
	var seen bool
	err := s.withService(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM ao_stripe_events WHERE event_id = $1)`, eventID).Scan(&seen)
	})
	return seen, err
}

// RecordStripeEvent marks a webhook event processed.
func (s *Store) RecordStripeEvent(ctx context.Context, eventID, eventType string) error {
	return s.withService(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO ao_stripe_events (event_id, type) VALUES ($1, $2) ON CONFLICT (event_id) DO NOTHING`,
			eventID, eventType)
		return err
	})
}

// usageTx measures an organization's usage against its plan at now.
func usageTx(ctx context.Context, tx pgx.Tx, billing domain.OrgBilling, now time.Time) (domain.UsageSummary, error) {
	var usage domain.UsageSummary
	if err := tx.QueryRow(ctx,
		`SELECT
			count(*) FILTER (WHERE se.kind = 'orchestrator'),
			count(*) FILTER (WHERE se.kind <> 'orchestrator'),
			count(*) FILTER (WHERE se.kind = 'orchestrator' AND sb.desired_state = 'running'),
			count(*) FILTER (WHERE se.kind <> 'orchestrator' AND sb.desired_state = 'running')
		FROM ao_sandboxes sb
		JOIN ao_sessions se ON se.org_id = sb.org_id AND se.id = sb.session_id
		WHERE sb.org_id = $1 AND sb.provider = $2
			AND sb.observed_state NOT IN ('deleted', 'deleting', 'terminated', 'failed')`,
		billing.OrgID, sandbox.ProviderFreestyle,
	).Scan(&usage.ActiveOrchestrators, &usage.ActiveWorkers,
		&usage.RunningOrchestrators, &usage.RunningWorkers); err != nil {
		return usage, err
	}
	limits := billing.Limits
	if limits == nil {
		return usage, nil
	}
	usage.WindowHours = limits.WindowHours
	usage.WindowLimitMinutes = limits.WindowMinutes()
	usage.WeeklyLimitMinutes = limits.WeeklyMinutes()
	usage.ManualResetsAllowed = limits.ManualResetsPerMonth

	window := time.Duration(limits.WindowHours) * time.Hour
	windowStart := now.Add(-window)
	// Time run before the organization subscribed is not charged to its plan.
	if billing.UsageWeekAnchor != nil && billing.UsageWeekAnchor.After(windowStart) {
		windowStart = *billing.UsageWeekAnchor
	}
	if err := tx.QueryRow(ctx,
		`SELECT count(*) FROM ao_usage_minutes WHERE org_id = $1 AND minute > $2`,
		billing.OrgID, windowStart,
	).Scan(&usage.WindowUsedMinutes); err != nil {
		return usage, err
	}
	if usage.WindowUsedMinutes >= usage.WindowLimitMinutes && usage.WindowLimitMinutes > 0 {
		// The rolling window falls back under the limit once enough of its
		// oldest minutes age out.
		var minute time.Time
		err := tx.QueryRow(ctx,
			`SELECT minute FROM ao_usage_minutes WHERE org_id = $1 AND minute > $2
			ORDER BY minute OFFSET $3 LIMIT 1`,
			billing.OrgID, windowStart, usage.WindowUsedMinutes-usage.WindowLimitMinutes,
		).Scan(&minute)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return usage, err
		}
		if err == nil {
			resets := minute.Add(window + time.Minute)
			usage.WindowResetsAt = &resets
		}
	}

	resetPeriod := domain.ResetPeriodStart(billing.PeriodStart, now)
	var lastReset *time.Time
	if err := tx.QueryRow(ctx,
		`SELECT count(*) FILTER (WHERE reset_at >= $2), max(reset_at)
		FROM ao_usage_resets WHERE org_id = $1`,
		billing.OrgID, resetPeriod,
	).Scan(&usage.ManualResetsUsed, &lastReset); err != nil {
		return usage, err
	}
	if billing.PeriodEnd != nil && billing.PeriodEnd.After(now) {
		renew := *billing.PeriodEnd
		usage.ManualResetsRenewAt = &renew
	} else {
		renew := resetPeriod.AddDate(0, 1, 0)
		usage.ManualResetsRenewAt = &renew
	}

	anchor := now
	if billing.UsageWeekAnchor != nil {
		anchor = *billing.UsageWeekAnchor
	}
	weekStart, weekResets := domain.UsageWeekBounds(anchor, lastReset, now)
	usage.WeeklyResetsAt = &weekResets
	if err := tx.QueryRow(ctx,
		`SELECT count(*) FROM ao_usage_minutes WHERE org_id = $1 AND minute >= $2`,
		billing.OrgID, weekStart,
	).Scan(&usage.WeeklyUsedMinutes); err != nil {
		return usage, err
	}
	return usage, nil
}

// admission is what a plan check is for.
type admission int

const (
	// admitCreate checks a new session: total VMs, a running slot, usage.
	admitCreate admission = iota
	// admitWake checks waking a paused session: a running slot and usage. It
	// already holds its share of the total.
	admitWake
	// admitUsage checks entitlement and usage only, for the usage enforcer.
	admitUsage
)

// planAdmissionTx decides whether the organization may create or wake a
// session of kind under its plan.
func (p billingPolicy) planAdmissionTx(
	ctx context.Context, tx pgx.Tx, orgID, kind string, mode admission, now time.Time,
) error {
	if !p.enforced {
		return nil
	}
	billing, err := orgBillingTx(ctx, tx, orgID)
	if err != nil {
		return err
	}
	if p.exempt(billing) {
		return nil
	}
	if billing.Limits == nil ||
		!domain.SubscriptionEntitled(billing.SubscriptionStatus, billing.PastDueSince, p.pastDueGrace, now) {
		return ErrPlanRequired
	}
	limits := *billing.Limits
	usage, err := usageTx(ctx, tx, billing, now)
	if err != nil {
		return err
	}
	if mode == admitCreate {
		total := usage.ActiveOrchestrators + usage.ActiveWorkers
		if total >= limits.MaxActiveSandboxes {
			return &PlanLimitError{Code: LimitTotalSessions, Plan: limits.Plan, Limit: limits.MaxActiveSandboxes, Used: total}
		}
	}
	if mode == admitCreate || mode == admitWake {
		if kind == "orchestrator" && limits.OrchestratorSlots > 0 {
			if usage.RunningOrchestrators >= limits.OrchestratorSlots {
				return &PlanLimitError{Code: LimitOrchestratorSlots, Plan: limits.Plan,
					Limit: limits.OrchestratorSlots, Used: usage.RunningOrchestrators}
			}
		} else {
			// Without reserved orchestrator slots an orchestrator shares the
			// running pool with workers.
			used, limit := usage.RunningWorkers, limits.WorkerSlots()
			if kind == "orchestrator" {
				used, limit = usage.RunningWorkers+usage.RunningOrchestrators, limits.RunningSlots()
			}
			if used >= limit {
				return &PlanLimitError{Code: LimitWorkerSlots, Plan: limits.Plan, Limit: limit, Used: used}
			}
		}
	}
	if usage.WindowUsedMinutes >= usage.WindowLimitMinutes {
		return &PlanLimitError{Code: LimitWindow, Plan: limits.Plan, Limit: usage.WindowLimitMinutes,
			Used: usage.WindowUsedMinutes, ResetsAt: usage.WindowResetsAt}
	}
	if usage.WeeklyUsedMinutes >= usage.WeeklyLimitMinutes {
		return &PlanLimitError{Code: LimitWeekly, Plan: limits.Plan, Limit: usage.WeeklyLimitMinutes,
			Used: usage.WeeklyUsedMinutes, ResetsAt: usage.WeeklyResetsAt}
	}
	return nil
}

// SubscriptionCancelAt returns when the organization's subscription, set to
// cancel, ends; nil while it renews.
func (s *Store) SubscriptionCancelAt(ctx context.Context, principal domain.Principal, orgID string) (*time.Time, error) {
	var cancelAt *time.Time
	err := s.withTenant(ctx, principal, orgID, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx,
			`SELECT subscription_cancel_at FROM ao_organizations WHERE id = $1`, orgID).Scan(&cancelAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	})
	return cancelAt, err
}

// BillingSummary returns what the desktop app shows on its billing screen.
func (s *Store) BillingSummary(ctx context.Context, principal domain.Principal, orgID string) (domain.BillingSummary, error) {
	summary := domain.BillingSummary{Enabled: s.billing.enforced}
	err := s.withTenant(ctx, principal, orgID, func(tx pgx.Tx) error {
		billing, err := orgBillingTx(ctx, tx, orgID)
		if err != nil {
			return err
		}
		now := time.Now()
		summary.Plan = billing.Plan
		summary.Exempt = s.billing.exempt(billing)
		summary.SubscriptionStatus = billing.SubscriptionStatus
		summary.CurrentPeriodEnd = billing.PeriodEnd
		summary.Limits = billing.Limits
		summary.Entitled = summary.Exempt || (billing.Limits != nil &&
			domain.SubscriptionEntitled(billing.SubscriptionStatus, billing.PastDueSince, s.billing.pastDueGrace, now))
		usage, err := usageTx(ctx, tx, billing, now)
		if err != nil {
			return err
		}
		summary.Usage = &usage
		return nil
	})
	return summary, err
}

// ResetWeeklyUsage spends one of the plan's manual resets: the weekly usage
// window starts afresh now. Only organization admins may reset.
func (s *Store) ResetWeeklyUsage(ctx context.Context, principal domain.Principal, orgID string) error {
	return s.withTenant(ctx, principal, orgID, func(tx pgx.Tx) error {
		if err := requireOrgAdmin(ctx, tx, orgID, principal.UserID); err != nil {
			return err
		}
		// Serialize with session creation and concurrent resets.
		if _, err := tx.Exec(ctx, `SELECT id FROM ao_organizations WHERE id = $1 FOR UPDATE`, orgID); err != nil {
			return err
		}
		billing, err := orgBillingTx(ctx, tx, orgID)
		if err != nil {
			return err
		}
		now := time.Now()
		if s.billing.exempt(billing) {
			return nil
		}
		if billing.Limits == nil ||
			!domain.SubscriptionEntitled(billing.SubscriptionStatus, billing.PastDueSince, s.billing.pastDueGrace, now) {
			return ErrPlanRequired
		}
		var used int
		if err := tx.QueryRow(ctx,
			`SELECT count(*) FROM ao_usage_resets WHERE org_id = $1 AND reset_at >= $2`,
			orgID, domain.ResetPeriodStart(billing.PeriodStart, now),
		).Scan(&used); err != nil {
			return err
		}
		if used >= billing.Limits.ManualResetsPerMonth {
			return ErrNoResetsLeft
		}
		_, err = tx.Exec(ctx,
			`INSERT INTO ao_usage_resets (org_id, reset_at, actor_user_id) VALUES ($1, $2, $3)`,
			orgID, now, principal.UserID)
		return err
	})
}

// WakeAllowed reports whether a paused session may be woken under the plan.
// A running session is always allowed; it already holds its slot and its
// usage is enforced by the usage enforcer.
func (s *Store) WakeAllowed(ctx context.Context, principal domain.Principal, orgID, sessionID string) error {
	if !s.billing.enforced {
		return nil
	}
	return s.withTenant(ctx, principal, orgID, func(tx pgx.Tx) error {
		// Serialize with session creation, so two wakes cannot both take the
		// last running slot.
		if _, err := tx.Exec(ctx, `SELECT id FROM ao_organizations WHERE id = $1 FOR UPDATE`, orgID); err != nil {
			return err
		}
		var desired, kind, provider string
		err := tx.QueryRow(ctx,
			`SELECT sb.desired_state, se.kind, sb.provider FROM ao_sandboxes sb
			JOIN ao_sessions se ON se.org_id = sb.org_id AND se.id = sb.session_id
			WHERE sb.org_id = $1 AND sb.session_id = $2`, orgID, sessionID,
		).Scan(&desired, &kind, &provider)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && (desired == "running" || !billedProvider(provider))) {
			return nil
		}
		if err != nil {
			return err
		}
		return s.billing.planAdmissionTx(ctx, tx, orgID, kind, admitWake, time.Now())
	})
}

// SampleUsage records the current minute for every running sandbox and
// returns the organizations that have one, for the usage enforcer.
func (s *Store) SampleUsage(ctx context.Context) ([]string, error) {
	var orgIDs []string
	err := s.withService(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`INSERT INTO ao_usage_minutes (org_id, minute, session_id)
			SELECT org_id, date_trunc('minute', now()), session_id FROM ao_sandboxes
			WHERE desired_state = 'running' AND observed_state = 'running' AND provider = $1
			ON CONFLICT DO NOTHING`, sandbox.ProviderFreestyle,
		); err != nil {
			return fmt.Errorf("sample usage: %w", err)
		}
		rows, err := tx.Query(ctx,
			`SELECT DISTINCT org_id::text FROM ao_sandboxes
			WHERE desired_state = 'running' AND observed_state = 'running' AND provider = $1`, sandbox.ProviderFreestyle)
		if err != nil {
			return err
		}
		orgIDs, err = pgx.CollectRows(rows, pgx.RowTo[string])
		return err
	})
	return orgIDs, err
}

// PruneUsage drops usage older than any window still measured.
func (s *Store) PruneUsage(ctx context.Context, before time.Time) error {
	return s.withService(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM ao_usage_minutes WHERE minute < $1`, before)
		return err
	})
}

// PauseOverLimit pauses an organization's running sessions when it is over
// its plan (no entitled subscription, or a usage limit reached). A session is
// paused only between turns: work in flight is allowed to finish and is
// paused on a later pass. Returns how many sessions were paused.
func (s *Store) PauseOverLimit(ctx context.Context, orgID string) (int64, error) {
	if !s.billing.enforced {
		return 0, nil
	}
	var paused int64
	err := s.withOrg(ctx, orgID, func(tx pgx.Tx) error {
		err := s.billing.planAdmissionTx(ctx, tx, orgID, "", admitUsage, time.Now())
		var limitErr *PlanLimitError
		if err == nil || !(errors.Is(err, ErrPlanRequired) || errors.As(err, &limitErr)) {
			return err
		}
		tag, err := tx.Exec(ctx,
			`UPDATE ao_sandboxes SET desired_state = 'paused', startup_started_at = NULL,
				reconcile_after = now(), updated_at = now()
			WHERE org_id = $1
				AND provider = $2
				AND desired_state = 'running'
				AND observed_state = 'running'
				AND NOT EXISTS (
					SELECT 1 FROM ao_sessions se
					WHERE se.org_id = ao_sandboxes.org_id AND se.id = ao_sandboxes.session_id
						AND se.activity_state = 'active'
				)
				AND NOT EXISTS (
					SELECT 1 FROM ao_turns t
					WHERE t.org_id = ao_sandboxes.org_id AND t.session_id = ao_sandboxes.session_id
						AND t.state IN ('queued', 'provisioning', 'running', 'cancel_requested')
				)
				AND NOT EXISTS (
					SELECT 1 FROM ao_review_runs r
					WHERE r.org_id = ao_sandboxes.org_id AND r.review_session_id = ao_sandboxes.session_id
						AND r.status = 'running'
				)`,
			orgID, sandbox.ProviderFreestyle)
		if err != nil {
			return err
		}
		paused = tag.RowsAffected()
		return nil
	})
	return paused, err
}
