package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

// claimWithAdmission 仅为旧生命周期测试显式增加准入证据，不改原输入或自动释放占位。
func claimWithAdmission(ctx context.Context, t *testing.T, s *sqlite.Store, a domain.WorkflowAttempt, revision int64, key, fp string) (domain.WorkflowAttempt, bool, error) {
	t.Helper()
	f := sqlitetest.NewAdmissionFixture(t, s, func() time.Time { return a.UpdatedAt })
	return f.Claim(ctx, a, revision, key, fp)
}
