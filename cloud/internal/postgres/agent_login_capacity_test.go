package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// A member's ordinary session must count an owner's private auth reservation
// without gaining SELECT access to the owner's personal attempt.
func TestAgentLoginReservationBlocksOtherMembersNormalCreateUnderRLS(t *testing.T) {
	_, admin, f := openNotificationTestStore(t)
	s := openRLSStore(t, admin)
	ctx := context.Background()
	owner := domain.Principal{UserID: f.userID, Provider: "local"}
	a := loginAttempt(f)
	if _, _, err := s.CreateAgentLogin(ctx, owner, a, 2); err != nil {
		t.Fatal(err)
	}
	memberID := uuid.NewString()
	if _, err := admin.Exec(ctx, `INSERT INTO ao_users(id,auth_provider,external_user_id,email,password_hash)
        VALUES($1::uuid,'local',$1::uuid::text,$1::uuid::text||'@example.test','hash')`, memberID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = admin.Exec(context.Background(), `DELETE FROM ao_users WHERE id=$1`, memberID) })
	if err := s.withOrg(ctx, f.orgID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO ao_org_memberships(org_id,user_id,role) VALUES($1,$2,'member')`, f.orgID, memberID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	member := domain.Principal{UserID: memberID, Provider: "local"}
	if err := s.withTenant(ctx, member, f.orgID, func(tx pgx.Tx) error {
		count, err := agentLoginReservationsTx(ctx, tx, f.orgID)
		if err != nil {
			return err
		}
		if count != 1 {
			t.Fatalf("private reservation count=%d want1", count)
		}
		var visible int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM ao_agent_login_attempts WHERE id=$1`, a.ID).Scan(&visible); err != nil {
			return err
		}
		if visible != 0 {
			t.Fatal("reservation count leaked owner RLS scope")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	create := func() (domain.Session, error) {
		return s.CreateSession(ctx, member, f.orgID, "auth-capacity-"+uuid.NewString(), 2, domain.CreateSession{ProjectID: f.projectID, Kind: "worker", Harness: "codex", Provider: "freestyle", DisplayName: "Member task"})
	}
	if _, err := create(); !errors.Is(err, ErrSandboxQuotaExceeded) {
		t.Fatalf("normal create ignored native reservation: %v", err)
	}
	// A confirmed cleanup releases the slot for the same ordinary create.
	if _, err := s.CancelAgentLogin(ctx, f.userID, a.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishAgentLoginJob(ctx, f.userID, a.ID, a.JobID); err != nil {
		t.Fatal(err)
	}
	if _, err := create(); err != nil {
		t.Fatalf("normal create after native cleanup: %v", err)
	}
}

func TestAgentLoginReservationFillsWorkerPoolForEveryNormalWakePath(t *testing.T) {
	_, admin, f := openNotificationTestStore(t)
	s := openRLSStore(t, admin)
	ctx := context.Background()
	p := domain.Principal{UserID: f.userID, Provider: "local"}
	billedPaused(t, admin, f)
	if _, _, err := s.CreateAgentLogin(ctx, p, loginAttempt(f), 100); err != nil {
		t.Fatal(err)
	}
	s.EnableBilling(7*24*time.Hour, time.Time{})
	limits := starterLimits()
	limits.MaxRunningSandboxes = 1
	limits.OrchestratorSlots = 0
	subscribe(t, s, f.orgID, "active", limits)
	pr, err := s.CreatePullRequestRecord(ctx, f.orgID, f.sessionID, "github", "owner/repo", "author", 1, "https://github.test/owner/repo/pull/1", "feature", "main", "sha-1", "Review", 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	paths := map[string]func() error{
		"new worker":  func() error { _, err := createKind(s, f, "worker"); return err },
		"resume":      func() error { _, err := s.ResumeSession(ctx, p, f.orgID, f.sessionID); return err },
		"set running": func() error { return s.SetSandboxDesiredState(ctx, p, f.orgID, f.sessionID, "running") },
		"wake all":    func() error { _, err := s.WakePausedSessions(ctx, p, f.orgID); return err },
		"send message": func() error {
			_, err := s.SendMessage(ctx, p, f.orgID, f.sessionID, "auth-wake-"+uuid.NewString(), "hi", domain.ChatTurnSettings{})
			return err
		},
		"terminal": func() error {
			_, _, err := s.IssueTerminalTicket(ctx, p, f.orgID, f.sessionID, "workspace", "", time.Minute)
			return err
		},
		"advisory wake": func() error { return s.WakeAllowed(ctx, p, f.orgID, f.sessionID) },
		"review": func() error {
			run, created, err := s.CreateReviewRun(ctx, f.orgID, pr.ID, f.sessionID, "sha-1", "", "manual")
			if err != nil || !created {
				t.Fatalf("review setup: created=%v err=%v", created, err)
			}
			defer func() { _, _ = s.FailReviewRun(ctx, f.orgID, run.ID, f.sessionID, "test") }()
			_, err = s.OpenReviewTerminal(ctx, f.orgID, f.sessionID, run.ID, "Review", "")
			return err
		},
	}
	for name, admit := range paths {
		if err := admit(); limitCode(err) != LimitWorkerSlots {
			t.Errorf("%s ignored native worker reservation: %v", name, err)
		}
		if desired := desiredState(t, admin, f.sessionID); desired != "paused" {
			t.Fatalf("%s mutated refused session to %s", name, desired)
		}
	}
}

func TestAgentLoginReservationCountsForFreshRestoreAndStartupRetry(t *testing.T) {
	_, admin, f := openNotificationTestStore(t)
	s := openRLSStore(t, admin)
	ctx := context.Background()
	p := domain.Principal{UserID: f.userID, Provider: "local"}
	billedPaused(t, admin, f)
	if _, _, err := s.CreateAgentLogin(ctx, p, loginAttempt(f), 100); err != nil {
		t.Fatal(err)
	}
	s.EnableBilling(7*24*time.Hour, time.Time{})
	limits := starterLimits()
	limits.MaxActiveSandboxes = 1
	limits.MaxRunningSandboxes = 4
	limits.OrchestratorSlots = 0
	subscribe(t, s, f.orgID, "active", limits)
	if _, err := admin.Exec(ctx, `UPDATE ao_sandboxes SET desired_state='running',observed_state='failed',worker_last_seen_at=NULL WHERE session_id=$1`, f.sessionID); err != nil {
		t.Fatal(err)
	}
	if err := s.RetrySessionStartup(ctx, p, f.orgID, f.sessionID); limitCode(err) != LimitTotalSessions {
		t.Fatalf("retry ignored native VM reservation: %v", err)
	}
	if _, err := admin.Exec(ctx, `UPDATE ao_sandboxes SET desired_state='deleted',observed_state='deleted' WHERE session_id=$1`, f.sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `UPDATE ao_sessions SET is_terminated=true WHERE id=$1`, f.sessionID); err != nil {
		t.Fatal(err)
	}
	if err := s.RestoreSession(ctx, p, f.orgID, f.sessionID); limitCode(err) != LimitTotalSessions {
		t.Fatalf("restore ignored native VM reservation: %v", err)
	}
	var terminated bool
	if err := admin.QueryRow(ctx, `SELECT is_terminated FROM ao_sessions WHERE id=$1`, f.sessionID).Scan(&terminated); err != nil || !terminated {
		t.Fatalf("refused restore changed session: terminated=%v err=%v", terminated, err)
	}
}

func TestAgentLoginUsesWorkerPoolAndPreservesReservedOrchestratorSlot(t *testing.T) {
	_, admin, f := openNotificationTestStore(t)
	s := openRLSStore(t, admin)
	ctx := context.Background()
	p := domain.Principal{UserID: f.userID, Provider: "local"}
	billedPaused(t, admin, f)
	s.EnableBilling(7*24*time.Hour, time.Time{})
	subscribe(t, s, f.orgID, "active", starterLimits())
	if _, _, err := s.CreateAgentLogin(ctx, p, loginAttempt(f), 100); err != nil {
		t.Fatal(err)
	}
	if _, err := createKind(s, f, "worker"); limitCode(err) != LimitWorkerSlots {
		t.Fatalf("worker pool not full: %v", err)
	}
	if _, err := createKind(s, f, "orchestrator"); err != nil {
		t.Fatalf("native login consumed reserved orchestrator slot: %v", err)
	}
}
