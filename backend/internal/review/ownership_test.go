package review

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestAutomaticReviewRequiresOwnershipEvidence(t *testing.T) {
	for _, tc := range []struct {
		name      string
		source    domain.ReviewTriggerSource
		ownership string
		age       time.Duration
		want      bool
	}{
		{"uncertain automatic", domain.ReviewTriggerAuto, "", -18 * 24 * time.Hour, false},
		{"claimed automatic", domain.ReviewTriggerAuto, domain.PROwnershipClaimed, -18 * 24 * time.Hour, true},
		{"current automatic", domain.ReviewTriggerAuto, domain.PROwnershipDiscovered, time.Minute, true},
		{"undated automatic", domain.ReviewTriggerAuto, domain.PROwnershipDiscovered, 0, false},
		{"explicit review", domain.ReviewTriggerManual, "", -18 * 24 * time.Hour, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			worker := liveWorker()
			worker.CreatedAt = time.Date(2026, 1, 20, 12, 0, 0, 0, time.UTC)
			prs := prAt("sha1")
			prs.prs[0].CreatedAtProvider = worker.CreatedAt.Add(tc.age)
			if tc.age == 0 {
				prs.prs[0].CreatedAtProvider = time.Time{}
			}
			prs.prs[0].OwnershipSource = tc.ownership
			launcher := &fakeLauncher{}
			engine := newEngineForTest(&fakeStore{}, fakeSessions{rec: worker, ok: true}, prs, fakeProjects{}, launcher)
			result, err := engine.TriggerWithOptions(context.Background(), worker.ID, TriggerOptions{Source: tc.source})
			if err != nil {
				t.Fatal(err)
			}
			if result.Created != tc.want || launcher.spawned != tc.want {
				t.Fatalf("result=%+v spawned=%v, want %v", result, launcher.spawned, tc.want)
			}
		})
	}
}
