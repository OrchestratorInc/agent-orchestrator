package store_test

import (
	"context"
	"database/sql"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

func semanticDeliveryFixture(t *testing.T) (*sqlite.Store, domain.SessionID, string) {
	t.Helper()
	dataDir := t.TempDir()
	s, err := sqlite.Open(dataDir)
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("close sqlite store: %v", err)
		}
	})
	seedProject(t, s, "semantic")
	rec := sampleRecord("semantic")
	rec.Mode = domain.SessionModeTUI
	rec.Metadata.RuntimeLaunchID = "launch-1"
	created, err := s.CreateSession(context.Background(), rec)
	if err != nil {
		t.Fatalf("create semantic TUI session: %v", err)
	}
	return s, created.ID, dataDir
}

func TestSemanticDeliveryReservationIsLaunchScopedAndIdempotent(t *testing.T) {
	s, sessionID, _ := semanticDeliveryFixture(t)
	ctx := context.Background()
	const deliveryID = "report-batch:reserve1"

	for attempt := 0; attempt < 2; attempt++ {
		registered, err := s.RegisterSemanticDelivery(ctx, sessionID, "launch-1", deliveryID)
		if err != nil || !registered {
			t.Fatalf("reservation %d: registered=%v err=%v", attempt+1, registered, err)
		}
	}
	if registered, err := s.RegisterSemanticDelivery(ctx, sessionID, "stale-launch", deliveryID); err != nil || registered {
		t.Fatalf("stale launch reservation: registered=%v err=%v", registered, err)
	}
	if accepted, err := s.SemanticDeliveryAccepted(ctx, sessionID, "launch-1", deliveryID); err != nil || accepted {
		t.Fatalf("pending receipt: accepted=%v err=%v", accepted, err)
	}

	rec, found, err := s.GetSession(ctx, sessionID)
	if err != nil || !found {
		t.Fatalf("load session: found=%v err=%v", found, err)
	}
	rec.Metadata.LatestUserPrompt = "human text containing an internal report"
	rec.Metadata.LatestUserPromptAt = time.Now().UTC()
	rec.UpdatedAt = rec.Metadata.LatestUserPromptAt
	applied, err := s.UpdateSessionFromActivitySignalAndAcceptSemanticDelivery(ctx, rec, rec.Revision, "launch-1", deliveryID)
	if err != nil || !applied {
		t.Fatalf("accepted activity projection: applied=%v err=%v", applied, err)
	}
	if registered, err := s.RegisterSemanticDelivery(ctx, sessionID, "launch-1", deliveryID); err != nil || !registered {
		t.Fatalf("duplicate accepted reservation: registered=%v err=%v", registered, err)
	}
	if accepted, err := s.SemanticDeliveryAccepted(ctx, sessionID, "launch-1", deliveryID); err != nil || !accepted {
		t.Fatalf("accepted receipt after duplicate reservation: accepted=%v err=%v", accepted, err)
	}
}

func TestSemanticDeliveryUnregisteredIDDoesNotAccept(t *testing.T) {
	s, sessionID, _ := semanticDeliveryFixture(t)
	ctx := context.Background()
	rec, found, err := s.GetSession(ctx, sessionID)
	if err != nil || !found {
		t.Fatalf("load session: found=%v err=%v", found, err)
	}
	const unregisteredID = "report-batch:unregistered"
	applied, err := s.UpdateSessionFromActivitySignalAndAcceptSemanticDelivery(ctx, rec, rec.Revision, "launch-1", unregisteredID)
	if err != nil || !applied {
		t.Fatalf("ordinary activity projection: applied=%v err=%v", applied, err)
	}
	if accepted, err := s.SemanticDeliveryAccepted(ctx, sessionID, "launch-1", unregisteredID); err != nil || accepted {
		t.Fatalf("unregistered receipt: accepted=%v err=%v", accepted, err)
	}
}

func TestSemanticDeliveryProjectionRollsBackWhenReceiptWriteFails(t *testing.T) {
	s, sessionID, dataDir := semanticDeliveryFixture(t)
	ctx := context.Background()
	const deliveryID = "report-batch:rollback1"
	if registered, err := s.RegisterSemanticDelivery(ctx, sessionID, "launch-1", deliveryID); err != nil || !registered {
		t.Fatalf("register receipt: registered=%v err=%v", registered, err)
	}
	dbPath := url.URL{Path: filepath.Join(dataDir, "ao.db")}
	admin, err := sql.Open("sqlite", "file:"+dbPath.EscapedPath())
	if err != nil {
		t.Fatalf("open sqlite admin connection: %v", err)
	}
	t.Cleanup(func() { _ = admin.Close() })
	if _, err := admin.ExecContext(ctx, `
CREATE TRIGGER fail_semantic_delivery_accept
BEFORE UPDATE OF accepted_at ON semantic_deliveries
WHEN NEW.accepted_at IS NOT OLD.accepted_at
BEGIN
    SELECT RAISE(ABORT, 'forced semantic receipt failure');
END;`); err != nil {
		t.Fatalf("install receipt failure trigger: %v", err)
	}
	before, found, err := s.GetSession(ctx, sessionID)
	if err != nil || !found {
		t.Fatalf("load session: found=%v err=%v", found, err)
	}
	before.Metadata.LatestUserPrompt = "must roll back"
	before.Metadata.LatestUserPromptAt = time.Now().UTC()
	before.UpdatedAt = before.Metadata.LatestUserPromptAt
	if applied, err := s.UpdateSessionFromActivitySignalAndAcceptSemanticDelivery(ctx, before, before.Revision, "launch-1", deliveryID); err == nil || applied {
		t.Fatalf("projection with failed receipt write: applied=%v err=%v, want rollback error", applied, err)
	}
	after, found, err := s.GetSession(ctx, sessionID)
	if err != nil || !found {
		t.Fatalf("reload session: found=%v err=%v", found, err)
	}
	if after.Revision != before.Revision || after.Metadata.LatestUserPrompt == "must roll back" {
		t.Fatalf("session projection escaped rollback: revision %d->%d prompt=%q", before.Revision, after.Revision, after.Metadata.LatestUserPrompt)
	}
	if accepted, err := s.SemanticDeliveryAccepted(ctx, sessionID, "launch-1", deliveryID); err != nil || accepted {
		t.Fatalf("failed receipt write became accepted: accepted=%v err=%v", accepted, err)
	}
}
