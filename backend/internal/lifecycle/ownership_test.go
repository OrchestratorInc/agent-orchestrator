package lifecycle

import (
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestPRObservation_OlderOwnershipCannotStartWork(t *testing.T) {
	for _, source := range []string{"", domain.PROwnershipDiscovered, domain.PROwnershipClaimed} {
		t.Run("source="+source, func(t *testing.T) {
			m, st, messages := newManager()
			rec := working("mer-1")
			rec.CreatedAt = time.Date(2026, 1, 20, 12, 0, 0, 0, time.UTC)
			rec.AutoInjectCI, rec.AutoInjectReview = false, false
			st.sessions[rec.ID] = rec
			st.prs[rec.ID] = []domain.PullRequest{{URL: "pr1", SessionID: rec.ID, OwnershipSource: source, CreatedAtProvider: rec.CreatedAt.Add(-18 * 24 * time.Hour)}}
			st.prPolicies["pr1"] = false
			o := ports.PRObservation{Fetched: true, URL: "pr1", Mergeability: domain.MergeConflicting}
			if err := m.ApplyPRObservation(ctx, rec.ID, o); err != nil {
				t.Fatal(err)
			}
			want := 0
			if source == domain.PROwnershipClaimed {
				want = 1
			}
			if len(messages.msgs) != want {
				t.Fatalf("messages=%v, want %d", messages.msgs, want)
			}
		})
	}
}
