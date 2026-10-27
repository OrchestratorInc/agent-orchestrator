package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
)

// Whether a connection's worker uses the git proxy is what it reported at
// bootstrap; a heartbeat (which the sandbox's agent could forge with the
// worker token) never changes it.
func TestWorkerUsesGitProxyIsFixedAtBootstrap(t *testing.T) {
	store, admin, fixture := openNotificationTestStore(t)
	ctx := context.Background()
	epochsAfterFixture(t, admin, fixture.epoch)
	bootstrap := func(capabilities []string) (string, int64) {
		t.Helper()
		token, err := store.IssueAccessTicket(ctx, fixture.orgID, fixture.sessionID, "worker_bootstrap",
			[]string{"worker:connect"}, time.Hour)
		if err != nil {
			t.Fatalf("issue ticket: %v", err)
		}
		ticket, err := store.RedeemWorkerBootstrapTicket(ctx, token)
		if err != nil {
			t.Fatalf("redeem: %v", err)
		}
		workerID := worker.NextWorkerID(fixture.sessionID, ticket.WorkerEpoch)
		if err := store.RegisterWorkerBootstrap(ctx, fixture.orgID, fixture.sessionID, workerID, "test",
			ticket.WorkerEpoch, capabilities); err != nil {
			t.Fatalf("register: %v", err)
		}
		return workerID, ticket.WorkerEpoch
	}
	usesProxy := func(workerID string, epoch int64) bool {
		t.Helper()
		got, err := store.WorkerUsesGitProxy(ctx, fixture.orgID, fixture.sessionID, workerID, epoch)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}

	oldWorker, oldEpoch := bootstrap([]string{"worker.heartbeat"})
	if usesProxy(oldWorker, oldEpoch) {
		t.Fatal("a worker bootstrapped without git.proxy reads as using the git proxy")
	}
	if err := store.MarkWorkerSeen(ctx, fixture.orgID, fixture.sessionID, oldWorker, "test", oldEpoch,
		[]string{worker.CapabilityGitProxy}); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	if usesProxy(oldWorker, oldEpoch) {
		t.Fatal("a heartbeat changed the bootstrap-time git proxy capability")
	}

	newWorker, newEpoch := bootstrap([]string{"worker.heartbeat", worker.CapabilityGitProxy})
	if !usesProxy(newWorker, newEpoch) {
		t.Fatal("a worker bootstrapped with git.proxy reads as a pre-proxy worker")
	}
	if err := store.MarkWorkerSeen(ctx, fixture.orgID, fixture.sessionID, newWorker, "test", newEpoch,
		[]string{"worker.heartbeat"}); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	if !usesProxy(newWorker, newEpoch) {
		t.Fatal("a heartbeat without git.proxy made a current worker look pre-proxy")
	}
	if !usesProxy("unknown-worker", newEpoch+100) {
		t.Fatal("an unknown connection must not read as a pre-proxy worker")
	}
}
