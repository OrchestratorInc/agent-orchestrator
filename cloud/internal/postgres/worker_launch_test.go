package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
)

// The newest bootstrap ticket is the durable record of the newest worker
// launch: its epoch, when it was issued, whether the worker redeemed it, and
// whether the sandbox's worker has checked in. It is read under the
// organization's RLS context, as the control plane runs without BYPASSRLS.
func TestLatestWorkerLaunchFollowsTheNewestBootstrapTicket(t *testing.T) {
	store, admin, fixture := openNotificationTestStore(t)
	rls := openRLSStore(t, admin)
	ctx := context.Background()
	epochsAfterFixture(t, admin, fixture.epoch)

	if _, err := rls.LatestWorkerLaunch(ctx, fixture.orgID, fixture.sessionID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("before any launch: err = %v, want ErrNotFound", err)
	}
	issue := func() {
		t.Helper()
		if _, err := store.IssueAccessTicket(ctx, fixture.orgID, fixture.sessionID, "worker_bootstrap",
			[]string{"worker:connect"}, time.Hour); err != nil {
			t.Fatalf("issue ticket: %v", err)
		}
	}
	// Other ticket purposes are not worker launches.
	if _, err := store.IssueAccessTicket(ctx, fixture.orgID, fixture.sessionID, "terminal_attach",
		[]string{"terminal:attach"}, time.Hour); err != nil {
		t.Fatalf("issue terminal ticket: %v", err)
	}

	issue()
	first, err := rls.LatestWorkerLaunch(ctx, fixture.orgID, fixture.sessionID)
	if err != nil {
		t.Fatalf("latest launch: %v", err)
	}
	if first.Epoch <= fixture.epoch || first.IssuedAt.IsZero() || first.RedeemedAt != nil || first.WorkerSeen {
		t.Fatalf("first launch = %+v, want a fresh unredeemed launch with no check-in", first)
	}

	// The relaunch's ticket replaces it; the older one is marked consumed by
	// the new issue, which must not read as the new launch's redemption.
	issue()
	second, err := rls.LatestWorkerLaunch(ctx, fixture.orgID, fixture.sessionID)
	if err != nil {
		t.Fatalf("latest launch: %v", err)
	}
	if second.Epoch <= first.Epoch || second.RedeemedAt != nil {
		t.Fatalf("relaunch = %+v, want a newer unredeemed launch than %+v", second, first)
	}

	token, err := store.IssueAccessTicket(ctx, fixture.orgID, fixture.sessionID, "worker_bootstrap",
		[]string{"worker:connect"}, time.Hour)
	if err != nil {
		t.Fatalf("issue ticket: %v", err)
	}
	ticket, err := store.RedeemWorkerBootstrapTicket(ctx, token)
	if err != nil {
		t.Fatalf("redeem: %v", err)
	}
	redeemed, err := rls.LatestWorkerLaunch(ctx, fixture.orgID, fixture.sessionID)
	if err != nil {
		t.Fatalf("latest launch: %v", err)
	}
	if redeemed.Epoch != ticket.WorkerEpoch || redeemed.RedeemedAt == nil || redeemed.WorkerSeen {
		t.Fatalf("redeemed launch = %+v, want epoch %d redeemed and not yet checked in", redeemed, ticket.WorkerEpoch)
	}

	workerID := worker.NextWorkerID(fixture.sessionID, ticket.WorkerEpoch)
	if err := store.RegisterWorkerBootstrap(ctx, fixture.orgID, fixture.sessionID, workerID, "test", ticket.WorkerEpoch, nil); err != nil {
		t.Fatalf("register: %v", err)
	}
	if registered, err := rls.LatestWorkerLaunch(ctx, fixture.orgID, fixture.sessionID); err != nil || registered.WorkerSeen {
		t.Fatalf("after registration = %+v, %v; registration is not a check-in", registered, err)
	}
	if err := store.MarkWorkerSeen(ctx, fixture.orgID, fixture.sessionID, workerID, "test", ticket.WorkerEpoch, nil); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	if seen, err := rls.LatestWorkerLaunch(ctx, fixture.orgID, fixture.sessionID); err != nil || !seen.WorkerSeen {
		t.Fatalf("after the first heartbeat = %+v, %v; want the check-in reported", seen, err)
	}
}
