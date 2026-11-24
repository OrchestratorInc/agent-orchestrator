package store_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/domain"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/storage/sqlite"
)

func semanticDeliveryFixture(t *testing.T) (*sqlite.Store, domain.SessionID) {
	t.Helper()
	s, err := sqlite.Open(t.TempDir())
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
	return s, created.ID
}

func TestAcceptedDeliveriesSurviveHumanPromptsAndLaterDeliveries(t *testing.T) {
	s, sessionID := semanticDeliveryFixture(t)
	ctx := context.Background()
	project := func(deliveryID, prompt string) {
		t.Helper()
		rec, found, err := s.GetSession(ctx, sessionID)
		if err != nil || !found {
			t.Fatalf("load session: found=%v err=%v", found, err)
		}
		next := rec
		next.Metadata.LatestUserPrompt = prompt
		next.Metadata.LatestUserPromptAt = time.Now().UTC()
		var applied bool
		if deliveryID == "" {
			applied, err = s.UpdateSessionFromActivitySignal(ctx, next, rec.Revision)
		} else {
			applied, err = s.UpdateSessionFromActivitySignalAndAcceptSemanticDelivery(ctx, next, rec.Revision, deliveryID)
		}
		if err != nil || !applied {
			t.Fatalf("project %q: applied=%v err=%v", prompt, applied, err)
		}
	}
	accepted := func(launchID, deliveryID string) bool {
		t.Helper()
		ok, err := s.SemanticDeliveryAccepted(ctx, sessionID, launchID, deliveryID)
		if err != nil {
			t.Fatalf("read accepted delivery: %v", err)
		}
		return ok
	}

	project("report-batch:first", "report")
	if !accepted("launch-1", "report-batch:first") {
		t.Fatal("accepting projection did not record the delivery")
	}
	project("", "human prompt")
	if !accepted("launch-1", "report-batch:first") {
		t.Fatal("human prompt erased the accepted delivery")
	}
	if accepted("launch-2", "report-batch:first") || accepted("launch-1", "report-batch:other") || accepted("launch-1", "batch:first") {
		t.Fatal("accepted delivery matched another launch or delivery")
	}
	// A delivery accepted just before another one must still be found when its
	// sender checks again, or that sender pastes it a second time.
	project("report-batch:second", "next report")
	if !accepted("launch-1", "report-batch:second") || !accepted("launch-1", "report-batch:first") {
		t.Fatal("next accepted delivery erased the first")
	}
	for i := 3; i <= 9; i++ {
		project(fmt.Sprintf("report-batch:%d", i), "later report")
	}
	if accepted("launch-1", "report-batch:first") || !accepted("launch-1", "report-batch:second") || !accepted("launch-1", "report-batch:9") {
		t.Fatal("accepted deliveries are not the launch's last 8")
	}
	rec, _, err := s.GetSession(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateSessionFromActivitySignalAndAcceptSemanticDelivery(ctx, rec, rec.Revision, ""); err == nil {
		t.Fatal("empty delivery id was accepted")
	}
}
