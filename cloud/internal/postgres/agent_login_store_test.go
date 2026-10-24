package postgres

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/agentconnect"
	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func loginAttempt(f notificationFixture) agentconnect.Attempt {
	hash := sha256.Sum256([]byte("attempt-specific-bearer"))
	id := uuid.NewString()
	return agentconnect.Attempt{ID: id, JobID: id, UserID: f.userID, OrgID: f.orgID, Harness: "codex", Status: "starting", IdempotencyKey: uuid.NewString(), ExpiresAt: time.Now().Add(10 * time.Minute), TokenHash: hash[:]}
}

func TestAgentLoginIdempotencyReplacementOwnershipAndAtomicPromotion(t *testing.T) {
	s, _, f := openNotificationTestStore(t)
	ctx := context.Background()
	p := domain.Principal{UserID: f.userID, Provider: "local"}
	a := loginAttempt(f)
	first, created, err := s.CreateAgentLogin(ctx, p, a, 100)
	if err != nil || !created {
		t.Fatalf("create: created=%v err=%v", created, err)
	}
	again, created, err := s.CreateAgentLogin(ctx, p, a, 100)
	if err != nil || created || again.ID != first.ID {
		t.Fatalf("idempotent create: created=%v err=%v", created, err)
	}
	mismatch := a
	mismatch.Harness = "claude-code"
	if _, _, err := s.CreateAgentLogin(ctx, p, mismatch, 100); !errors.Is(err, ErrIdempotencyMismatch) {
		t.Fatalf("mismatched request: %v", err)
	}
	if _, err := s.GetAgentLogin(ctx, uuid.NewString(), a.ID); !errors.Is(err, agentconnect.ErrNotFound) {
		t.Fatalf("cross user get: %v", err)
	}
	if _, err := s.FindAgentLoginRunner(ctx, a.ID, bytes.Repeat([]byte{1}, 32)); !errors.Is(err, agentconnect.ErrNotFound) {
		t.Fatalf("wrong runner: %v", err)
	}
	replacement := loginAttempt(f)
	if _, _, err := s.CreateAgentLogin(ctx, p, replacement, 100); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateAgentLogin(ctx, a, agentconnect.Update{Status: "connected", CredentialType: "auth_json"}, []byte("late"), []byte("nonce")); !errors.Is(err, agentconnect.ErrConflict) {
		t.Fatalf("replaced completion: %v", err)
	}
	if err := s.UpdateAgentLogin(ctx, replacement, agentconnect.Update{Status: "connected", CredentialType: "auth_json"}, []byte("new-encrypted"), []byte("new-nonce")); err != nil {
		t.Fatal(err)
	}
	got, nonce, err := s.UserProviderConnectionSecret(ctx, p, "codex", "default")
	if err != nil || string(got) != "new-encrypted" || string(nonce) != "new-nonce" {
		t.Fatalf("promoted secret=%s nonce=%s err=%v", got, nonce, err)
	}
	current, err := s.GetAgentLogin(ctx, f.userID, replacement.ID)
	if err != nil || current.Status != "connected" {
		t.Fatalf("completed attempt: %+v err=%v", current, err)
	}
}

func TestAgentLoginDirectCredentialWriteCancelsPendingCompletion(t *testing.T) {
	for _, operation := range []string{"put", "delete"} {
		t.Run(operation, func(t *testing.T) {
			s, _, f := openNotificationTestStore(t)
			ctx := context.Background()
			p := domain.Principal{UserID: f.userID, Provider: "local"}
			config := json.RawMessage(`{"credentialType":"api_key"}`)
			if _, err := s.UpsertUserProviderConnection(ctx, p, "codex", "default", []byte("old"), []byte("nonce"), config); err != nil {
				t.Fatal(err)
			}
			a := loginAttempt(f)
			if _, _, err := s.CreateAgentLogin(ctx, p, a, 100); err != nil {
				t.Fatal(err)
			}
			if operation == "put" {
				if _, err := s.UpsertUserProviderConnection(ctx, p, "codex", "default", []byte("direct"), []byte("nonce"), config); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := s.DeleteUserProviderConnection(ctx, p, "codex", "default"); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.UpdateAgentLogin(ctx, a, agentconnect.Update{Status: "connected", CredentialType: "auth_json"}, []byte("late"), []byte("nonce")); !errors.Is(err, agentconnect.ErrConflict) {
				t.Fatalf("late completion after %s: %v", operation, err)
			}
			got, _, err := s.UserProviderConnectionSecret(ctx, p, "codex", "default")
			if operation == "put" && (err != nil || string(got) != "direct") {
				t.Fatalf("direct credential overwritten: %s err=%v", got, err)
			}
			if operation == "delete" && !errors.Is(err, ErrNotFound) {
				t.Fatalf("deleted credential recreated: %s err=%v", got, err)
			}
		})
	}
}

func TestAgentLoginCodeConsumptionCancelExpiryAndRecovery(t *testing.T) {
	s, _, f := openNotificationTestStore(t)
	ctx := context.Background()
	p := domain.Principal{UserID: f.userID, Provider: "local"}
	a := loginAttempt(f)
	a.Harness = "claude-code"
	if _, _, err := s.CreateAgentLogin(ctx, p, a, 100); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateAgentLogin(ctx, a, agentconnect.Update{Status: "waiting", RequiresCode: true}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetAgentLoginCode(ctx, f.userID, a.ID, []byte("encrypted-code"), bytes.Repeat([]byte{2}, 12)); err != nil {
		t.Fatal(err)
	}
	first, err := s.ConsumeAgentLoginCode(ctx, f.userID, a.ID, a.TokenHash)
	if err != nil || string(first.InputCiphertext) != "encrypted-code" {
		t.Fatalf("first code: %+v err=%v", first, err)
	}
	second, err := s.ConsumeAgentLoginCode(ctx, f.userID, a.ID, a.TokenHash)
	if err != nil || len(second.InputCiphertext) != 0 {
		t.Fatalf("input replayed: %+v err=%v", second, err)
	}
	if _, err := s.CancelAgentLogin(ctx, f.userID, a.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateAgentLogin(ctx, a, agentconnect.Update{Status: "connected", CredentialType: "oauth_token"}, []byte("late"), []byte("nonce")); !errors.Is(err, agentconnect.ErrConflict) {
		t.Fatalf("cancelled completion: %v", err)
	}
	if _, err := s.SetAgentLoginCode(ctx, f.userID, a.ID, []byte("late-code"), bytes.Repeat([]byte{2}, 12)); !errors.Is(err, agentconnect.ErrConflict) {
		t.Fatalf("cancelled input accepted: %v", err)
	}
	b := loginAttempt(f)
	if _, _, err := s.CreateAgentLogin(ctx, p, b, 100); err != nil {
		t.Fatal(err)
	}
	if err := s.withUser(ctx, f.userID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE ao_agent_login_attempts SET created_at=now()-interval '10 minutes',expires_at = now() - interval '1 second' WHERE id=$1`, b.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateAgentLogin(ctx, b, agentconnect.Update{Status: "connected", CredentialType: "auth_json"}, []byte("late"), []byte("nonce")); !errors.Is(err, agentconnect.ErrConflict) {
		t.Fatalf("expired completion: %v", err)
	}
	cleanup, err := s.AgentLoginsNeedingCleanup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, attempt := range cleanup {
		if attempt.ID == b.ID {
			found = attempt.Status == "expired" && attempt.JobID == b.ID
		}
	}
	if !found {
		t.Fatal("expired job missing durable cleanup handle")
	}
}

func TestAgentLoginAdmissionRequiresOwnedOrgAndChecksCapacityAndBilling(t *testing.T) {
	s, _, f := openNotificationTestStore(t)
	ctx := context.Background()
	p := domain.Principal{UserID: f.userID, Provider: "local"}
	if _, _, err := s.CreateAgentLogin(ctx, p, loginAttempt(f), 1); !errors.Is(err, ErrSandboxQuotaExceeded) {
		t.Fatalf("full capacity: %v", err)
	}
	s.EnableBilling(0, time.Time{})
	if _, _, err := s.CreateAgentLogin(ctx, p, loginAttempt(f), 100); !errors.Is(err, ErrPlanRequired) {
		t.Fatalf("no billing plan: %v", err)
	}
	s.billing.enforced = false
	if err := s.withTenant(ctx, p, f.orgID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE ao_org_memberships SET role='member' WHERE org_id=$1 AND user_id=$2`, f.orgID, f.userID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.CreateAgentLogin(ctx, p, loginAttempt(f), 100); !errors.Is(err, ErrForbidden) {
		t.Fatalf("nonowner admission: %v", err)
	}
}

func TestAgentLoginCleanupRetainsInflightProvisioningReservation(t *testing.T) {
	s, _, f := openNotificationTestStore(t)
	ctx := context.Background()
	p := domain.Principal{UserID: f.userID, Provider: "local"}
	a := loginAttempt(f)
	if _, _, err := s.CreateAgentLogin(ctx, p, a, 100); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CancelAgentLogin(ctx, f.userID, a.ID); err != nil {
		t.Fatal(err)
	}
	jobs, err := s.AgentLoginsNeedingCleanup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, job := range jobs {
		if job.ID == a.ID {
			t.Fatal("in-flight provision may be marked cleaned before its sandbox appears")
		}
	}
	if active, err := s.SetAgentLoginJob(ctx, f.userID, a.ID, a.ID); err != nil || active {
		t.Fatalf("cancelled launch active=%v err=%v", active, err)
	}
	jobs, err = s.AgentLoginsNeedingCleanup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, job := range jobs {
		found = found || job.ID == a.ID
	}
	if !found {
		t.Fatal("finished cancelled launch missing cleanup")
	}
}

func TestAgentLoginDeletingMissingPersonalCredentialStillCancelsPendingAttempt(t *testing.T) {
	s, _, f := openNotificationTestStore(t)
	ctx := context.Background()
	p := domain.Principal{UserID: f.userID, Provider: "local"}
	a := loginAttempt(f)
	if _, _, err := s.CreateAgentLogin(ctx, p, a, 100); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteUserProviderConnection(ctx, p, "codex", "default"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete missing credential: %v", err)
	}
	got, err := s.GetAgentLogin(ctx, f.userID, a.ID)
	if err != nil || got.Status != "cancelled" {
		t.Fatalf("pending attempt status=%s err=%v", got.Status, err)
	}
}

func TestAgentLoginCompletionWaitingOnLockCannotCrossExpiry(t *testing.T) {
	s, _, f := openNotificationTestStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	p := domain.Principal{UserID: f.userID, Provider: "local"}
	a := loginAttempt(f)
	if _, _, err := s.CreateAgentLogin(ctx, p, a, 100); err != nil {
		t.Fatal(err)
	}
	if err := s.withUser(ctx, f.userID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE ao_agent_login_attempts SET expires_at=clock_timestamp()+interval '2 seconds' WHERE id=$1`, a.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	hold, err := s.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer hold.Rollback(context.Background())
	if _, err := hold.Exec(ctx, `SELECT set_config('ao.user_id',$1,true)`, f.userID); err != nil {
		t.Fatal(err)
	}
	if err := lockAgentLogin(ctx, hold, f.userID, a.Harness); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		result <- s.UpdateAgentLogin(ctx, a, agentconnect.Update{Status: "connected", CredentialType: "auth_json"}, []byte("late-encrypted"), []byte("nonce"))
	}()
	lockKey := "agent-login:" + f.userID + ":" + a.Harness
	for {
		var waiting bool
		if err := hold.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE locktype='advisory' AND NOT granted
            AND classid=((hashtextextended($1,0)>>32)&4294967295)::oid AND objid=(hashtextextended($1,0)&4294967295)::oid)`, lockKey).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("completion never waited on attempt lock")
		case <-time.After(10 * time.Millisecond):
		}
	}
	var unexpired bool
	if err := hold.QueryRow(ctx, `SELECT expires_at>clock_timestamp() FROM ao_agent_login_attempts WHERE id=$1`, a.ID).Scan(&unexpired); err != nil || !unexpired {
		t.Fatalf("completion did not begin before deadline: %v", err)
	}
	for unexpired {
		if err := hold.QueryRow(ctx, `SELECT expires_at>clock_timestamp() FROM ao_agent_login_attempts WHERE id=$1`, a.ID).Scan(&unexpired); err != nil {
			t.Fatal(err)
		}
		if unexpired {
			select {
			case <-ctx.Done():
				t.Fatal("attempt did not expire")
			case <-time.After(10 * time.Millisecond):
			}
		}
	}
	if err := hold.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-result; !errors.Is(err, agentconnect.ErrConflict) {
		t.Fatalf("post-deadline promotion: %v", err)
	}
	if _, _, err := s.UserProviderConnectionSecret(ctx, p, "codex", "default"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("post-deadline credential persisted: %v", err)
	}
}

func TestAgentLoginExpiredUnknownProvisionKeepsCleanupWithoutReservingCapacityForever(t *testing.T) {
	s, _, f := openNotificationTestStore(t)
	ctx := context.Background()
	p := domain.Principal{UserID: f.userID, Provider: "local"}
	a := loginAttempt(f)
	// The fixture already has one normal sandbox, leaving one auth slot.
	if _, _, err := s.CreateAgentLogin(ctx, p, a, 2); err != nil {
		t.Fatal(err)
	}
	if err := s.withUser(ctx, f.userID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE ao_agent_login_attempts
        SET created_at=clock_timestamp()-interval '10 minutes',expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, a.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, created, err := s.CreateAgentLogin(ctx, p, loginAttempt(f), 2); err != nil || !created {
		t.Fatalf("expired unknown job permanently reserved capacity: created=%v err=%v", created, err)
	}
	old, err := s.GetAgentLogin(ctx, f.userID, a.ID)
	if err != nil || old.JobID != a.ID || old.LaunchFinished {
		t.Fatalf("uncertain cleanup handle discarded: %+v err=%v", old, err)
	}
}
