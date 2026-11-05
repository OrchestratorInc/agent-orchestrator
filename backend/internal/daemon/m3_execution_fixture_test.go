package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// daemonExecutionFence 捕获本 fixture 已持有的任期，避免协调器运行时跟随其他实例的新 Leader。
func daemonExecutionFence(t *testing.T, store *sqlite.Store) func(context.Context) (domain.SchedulerFence, error) {
	t.Helper()
	leader, err := store.GetSchedulerLeader(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return func(context.Context) (domain.SchedulerFence, error) { return leader.Fence, nil }
}

// materializeAdmittedDaemonSession 让假的外部启动通过真实 SQLite 准入事务落地，保留调用者原许可。
func materializeAdmittedDaemonSession(ctx context.Context, store *sqlite.Store, seed domain.SessionRecord, request ports.SpawnRequest, now time.Time) (domain.SessionID, error) {
	admission, found, err := store.GetSchedulerAdmissionForAttempt(ctx, request.AttemptID)
	if err != nil {
		return "", err
	}
	if !found {
		return "", domain.ErrAdmissionRequired
	}
	binding, found, err := store.GetWorkflowAttemptBinding(ctx, request.AttemptID)
	if err != nil {
		return "", err
	}
	if !found {
		return "", domain.ErrWorkflowBindingNotFound
	}
	seed.ClientRequestID, seed.ClientRequestHash = binding.SpawnRequestID, binding.SpawnHash
	seed.CreatedAt, seed.UpdatedAt = now, now
	created, _, err := store.CreateAdmittedSession(ctx, seed, admission, "", now)
	return created.ID, err
}
