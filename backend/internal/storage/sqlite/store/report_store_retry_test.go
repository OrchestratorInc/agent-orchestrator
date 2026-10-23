package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Issue #6931: reports looped for hours with an empty last_error because each
// new claim erased the reason the previous attempt failed.
func TestReportBatchKeepsLastFailureUntilAcknowledged(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "ao")
	sess, err := s.CreateSession(ctx, sampleRecord("ao"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	rec := domain.ReportRecord{ID: "rpt_retry", SessionID: sess.ID, ProjectID: sess.ProjectID, Message: "information", CreatedAt: now, DeliveryState: domain.ReportPending, AvailableAt: now, RepeatCount: 1}
	if _, err = s.CreateReport(ctx, rec); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ClaimPendingReportBatch(ctx, sess.ProjectID, "try-1", now); err != nil {
		t.Fatal(err)
	}
	const reason = "prompt submission was not observed"
	if _, err = s.ReleaseReportBatch(ctx, sess.ProjectID, "try-1", now, reason); err != nil {
		t.Fatal(err)
	}
	claimed, err := s.ClaimPendingReportBatch(ctx, sess.ProjectID, "try-2", now)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("second claim = %+v, err = %v", claimed, err)
	}
	if claimed[0].LastError != reason || claimed[0].DeliveryAttempts != 2 {
		t.Fatalf("second claim last_error = %q, attempts = %d; want %q, 2", claimed[0].LastError, claimed[0].DeliveryAttempts, reason)
	}
	if _, err = s.AcknowledgeReportBatch(ctx, sess.ProjectID, "try-2", now); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.GetReport(ctx, rec.ID)
	if err != nil || !ok || got.DeliveryState != domain.ReportAcknowledged || got.LastError != "" {
		t.Fatalf("after ack = %+v, ok = %v, err = %v; want acknowledged with no error", got, ok, err)
	}
}
