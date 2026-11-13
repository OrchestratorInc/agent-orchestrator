package lifecycle

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type observationOwnershipStore struct {
	*fakeStore
	pr    domain.PullRequest
	found bool
}

func (s *observationOwnershipStore) GetPR(context.Context, string) (domain.PullRequest, bool, error) {
	return s.pr, s.found, nil
}

func TestSCMObservation_OwnershipGuardsNotifications(t *testing.T) {
	now := time.Date(2026, 1, 20, 12, 0, 0, 0, time.UTC)
	for _, owner := range []struct {
		name, source string
		created      time.Time
		found, want  bool
	}{
		{"older", "", now.Add(-18 * 24 * time.Hour), true, false},
		{"undated", domain.PROwnershipDiscovered, time.Time{}, true, false},
		{"missing", "", time.Time{}, false, false},
		{"current", domain.PROwnershipDiscovered, now, true, true},
		{"claimed older", domain.PROwnershipClaimed, now.Add(-18 * 24 * time.Hour), true, true},
	} {
		for _, state := range []string{"ready", "merged", "closed", "failing CI"} {
			t.Run(owner.name+"/"+state, func(t *testing.T) {
				st := &observationOwnershipStore{fakeStore: newFakeStore(), found: owner.found}
				rec := working("mer-1")
				rec.CreatedAt = now
				st.sessions[rec.ID] = rec
				st.pr = domain.PullRequest{URL: "pr1", SessionID: rec.ID, OwnershipSource: owner.source, CreatedAtProvider: owner.created}
				sink := &fakeNotificationSink{}
				m := New(st, nil, WithNotificationSink(sink))
				obs := ports.SCMObservation{
					Fetched: true, PR: ports.SCMPRObservation{URL: st.pr.URL, Merged: state == "merged", Closed: state == "closed"},
					CI:           ports.SCMCIObservation{Summary: string(domain.CIPassing)},
					Review:       ports.SCMReviewObservation{Decision: string(domain.ReviewApproved)},
					Mergeability: ports.SCMMergeabilityObservation{State: string(domain.MergeMergeable)},
				}
				if state == "failing CI" {
					obs.CI.Summary = string(domain.CIFailing)
				}
				if err := m.ApplySCMObservation(ctx, rec.ID, obs); err != nil {
					t.Fatal(err)
				}
				if got, want := len(sink.intents) > 0, owner.want && state != "failing CI"; got != want {
					t.Fatalf("notification intents=%v, want emission=%v", sink.intents, want)
				}
				if got, want := len(sink.resolutions) > 0, owner.want && state != "ready"; got != want {
					t.Fatalf("notification resolutions=%v, want resolution=%v", sink.resolutions, want)
				}
			})
		}
	}
}

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

func TestSessionCompletionIgnoresUncertainAssociations(t *testing.T) {
	for _, ownedMerged := range []bool{false, true} {
		t.Run(map[bool]string{false: "unowned merge cannot complete", true: "unowned open cannot block"}[ownedMerged], func(t *testing.T) {
			m, st, _ := newManager()
			rec := working("mer-1")
			rec.CreatedAt = time.Date(2026, 1, 20, 12, 0, 0, 0, time.UTC)
			st.prs[rec.ID] = []domain.PullRequest{
				{URL: "owned", SessionID: rec.ID, CreatedAtProvider: rec.CreatedAt, Merged: ownedMerged, Closed: !ownedMerged},
				{URL: "uncertain", SessionID: rec.ID, CreatedAtProvider: rec.CreatedAt.Add(-18 * 24 * time.Hour), Merged: !ownedMerged},
			}
			complete, err := m.sessionComplete(ctx, rec)
			if err != nil || complete != ownedMerged {
				t.Fatalf("complete=%v err=%v, want %v", complete, err, ownedMerged)
			}
		})
	}
}
