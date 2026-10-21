package postgres

import (
	"context"
	"errors"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
)

// openRLSStore opens a second store on the test database as a role without
// BYPASSRLS, which is how the control plane runs: every policy applies.
func openRLSStore(t *testing.T, admin *pgxpool.Pool) *Store {
	t.Helper()
	ctx := context.Background()
	role := "ao_billing_rls_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	quoted := pgx.Identifier{role}.Sanitize()
	for _, statement := range []string{
		"CREATE ROLE " + quoted + " LOGIN PASSWORD 'rls' NOSUPERUSER NOBYPASSRLS",
		"GRANT USAGE ON SCHEMA public TO " + quoted,
		"GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO " + quoted,
		"GRANT USAGE, SELECT, UPDATE ON ALL SEQUENCES IN SCHEMA public TO " + quoted,
	} {
		if _, err := admin.Exec(ctx, statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
	parsed, err := url.Parse(os.Getenv("AO_CLOUD_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	parsed.User = url.UserPassword(role, "rls")
	store, err := Open(ctx, parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		store.Close()
		_, _ = admin.Exec(context.Background(), "DROP OWNED BY "+quoted)
		_, _ = admin.Exec(context.Background(), "DROP ROLE "+quoted)
	})
	var bypass bool
	if err := store.pool.QueryRow(ctx, `SELECT rolbypassrls OR rolsuper FROM pg_roles WHERE rolname = current_user`).Scan(&bypass); err != nil || bypass {
		t.Fatalf("test role bypasses RLS: %v %v", bypass, err)
	}
	return store
}

// billedPaused puts the fixture's session on Freestyle, paused.
func billedPaused(t *testing.T, admin *pgxpool.Pool, fixture notificationFixture) {
	t.Helper()
	if _, err := admin.Exec(context.Background(),
		`UPDATE ao_sandboxes SET provider = 'freestyle', desired_state = 'paused', observed_state = 'stopped'
		WHERE session_id = $1`, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
}

// recordSelfServe records the organization as self-serve, as a member's
// request would, so the usage enforcer applies its plan.
func recordSelfServe(t *testing.T, admin *pgxpool.Pool, orgID string) {
	t.Helper()
	if _, err := admin.Exec(context.Background(),
		`UPDATE ao_organizations SET settings = settings || jsonb_build_object('billing_mode', $2::text) WHERE id = $1`,
		orgID, domain.BillingModeSelfServe); err != nil {
		t.Fatal(err)
	}
}

func desiredState(t *testing.T, admin *pgxpool.Pool, sessionID string) string {
	t.Helper()
	var desired string
	if err := admin.QueryRow(context.Background(),
		`SELECT desired_state FROM ao_sandboxes WHERE session_id = $1`, sessionID).Scan(&desired); err != nil {
		t.Fatal(err)
	}
	return desired
}

// Every path that sets a billed session running is refused for a self-serve
// organization without a plan, in the transaction that would wake it, and
// leaves the session paused.
func TestEveryWakePathIsRefusedWithoutAPlan(t *testing.T) {
	_, admin, fixture := openNotificationTestStore(t)
	store := openRLSStore(t, admin)
	store.EnableBilling(7*24*time.Hour, time.Time{})
	ctx := context.Background()
	principal := domain.Principal{UserID: fixture.userID, Provider: "local"}

	pr, err := store.CreatePullRequestRecord(ctx, fixture.orgID, fixture.sessionID, "github", "owner/repo", "author", 1,
		"https://github.test/owner/repo/pull/1", "feature", "main", "sha-1", "Review", 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}

	paths := map[string]func() error{
		"send message": func() error {
			_, err := store.SendMessage(ctx, principal, fixture.orgID, fixture.sessionID, "wake-"+uuid.NewString(), "hi",
				domain.ChatTurnSettings{})
			return err
		},
		"resume": func() error {
			_, err := store.ResumeSession(ctx, principal, fixture.orgID, fixture.sessionID)
			return err
		},
		"wake all paused": func() error {
			_, err := store.WakePausedSessions(ctx, principal, fixture.orgID)
			return err
		},
		"terminal ticket": func() error {
			_, _, err := store.IssueTerminalTicket(ctx, principal, fixture.orgID, fixture.sessionID, "workspace", "", time.Minute)
			return err
		},
		"set running": func() error {
			return store.SetSandboxDesiredState(ctx, principal, fixture.orgID, fixture.sessionID, "running")
		},
		"review": func() error {
			run, created, err := store.CreateReviewRun(ctx, fixture.orgID, pr.ID, fixture.sessionID, "sha-1", "", "manual")
			if err != nil || !created {
				t.Fatalf("create review run: created=%v err=%v", created, err)
			}
			defer func() { _, _ = store.FailReviewRun(ctx, fixture.orgID, run.ID, fixture.sessionID, "test") }()
			_, err = store.OpenReviewTerminal(ctx, fixture.orgID, fixture.sessionID, run.ID, "Review", "")
			return err
		},
	}
	for name, wake := range paths {
		billedPaused(t, admin, fixture)
		if err := wake(); !errors.Is(err, ErrPlanRequired) {
			t.Errorf("%s: err = %v, want ErrPlanRequired", name, err)
		}
		if desired := desiredState(t, admin, fixture.sessionID); desired != "paused" {
			t.Errorf("%s woke the session: desired_state = %s", name, desired)
		}
	}
	var messages int
	if err := admin.QueryRow(ctx, `SELECT count(*) FROM ao_commands WHERE org_id = $1 AND kind = 'message.send'`,
		fixture.orgID).Scan(&messages); err != nil || messages != 0 {
		t.Fatalf("a refused message was recorded: %d %v", messages, err)
	}

	// Startup retry provisions a VM for a failed session that is meant to run.
	if _, err := admin.Exec(ctx,
		`UPDATE ao_sandboxes SET desired_state = 'running', observed_state = 'failed', worker_last_seen_at = NULL
		WHERE session_id = $1`, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	if err := store.RetrySessionStartup(ctx, principal, fixture.orgID, fixture.sessionID); !errors.Is(err, ErrPlanRequired) {
		t.Errorf("startup retry: err = %v, want ErrPlanRequired", err)
	}

	// Restore re-provisions a deleted session.
	if _, err := admin.Exec(ctx,
		`UPDATE ao_sandboxes SET desired_state = 'deleted', observed_state = 'deleted' WHERE session_id = $1;`,
		fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `UPDATE ao_sessions SET is_terminated = true WHERE id = $1`, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	if err := store.RestoreSession(ctx, principal, fixture.orgID, fixture.sessionID); !errors.Is(err, ErrPlanRequired) {
		t.Errorf("restore: err = %v, want ErrPlanRequired", err)
	}
	var terminated bool
	if err := admin.QueryRow(ctx, `SELECT is_terminated FROM ao_sessions WHERE id = $1`, fixture.sessionID).Scan(&terminated); err != nil || !terminated {
		t.Fatalf("a refused restore un-terminated the session: %v %v", terminated, err)
	}
}

// Restore creates a new VM, so it needs room under the plan's total, not only
// a running slot.
func TestRestoreCountsAgainstTheTotalVMs(t *testing.T) {
	store, admin, fixture := openNotificationTestStore(t)
	store.EnableBilling(7*24*time.Hour, time.Time{})
	ctx := context.Background()
	principal := domain.Principal{UserID: fixture.userID, Provider: "local"}
	limits := starterLimits()
	limits.MaxActiveSandboxes, limits.MaxRunningSandboxes, limits.OrchestratorSlots = 1, 1, 0
	subscribe(t, store, fixture.orgID, "active", limits)
	// The fixture is deleted; another session takes the plan's only VM.
	if _, err := admin.Exec(ctx,
		`UPDATE ao_sandboxes SET provider = 'freestyle', desired_state = 'deleted', observed_state = 'deleted' WHERE session_id = $1`,
		fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `UPDATE ao_sessions SET is_terminated = true WHERE id = $1`, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	other, err := createKind(store, fixture, "worker")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `UPDATE ao_sandboxes SET desired_state = 'paused', observed_state = 'stopped' WHERE session_id = $1`,
		other.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.RestoreSession(ctx, principal, fixture.orgID, fixture.sessionID); limitCode(err) != LimitTotalSessions {
		t.Fatalf("restore past the total: err = %v, want %s", err, LimitTotalSessions)
	}
}

// A contract organization and an unbilled provider are never refused, and a
// Coder session is never made to wait on the organization lock.
func TestWakesOutsideSelfServeBillingAreUntouched(t *testing.T) {
	_, admin, fixture := openNotificationTestStore(t)
	store := openRLSStore(t, admin)
	store.EnableBilling(7*24*time.Hour, time.Time{})
	ctx := context.Background()
	principal := domain.Principal{UserID: fixture.userID, Provider: "local"}

	// Coder: wake while another transaction holds the organization lock.
	if _, err := admin.Exec(ctx,
		`UPDATE ao_sandboxes SET provider = 'coder', desired_state = 'paused', observed_state = 'stopped' WHERE session_id = $1`,
		fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	holder, err := admin.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := holder.Exec(ctx, `SELECT id FROM ao_organizations WHERE id = $1 FOR UPDATE`, fixture.orgID); err != nil {
		t.Fatal(err)
	}
	quick, cancel := context.WithTimeout(ctx, 3*time.Second)
	if err := store.WakeAllowed(quick, principal, fixture.orgID, fixture.sessionID); err != nil {
		t.Errorf("coder WakeAllowed: %v", err)
	}
	if _, err := store.ResumeSession(quick, principal, fixture.orgID, fixture.sessionID); err != nil {
		t.Errorf("coder resume waited on, or was refused by, billing: %v", err)
	}
	cancel()
	_ = holder.Rollback(ctx)
	if _, err := admin.Exec(ctx, `UPDATE ao_sandboxes SET desired_state = 'paused' WHERE session_id = $1`, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.IssueTerminalTicket(ctx, principal, fixture.orgID, fixture.sessionID, "workspace", "", time.Minute); err != nil &&
		!errors.Is(err, ErrWorkerUnavailable) {
		t.Errorf("coder terminal ticket: %v", err)
	}
	if desired := desiredState(t, admin, fixture.sessionID); desired != "running" {
		t.Fatalf("coder session not woken: %s", desired)
	}

	// Contract: Freestyle is paid by every organization, so a contract one's
	// Freestyle session needs a plan to resume too. Its billing is still
	// recorded for the usage enforcer.
	billedPaused(t, admin, fixture)
	contract := principal
	contract.OrgBilling = domain.OrgBillingContract
	// WorkOS billing describes only the organization the token's WorkOS
	// organization maps to.
	contract.TeamOrgID = fixture.orgID
	// A member's request records the organization's billing; a refused
	// resume rolls back, so read the billing summary first.
	if _, err := store.BillingSummary(ctx, contract, fixture.orgID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ResumeSession(ctx, contract, fixture.orgID, fixture.sessionID); !errors.Is(err, ErrPlanRequired) {
		t.Fatalf("contract freestyle resume without a plan: err = %v, want ErrPlanRequired", err)
	}
	billing, err := store.OrgBilling(ctx, principal, fixture.orgID)
	if err != nil || billing.BillingMode != domain.OrgBillingContract {
		t.Fatalf("contract not recorded: %+v %v", billing.BillingMode, err)
	}
	summary, err := store.BillingSummary(ctx, principal, fixture.orgID)
	if err != nil || summary.BillingMode != domain.BillingModeSelfServe || summary.Exempt || summary.Entitled {
		t.Fatalf("summary = %+v err = %v; want self-serve and not entitled", summary, err)
	}
	// Background work pauses a contract organization's Freestyle session
	// without a plan like anyone else's.
	if _, err := admin.Exec(ctx, `UPDATE ao_sandboxes SET desired_state = 'running', observed_state = 'running' WHERE session_id = $1`, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	if paused, err := store.PauseOverLimit(ctx, fixture.orgID); err != nil || paused != 1 {
		t.Fatalf("enforcer on a contract organization without a plan: %d %v", paused, err)
	}
	if _, err := admin.Exec(ctx, `UPDATE ao_sandboxes SET desired_state = 'running', observed_state = 'running' WHERE session_id = $1`, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	// Back to self-serve: the next request records it.
	selfServe := principal
	selfServe.OrgBilling = domain.BillingModeSelfServe
	selfServe.TeamOrgID = fixture.orgID
	if summary, err := store.BillingSummary(ctx, selfServe, fixture.orgID); err != nil || summary.BillingMode != domain.BillingModeSelfServe {
		t.Fatalf("self-serve summary = %+v err = %v", summary, err)
	}
	if paused, err := store.PauseOverLimit(ctx, fixture.orgID); err != nil || paused != 1 {
		t.Fatalf("enforcer on an unentitled self-serve organization: %d %v", paused, err)
	}
}

// Freestyle sessions that ran before billing was enabled belong to an
// organization whose billing no request has recorded yet; it may be a contract
// one, so the enforcer leaves it alone until a member's request records it.
func TestEnforcerLeavesUnrecordedOrganizationsAlone(t *testing.T) {
	_, admin, fixture := openNotificationTestStore(t)
	store := openRLSStore(t, admin)
	store.EnableBilling(7*24*time.Hour, time.Time{})
	ctx := context.Background()
	if _, err := admin.Exec(ctx,
		`UPDATE ao_sandboxes SET provider = 'freestyle', desired_state = 'running', observed_state = 'running' WHERE session_id = $1`,
		fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	if paused, err := store.PauseOverLimit(ctx, fixture.orgID); err != nil || paused != 0 {
		t.Fatalf("enforcer paused an organization with unrecorded billing: %d %v", paused, err)
	}
	selfServe := domain.Principal{UserID: fixture.userID, Provider: "local", OrgBilling: domain.BillingModeSelfServe, TeamOrgID: fixture.orgID}
	if _, err := store.BillingSummary(ctx, selfServe, fixture.orgID); err != nil {
		t.Fatal(err)
	}
	if paused, err := store.PauseOverLimit(ctx, fixture.orgID); err != nil || paused != 1 {
		t.Fatalf("enforcer on a recorded self-serve organization without a plan: %d %v", paused, err)
	}
}

// A share grantee (editor) may wake within the plan and is refused beyond it
// like a member; the grantee's own organization billing does not apply.
func TestShareGranteeWakesUnderTheSessionOrganizationsPlan(t *testing.T) {
	_, admin, fixture := openNotificationTestStore(t)
	store := openRLSStore(t, admin)
	store.EnableBilling(7*24*time.Hour, time.Time{})
	ctx := context.Background()
	owner := domain.Principal{UserID: fixture.userID, Provider: "local"}
	link, token, err := store.CreateProjectShareLink(ctx, owner, fixture.orgID, fixture.projectID,
		domain.CreateShareLink{Role: "editor", AccessScope: "anyone", Interaction: "interact"})
	if err != nil {
		t.Fatal(err)
	}
	_ = link
	guestID := uuid.NewString()
	if _, err := admin.Exec(ctx, `INSERT INTO ao_users (id, auth_provider, external_user_id, email, display_name, password_hash)
		VALUES ($1::uuid, 'local', $1::uuid::text, $1::uuid::text || '@example.test', 'Guest', 'hash')`, guestID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = admin.Exec(context.Background(), `DELETE FROM ao_users WHERE id = $1`, guestID) })
	// A contract principal from another organization must not exempt it.
	guest := domain.Principal{UserID: guestID, Provider: "local", OrgBilling: domain.OrgBillingContract}
	if _, err := store.RedeemProjectShareLink(ctx, guest, fixture.orgID, token); err != nil {
		t.Fatal(err)
	}
	billedPaused(t, admin, fixture)
	if err := store.WakeAllowed(ctx, guest, fixture.orgID, fixture.sessionID); !errors.Is(err, ErrPlanRequired) {
		t.Fatalf("grantee wake without a plan: err = %v, want ErrPlanRequired", err)
	}
	if _, err := store.ResumeSession(ctx, guest, fixture.orgID, fixture.sessionID); !errors.Is(err, ErrPlanRequired) {
		t.Fatalf("grantee resume without a plan: err = %v", err)
	}
	subscribe(t, store, fixture.orgID, "active", starterLimits())
	if err := store.WakeAllowed(ctx, guest, fixture.orgID, fixture.sessionID); err != nil {
		t.Fatalf("grantee wake within the plan: %v", err)
	}
	if _, err := store.ResumeSession(ctx, guest, fixture.orgID, fixture.sessionID); err != nil {
		t.Fatalf("grantee resume within the plan: %v", err)
	}
}

// Two resumes racing for the last running slot: exactly one wins.
func TestConcurrentWakesCannotBothTakeTheLastSlot(t *testing.T) {
	store, admin, fixture := openNotificationTestStore(t)
	store.EnableBilling(7*24*time.Hour, time.Time{})
	ctx := context.Background()
	principal := domain.Principal{UserID: fixture.userID, Provider: "local"}
	limits := starterLimits()
	limits.MaxActiveSandboxes, limits.MaxRunningSandboxes, limits.OrchestratorSlots = 8, 1, 0
	subscribe(t, store, fixture.orgID, "active", limits)
	if _, err := admin.Exec(ctx, `UPDATE ao_sandboxes SET provider = 'freestyle', desired_state = 'paused', observed_state = 'stopped'
		WHERE session_id = $1`, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	sessions := []string{fixture.sessionID}
	for range 3 {
		session, err := createKind(store, fixture, "worker")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := admin.Exec(ctx, `UPDATE ao_sandboxes SET desired_state = 'paused', observed_state = 'stopped' WHERE session_id = $1`,
			session.ID); err != nil {
			t.Fatal(err)
		}
		sessions = append(sessions, session.ID)
	}
	results := make(chan error, len(sessions))
	start := make(chan struct{})
	var wg sync.WaitGroup
	for _, sessionID := range sessions {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := store.ResumeSession(ctx, principal, fixture.orgID, sessionID)
			results <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	won := 0
	for err := range results {
		switch {
		case err == nil:
			won++
		case limitCode(err) != LimitWorkerSlots:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if won != 1 {
		t.Fatalf("%d resumes won the single running slot", won)
	}
}

// A paused session over the plan still receives automated messages: they
// are queued without waking it.
func TestAutomatedMessagesQueueWithoutWakingBeyondThePlan(t *testing.T) {
	store, admin, fixture := openNotificationTestStore(t)
	store.EnableBilling(7*24*time.Hour, time.Time{})
	ctx := context.Background()
	billedPaused(t, admin, fixture)
	err := store.withOrg(ctx, fixture.orgID, func(tx pgx.Tx) error {
		wake, err := store.billing.messageWakeTx(ctx, tx, fixture.orgID, fixture.sessionID, false)
		if err != nil || wake {
			t.Fatalf("deferred wake = %v err = %v", wake, err)
		}
		_, err = sendMessageTx(ctx, tx, fixture.orgID, fixture.sessionID, "auto-"+uuid.NewString(), "CI failed", "", fixture.sessionID,
			"", nil, domain.ChatTurnSettings{}, wake)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if desired := desiredState(t, admin, fixture.sessionID); desired != "paused" {
		t.Fatalf("automation woke the session: %s", desired)
	}
}

// The usage enforcer pauses an over-limit organization's busy sessions once
// the grace has passed, idle ones at once, and never another provider's.
func TestEnforcerPausesBusySessionsAfterTheGrace(t *testing.T) {
	_, admin, fixture := openNotificationTestStore(t)
	store := openRLSStore(t, admin)
	store.EnableBilling(7*24*time.Hour, time.Time{})
	store.SetBillingBusyGrace(5 * time.Minute)
	ctx := context.Background()
	recordSelfServe(t, admin, fixture.orgID)
	// No plan at all: over the plan. The fixture runs busy on Freestyle.
	if _, err := admin.Exec(ctx,
		`UPDATE ao_sandboxes SET provider = 'freestyle', desired_state = 'running', observed_state = 'running' WHERE session_id = $1`,
		fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `UPDATE ao_sessions SET activity_state = 'active' WHERE id = $1`, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	// A Coder session in the same organization.
	coderID := uuid.NewString()
	if _, err := admin.Exec(ctx, `INSERT INTO ao_sessions (id, org_id, project_id, kind, harness, display_name, branch, created_by_user_id)
		VALUES ($1, $2, $3, 'worker', 'codex', 'Coder', 'coder', $4)`, coderID, fixture.orgID, fixture.projectID, fixture.userID); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `INSERT INTO ao_sandboxes (session_id, org_id, provider, desired_state, observed_state)
		VALUES ($1, $2, 'coder', 'running', 'running')`, coderID, fixture.orgID); err != nil {
		t.Fatal(err)
	}
	if paused, err := store.PauseOverLimit(ctx, fixture.orgID); err != nil || paused != 0 {
		t.Fatalf("busy within the grace: paused %d err %v", paused, err)
	}
	if _, err := admin.Exec(ctx,
		`UPDATE ao_organizations SET settings = jsonb_set(settings, '{billing_over_limit_since}', to_jsonb($2::text))
		WHERE id = $1`, fixture.orgID, time.Now().Add(-6*time.Minute).UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if paused, err := store.PauseOverLimit(ctx, fixture.orgID); err != nil || paused != 1 {
		t.Fatalf("busy past the grace: paused %d err %v", paused, err)
	}
	if desired := desiredState(t, admin, coderID); desired != "running" {
		t.Fatalf("enforcer touched a coder session: %s", desired)
	}
	// Back within the plan, the over-limit mark is cleared.
	subscribe(t, store, fixture.orgID, "active", starterLimits())
	if _, err := store.PauseOverLimit(ctx, fixture.orgID); err != nil {
		t.Fatal(err)
	}
	var marked bool
	if err := admin.QueryRow(ctx, `SELECT settings ? 'billing_over_limit_since' FROM ao_organizations WHERE id = $1`,
		fixture.orgID).Scan(&marked); err != nil || marked {
		t.Fatalf("over-limit mark kept: %v %v", marked, err)
	}
}

// Within its usage an organization still keeps to its running slots: the
// newest sessions beyond them are paused.
func TestEnforcerPausesTheNewestSessionsBeyondTheRunningSlots(t *testing.T) {
	store, admin, fixture := openNotificationTestStore(t)
	store.EnableBilling(7*24*time.Hour, time.Time{})
	ctx := context.Background()
	limits := starterLimits()
	limits.MaxActiveSandboxes, limits.MaxRunningSandboxes, limits.OrchestratorSlots = 8, 4, 0
	subscribe(t, store, fixture.orgID, "active", limits)
	recordSelfServe(t, admin, fixture.orgID)
	if _, err := admin.Exec(ctx,
		`UPDATE ao_sandboxes SET provider = 'freestyle', desired_state = 'running', observed_state = 'running' WHERE session_id = $1`,
		fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	var created []string
	for range 3 {
		session, err := createKind(store, fixture, "worker")
		if err != nil {
			t.Fatal(err)
		}
		created = append(created, session.ID)
	}
	// A downgrade to two running slots leaves four running.
	limits.MaxRunningSandboxes = 2
	if _, err := admin.Exec(ctx, `UPDATE ao_sandboxes SET observed_state = 'running', startup_started_at = NULL WHERE org_id = $1`,
		fixture.orgID); err != nil {
		t.Fatal(err)
	}
	for index, sessionID := range append([]string{fixture.sessionID}, created...) {
		if _, err := admin.Exec(ctx, `UPDATE ao_sessions SET created_at = now() - make_interval(hours => $2::int) WHERE id = $1`,
			sessionID, 10-index); err != nil {
			t.Fatal(err)
		}
	}
	subscribe(t, store, fixture.orgID, "active", limits)
	paused, err := store.PauseOverLimit(ctx, fixture.orgID)
	if err != nil || paused != 2 {
		t.Fatalf("paused %d err %v; want the two newest", paused, err)
	}
	for _, sessionID := range created[1:] {
		if desired := desiredState(t, admin, sessionID); desired != "paused" {
			t.Errorf("newest session %s still %s", sessionID, desired)
		}
	}
	if desiredState(t, admin, fixture.sessionID) != "running" || desiredState(t, admin, created[0]) != "running" {
		t.Error("an older session was paused instead of a newer one")
	}
}

func subscriptionAt(id, customer, status string, created time.Time) SubscriptionUpdate {
	return SubscriptionUpdate{SubscriptionID: id, CustomerID: customer, Status: status, Limits: starterLimits(), Created: &created}
}

// Only the organization's own subscription is applied: a stale one is
// ignored, a second live one keeps the newest, another customer's is
// ignored, and a new one replaces one that ended.
func TestApplySubscriptionKeepsTheOrganizationsSubscription(t *testing.T) {
	_, admin, fixture := openNotificationTestStore(t)
	store := openRLSStore(t, admin)
	ctx := context.Background()
	principal := domain.Principal{UserID: fixture.userID, Provider: "local"}
	customer := "cus_" + uuid.NewString()[:8]
	if err := store.LinkStripeCustomer(ctx, fixture.orgID, customer); err != nil {
		t.Fatal(err)
	}
	day := time.Now().Add(-48 * time.Hour).Truncate(time.Second)
	apply := func(update SubscriptionUpdate, want SubscriptionOutcome) {
		t.Helper()
		outcome, err := store.ApplySubscription(ctx, fixture.orgID, update)
		if err != nil || outcome != want {
			t.Fatalf("%s %s: outcome %q err %v, want %q", update.SubscriptionID, update.Status, outcome, err, want)
		}
	}
	current := func() domain.OrgBilling {
		t.Helper()
		billing, err := store.OrgBilling(ctx, principal, fixture.orgID)
		if err != nil {
			t.Fatal(err)
		}
		return billing
	}
	apply(subscriptionAt("sub_old", customer, "canceled", day), SubscriptionApplied)
	apply(subscriptionAt("sub_a", customer, "active", day.Add(time.Hour)), SubscriptionReplaced)
	apply(subscriptionAt("sub_old", customer, "canceled", day), SubscriptionIgnoredStale)
	if billing := current(); billing.StripeSubscriptionID != "sub_a" || billing.SubscriptionStatus != "active" {
		t.Fatalf("a stale event overwrote the subscription: %+v", billing)
	}
	apply(subscriptionAt("sub_older", customer, "active", day.Add(-time.Hour)), SubscriptionIgnoredDuplicate)
	apply(subscriptionAt("sub_b", customer, "active", day.Add(2*time.Hour)), SubscriptionReplacedDuplicate)
	if billing := current(); billing.StripeSubscriptionID != "sub_b" || billing.SubscriptionCreated == nil ||
		!billing.SubscriptionCreated.Equal(day.Add(2*time.Hour)) {
		t.Fatalf("the newest live subscription was not kept: %+v", billing)
	}
	apply(subscriptionAt("sub_b", "cus_other", "canceled", day), SubscriptionIgnoredForeign)
	apply(subscriptionAt("sub_b", customer, "past_due", day.Add(2*time.Hour)), SubscriptionApplied)
	if billing := current(); billing.SubscriptionStatus != "past_due" {
		t.Fatalf("the current subscription's update was not applied: %+v", billing)
	}
	if _, err := store.ApplySubscription(ctx, uuid.NewString(), subscriptionAt("sub_x", "cus_x", "active", day)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing organization: err = %v, want ErrNotFound", err)
	}
}

// Webhooks for one organization apply one at a time: a fetch that starts
// while another update holds the organization waits for it.
func TestApplySubscriptionLockedSerializesFetchAndWrite(t *testing.T) {
	store, _, fixture := openNotificationTestStore(t)
	ctx := context.Background()
	entered := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := store.ApplySubscriptionLocked(ctx, fixture.orgID, func(context.Context, domain.OrgBilling) (SubscriptionUpdate, error) {
			close(entered)
			<-release
			return subscriptionAt("sub_1", "", "active", time.Now().Add(-time.Hour)), nil
		})
		done <- err
	}()
	<-entered
	secondSaw := make(chan string, 1)
	go func() {
		_, _ = store.ApplySubscriptionLocked(ctx, fixture.orgID, func(_ context.Context, current domain.OrgBilling) (SubscriptionUpdate, error) {
			secondSaw <- current.SubscriptionStatus
			return subscriptionAt("sub_1", "", "past_due", time.Now().Add(-time.Hour)), nil
		})
	}()
	select {
	case status := <-secondSaw:
		t.Fatalf("second fetch ran while the first held the organization (saw %q)", status)
	case <-time.After(300 * time.Millisecond):
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if status := <-secondSaw; status != "active" {
		t.Fatalf("second fetch saw %q, want the first update's state", status)
	}
}

// StartCheckout records the customer and the open Checkout under the
// organization's lock; an active subscription clears it.
func TestStartCheckoutRecordsThePendingSession(t *testing.T) {
	_, admin, fixture := openNotificationTestStore(t)
	store := openRLSStore(t, admin)
	ctx := context.Background()
	principal := domain.Principal{UserID: fixture.userID, Provider: "local"}
	customer := "cus_" + uuid.NewString()[:8]
	expires := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	pending, err := store.StartCheckout(ctx, principal, fixture.orgID,
		func(_ context.Context, state domain.OrgBilling) (CheckoutStart, error) {
			if state.PendingCheckout != nil || state.StripeCustomerID != "" {
				t.Fatalf("fresh state: %+v", state)
			}
			return CheckoutStart{CustomerID: customer, Pending: &domain.PendingCheckout{
				ID: "cs_1", URL: "https://pay/cs_1", PriceID: "price_a", ExpiresAt: expires}}, nil
		})
	if err != nil || pending == nil || pending.ID != "cs_1" {
		t.Fatalf("pending = %+v err = %v", pending, err)
	}
	billing, _ := store.OrgBilling(ctx, principal, fixture.orgID)
	if billing.StripeCustomerID != customer || billing.PendingCheckout == nil || !billing.PendingCheckout.ExpiresAt.Equal(expires) {
		t.Fatalf("recorded = %+v", billing)
	}
	// A failure after creating a customer still records the customer.
	failure := errors.New("stripe down")
	if _, err := store.StartCheckout(ctx, principal, fixture.orgID,
		func(context.Context, domain.OrgBilling) (CheckoutStart, error) {
			return CheckoutStart{ClearPending: true}, failure
		}); !errors.Is(err, failure) {
		t.Fatalf("err = %v", err)
	}
	if billing, _ := store.OrgBilling(ctx, principal, fixture.orgID); billing.PendingCheckout != nil {
		t.Fatalf("pending kept after it was cleared: %+v", billing.PendingCheckout)
	}
	if _, err := store.StartCheckout(ctx, principal, fixture.orgID,
		func(context.Context, domain.OrgBilling) (CheckoutStart, error) {
			return CheckoutStart{Pending: &domain.PendingCheckout{ID: "cs_2", URL: "u", PriceID: "p", ExpiresAt: expires}}, nil
		}); err != nil {
		t.Fatal(err)
	}
	if err := store.ClearPendingCheckout(ctx, fixture.orgID, "cs_other"); err != nil {
		t.Fatal(err)
	}
	if billing, _ := store.OrgBilling(ctx, principal, fixture.orgID); billing.PendingCheckout == nil {
		t.Fatal("another session's event cleared the pending checkout")
	}
	subscribe(t, store, fixture.orgID, "active", starterLimits())
	if billing, _ := store.OrgBilling(ctx, principal, fixture.orgID); billing.PendingCheckout != nil {
		t.Fatal("an active subscription must clear the pending checkout")
	}
	// Members who are not admins cannot start one.
	if _, err := admin.Exec(ctx, `UPDATE ao_org_memberships SET role = 'member' WHERE org_id = $1`, fixture.orgID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.StartCheckout(ctx, principal, fixture.orgID,
		func(context.Context, domain.OrgBilling) (CheckoutStart, error) {
			t.Fatal("started for a member")
			return CheckoutStart{}, nil
		}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("member: err = %v", err)
	}
}

func TestPruneUsageAndStripeEvents(t *testing.T) {
	store, admin, fixture := openNotificationTestStore(t)
	ctx := context.Background()
	addUsage(t, admin, fixture, 1, 10, 50*24*time.Hour)
	addUsage(t, admin, fixture, 1, 5, 0)
	if err := store.PruneUsage(ctx, time.Now().Add(-40*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	var left int
	if err := admin.QueryRow(ctx, `SELECT count(*) FROM ao_usage_minutes WHERE org_id = $1`, fixture.orgID).Scan(&left); err != nil || left != 5 {
		t.Fatalf("usage left %d err %v; want the 5 recent minutes", left, err)
	}
	oldEvent, newEvent := "evt_old_"+uuid.NewString(), "evt_new_"+uuid.NewString()
	if _, err := admin.Exec(ctx, `INSERT INTO ao_stripe_events (event_id, type, received_at) VALUES
		($1, 'invoice.paid', now() - interval '91 days'), ($2, 'invoice.paid', now())`, oldEvent, newEvent); err != nil {
		t.Fatal(err)
	}
	if err := store.PruneStripeEvents(ctx, time.Now().Add(-90*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if seen, _ := store.StripeEventSeen(ctx, oldEvent); seen {
		t.Fatal("an event older than 90 days was kept")
	}
	if seen, _ := store.StripeEventSeen(ctx, newEvent); !seen {
		t.Fatal("a recent event was pruned")
	}
}

// Paywalled features need a self-serve plan that lists them; nothing is
// paywalled by default.
func TestOrgHasFeature(t *testing.T) {
	store, admin, fixture := openNotificationTestStore(t)
	ctx := context.Background()
	store.EnableBilling(7*24*time.Hour, time.Time{})
	if !store.OrgHasFeature(ctx, fixture.orgID, "sso") {
		t.Fatal("nothing is paywalled by default")
	}
	store.SetPaywalledFeatures([]string{"SSO"})
	if store.OrgHasFeature(ctx, fixture.orgID, "sso") {
		t.Fatal("an unentitled self-serve organization has a paywalled feature")
	}
	limits := starterLimits()
	limits.Features = []string{"sso"}
	subscribe(t, store, fixture.orgID, "active", limits)
	if !store.OrgHasFeature(ctx, fixture.orgID, "sso") || store.OrgHasFeature(ctx, uuid.NewString(), "sso") {
		t.Fatal("plan feature not honored, or a missing organization was allowed")
	}
	summary, err := store.BillingSummary(ctx, domain.Principal{UserID: fixture.userID, Provider: "local"}, fixture.orgID)
	if err != nil || len(summary.Features) != 1 || summary.Features[0] != "sso" {
		t.Fatalf("summary features = %v err = %v", summary.Features, err)
	}
	subscribe(t, store, fixture.orgID, "canceled", nil)
	if _, err := admin.Exec(ctx, `UPDATE ao_organizations SET plan = 'internal' WHERE id = $1`, fixture.orgID); err != nil {
		t.Fatal(err)
	}
	if !store.OrgHasFeature(ctx, fixture.orgID, "sso") {
		t.Fatal("an internal organization must have every feature")
	}
}

// Viewers do not keep a session awake, and presence is written at most once
// per interval.
func TestPresenceIgnoresViewersAndIsRateLimited(t *testing.T) {
	_, admin, fixture := openNotificationTestStore(t)
	store := openRLSStore(t, admin)
	ctx := context.Background()
	owner := domain.Principal{UserID: fixture.userID, Provider: "local"}
	if _, err := admin.Exec(ctx,
		`UPDATE ao_sandboxes SET provider = 'freestyle', desired_state = 'running', observed_state = 'running',
			interactive_until = NULL, last_interaction_at = NULL WHERE session_id = $1`, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	_, token, err := store.CreateProjectShareLink(ctx, owner, fixture.orgID, fixture.projectID,
		domain.CreateShareLink{Role: "viewer", AccessScope: "anyone", Interaction: "view"})
	if err != nil {
		t.Fatal(err)
	}
	viewerID := uuid.NewString()
	if _, err := admin.Exec(ctx, `INSERT INTO ao_users (id, auth_provider, external_user_id, email, display_name, password_hash)
		VALUES ($1::uuid, 'local', $1::uuid::text, $1::uuid::text || '@example.test', 'Viewer', 'hash')`, viewerID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = admin.Exec(context.Background(), `DELETE FROM ao_users WHERE id = $1`, viewerID) })
	viewer := domain.Principal{UserID: viewerID, Provider: "local"}
	if _, err := store.RedeemProjectShareLink(ctx, viewer, fixture.orgID, token); err != nil {
		t.Fatal(err)
	}
	if err := store.TouchSessionPresence(ctx, viewer, fixture.orgID, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	var touched *time.Time
	if err := admin.QueryRow(ctx, `SELECT last_interaction_at FROM ao_sandboxes WHERE session_id = $1`, fixture.sessionID).Scan(&touched); err != nil {
		t.Fatal(err)
	}
	if touched != nil {
		t.Fatal("a viewer's presence kept the session awake")
	}
	if err := store.TouchSessionPresence(ctx, owner, fixture.orgID, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `UPDATE ao_sandboxes SET last_interaction_at = now() - interval '10 seconds' WHERE session_id = $1`,
		fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	if err := store.TouchSessionPresence(ctx, owner, fixture.orgID, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	var age float64
	if err := admin.QueryRow(ctx, `SELECT extract(epoch FROM now() - last_interaction_at)::float8 FROM ao_sandboxes WHERE session_id = $1`,
		fixture.sessionID).Scan(&age); err != nil {
		t.Fatal(err)
	}
	if age < 9 {
		t.Fatalf("presence rewritten within the interval (age %v)", age)
	}
}

// A sandbox being deleted may resume to run its Cleanup only within its
// organization's plan: an organization over it (here, without a plan) keeps
// its billed sandboxes paused, and an unbilled sandbox or billing that is off
// always may. Its writes carry the organization's context, so it also passes
// as a table-owning non-superuser (forced RLS).
func TestCleanupResumeFollowsThePlan(t *testing.T) {
	store, admin, fixture := openNotificationTestStore(t)
	store = nonBypassStore(t, store, admin)
	ctx := context.Background()
	asOrg := func(query string, args ...any) {
		t.Helper()
		tx, err := admin.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		if _, err := tx.Exec(ctx, `SELECT set_config('ao.org_id', $1, true)`, fixture.orgID); err != nil {
			t.Fatal(err)
		}
		if tag, err := tx.Exec(ctx, query, args...); err != nil || tag.RowsAffected() != 1 {
			t.Fatalf("%s: %v (%d rows)", query, err, tag.RowsAffected())
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}
	sandboxIs := func(provider string, billed bool) {
		t.Helper()
		asOrg(`UPDATE ao_sandboxes SET provider = $2, billed = $3, desired_state = 'deleted', observed_state = 'stopped'
			WHERE session_id = $1`, fixture.sessionID, provider, billed)
	}
	sandboxIs("freestyle", false)
	if err := store.CleanupResumeAllowed(ctx, fixture.orgID, fixture.sessionID); err != nil {
		t.Fatalf("billing off: %v", err)
	}
	store.EnableBilling(7*24*time.Hour, time.Time{})
	if err := store.CleanupResumeAllowed(ctx, fixture.orgID, fixture.sessionID); err != nil {
		t.Fatalf("unrecorded organization: %v", err)
	}
	asOrg(`UPDATE ao_organizations SET settings = settings || jsonb_build_object('billing_mode', $2::text) WHERE id = $1`,
		fixture.orgID, domain.BillingModeSelfServe)
	if err := store.CleanupResumeAllowed(ctx, fixture.orgID, fixture.sessionID); !IsPlanRefusal(err) {
		t.Fatalf("over its plan: err = %v, want a plan refusal", err)
	}
	sandboxIs("coder", false)
	if err := store.CleanupResumeAllowed(ctx, fixture.orgID, fixture.sessionID); err != nil {
		t.Fatalf("unbilled sandbox: %v", err)
	}
	sandboxIs("coder", true)
	if err := store.CleanupResumeAllowed(ctx, fixture.orgID, fixture.sessionID); !IsPlanRefusal(err) {
		t.Fatalf("billed Coder sandbox over its plan: err = %v, want a plan refusal", err)
	}
	subscribe(t, store, fixture.orgID, "active", starterLimits())
	if err := store.CleanupResumeAllowed(ctx, fixture.orgID, fixture.sessionID); err != nil {
		t.Fatalf("within its plan: %v", err)
	}
}
