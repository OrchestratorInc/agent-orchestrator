package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

// 重开数据库后，报告仍按编排器隔离，批次重试不会吞掉另一组任务。
func TestIndependentOrchestratorReportBatchesSurviveRestart(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := sqlitetest.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	seedProject(t, s, "ao")
	now := time.Now().UTC().Truncate(time.Second)
	owners := []domain.SessionID{""}
	for range 2 {
		rec := sampleRecord("ao")
		rec.Kind, rec.IndependentOrchestrator = domain.KindOrchestrator, true
		created, err := s.CreateSession(ctx, rec)
		if err != nil {
			t.Fatal(err)
		}
		owners = append(owners, created.ID)
	}
	for _, owner := range owners {
		rec := sampleRecord("ao")
		rec.ID, rec.OrchestratorSessionID = domain.SessionID("worker-"+string(owner)), owner
		worker, err := s.CreateSession(ctx, rec)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.CreateReport(ctx, domain.ReportRecord{ID: "report-" + string(owner), SessionID: worker.ID, ProjectID: "ao", State: domain.ReportCheckpoint,
			Note: "done", CreatedAt: now, AvailableAt: now, DeliveryState: domain.ReportPending, RepeatCount: 1}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, owner := range owners {
		if owner != "" {
			rec, found, err := s.GetSession(ctx, owner)
			if err != nil || !found || !rec.IndependentOrchestrator {
				t.Fatalf("owner not persisted: %+v %v", rec, err)
			}
		}
		batch, err := s.ClaimPendingReportBatch(ctx, "ao", owner, "token-"+string(owner), now)
		if err != nil || len(batch) != 1 || batch[0].ID != "report-"+string(owner) {
			t.Fatalf("owner %s claimed %+v: %v", owner, batch, err)
		}
	}
}
