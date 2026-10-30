package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// TestAgentLimitReadingsRoundTripAndExpire pins the reading history: a reading
// comes back as it was saved, a repeated save of the same read is ignored, and
// a save drops readings older than 14 days.
func TestAgentLimitReadingsRoundTripAndExpire(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	t0 := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	resets := t0.Add(3 * time.Hour)
	reading := func(at time.Time, used float64) domain.AgentLimitReading {
		return domain.AgentLimitReading{
			AccountID: "device-claude", Provider: domain.AgentAccountProviderClaude, Email: "a@example.com", Plan: "max",
			WindowID: "five_hour", Label: "Session (5h)", DurationMinutes: 300, UsedPercent: used, ResetsAt: &resets, ReadAt: at,
		}
	}
	mustNoError(t, s.RecordAgentLimitReadings(ctx, []domain.AgentLimitReading{reading(t0, 12)}))
	mustNoError(t, s.RecordAgentLimitReadings(ctx, []domain.AgentLimitReading{reading(t0, 12)}))
	mustNoError(t, s.RecordAgentLimitReadings(ctx, []domain.AgentLimitReading{{
		AccountID: "device-codex", Provider: domain.AgentAccountProviderCodex, WindowID: "codex:Weekly", UsedPercent: 40, ReadAt: t0.Add(time.Minute),
	}}))

	got, err := s.ListAgentLimitReadings(ctx, t0.Add(-time.Hour))
	mustNoError(t, err)
	if len(got) != 2 {
		t.Fatalf("readings = %+v, want the Claude read once and the Codex read", got)
	}
	claude := got[0]
	if claude.Email != "a@example.com" || claude.Plan != "max" || claude.DurationMinutes != 300 || claude.UsedPercent != 12 || !claude.ReadAt.Equal(t0) ||
		claude.ResetsAt == nil || !claude.ResetsAt.Equal(resets) {
		t.Fatalf("claude reading = %+v", claude)
	}
	if got[1].ResetsAt != nil {
		t.Fatalf("codex reading without a reset = %+v", got[1])
	}

	mustNoError(t, s.RecordAgentLimitReadings(ctx, []domain.AgentLimitReading{reading(t0.Add(15*24*time.Hour), 30)}))
	got, err = s.ListAgentLimitReadings(ctx, t0.Add(-time.Hour))
	mustNoError(t, err)
	if len(got) != 1 || got[0].UsedPercent != 30 {
		t.Fatalf("after 15 days readings = %+v, want only the new one", got)
	}
}
