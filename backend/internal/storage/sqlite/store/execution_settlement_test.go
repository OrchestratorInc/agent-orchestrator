package store_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// 删除终态回放的当前许可、epoch 或指纹判断会让迟到结算获得已消费的执行身份。
func TestM3ExecutionSettlementReplayRetainsAuthorization(t *testing.T) {
	x := newExecutionFixture(t)
	x.accept(t)
	old := x.context(t)
	fingerprint := strings.Repeat("a", 64)
	settle := func(ctx context.Context, key, fp string) (bool, error) {
		_, applied, err := x.s.SettleWorkflowAttempt(ctx, x.attempt.WorkflowID, x.attempt.ID, x.attempt.LeaseToken, domain.WorkflowAttemptStateSucceeded, "", key, fp, x.now)
		return applied, err
	}
	if applied, err := settle(old, "settle-1", fingerprint); err != nil || !applied {
		t.Fatalf("settle: applied=%v err=%v", applied, err)
	}
	current := x.context(t)
	if applied, err := settle(current, "settle-new-key", fingerprint); err != nil || applied {
		t.Fatalf("terminal replay: applied=%v err=%v", applied, err)
	}
	if _, err := settle(current, "settle-wrong", strings.Repeat("b", 64)); !errors.Is(err, domain.ErrWorkflowAttemptLeaseConflict) {
		t.Fatalf("different request replayed: %v", err)
	}
	if _, err := settle(old, "settle-1", fingerprint); !errors.Is(err, domain.ErrSchedulerFence) {
		t.Fatalf("old snapshot replayed: %v", err)
	}
	if _, err := settle(context.Background(), "settle-1", fingerprint); !errors.Is(err, domain.ErrAdmissionRequired) {
		t.Fatalf("anonymous replayed: %v", err)
	}
	x.now = x.now.Add(time.Minute)
	if _, _, err := x.s.AcquireSchedulerLeader(context.Background(), "replacement", x.now, x.now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := settle(current, "settle-1", fingerprint); !errors.Is(err, domain.ErrSchedulerFence) {
		t.Fatalf("old leader replayed: %v", err)
	}
}
