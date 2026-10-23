package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
)

// A worker whose bootstrap response was lost (the control plane answered after
// the worker's request timeout) retries with the same ticket. The retry gets
// the same epoch until a worker checks in on it; then the ticket is spent.
func TestBootstrapTicketRedeemsAgainUntilTheWorkerChecksIn(t *testing.T) {
	store, admin, fixture := openNotificationTestStore(t)
	ctx := context.Background()
	epochsAfterFixture(t, admin, fixture.epoch)

	token, err := store.IssueAccessTicket(ctx, fixture.orgID, fixture.sessionID, "worker_bootstrap", []string{"worker:connect"}, time.Hour)
	if err != nil {
		t.Fatalf("issue ticket: %v", err)
	}
	first, err := store.RedeemWorkerBootstrapTicket(ctx, token)
	if err != nil {
		t.Fatalf("redeem: %v", err)
	}
	again, err := store.RedeemWorkerBootstrapTicket(ctx, token)
	if err != nil || again.ID != first.ID || again.WorkerEpoch != first.WorkerEpoch || again.SessionID != fixture.sessionID {
		t.Fatalf("retry before any registration = %+v, %v; want ticket %s epoch %d", again, err, first.ID, first.WorkerEpoch)
	}

	// The first request registered the worker before its response was lost.
	workerID := worker.NextWorkerID(fixture.sessionID, first.WorkerEpoch)
	if err := store.RegisterWorkerBootstrap(ctx, fixture.orgID, fixture.sessionID, workerID, "test", first.WorkerEpoch, nil); err != nil {
		t.Fatalf("register: %v", err)
	}
	if again, err := store.RedeemWorkerBootstrapTicket(ctx, token); err != nil || again.WorkerEpoch != first.WorkerEpoch {
		t.Fatalf("retry after registration = %+v, %v; want epoch %d", again, err, first.WorkerEpoch)
	}

	if err := store.MarkWorkerSeen(ctx, fixture.orgID, fixture.sessionID, workerID, "test", first.WorkerEpoch, nil); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	if _, err := store.RedeemWorkerBootstrapTicket(ctx, token); !errors.Is(err, ErrInvalidTicket) {
		t.Fatalf("redeem after the worker checked in: err = %v, want ErrInvalidTicket", err)
	}
}

// Outside the retry window, after a newer ticket, or once the epoch is fenced,
// a redeemed ticket buys nothing.
func TestBootstrapTicketRetryIsRefusedOnceSettled(t *testing.T) {
	store, admin, fixture := openNotificationTestStore(t)
	ctx := context.Background()
	epochsAfterFixture(t, admin, fixture.epoch)
	issueAndRedeem := func() (string, int64) {
		t.Helper()
		token, err := store.IssueAccessTicket(ctx, fixture.orgID, fixture.sessionID, "worker_bootstrap", []string{"worker:connect"}, time.Hour)
		if err != nil {
			t.Fatalf("issue ticket: %v", err)
		}
		ticket, err := store.RedeemWorkerBootstrapTicket(ctx, token)
		if err != nil {
			t.Fatalf("redeem: %v", err)
		}
		return token, ticket.WorkerEpoch
	}
	refused := func(name, token string) {
		t.Helper()
		if _, err := store.RedeemWorkerBootstrapTicket(ctx, token); !errors.Is(err, ErrInvalidTicket) {
			t.Fatalf("%s: err = %v, want ErrInvalidTicket", name, err)
		}
	}

	expired, _ := issueAndRedeem()
	if _, err := admin.Exec(ctx, `UPDATE ao_access_tickets
		SET created_at = now() - $2::interval, consumed_at = now() - $2::interval
		WHERE org_id = $1 AND consumed_at IS NOT NULL`,
		fixture.orgID, intervalString(WorkerBootstrapRetryWindow+time.Minute)); err != nil {
		t.Fatal(err)
	}
	refused("past the retry window", expired)

	replaced, _ := issueAndRedeem()
	issueAndRedeem()
	refused("after a newer ticket", replaced)

	neverRedeemed, err := store.IssueAccessTicket(ctx, fixture.orgID, fixture.sessionID, "worker_bootstrap", []string{"worker:connect"}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.IssueAccessTicket(ctx, fixture.orgID, fixture.sessionID, "worker_bootstrap", []string{"worker:connect"}, time.Hour); err != nil {
		t.Fatal(err)
	}
	refused("a ticket invalidated before it was redeemed", neverRedeemed)

	fenced, epoch := issueAndRedeem()
	if err := store.RegisterWorkerBootstrap(ctx, fixture.orgID, fixture.sessionID,
		worker.NextWorkerID(fixture.sessionID, epoch), "test", epoch, nil); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := store.DisconnectSessionWorkers(ctx, fixture.orgID, fixture.sessionID); err != nil {
		t.Fatalf("disconnect: %v", err)
	}
	refused("a fenced epoch", fenced)
}

// epochsAfterFixture moves the epoch sequence past the fixture worker's
// hard-coded epoch, as issued epochs always are in production.
func epochsAfterFixture(t *testing.T, admin *pgxpool.Pool, epoch int64) {
	t.Helper()
	if _, err := admin.Exec(context.Background(),
		`SELECT setval('ao_worker_epoch_sequence', GREATEST($1, (SELECT last_value FROM ao_worker_epoch_sequence)))`,
		epoch); err != nil {
		t.Fatal(err)
	}
}

// The control plane runs without BYPASSRLS, so the check-in test must see the
// session's worker connections under the ticket's organization: as a plain
// role, a ticket whose worker checked in is still refused.
func TestBootstrapTicketRetryIsRefusedAfterCheckInUnderRLS(t *testing.T) {
	store, admin, fixture := openNotificationTestStore(t)
	rls := openRLSStore(t, admin)
	ctx := context.Background()
	epochsAfterFixture(t, admin, fixture.epoch)

	token, err := store.IssueAccessTicket(ctx, fixture.orgID, fixture.sessionID, "worker_bootstrap", []string{"worker:connect"}, time.Hour)
	if err != nil {
		t.Fatalf("issue ticket: %v", err)
	}
	first, err := rls.RedeemWorkerBootstrapTicket(ctx, token)
	if err != nil {
		t.Fatalf("redeem: %v", err)
	}
	if again, err := rls.RedeemWorkerBootstrapTicket(ctx, token); err != nil || again.WorkerEpoch != first.WorkerEpoch {
		t.Fatalf("retry before check-in = %+v, %v", again, err)
	}
	workerID := worker.NextWorkerID(fixture.sessionID, first.WorkerEpoch)
	if err := store.RegisterWorkerBootstrap(ctx, fixture.orgID, fixture.sessionID, workerID, "test", first.WorkerEpoch, nil); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := store.MarkWorkerSeen(ctx, fixture.orgID, fixture.sessionID, workerID, "test", first.WorkerEpoch, nil); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	if _, err := rls.RedeemWorkerBootstrapTicket(ctx, token); !errors.Is(err, ErrInvalidTicket) {
		t.Fatalf("redeem after check-in under RLS: err = %v, want ErrInvalidTicket", err)
	}
}

// A ticket issued while the workspace was still installing the worker can be
// close to expiring when the worker first reaches the control plane. Its retry
// window runs from that first redemption, not from the ticket's expiry.
func TestBootstrapTicketRetryOutlivesTheTicketExpiry(t *testing.T) {
	store, admin, fixture := openNotificationTestStore(t)
	ctx := context.Background()
	epochsAfterFixture(t, admin, fixture.epoch)

	token, err := store.IssueAccessTicket(ctx, fixture.orgID, fixture.sessionID, "worker_bootstrap", []string{"worker:connect"}, time.Hour)
	if err != nil {
		t.Fatalf("issue ticket: %v", err)
	}
	first, err := store.RedeemWorkerBootstrapTicket(ctx, token)
	if err != nil {
		t.Fatalf("redeem: %v", err)
	}
	if _, err := admin.Exec(ctx, `UPDATE ao_access_tickets SET created_at = now() - interval '20 minutes', expires_at = now() - interval '1 minute' WHERE id = $1`, first.ID); err != nil {
		t.Fatal(err)
	}
	if again, err := store.RedeemWorkerBootstrapTicket(ctx, token); err != nil || again.WorkerEpoch != first.WorkerEpoch {
		t.Fatalf("retry past expiry, inside the window = %+v, %v; want epoch %d", again, err, first.WorkerEpoch)
	}
}
