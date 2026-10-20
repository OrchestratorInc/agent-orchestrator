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

// IsPlanRefusal reports whether err is a billing refusal (no plan, or a plan
// limit) rather than a failure.
func IsPlanRefusal(err error) bool {
	var limitErr *PlanLimitError
	return errors.Is(err, ErrPlanRequired) || errors.As(err, &limitErr)
}

// billingPolicy is how session creation treats billing. The zero value turns
// enforcement off, which is what local and self-hosted control planes get.
type billingPolicy struct {
	enforced     bool
	pastDueGrace time.Duration
	// exemptBefore grandfathers organizations created before it, so turning
	// billing on does not block the people already using a deployment.
	exemptBefore time.Time
	// busyGrace is how long the usage enforcer lets a busy session finish its
	// work once its organization is over its plan, before pausing it anyway.
	busyGrace time.Duration
	// paywalled are the features a self-serve plan must include to be used.
	paywalled map[string]bool
	// principalBilling is the requesting principal's organization billing
	// (domain.Principal.OrgBilling), set by forPrincipal. Empty for background
	// work, which uses the value last recorded on the organization.
	principalBilling string
	// defaultBilling is the billing of an organization the principal's WorkOS
	// organization does not describe (a personal organization, or a principal
	// without one): AO_CLOUD_DEFAULT_ORG_BILLING.
	defaultBilling string
}

// defaultBusyGrace is the usage enforcer's grace for busy sessions when none
// is configured.
const defaultBusyGrace = 5 * time.Minute

// EnableBilling turns plan enforcement on for session creation. Organizations
// created before exemptBefore (zero: none) are exempt, like plan internal.
func (s *Store) EnableBilling(pastDueGrace time.Duration, exemptBefore time.Time) {
	s.billing.enforced = true
	s.billing.pastDueGrace = pastDueGrace
	s.billing.exemptBefore = exemptBefore
	if s.billing.busyGrace <= 0 {
		s.billing.busyGrace = defaultBusyGrace
	}
}

// SetBillingBusyGrace sets how long the usage enforcer lets a busy session of
// an over-limit organization run before pausing it. Zero or less restores
// the default.
func (s *Store) SetBillingBusyGrace(grace time.Duration) {
	if grace <= 0 {
		grace = defaultBusyGrace
	}
	s.billing.busyGrace = grace
}

// SetPaywalledFeatures sets the features a self-serve plan must list (Stripe
// product metadata ao_features) for its organization to use them. Empty, the
// default, gates nothing.
func (s *Store) SetPaywalledFeatures(features []string) {
	gated := map[string]bool{}
	for _, feature := range domain.ParseFeatureList(strings.Join(features, ",")) {
		gated[feature] = true
	}
	s.billing.paywalled = gated
}

// forPrincipal is the policy for a request by principal on orgID. The
// principal's WorkOS billing describes only the organization its WorkOS
// organization maps to (TeamOrgID); any other organization, such as the
// user's personal one, gets the deployment default. Work for no principal
// uses the policy unchanged.
func (p billingPolicy) forPrincipal(principal domain.Principal, orgID string) billingPolicy {
	billing := strings.TrimSpace(principal.OrgBilling)
	if billing == "" || principal.TeamOrgID == "" || orgID != principal.TeamOrgID {
		billing = p.defaultBilling
	}
	p.principalBilling = billing
	return p
}

// SetDefaultOrgBilling sets the billing of organizations without WorkOS
// billing metadata (AO_CLOUD_DEFAULT_ORG_BILLING: self_serve or contract).
func (s *Store) SetDefaultOrgBilling(billing string) {
	s.billing.defaultBilling = domain.NormalizeOrgBilling(billing)
}

// forAccess is forPrincipal inside a session-access transaction: a share
// grantee's principal describes its own organization, not the session's.
func (p billingPolicy) forAccess(principal domain.Principal, access sessionAccess, orgID string) billingPolicy {
	if access.Role == "member" {
		return p.forPrincipal(principal, orgID)
	}
	return p
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

// mode is the organization's billing mode: domain.BillingModeSelfServe for
// every organization while billing is enforced. Freestyle is paid by everyone,
// including organizations under a contract (billing=contract), on the internal
// plan or grandfathered; what an organization does not pay for is decided per
// sandbox instead (Coder for an organization with the coder capability; see
// sandboxBilledTx).
func (p billingPolicy) mode(domain.OrgBilling) string {
	if !p.enforced {
		return domain.BillingModeOff
	}
	return domain.BillingModeSelfServe
}

// featureExempt reports whether the organization gets every plan feature
// without a plan: under a contract (billing=contract), on the internal plan, or
// grandfathered. It gates paywalled features only; Freestyle is paid by every
// organization (mode).
func (p billingPolicy) featureExempt(billing domain.OrgBilling) bool {
	if p.exempt(billing) {
		return true
	}
	recorded := p.principalBilling
	if recorded == "" {
		recorded = billing.BillingMode
	}
	return recorded == domain.OrgBillingContract
}

// entitled reports whether the organization's subscription may use its plan
// at now.
func (p billingPolicy) entitled(billing domain.OrgBilling, now time.Time) bool {
	return billing.Limits != nil &&
		domain.SubscriptionEntitled(billing.SubscriptionStatus, billing.PastDueSince, p.pastDueGrace, now)
}

// Keys of ao_organizations.settings that billing keeps its bookkeeping in.
const (
	// settingsBillingMode is the organization's billing as last seen on a
	// principal (domain.OrgBillingContract or domain.BillingModeSelfServe).
	settingsBillingMode = "billing_mode"
	// settingsOverLimitSince is when the usage enforcer first found the
	// organization over its plan, for the busy-session grace (RFC 3339).
	settingsOverLimitSince = "billing_over_limit_since"
	// settingsPendingCheckout is the open Checkout session
	// (domain.PendingCheckout).
	settingsPendingCheckout = "billing_pending_checkout"
	// settingsSubscriptionCreated is when the current subscription was
	// created in Stripe (RFC 3339).
	settingsSubscriptionCreated = "billing_subscription_created"
)

// patchOrgSettingsTx removes keys from and merges set into the
// organization's settings.
func patchOrgSettingsTx(ctx context.Context, tx pgx.Tx, orgID string, remove []string, set map[string]any) error {
	if remove == nil {
		remove = []string{}
	}
	if set == nil {
		set = map[string]any{}
	}
	patch, err := json.Marshal(set)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx,
		`UPDATE ao_organizations SET settings = (settings - $2::text[]) || $3::jsonb WHERE id = $1`,
		orgID, remove, patch)
	return err
}

// recordBillingModeTx stores the principal's organization billing on the
// organization when it changed, so background work without a principal (the
// usage enforcer) treats a contract organization as one.
func (p billingPolicy) recordBillingModeTx(ctx context.Context, tx pgx.Tx, billing *domain.OrgBilling) error {
	if p.principalBilling == "" || p.principalBilling == billing.BillingMode {
		return nil
	}
	if err := patchOrgSettingsTx(ctx, tx, billing.OrgID, nil,
		map[string]any{settingsBillingMode: p.principalBilling}); err != nil {
		return fmt.Errorf("record organization billing: %w", err)
	}
	billing.BillingMode = p.principalBilling
	return nil
}

// lockOrgTx takes the organization lock that serializes plan admission:
// session creation, wakes, the usage enforcer and subscription updates. Take
// it before inserting any row that references the organization: those
// inserts hold key-share locks on it, and two transactions upgrading from
// them deadlock.
func lockOrgTx(ctx context.Context, tx pgx.Tx, orgID string) error {
	var locked string
	err := tx.QueryRow(ctx, `SELECT id FROM ao_organizations WHERE id = $1 FOR UPDATE`, orgID).Scan(&locked)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

// sessionProvider is the sandbox provider a new session gets.
func sessionProvider(requested string) string {
	if provider := strings.ToLower(strings.TrimSpace(requested)); provider != "" {
		return provider
	}
	return sandbox.DefaultProvider
}

// sandboxBilledTx decides whether a new session's sandbox counts against its
// organization's plan. Plans sell Coder and Freestyle capacity, which every
// organization may run on:
//   - Freestyle is always billed, whatever the organization's capabilities or
//     billing arrangement.
//   - Coder is billed unless the organization holds the coder capability. A
//     worker spawned by a parent session on Coder inherits the parent's
//     billing, so an unbilled orchestrator's workers stay unbilled.
//   - Coder on the organization's own deployment (a bring-your-own
//     connection) is never billed: plans sell AO's capacity, not the
//     organization's.
//   - Every other provider (docker, nodeops, ...) is not sold by plans and is
//     not billed. The capability gate (orgAllowsProvider) still decides which
//     organizations may run on it.
func sandboxBilledTx(
	ctx context.Context, tx pgx.Tx, orgID, provider, connectionID string, coderCapability bool, parentSessionID string,
) (bool, error) {
	switch provider {
	case sandbox.ProviderFreestyle:
		return true, nil
	case sandbox.ProviderCoder:
		if connectionID != "" {
			return false, nil
		}
		// Billed unless the organization holds the coder capability; see below.
	default:
		return false, nil
	}
	if parentSessionID != "" {
		var parentBilled bool
		err := tx.QueryRow(ctx,
			`SELECT billed FROM ao_sandboxes WHERE org_id = $1 AND session_id = $2 AND provider = $3`,
			orgID, parentSessionID, provider,
		).Scan(&parentBilled)
		if err == nil {
			return parentBilled, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return false, err
		}
	}
	return !coderCapability, nil
}

// billedSandboxSQL is the predicate for a sandbox (alias sb) that counts
// against its organization's plan; see sandboxBilledTx. Freestyle sandboxes
// from before the billed column existed match on their provider.
const billedSandboxSQL = `(sb.provider = 'freestyle' OR sb.billed)`

// BillingEnforced reports whether plan enforcement is on.
func (s *Store) BillingEnforced() bool { return s.billing.enforced }

func orgBillingTx(ctx context.Context, tx pgx.Tx, orgID string) (domain.OrgBilling, error) {
	billing := domain.OrgBilling{OrgID: orgID}
	var customer, subscription, status, mode, subscriptionCreated *string
	var limits, pending []byte
	err := tx.QueryRow(ctx,
		`SELECT plan, stripe_customer_id, stripe_subscription_id, subscription_status, plan_limits,
			billing_period_start, billing_period_end, usage_week_anchor, past_due_since, created_at,
			settings->>'`+settingsBillingMode+`', settings->'`+settingsPendingCheckout+`',
			settings->>'`+settingsSubscriptionCreated+`'
		FROM ao_organizations WHERE id = $1`,
		orgID,
	).Scan(&billing.Plan, &customer, &subscription, &status, &limits,
		&billing.PeriodStart, &billing.PeriodEnd, &billing.UsageWeekAnchor, &billing.PastDueSince, &billing.CreatedAt,
		&mode, &pending, &subscriptionCreated)
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
	if mode != nil {
		billing.BillingMode = *mode
	}
	if subscriptionCreated != nil {
		if created, err := time.Parse(time.RFC3339, *subscriptionCreated); err == nil {
			billing.SubscriptionCreated = &created
		}
	}
	if len(pending) > 0 {
		var checkout domain.PendingCheckout
		if err := json.Unmarshal(pending, &checkout); err == nil && checkout.ID != "" {
			billing.PendingCheckout = &checkout
		}
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

// RequireOrgMember returns ErrForbidden unless the principal is an active
// member of the organization.
func (s *Store) RequireOrgMember(ctx context.Context, principal domain.Principal, orgID string) error {
	return s.withTenant(ctx, principal, orgID, func(pgx.Tx) error { return nil })
}

// OrgDisplayName returns an organization's name for its Stripe customer.
func (s *Store) OrgDisplayName(ctx context.Context, orgID string) (string, error) {
	var name string
	err := s.withOrg(ctx, orgID, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `SELECT display_name FROM ao_organizations WHERE id = $1`, orgID).Scan(&name)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	})
	return name, err
}

// LinkStripeCustomer records the organization's Stripe customer. Linking the
// same pair again is a no-op; a different customer for an organization that
// already has one is refused, so a webhook can never move an organization.
// An organization that does not exist (deleted) is ErrNotFound.
func (s *Store) LinkStripeCustomer(ctx context.Context, orgID, customerID string) error {
	return s.withOrg(ctx, orgID, func(tx pgx.Tx) error {
		var existing *string
		err := tx.QueryRow(ctx,
			`SELECT stripe_customer_id FROM ao_organizations WHERE id = $1 FOR UPDATE`, orgID,
		).Scan(&existing)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		current := ""
		if existing != nil {
			current = *existing
		}
		return linkStripeCustomerTx(ctx, tx, orgID, current, customerID)
	})
}

// linkStripeCustomerTx links customerID to the organization, whose row the
// caller has locked and whose current customer is current.
func linkStripeCustomerTx(ctx context.Context, tx pgx.Tx, orgID, current, customerID string) error {
	if current == customerID {
		return nil
	}
	if current != "" {
		return fmt.Errorf("organization %s already has Stripe customer %s: %w", orgID, current, ErrConflict)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO ao_stripe_customers (stripe_customer_id, org_id) VALUES ($1, $2)
		ON CONFLICT (stripe_customer_id) DO NOTHING`, customerID, orgID,
	); err != nil {
		return normalizeConstraintError(err)
	}
	_, err := tx.Exec(ctx, `UPDATE ao_organizations SET stripe_customer_id = $2, updated_at = now() WHERE id = $1`,
		orgID, customerID)
	return normalizeConstraintError(err)
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
	// CustomerID is the subscription's customer; a subscription of another
	// customer than the organization's is never applied.
	CustomerID  string
	Status      string
	Limits      *domain.PlanLimits
	PeriodStart *time.Time
	PeriodEnd   *time.Time
	// Created is when Stripe created the subscription, to keep the newest
	// when an organization ends up with two live ones.
	Created *time.Time
	// CancelAt is when a subscription set to cancel ends; nil while it renews.
	CancelAt *time.Time
}

// SubscriptionOutcome is what ApplySubscription did with an update.
type SubscriptionOutcome string

const (
	// SubscriptionApplied: the update was the organization's subscription (or
	// it had none) and is stored.
	SubscriptionApplied SubscriptionOutcome = "applied"
	// SubscriptionReplaced: a live subscription replaced one that has ended.
	SubscriptionReplaced SubscriptionOutcome = "replaced"
	// SubscriptionReplacedDuplicate: the organization has two live
	// subscriptions; the update is the newer one and replaced the other.
	SubscriptionReplacedDuplicate SubscriptionOutcome = "replaced_duplicate"
	// SubscriptionIgnoredDuplicate: the organization has two live
	// subscriptions; the update is the older one and was ignored.
	SubscriptionIgnoredDuplicate SubscriptionOutcome = "ignored_duplicate"
	// SubscriptionIgnoredStale: the update is another subscription than the
	// organization's and is not live (an old, canceled one).
	SubscriptionIgnoredStale SubscriptionOutcome = "ignored_stale"
	// SubscriptionIgnoredForeign: the subscription belongs to another
	// customer than the organization's.
	SubscriptionIgnoredForeign SubscriptionOutcome = "ignored_foreign_customer"
)

// Duplicate reports whether the organization was found with two live
// subscriptions, which needs a person to refund or cancel one.
func (o SubscriptionOutcome) Duplicate() bool {
	return o == SubscriptionReplacedDuplicate || o == SubscriptionIgnoredDuplicate
}

// liveStatus reports whether a subscription in status grants (or may again
// grant) the plan: everything Stripe has not ended.
func liveStatus(status string) bool {
	return status == "active" || status == "trialing" || status == "past_due"
}

// subscriptionOutcome decides what to do with update for an organization in
// state billing.
func subscriptionOutcome(billing domain.OrgBilling, update SubscriptionUpdate) SubscriptionOutcome {
	switch {
	case update.CustomerID != "" && billing.StripeCustomerID != "" && update.CustomerID != billing.StripeCustomerID:
		return SubscriptionIgnoredForeign
	case billing.StripeSubscriptionID == "" || billing.StripeSubscriptionID == update.SubscriptionID:
		return SubscriptionApplied
	case !liveStatus(update.Status):
		return SubscriptionIgnoredStale
	case !liveStatus(billing.SubscriptionStatus):
		return SubscriptionReplaced
	case update.Created != nil && (billing.SubscriptionCreated == nil || update.Created.After(*billing.SubscriptionCreated)):
		return SubscriptionReplacedDuplicate
	default:
		return SubscriptionIgnoredDuplicate
	}
}

// ApplySubscription stores the subscription's state on the organization, if
// it is the organization's subscription (see SubscriptionOutcome). An
// internal (exempt) plan is never overwritten.
func (s *Store) ApplySubscription(ctx context.Context, orgID string, update SubscriptionUpdate) (SubscriptionOutcome, error) {
	return s.ApplySubscriptionLocked(ctx, orgID, func(context.Context, domain.OrgBilling) (SubscriptionUpdate, error) {
		return update, nil
	})
}

// ApplySubscriptionLocked reads a subscription with fetch and applies it like
// ApplySubscription, holding the organization's lock from before the read
// until the write commits. Concurrent webhooks for one organization thus
// apply one at a time, each with the state Stripe reported after the
// previous one was stored, so the last write is the latest state. A missing
// (deleted) organization is ErrNotFound.
func (s *Store) ApplySubscriptionLocked(
	ctx context.Context, orgID string,
	fetch func(ctx context.Context, current domain.OrgBilling) (SubscriptionUpdate, error),
) (SubscriptionOutcome, error) {
	var outcome SubscriptionOutcome
	err := s.withOrg(ctx, orgID, func(tx pgx.Tx) error {
		if err := lockOrgTx(ctx, tx, orgID); err != nil {
			return err
		}
		current, err := orgBillingTx(ctx, tx, orgID)
		if err != nil {
			return err
		}
		update, err := fetch(ctx, current)
		if err != nil {
			return err
		}
		outcome = subscriptionOutcome(current, update)
		switch outcome {
		case SubscriptionApplied, SubscriptionReplaced, SubscriptionReplacedDuplicate:
			return applySubscriptionTx(ctx, tx, orgID, current, update)
		}
		return nil
	})
	return outcome, err
}

func applySubscriptionTx(ctx context.Context, tx pgx.Tx, orgID string, current domain.OrgBilling, update SubscriptionUpdate) error {
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
	entitled := liveStatus(update.Status)
	if !entitled {
		plan = "free"
	}
	if _, err := tx.Exec(ctx,
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
	); err != nil {
		return err
	}
	var remove []string
	set := map[string]any{}
	if update.Created != nil {
		set[settingsSubscriptionCreated] = update.Created.UTC().Format(time.RFC3339)
	} else if current.StripeSubscriptionID != update.SubscriptionID {
		remove = append(remove, settingsSubscriptionCreated)
	}
	if entitled {
		// The subscription exists: any Checkout still open would start a
		// second one.
		remove = append(remove, settingsPendingCheckout)
	}
	return patchOrgSettingsTx(ctx, tx, orgID, remove, set)
}

// CheckoutStart is what starting a Checkout produced: the organization's
// Stripe customer (possibly created just now) and the open session.
type CheckoutStart struct {
	CustomerID string
	// Pending is the Checkout session to record; nil records none.
	Pending *domain.PendingCheckout
	// ClearPending drops the recorded session (it was expired at Stripe).
	ClearPending bool
}

// StartCheckout starts a Checkout for an organization an admin of it asked
// for. start runs under the organization's lock with its current billing
// state, so two requests cannot both find no open Checkout and start one
// each. The customer and Checkout session start returns are recorded even
// when it also returns an error (a customer created before a failed Checkout
// is still the organization's); the error is then returned as is.
func (s *Store) StartCheckout(
	ctx context.Context, principal domain.Principal, orgID string,
	start func(ctx context.Context, state domain.OrgBilling) (CheckoutStart, error),
) (*domain.PendingCheckout, error) {
	var pending *domain.PendingCheckout
	var startErr error
	err := s.withTenant(ctx, principal, orgID, func(tx pgx.Tx) error {
		if err := requireOrgAdmin(ctx, tx, orgID, principal.UserID); err != nil {
			return err
		}
		if err := lockOrgTx(ctx, tx, orgID); err != nil {
			return err
		}
		state, err := orgBillingTx(ctx, tx, orgID)
		if err != nil {
			return err
		}
		started, err := start(ctx, state)
		startErr = err
		if started.CustomerID != "" {
			if err := linkStripeCustomerTx(ctx, tx, orgID, state.StripeCustomerID, started.CustomerID); err != nil {
				return err
			}
		}
		switch {
		case started.Pending != nil:
			if err := patchOrgSettingsTx(ctx, tx, orgID, nil,
				map[string]any{settingsPendingCheckout: started.Pending}); err != nil {
				return err
			}
		case started.ClearPending:
			if err := patchOrgSettingsTx(ctx, tx, orgID, []string{settingsPendingCheckout}, nil); err != nil {
				return err
			}
		}
		pending = started.Pending
		return nil
	})
	if err != nil {
		return nil, err
	}
	return pending, startErr
}

// ClearPendingCheckout forgets the organization's open Checkout session if it
// is checkoutID (it completed or expired). A missing organization is a no-op.
func (s *Store) ClearPendingCheckout(ctx context.Context, orgID, checkoutID string) error {
	return s.withOrg(ctx, orgID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`UPDATE ao_organizations SET settings = settings - $2::text
			WHERE id = $1 AND settings->$2::text->>'id' = $3`,
			orgID, settingsPendingCheckout, checkoutID)
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

// PruneStripeEvents forgets processed webhook events received before before.
// Stripe stops retrying a delivery after three days, so an old id is never
// seen again.
func (s *Store) PruneStripeEvents(ctx context.Context, before time.Time) error {
	return s.withService(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM ao_stripe_events WHERE received_at < $1`, before)
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
		WHERE sb.org_id = $1 AND `+billedSandboxSQL+`
			AND sb.observed_state NOT IN ('deleted', 'deleting', 'terminated', 'failed')`,
		billing.OrgID,
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
	// admitCreate checks a new sandbox: total VMs, a running slot, usage.
	// Restoring a deleted session and retrying a failed startup create one too.
	admitCreate admission = iota
	// admitWake checks waking a paused session: a running slot and usage. It
	// already holds its share of the total.
	admitWake
	// admitUsage checks entitlement and usage only, for the usage enforcer
	// and for a running session whose startup is retried.
	admitUsage
)

// planAdmissionTx decides whether the organization may create or wake a
// session of kind under its plan. For a self-serve organization it takes the
// organization lock (lockOrgTx), so concurrent creators and wakers cannot
// both take the last slot; run it before the transaction inserts rows that
// reference the organization.
func (p billingPolicy) planAdmissionTx(
	ctx context.Context, tx pgx.Tx, orgID, kind string, mode admission, now time.Time,
) error {
	return p.planCheckTx(ctx, tx, orgID, kind, mode, now, true)
}

// planCheckTx is planAdmissionTx; lock false checks without the organization
// lock, for an advisory answer that does not hold up other work.
func (p billingPolicy) planCheckTx(
	ctx context.Context, tx pgx.Tx, orgID, kind string, mode admission, now time.Time, lock bool,
) error {
	if !p.enforced {
		return nil
	}
	billing, err := orgBillingTx(ctx, tx, orgID)
	if err != nil {
		return err
	}
	if lock {
		if err := p.recordBillingModeTx(ctx, tx, &billing); err != nil {
			return err
		}
	}
	if p.mode(billing) != domain.BillingModeSelfServe {
		return nil
	}
	if lock {
		if err := lockOrgTx(ctx, tx, orgID); err != nil {
			return err
		}
		// Re-read under the lock: a concurrent subscription update or reset
		// may have committed while this transaction waited.
		if billing, err = orgBillingTx(ctx, tx, orgID); err != nil {
			return err
		}
	}
	return p.checkPlanTx(ctx, tx, billing, kind, mode, now)
}

// checkPlanTx checks a self-serve organization's plan for mode.
func (p billingPolicy) checkPlanTx(
	ctx context.Context, tx pgx.Tx, billing domain.OrgBilling, kind string, mode admission, now time.Time,
) error {
	if !p.entitled(billing, now) {
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

// wakeKind is which transition to running a wake admission is for.
type wakeKind int

const (
	// wakeFromPaused sets a paused session running (a message, resume, a
	// terminal, a review). Any other state is not woken and needs no check.
	wakeFromPaused wakeKind = iota
	// wakeAny sets any session running, including a deleted one being
	// restored, which gets a new sandbox.
	wakeAny
	// wakeRetry re-runs the startup of a session that is meant to be running.
	wakeRetry
)

// needsNewSandbox reports whether a sandbox in observed state has no VM, so
// running it again provisions one.
func needsNewSandbox(observed string) bool {
	switch observed {
	case "deleted", "deleting", "terminated", "failed":
		return true
	}
	return false
}

// wakeAdmissionTx decides whether the session may be set running under its
// organization's plan. It looks the session up first and returns at once,
// without any lock, for an unbilled provider (Coder, Docker) and for a
// session the transition does not apply to; only a billed session of a
// self-serve organization takes the organization lock. Run it in the
// transaction that sets desired_state, before that transaction inserts rows
// that reference the organization (see planAdmissionTx).
func (p billingPolicy) wakeAdmissionTx(ctx context.Context, tx pgx.Tx, orgID, sessionID string, kind wakeKind) error {
	return p.wakeCheckTx(ctx, tx, orgID, sessionID, kind, true)
}

func (p billingPolicy) wakeCheckTx(ctx context.Context, tx pgx.Tx, orgID, sessionID string, kind wakeKind, lock bool) error {
	if !p.enforced {
		return nil
	}
	var desired, observed, sessionKind string
	var billed bool
	err := tx.QueryRow(ctx,
		`SELECT sb.desired_state, sb.observed_state, se.kind, `+billedSandboxSQL+` FROM ao_sandboxes sb
		JOIN ao_sessions se ON se.org_id = sb.org_id AND se.id = sb.session_id
		WHERE sb.org_id = $1 AND sb.session_id = $2`, orgID, sessionID,
	).Scan(&desired, &observed, &sessionKind, &billed)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if !billed {
		return nil
	}
	fresh := needsNewSandbox(observed)
	mode := admitWake
	switch kind {
	case wakeFromPaused:
		if desired != "paused" {
			return nil
		}
	case wakeAny:
		if desired == "running" && !fresh {
			return nil
		}
	case wakeRetry:
		if desired != "running" {
			return nil
		}
		mode = admitUsage
	}
	if fresh {
		mode = admitCreate
	}
	return p.planCheckTx(ctx, tx, orgID, sessionKind, mode, time.Now(), lock)
}

// messageWakeTx decides whether a message may wake its paused session. A
// refusal is returned when refuse is set (a person or an orchestrator sent
// it, and should see why); otherwise the message is delivered without waking
// the session (false), and runs when someone wakes it under the plan.
func (p billingPolicy) messageWakeTx(ctx context.Context, tx pgx.Tx, orgID, sessionID string, refuse bool) (bool, error) {
	err := p.wakeAdmissionTx(ctx, tx, orgID, sessionID, wakeFromPaused)
	if err == nil {
		return true, nil
	}
	if !refuse && IsPlanRefusal(err) {
		return false, nil
	}
	return false, err
}

// replayOrMessageWakeTx is messageWakeTx for a message sent under
// idempotencyKey. A replay of a message already accepted returns the original
// and changes nothing, so it is never refused (nor does it wake).
func (p billingPolicy) replayOrMessageWakeTx(
	ctx context.Context, tx pgx.Tx, orgID, sessionID, idempotencyKey string, refuse bool,
) (bool, error) {
	if !p.enforced {
		return true, nil
	}
	var replay bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM ao_commands WHERE org_id = $1 AND idempotency_key = $2)`,
		orgID, idempotencyKey,
	).Scan(&replay); err != nil {
		return false, err
	}
	if replay {
		return false, nil
	}
	return p.messageWakeTx(ctx, tx, orgID, sessionID, refuse)
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
	policy := s.billing.forPrincipal(principal, orgID)
	summary := domain.BillingSummary{Enabled: policy.enforced, Features: []string{}}
	err := s.withTenant(ctx, principal, orgID, func(tx pgx.Tx) error {
		billing, err := orgBillingTx(ctx, tx, orgID)
		if err != nil {
			return err
		}
		if policy.enforced {
			if err := policy.recordBillingModeTx(ctx, tx, &billing); err != nil {
				return err
			}
		}
		now := time.Now()
		summary.BillingMode = policy.mode(billing)
		summary.Plan = billing.Plan
		// Exempt stays false: every organization pays for Freestyle (mode).
		// Desktop releases from before that still read it, so it is kept.
		summary.SubscriptionStatus = billing.SubscriptionStatus
		summary.CurrentPeriodEnd = billing.PeriodEnd
		summary.Limits = billing.Limits
		summary.Entitled = policy.entitled(billing, now)
		if billing.Limits != nil && len(billing.Limits.Features) > 0 {
			summary.Features = append([]string(nil), billing.Limits.Features...)
		}
		usage, err := usageTx(ctx, tx, billing, now)
		if err != nil {
			return err
		}
		summary.Usage = &usage
		return nil
	})
	return summary, err
}

// OrgHasFeature reports whether the organization may use a paywalled
// feature. Every feature is available when billing is off, for contract,
// internal and grandfathered organizations, and when the feature is not
// paywalled (AO_CLOUD_PAYWALLED_FEATURES); a self-serve organization
// otherwise needs an entitled plan that lists it (ao_features). It fails
// closed: an error reading the plan denies a paywalled feature.
func (s *Store) OrgHasFeature(ctx context.Context, orgID, feature string) bool {
	feature = strings.ToLower(strings.TrimSpace(feature))
	if !s.billing.enforced || !s.billing.paywalled[feature] {
		return true
	}
	has := false
	err := s.withOrg(ctx, orgID, func(tx pgx.Tx) error {
		billing, err := orgBillingTx(ctx, tx, orgID)
		if err != nil {
			return err
		}
		if s.billing.mode(billing) != domain.BillingModeSelfServe || s.billing.featureExempt(billing) {
			has = true
			return nil
		}
		has = s.billing.entitled(billing, time.Now()) && billing.Limits.HasFeature(feature)
		return nil
	})
	return err == nil && has
}

// ResetWeeklyUsage spends one of the plan's manual resets: the weekly usage
// window starts afresh now. Only organization admins may reset.
func (s *Store) ResetWeeklyUsage(ctx context.Context, principal domain.Principal, orgID string) error {
	policy := s.billing.forPrincipal(principal, orgID)
	return s.withTenant(ctx, principal, orgID, func(tx pgx.Tx) error {
		if err := requireOrgAdmin(ctx, tx, orgID, principal.UserID); err != nil {
			return err
		}
		// Serialize with session creation and concurrent resets.
		if err := lockOrgTx(ctx, tx, orgID); err != nil {
			return err
		}
		billing, err := orgBillingTx(ctx, tx, orgID)
		if err != nil {
			return err
		}
		now := time.Now()
		if policy.mode(billing) != domain.BillingModeSelfServe {
			return nil
		}
		if !policy.entitled(billing, now) {
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

// WakeAllowed reports whether a paused session may be woken under the plan,
// for anyone who may open the session (share grantees included). It is an
// early answer that takes no lock: the transaction that wakes the session
// checks again (wakeAdmissionTx). An unbilled session and a session that is
// not paused are always allowed, without touching the organization.
func (s *Store) WakeAllowed(ctx context.Context, principal domain.Principal, orgID, sessionID string) error {
	if !s.billing.enforced {
		return nil
	}
	return s.withSessionAccess(ctx, principal, orgID, sessionID, func(tx pgx.Tx, access sessionAccess) error {
		return s.billing.forAccess(principal, access, orgID).wakeCheckTx(ctx, tx, orgID, sessionID, wakeFromPaused, false)
	})
}

// SampleUsage records the current minute for every running sandbox and
// returns the organizations that have one, for the usage enforcer.
func (s *Store) SampleUsage(ctx context.Context) ([]string, error) {
	var orgIDs []string
	err := s.withService(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`INSERT INTO ao_usage_minutes (org_id, minute, session_id)
			SELECT sb.org_id, date_trunc('minute', now()), sb.session_id FROM ao_sandboxes sb
			WHERE sb.desired_state = 'running' AND sb.observed_state = 'running' AND `+billedSandboxSQL+`
			ON CONFLICT DO NOTHING`,
		); err != nil {
			return fmt.Errorf("sample usage: %w", err)
		}
		rows, err := tx.Query(ctx,
			`SELECT DISTINCT sb.org_id::text FROM ao_sandboxes sb
			WHERE sb.desired_state = 'running' AND sb.observed_state = 'running' AND `+billedSandboxSQL)
		if err != nil {
			return err
		}
		orgIDs, err = pgx.CollectRows(rows, pgx.RowTo[string])
		return err
	})
	return orgIDs, err
}

// PruneUsage drops usage older than any window still measured. The ledger's
// key leads with the organization, so it finds the organizations with a
// skip scan of that key and deletes each one's old range through it, one
// short transaction each, instead of scanning the whole table.
func (s *Store) PruneUsage(ctx context.Context, before time.Time) error {
	var orgIDs []string
	if err := s.withService(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`WITH RECURSIVE orgs AS (
				(SELECT org_id FROM ao_usage_minutes ORDER BY org_id LIMIT 1)
				UNION ALL
				SELECT (SELECT u.org_id FROM ao_usage_minutes u WHERE u.org_id > orgs.org_id ORDER BY u.org_id LIMIT 1)
				FROM orgs WHERE orgs.org_id IS NOT NULL
			)
			SELECT org_id::text FROM orgs WHERE org_id IS NOT NULL`)
		if err != nil {
			return err
		}
		orgIDs, err = pgx.CollectRows(rows, pgx.RowTo[string])
		return err
	}); err != nil {
		return fmt.Errorf("list usage organizations: %w", err)
	}
	for _, orgID := range orgIDs {
		if err := s.withService(ctx, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `DELETE FROM ao_usage_minutes WHERE org_id = $1 AND minute < $2`, orgID, before)
			return err
		}); err != nil {
			return fmt.Errorf("prune usage for %s: %w", orgID, err)
		}
	}
	return nil
}

// billedRunningSession is a running billed session the usage enforcer may
// pause.
type billedRunningSession struct {
	id              string
	orchestrator    bool
	observedRunning bool
	busy            bool
}

// PauseOverLimit pauses an organization's running billed sessions (Freestyle,
// and Coder without the coder capability; see sandboxBilledTx) when it is over
// its plan, and its newest running billed sessions beyond the plan's running
// slots. Every organization pays for those, contract, internal and
// grandfathered ones included; unbilled sandboxes are never touched.
//
// An idle session is paused at once. A busy one (a turn or review in flight)
// may finish its work: it is paused once the organization has been over its
// plan for the busy grace, even if still busy, so a stream of short turns
// cannot keep an over-limit organization running. Returns how many sessions
// were paused.
func (s *Store) PauseOverLimit(ctx context.Context, orgID string) (int64, error) {
	if !s.billing.enforced {
		return 0, nil
	}
	var paused int64
	err := s.withOrg(ctx, orgID, func(tx pgx.Tx) error {
		billing, err := orgBillingTx(ctx, tx, orgID)
		if err != nil {
			return err
		}
		// The enforcer has no principal to read the organization's billing
		// from. Until a member's request records it, the organization may be
		// a contract one (its sessions predate billing), so it is left alone.
		if billing.BillingMode == "" || s.billing.mode(billing) != domain.BillingModeSelfServe {
			return s.clearOverLimitTx(ctx, tx, orgID)
		}
		if err := lockOrgTx(ctx, tx, orgID); err != nil {
			return err
		}
		if billing, err = orgBillingTx(ctx, tx, orgID); err != nil {
			return err
		}
		now := time.Now()
		verdict := s.billing.checkPlanTx(ctx, tx, billing, "", admitUsage, now)
		if verdict != nil && !IsPlanRefusal(verdict) {
			return verdict
		}
		sessions, err := billedRunningSessionsTx(ctx, tx, orgID)
		if err != nil {
			return err
		}
		var excess []billedRunningSession
		if verdict != nil {
			excess = sessions
		} else {
			excess = beyondRunningSlots(*billing.Limits, sessions)
		}
		if len(excess) == 0 {
			return s.clearOverLimitTx(ctx, tx, orgID)
		}
		var since time.Time
		var recorded *string
		if err := tx.QueryRow(ctx,
			`SELECT settings->>'`+settingsOverLimitSince+`' FROM ao_organizations WHERE id = $1`, orgID,
		).Scan(&recorded); err != nil {
			return err
		}
		if recorded != nil {
			since, _ = time.Parse(time.RFC3339Nano, *recorded)
		}
		if since.IsZero() {
			since = now
			if err := patchOrgSettingsTx(ctx, tx, orgID, nil,
				map[string]any{settingsOverLimitSince: now.UTC().Format(time.RFC3339Nano)}); err != nil {
				return err
			}
		}
		graceOver := now.Sub(since) >= s.billing.busyGrace
		var targets []string
		for _, session := range excess {
			if session.observedRunning && (!session.busy || graceOver) {
				targets = append(targets, session.id)
			}
		}
		if len(targets) == 0 {
			return nil
		}
		tag, err := tx.Exec(ctx,
			`UPDATE ao_sandboxes sb SET desired_state = 'paused', startup_started_at = NULL,
				reconcile_after = now(), updated_at = now()
			WHERE sb.org_id = $1 AND `+billedSandboxSQL+` AND sb.desired_state = 'running'
				AND sb.session_id = ANY($2::uuid[])`,
			orgID, targets)
		if err != nil {
			return err
		}
		paused = tag.RowsAffected()
		return nil
	})
	return paused, err
}

// clearOverLimitTx forgets that the organization was over its plan.
func (s *Store) clearOverLimitTx(ctx context.Context, tx pgx.Tx, orgID string) error {
	_, err := tx.Exec(ctx,
		`UPDATE ao_organizations SET settings = settings - $2::text WHERE id = $1 AND settings ? $2::text`,
		orgID, settingsOverLimitSince)
	return err
}

// billedRunningSessionsTx lists the organization's billed sessions (see
// sandboxBilledTx) that are meant to be running and hold a VM, newest first: still starting (just
// created or woken) before running, then by session creation.
func billedRunningSessionsTx(ctx context.Context, tx pgx.Tx, orgID string) ([]billedRunningSession, error) {
	rows, err := tx.Query(ctx,
		`SELECT sb.session_id::text, se.kind = 'orchestrator', sb.observed_state = 'running',
			se.activity_state = 'active'
			OR EXISTS (
				SELECT 1 FROM ao_turns t
				WHERE t.org_id = sb.org_id AND t.session_id = sb.session_id
					AND t.state IN ('queued', 'provisioning', 'running', 'cancel_requested')
					-- A message queued behind a failed checkout or Setup waits for a retry.
					AND NOT (t.state = 'queued' AND sb.startup_error_code = ANY($2))
			)
			OR EXISTS (
				SELECT 1 FROM ao_review_runs r
				WHERE r.org_id = sb.org_id AND r.review_session_id = sb.session_id AND r.status = 'running'
			)
		FROM ao_sandboxes sb
		JOIN ao_sessions se ON se.org_id = sb.org_id AND se.id = sb.session_id
		WHERE sb.org_id = $1 AND `+billedSandboxSQL+` AND sb.desired_state = 'running'
			AND sb.observed_state NOT IN ('deleted', 'deleting', 'terminated', 'failed')
		ORDER BY (sb.startup_started_at IS NOT NULL) DESC, sb.startup_started_at DESC NULLS LAST,
			se.created_at DESC, sb.session_id`,
		orgID, domain.WorkspaceStartupErrorCodes())
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (billedRunningSession, error) {
		var session billedRunningSession
		err := row.Scan(&session.id, &session.orchestrator, &session.observedRunning, &session.busy)
		return session, err
	})
}

// beyondRunningSlots returns the newest of sessions (listed newest first)
// beyond the plan's running slots: orchestrators beyond their reserved slots
// and workers beyond theirs, or, without reserved slots, any session beyond
// the shared pool.
func beyondRunningSlots(limits domain.PlanLimits, sessions []billedRunningSession) []billedRunningSession {
	var excess []billedRunningSession
	if limits.OrchestratorSlots > 0 {
		orchestrators, workers := 0, 0
		for _, session := range sessions {
			if session.orchestrator {
				orchestrators++
			} else {
				workers++
			}
		}
		extraOrchestrators := orchestrators - limits.OrchestratorSlots
		extraWorkers := workers - limits.WorkerSlots()
		for _, session := range sessions {
			if session.orchestrator && extraOrchestrators > 0 {
				excess = append(excess, session)
				extraOrchestrators--
			} else if !session.orchestrator && extraWorkers > 0 {
				excess = append(excess, session)
				extraWorkers--
			}
		}
		return excess
	}
	if extra := len(sessions) - limits.RunningSlots(); extra > 0 {
		excess = append(excess, sessions[:extra]...)
	}
	return excess
}
