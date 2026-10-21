package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
)

func starterLimits() *domain.PlanLimits {
	return &domain.PlanLimits{
		Plan: "starter", MaxActiveSandboxes: 8, MaxRunningSandboxes: 2, OrchestratorSlots: 1, WindowHours: 6,
		WindowSessionHours: 8, WeeklySessionHours: 20, ManualResetsPerMonth: 1,
	}
}

func subscribe(t *testing.T, store *Store, orgID, status string, limits *domain.PlanLimits) {
	t.Helper()
	start := time.Now().Add(-24 * time.Hour)
	end := start.AddDate(0, 1, 0)
	if _, err := store.ApplySubscription(context.Background(), orgID, SubscriptionUpdate{
		SubscriptionID: "sub_" + orgID[:8], Status: status, Limits: limits, PeriodStart: &start, PeriodEnd: &end,
	}); err != nil {
		t.Fatal(err)
	}
}

// addUsage records session-minutes of running time ending `ago` before now,
// spread over `parallel` sessions running side by side (one session can
// contribute at most one minute per minute).
func addUsage(t *testing.T, admin *pgxpool.Pool, fixture notificationFixture, parallel, minutesEach int, ago time.Duration) {
	t.Helper()
	for range parallel {
		if _, err := admin.Exec(context.Background(),
			`INSERT INTO ao_usage_minutes (org_id, minute, session_id)
			SELECT $1, date_trunc('minute', now() - $3::interval) - (n * interval '1 minute'), $2
			FROM generate_series(0, $4 - 1) AS n
			ON CONFLICT DO NOTHING`,
			fixture.orgID, uuid.NewString(), ago.String(), minutesEach,
		); err != nil {
			t.Fatal(err)
		}
	}
}

func createKind(store *Store, fixture notificationFixture, kind string) (domain.Session, error) {
	return store.CreateSession(context.Background(), domain.Principal{UserID: fixture.userID, Provider: "local"},
		fixture.orgID, "billing-"+uuid.NewString(), 10, domain.CreateSession{
			ProjectID: fixture.projectID, Kind: kind, Harness: "codex", DisplayName: kind, Provider: "freestyle",
		})
}

// billFixtureSandbox moves the fixture's sandbox onto Freestyle, the billed
// provider.
func billFixtureSandbox(t *testing.T, admin *pgxpool.Pool, fixture notificationFixture) {
	t.Helper()
	if _, err := admin.Exec(context.Background(),
		`UPDATE ao_sandboxes SET provider = 'freestyle' WHERE session_id = $1`, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
}

func limitCode(err error) string {
	var limitErr *PlanLimitError
	if errors.As(err, &limitErr) {
		return limitErr.Code
	}
	return ""
}

func TestBillingOffKeepsSessionCreationUnchanged(t *testing.T) {
	store, _, fixture := openNotificationTestStore(t)
	if _, err := createKind(store, fixture, "worker"); err != nil {
		t.Fatalf("billing off must not require a plan: %v", err)
	}
}

func TestPlanSlotsReserveOneOrchestratorAndCountWorkers(t *testing.T) {
	store, admin, fixture := openNotificationTestStore(t)
	billFixtureSandbox(t, admin, fixture)
	store.EnableBilling(7*24*time.Hour, time.Time{})

	if _, err := createKind(store, fixture, "orchestrator"); !errors.Is(err, ErrPlanRequired) {
		t.Fatalf("no subscription: err = %v, want ErrPlanRequired", err)
	}
	subscribe(t, store, fixture.orgID, "active", starterLimits())

	// The fixture already holds one worker session: Starter's single worker
	// slot is taken, but the orchestrator slot is reserved and free.
	if _, err := createKind(store, fixture, "worker"); limitCode(err) != LimitWorkerSlots {
		t.Fatalf("second worker: err = %v, want %s", err, LimitWorkerSlots)
	}
	if _, err := createKind(store, fixture, "orchestrator"); err != nil {
		t.Fatalf("orchestrator into its reserved slot: %v", err)
	}
	if _, err := createKind(store, fixture, "orchestrator"); limitCode(err) != LimitOrchestratorSlots {
		t.Fatalf("second orchestrator: err = %v, want %s", err, LimitOrchestratorSlots)
	}

	pro := starterLimits()
	pro.Plan, pro.MaxActiveSandboxes, pro.MaxRunningSandboxes = "pro", 16, 4
	subscribe(t, store, fixture.orgID, "active", pro)
	if _, err := createKind(store, fixture, "worker"); err != nil {
		t.Fatalf("worker after upgrading to 3 worker slots: %v", err)
	}
}

func TestEntitlementFollowsSubscriptionStatus(t *testing.T) {
	store, admin, fixture := openNotificationTestStore(t)
	store.EnableBilling(7*24*time.Hour, time.Time{})
	limits := starterLimits()
	limits.MaxActiveSandboxes, limits.MaxRunningSandboxes, limits.OrchestratorSlots = 10, 10, 0

	subscribe(t, store, fixture.orgID, "past_due", limits)
	if _, err := createKind(store, fixture, "worker"); err != nil {
		t.Fatalf("past_due within grace must still work: %v", err)
	}
	if _, err := admin.Exec(context.Background(),
		`UPDATE ao_organizations SET past_due_since = now() - interval '8 days' WHERE id = $1`, fixture.orgID); err != nil {
		t.Fatal(err)
	}
	if _, err := createKind(store, fixture, "worker"); !errors.Is(err, ErrPlanRequired) {
		t.Fatalf("past_due beyond grace: err = %v, want ErrPlanRequired", err)
	}
	subscribe(t, store, fixture.orgID, "canceled", nil)
	if _, err := createKind(store, fixture, "worker"); !errors.Is(err, ErrPlanRequired) {
		t.Fatalf("canceled: err = %v, want ErrPlanRequired", err)
	}

	// An internal organization still pays for Freestyle, and a webhook never
	// overwrites its plan.
	if _, err := admin.Exec(context.Background(),
		`UPDATE ao_organizations SET plan = 'internal' WHERE id = $1`, fixture.orgID); err != nil {
		t.Fatal(err)
	}
	subscribe(t, store, fixture.orgID, "canceled", nil)
	if _, err := createKind(store, fixture, "worker"); !errors.Is(err, ErrPlanRequired) {
		t.Fatalf("internal plan, freestyle without a subscription: err = %v, want ErrPlanRequired", err)
	}
	billing, err := store.OrgBilling(context.Background(), domain.Principal{UserID: fixture.userID, Provider: "local"}, fixture.orgID)
	if err != nil || billing.Plan != domain.PlanInternal {
		t.Fatalf("plan = %q, err = %v; a webhook overwrote the internal plan", billing.Plan, err)
	}
}

func TestUsageLimitsAndManualWeeklyReset(t *testing.T) {
	store, admin, fixture := openNotificationTestStore(t)
	store.EnableBilling(7*24*time.Hour, time.Time{})
	principal := domain.Principal{UserID: fixture.userID, Provider: "local"}
	limits := starterLimits()
	limits.MaxActiveSandboxes, limits.MaxRunningSandboxes, limits.OrchestratorSlots = 10, 10, 0
	limits.WindowHours, limits.WindowSessionHours = 5, 6
	subscribe(t, store, fixture.orgID, "active", limits)
	// Anchor the week three days ago, so usage from earlier this week counts.
	if _, err := admin.Exec(context.Background(),
		`UPDATE ao_organizations SET usage_week_anchor = now() - interval '3 days' WHERE id = $1`, fixture.orgID); err != nil {
		t.Fatal(err)
	}

	// 6 session-hours inside the 5-hour window (two sessions for 3 hours):
	// the window is full.
	addUsage(t, admin, fixture, 2, 180, 0)
	_, err := createKind(store, fixture, "worker")
	var limitErr *PlanLimitError
	if !errors.As(err, &limitErr) || limitErr.Code != LimitWindow || limitErr.ResetsAt == nil {
		t.Fatalf("window: err = %v", err)
	}
	if !limitErr.ResetsAt.After(time.Now()) {
		t.Fatalf("window resets in the past: %v", limitErr.ResetsAt)
	}

	// Move that usage out of the window but keep it in the week, and add
	// enough earlier usage to fill the week (20 session-hours).
	if _, err := admin.Exec(context.Background(),
		`UPDATE ao_usage_minutes SET minute = minute - interval '6 hours' WHERE org_id = $1`, fixture.orgID); err != nil {
		t.Fatal(err)
	}
	addUsage(t, admin, fixture, 4, 210, 30*time.Hour)
	if _, err := createKind(store, fixture, "worker"); limitCode(err) != LimitWeekly {
		t.Fatalf("weekly: err = %v, want %s", err, LimitWeekly)
	}
	summary, err := store.BillingSummary(context.Background(), principal, fixture.orgID)
	if err != nil || summary.Usage.WeeklyUsedMinutes < 1200 || summary.Usage.ManualResetsUsed != 0 {
		t.Fatalf("summary = %+v, err = %v", summary.Usage, err)
	}

	// One manual reset clears the week; the second is refused.
	if err := store.ResetWeeklyUsage(context.Background(), principal, fixture.orgID); err != nil {
		t.Fatal(err)
	}
	if _, err := createKind(store, fixture, "worker"); err != nil {
		t.Fatalf("after manual reset: %v", err)
	}
	if err := store.ResetWeeklyUsage(context.Background(), principal, fixture.orgID); !errors.Is(err, ErrNoResetsLeft) {
		t.Fatalf("second reset: err = %v, want ErrNoResetsLeft", err)
	}
	summary, _ = store.BillingSummary(context.Background(), principal, fixture.orgID)
	if summary.Usage.ManualResetsUsed != 1 || summary.Usage.WeeklyUsedMinutes != 0 {
		t.Fatalf("after reset: %+v", summary.Usage)
	}
}

func TestResetRequiresAnAdmin(t *testing.T) {
	store, admin, fixture := openNotificationTestStore(t)
	store.EnableBilling(7*24*time.Hour, time.Time{})
	subscribe(t, store, fixture.orgID, "active", starterLimits())
	if _, err := admin.Exec(context.Background(),
		`UPDATE ao_org_memberships SET role = 'member' WHERE org_id = $1 AND user_id = $2`, fixture.orgID, fixture.userID); err != nil {
		t.Fatal(err)
	}
	err := store.ResetWeeklyUsage(context.Background(), domain.Principal{UserID: fixture.userID, Provider: "local"}, fixture.orgID)
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("member reset: err = %v, want ErrForbidden", err)
	}
}

func TestStripeCustomerLinkIsStableAndLookedUpAcrossOrgs(t *testing.T) {
	store, _, fixture := openNotificationTestStore(t)
	ctx := context.Background()
	customer := "cus_" + uuid.NewString()[:8]
	if err := store.LinkStripeCustomer(ctx, fixture.orgID, customer); err != nil {
		t.Fatal(err)
	}
	if err := store.LinkStripeCustomer(ctx, fixture.orgID, customer); err != nil {
		t.Fatalf("relinking the same customer: %v", err)
	}
	if err := store.LinkStripeCustomer(ctx, fixture.orgID, "cus_other"); !errors.Is(err, ErrConflict) {
		t.Fatalf("a second customer: err = %v, want ErrConflict", err)
	}
	orgID, found, err := store.OrgForStripeCustomer(ctx, customer)
	if err != nil || !found || orgID != fixture.orgID {
		t.Fatalf("lookup = %q %v %v", orgID, found, err)
	}
	if _, found, _ := store.OrgForStripeCustomer(ctx, "cus_unknown"); found {
		t.Fatal("unknown customer found")
	}
}

func TestApplySubscriptionSetsAnchorOnceAndTracksPastDue(t *testing.T) {
	store, admin, fixture := openNotificationTestStore(t)
	ctx := context.Background()
	principal := domain.Principal{UserID: fixture.userID, Provider: "local"}
	subscribe(t, store, fixture.orgID, "active", starterLimits())
	first, _ := store.OrgBilling(ctx, principal, fixture.orgID)
	if first.UsageWeekAnchor == nil || first.Plan != "starter" || first.PastDueSince != nil {
		t.Fatalf("after subscribing: %+v", first)
	}
	if _, err := admin.Exec(ctx,
		`UPDATE ao_organizations SET usage_week_anchor = now() - interval '2 days' WHERE id = $1`, fixture.orgID); err != nil {
		t.Fatal(err)
	}
	subscribe(t, store, fixture.orgID, "past_due", starterLimits())
	pastDue, _ := store.OrgBilling(ctx, principal, fixture.orgID)
	if pastDue.PastDueSince == nil || pastDue.UsageWeekAnchor.After(time.Now().Add(-47*time.Hour)) {
		t.Fatalf("past_due must set its start and keep the week anchor: %+v", pastDue)
	}
	since := *pastDue.PastDueSince
	subscribe(t, store, fixture.orgID, "past_due", starterLimits())
	again, _ := store.OrgBilling(ctx, principal, fixture.orgID)
	if !again.PastDueSince.Equal(since) {
		t.Fatal("a repeated past_due event moved the grace period start")
	}
	subscribe(t, store, fixture.orgID, "active", nil)
	recovered, _ := store.OrgBilling(ctx, principal, fixture.orgID)
	if recovered.PastDueSince != nil || recovered.Limits == nil {
		t.Fatalf("recovery must clear past_due and keep limits when Stripe sent none: %+v", recovered)
	}
}

func TestUsageSamplingAndOverLimitPause(t *testing.T) {
	store, admin, fixture := openNotificationTestStore(t)
	billFixtureSandbox(t, admin, fixture)
	ctx := context.Background()
	store.EnableBilling(7*24*time.Hour, time.Time{})
	recordSelfServe(t, admin, fixture.orgID)
	if _, err := admin.Exec(ctx,
		`UPDATE ao_sandboxes SET desired_state = 'running', observed_state = 'running' WHERE session_id = $1`,
		fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	orgIDs, err := store.SampleUsage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SampleUsage(ctx); err != nil {
		t.Fatal(err)
	}
	var sampled int
	if err := admin.QueryRow(ctx, `SELECT count(*) FROM ao_usage_minutes WHERE org_id = $1`, fixture.orgID).Scan(&sampled); err != nil {
		t.Fatal(err)
	}
	if sampled != 1 || !containsString(orgIDs, fixture.orgID) {
		t.Fatalf("sampled %d rows (want 1, deduplicated), orgs %v", sampled, orgIDs)
	}

	// Within the plan: nothing paused.
	subscribe(t, store, fixture.orgID, "active", starterLimits())
	if _, err := admin.Exec(ctx,
		`UPDATE ao_organizations SET usage_week_anchor = now() - interval '1 day' WHERE id = $1`, fixture.orgID); err != nil {
		t.Fatal(err)
	}
	if paused, err := store.PauseOverLimit(ctx, fixture.orgID); err != nil || paused != 0 {
		t.Fatalf("within plan: paused %d, err %v", paused, err)
	}
	// Over the window, but a turn is in flight: it is allowed to finish.
	addUsage(t, admin, fixture, 2, 250, time.Minute)
	if _, err := admin.Exec(ctx,
		`UPDATE ao_sessions SET activity_state = 'active' WHERE id = $1`, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	if paused, err := store.PauseOverLimit(ctx, fixture.orgID); err != nil || paused != 0 {
		t.Fatalf("mid-turn: paused %d, err %v", paused, err)
	}
	if _, err := admin.Exec(ctx,
		`UPDATE ao_sessions SET activity_state = 'idle' WHERE id = $1`, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	if paused, err := store.PauseOverLimit(ctx, fixture.orgID); err != nil || paused != 1 {
		t.Fatalf("between turns: paused %d, err %v", paused, err)
	}
}

func TestTerminalInteractionDefersIdlePauseAndPresenceNeverWakes(t *testing.T) {
	store, admin, fixture := openNotificationTestStore(t)
	ctx := context.Background()
	principal := domain.Principal{UserID: fixture.userID, Provider: "local"}
	if _, err := admin.Exec(ctx,
		`UPDATE ao_sandboxes SET desired_state = 'running', observed_state = 'running' WHERE session_id = $1;`,
		fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx,
		`UPDATE ao_sessions SET created_at = now() - interval '2 hours', activity_state = 'idle' WHERE id = $1`,
		fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	// Someone interacted 5 minutes ago and the short lease has lapsed: the
	// session is still within a 15-minute idle threshold.
	if _, err := admin.Exec(ctx,
		`UPDATE ao_sandboxes SET last_interaction_at = now() - interval '5 minutes', interactive_until = now() - interval '3 minutes'
		WHERE session_id = $1`, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	if paused, err := store.PauseIfIdle(ctx, fixture.orgID, fixture.sessionID, 15*time.Minute); err != nil || paused {
		t.Fatalf("recent interaction: paused = %v, err = %v", paused, err)
	}
	if _, err := admin.Exec(ctx,
		`UPDATE ao_sandboxes SET last_interaction_at = now() - interval '20 minutes' WHERE session_id = $1`, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	if paused, err := store.PauseIfIdle(ctx, fixture.orgID, fixture.sessionID, 15*time.Minute); err != nil || !paused {
		t.Fatalf("quiet for 20 minutes: paused = %v, err = %v", paused, err)
	}
	// Presence on a paused session records nothing and does not wake it.
	if err := store.TouchSessionPresence(ctx, principal, fixture.orgID, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	var desired string
	if err := admin.QueryRow(ctx, `SELECT desired_state FROM ao_sandboxes WHERE session_id = $1`, fixture.sessionID).Scan(&desired); err != nil {
		t.Fatal(err)
	}
	if desired != "paused" {
		t.Fatalf("presence woke the session: desired_state = %s", desired)
	}
}

// Concurrent creators racing for the plan's last slot: exactly one wins. The
// plan check runs under the organization lock session creation already takes.
func TestConcurrentCreatesCannotBothTakeTheLastSlot(t *testing.T) {
	store, _, fixture := openNotificationTestStore(t)
	store.EnableBilling(7*24*time.Hour, time.Time{})
	subscribe(t, store, fixture.orgID, "active", starterLimits())
	const racers = 6
	results := make(chan error, racers)
	start := make(chan struct{})
	for range racers {
		go func() {
			<-start
			_, err := createKind(store, fixture, "orchestrator")
			results <- err
		}()
	}
	close(start)
	won, refused := 0, 0
	for range racers {
		switch err := <-results; {
		case err == nil:
			won++
		case limitCode(err) == LimitOrchestratorSlots:
			refused++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if won != 1 || refused != racers-1 {
		t.Fatalf("won %d, refused %d; want exactly one orchestrator", won, refused)
	}
}

// Organizations created before the cutoff are grandfathered; newer ones are
// billed.
func TestOrganizationsCreatedBeforeTheCutoffAreExempt(t *testing.T) {
	store, admin, fixture := openNotificationTestStore(t)
	ctx := context.Background()
	if _, err := admin.Exec(ctx,
		`UPDATE ao_organizations SET created_at = now() - interval '1 day' WHERE id = $1`, fixture.orgID); err != nil {
		t.Fatal(err)
	}
	// Freestyle is paid by every organization: grandfathering no longer
	// exempts it.
	store.EnableBilling(7*24*time.Hour, time.Now().Add(-time.Hour))
	if _, err := createKind(store, fixture, "worker"); !errors.Is(err, ErrPlanRequired) {
		t.Fatalf("an org created before the cutoff, freestyle: err = %v, want ErrPlanRequired", err)
	}
	summary, err := store.BillingSummary(ctx, domain.Principal{UserID: fixture.userID, Provider: "local"}, fixture.orgID)
	if err != nil || summary.Exempt || summary.Entitled {
		t.Fatalf("summary = %+v, err = %v; want not exempt", summary, err)
	}
	store.EnableBilling(7*24*time.Hour, time.Now().Add(-48*time.Hour))
	if _, err := createKind(store, fixture, "worker"); !errors.Is(err, ErrPlanRequired) {
		t.Fatalf("an org created after the cutoff: err = %v, want ErrPlanRequired", err)
	}

	// Subscribing opts a grandfathered org into its plan.
	store.EnableBilling(7*24*time.Hour, time.Now().Add(-time.Hour))
	subscribe(t, store, fixture.orgID, "active", starterLimits())
	summary, err = store.BillingSummary(ctx, domain.Principal{UserID: fixture.userID, Provider: "local"}, fixture.orgID)
	if err != nil || summary.Exempt || !summary.Entitled || summary.Usage == nil {
		t.Fatalf("summary = %+v, err = %v; want the subscribed plan to apply", summary, err)
	}
}

// Recent worker-reported activity (a build, a dev server, terminal output)
// keeps a session from idle pause even when nobody is typing.
func TestRecentSandboxActivityDefersIdlePause(t *testing.T) {
	store, admin, fixture := openNotificationTestStore(t)
	ctx := context.Background()
	if _, err := admin.Exec(ctx,
		`UPDATE ao_sandboxes SET desired_state = 'running', observed_state = 'running',
			interactive_until = now() - interval '1 hour', last_interaction_at = now() - interval '1 hour'
		WHERE session_id = $1`, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx,
		`UPDATE ao_sessions SET created_at = now() - interval '2 hours', activity_state = 'idle' WHERE id = $1`,
		fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkSandboxBusy(ctx, fixture.orgID, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	if paused, err := store.PauseIfIdle(ctx, fixture.orgID, fixture.sessionID, 15*time.Minute); err != nil || paused {
		t.Fatalf("busy 0 minutes ago: paused = %v, err = %v", paused, err)
	}
	if _, err := admin.Exec(ctx,
		`UPDATE ao_sandboxes SET last_busy_at = now() - interval '16 minutes' WHERE session_id = $1`, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	if paused, err := store.PauseIfIdle(ctx, fixture.orgID, fixture.sessionID, 15*time.Minute); err != nil || !paused {
		t.Fatalf("idle for 16 minutes: paused = %v, err = %v", paused, err)
	}
}

// Running slots and total VMs are separate limits: a paused session frees its
// running slot (so another may start) but still counts toward the total, and
// waking it again needs a free running slot.
func TestRunningSlotsAndTotalVMsAreSeparateLimits(t *testing.T) {
	store, admin, fixture := openNotificationTestStore(t)
	billFixtureSandbox(t, admin, fixture)
	ctx := context.Background()
	principal := domain.Principal{UserID: fixture.userID, Provider: "local"}
	store.EnableBilling(7*24*time.Hour, time.Time{})
	limits := starterLimits()
	limits.MaxActiveSandboxes, limits.MaxRunningSandboxes, limits.OrchestratorSlots = 3, 2, 1
	subscribe(t, store, fixture.orgID, "active", limits)
	setState := func(sessionID, desired string) {
		t.Helper()
		if _, err := admin.Exec(ctx, `UPDATE ao_sandboxes SET desired_state = $2 WHERE session_id = $1`, sessionID, desired); err != nil {
			t.Fatal(err)
		}
	}
	setState(fixture.sessionID, "running")

	// The fixture's worker holds the only worker running slot.
	if _, err := createKind(store, fixture, "worker"); limitCode(err) != LimitWorkerSlots {
		t.Fatalf("second running worker: err = %v, want %s", err, LimitWorkerSlots)
	}
	// Paused, it frees the running slot but still counts toward the total.
	setState(fixture.sessionID, "paused")
	second, err := createKind(store, fixture, "worker")
	if err != nil {
		t.Fatalf("worker after pausing the first: %v", err)
	}
	// Waking the first now needs the running slot the second holds.
	if err := store.WakeAllowed(ctx, principal, fixture.orgID, fixture.sessionID); limitCode(err) != LimitWorkerSlots {
		t.Fatalf("wake with no running slot: err = %v, want %s", err, LimitWorkerSlots)
	}
	setState(second.ID, "paused")
	if err := store.WakeAllowed(ctx, principal, fixture.orgID, fixture.sessionID); err != nil {
		t.Fatalf("wake with a free running slot: %v", err)
	}
	// Total VMs: orchestrator makes 3 of 3; a fourth session is refused even
	// though the worker running slot is free.
	if _, err := createKind(store, fixture, "orchestrator"); err != nil {
		t.Fatalf("orchestrator: %v", err)
	}
	if _, err := createKind(store, fixture, "worker"); limitCode(err) != LimitTotalSessions {
		t.Fatalf("fourth session: err = %v, want %s", err, LimitTotalSessions)
	}
}

// Time run before subscribing (or while grandfathered) is not charged to the
// new plan's rolling window or week.
func TestUsageBeforeSubscribingIsNotCharged(t *testing.T) {
	store, admin, fixture := openNotificationTestStore(t)
	store.EnableBilling(7*24*time.Hour, time.Time{})
	principal := domain.Principal{UserID: fixture.userID, Provider: "local"}
	addUsage(t, admin, fixture, 2, 60, 30*time.Minute)
	subscribe(t, store, fixture.orgID, "active", starterLimits())
	if _, err := admin.Exec(context.Background(),
		`UPDATE ao_organizations SET usage_week_anchor = now() - interval '20 minutes' WHERE id = $1`, fixture.orgID); err != nil {
		t.Fatal(err)
	}
	addUsage(t, admin, fixture, 1, 10, 0)
	summary, err := store.BillingSummary(context.Background(), principal, fixture.orgID)
	if err != nil || summary.Usage == nil {
		t.Fatalf("summary = %+v, err = %v", summary, err)
	}
	if summary.Usage.WindowUsedMinutes != 10 || summary.Usage.WeeklyUsedMinutes != 10 {
		t.Fatalf("window %d, week %d minutes; want only the 10 since subscribing",
			summary.Usage.WindowUsedMinutes, summary.Usage.WeeklyUsedMinutes)
	}
}

// Only Freestyle sandboxes are billed: a Coder or Docker session needs no plan
// and does not count toward the plan's slots or usage.
func TestOnlyFreestyleSessionsAreBilled(t *testing.T) {
	store, _, fixture := openNotificationTestStore(t)
	ctx := context.Background()
	store.EnableBilling(7*24*time.Hour, time.Time{})
	principal := domain.Principal{UserID: fixture.userID, Provider: "local"}
	if _, err := store.CreateSession(ctx, principal, fixture.orgID, "billing-"+uuid.NewString(), 10, domain.CreateSession{
		ProjectID: fixture.projectID, Kind: "worker", Harness: "codex", DisplayName: "docker worker", Provider: "docker",
	}); err != nil {
		t.Fatalf("an unbilled provider must not need a plan: %v", err)
	}
	if _, err := createKind(store, fixture, "worker"); !errors.Is(err, ErrPlanRequired) {
		t.Fatalf("freestyle without a plan: err = %v, want ErrPlanRequired", err)
	}
	subscribe(t, store, fixture.orgID, "active", starterLimits())
	summary, err := store.BillingSummary(ctx, principal, fixture.orgID)
	if err != nil || summary.Usage == nil || summary.Usage.ActiveWorkers != 0 {
		t.Fatalf("docker sessions must not count toward the plan: %+v, err = %v", summary.Usage, err)
	}
}

// Presence pings never touch a non-Freestyle sandbox.
func TestPresenceLeavesOtherProvidersAlone(t *testing.T) {
	store, admin, fixture := openNotificationTestStore(t)
	ctx := context.Background()
	if _, err := admin.Exec(ctx,
		`UPDATE ao_sandboxes SET provider = 'coder', desired_state = 'running', observed_state = 'running',
			interactive_until = NULL, last_interaction_at = NULL
		WHERE session_id = $1`, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	principal := domain.Principal{UserID: fixture.userID, Provider: "local"}
	if err := store.TouchSessionPresence(ctx, principal, fixture.orgID, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	var touched bool
	if err := admin.QueryRow(ctx,
		`SELECT interactive_until IS NOT NULL OR last_interaction_at IS NOT NULL FROM ao_sandboxes WHERE session_id = $1`,
		fixture.sessionID).Scan(&touched); err != nil {
		t.Fatal(err)
	}
	if touched {
		t.Fatal("presence changed a coder sandbox")
	}
}

// A subscription set to cancel records when it ends; renewing clears it.
func TestSubscriptionCancelAtFollowsStripe(t *testing.T) {
	store, _, fixture := openNotificationTestStore(t)
	ctx := context.Background()
	principal := domain.Principal{UserID: fixture.userID, Provider: "local"}
	ends := time.Now().Add(20 * 24 * time.Hour).UTC().Truncate(time.Second)
	// The same subscription subscribe renews below, so the renewal applies.
	outcome, err := store.ApplySubscription(ctx, fixture.orgID, SubscriptionUpdate{
		SubscriptionID: "sub_" + fixture.orgID[:8], Status: "active", Limits: starterLimits(), CancelAt: &ends,
	})
	if err != nil || outcome != SubscriptionApplied {
		t.Fatalf("outcome = %q err = %v", outcome, err)
	}
	got, err := store.SubscriptionCancelAt(ctx, principal, fixture.orgID)
	if err != nil || got == nil || !got.Equal(ends) {
		t.Fatalf("cancel at = %v, err = %v; want %v", got, err, ends)
	}
	subscribe(t, store, fixture.orgID, "active", starterLimits())
	if got, err = store.SubscriptionCancelAt(ctx, principal, fixture.orgID); err != nil || got != nil {
		t.Fatalf("after renewing: cancel at = %v, err = %v; want none", got, err)
	}
}
