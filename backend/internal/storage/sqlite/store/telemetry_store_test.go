package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/domain"
	sqlitestore "github.com/OrchestratorInc/agent-orchestrator/backend/internal/storage/sqlite/store"
)

func TestTelemetryStore_CreateListAndPrune(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	projectID := domain.ProjectID("mer")
	sessionID := domain.SessionID("mer-1")
	seedProject(t, s, string(projectID))

	oldAt := time.Now().UTC().Add(-31 * 24 * time.Hour).Truncate(time.Second)
	newAt := time.Now().UTC().Truncate(time.Second)

	if err := s.CreateTelemetryEvent(ctx, telemetryRecord("tev_old", oldAt, &projectID, &sessionID)); err != nil {
		t.Fatalf("CreateTelemetryEvent old: %v", err)
	}
	if err := s.CreateTelemetryEvent(ctx, telemetryRecord("tev_new", newAt, &projectID, &sessionID)); err != nil {
		t.Fatalf("CreateTelemetryEvent new: %v", err)
	}

	rows, err := s.ListTelemetryEventsSince(ctx, oldAt.Add(-time.Second), 10)
	if err != nil {
		t.Fatalf("ListTelemetryEventsSince: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	if rows[0].ID != "tev_old" || rows[1].ID != "tev_new" {
		t.Fatalf("ids = %q, %q", rows[0].ID, rows[1].ID)
	}

	n, err := s.PruneTelemetryEventsBefore(ctx, newAt.Add(-24*time.Hour), 100)
	if err != nil {
		t.Fatalf("PruneTelemetryEventsBefore: %v", err)
	}
	if n != 1 {
		t.Fatalf("pruned = %d, want 1", n)
	}

	rows, err = s.ListTelemetryEventsSince(ctx, oldAt.Add(-time.Second), 10)
	if err != nil {
		t.Fatalf("ListTelemetryEventsSince after prune: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != "tev_new" {
		t.Fatalf("remaining rows = %+v", rows)
	}
}

func telemetryRecord(id string, at time.Time, projectID *domain.ProjectID, sessionID *domain.SessionID) sqlitestore.TelemetryEventRecord {
	return sqlitestore.TelemetryEventRecord{
		ID:          id,
		OccurredAt:  at,
		Name:        "ao.daemon.started",
		Source:      "daemon",
		Level:       "info",
		ProjectID:   projectID,
		SessionID:   sessionID,
		RequestID:   "req_123",
		PayloadJSON: `{"port":3001}`,
	}
}
