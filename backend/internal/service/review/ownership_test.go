package review

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	reviewcore "github.com/aoagents/agent-orchestrator/backend/internal/review"
	sessionsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/session"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

type ownershipReviewProvider struct{ observation ports.SCMObservation }

func (ownershipReviewProvider) ParseRepository(string) (ports.SCMRepo, bool) {
	return ports.SCMRepo{Provider: "github", Host: "github.com", Owner: "o", Name: "r", Repo: "o/r"}, true
}

func (p ownershipReviewProvider) FetchPullRequests(context.Context, []ports.SCMPRRef) ([]ports.SCMObservation, error) {
	return []ports.SCMObservation{p.observation}, nil
}

func (ownershipReviewProvider) FetchReviewThreads(context.Context, ports.SCMPRRef) (ports.SCMReviewObservation, error) {
	return ports.SCMReviewObservation{}, nil
}

type ownershipPRRefresher struct{ sessions *sessionsvc.Service }

func (r ownershipPRRefresher) RefreshPR(ctx context.Context, id domain.SessionID, url string, claim bool) error {
	_, err := r.sessions.ClaimPR(ctx, id, url, sessionsvc.ClaimPROptions{PreserveOwnership: !claim})
	return err
}

func TestTriggerRequestedPreservesOwnershipUnlessPRIsNamed(t *testing.T) {
	for _, tc := range []struct {
		name, source   string
		named, current bool
	}{
		{name: "legacy"},
		{name: "discovered", source: domain.PROwnershipDiscovered},
		{name: "claimed", source: domain.PROwnershipClaimed},
		{name: "named legacy", named: true},
		{name: "named discovered", source: domain.PROwnershipDiscovered, named: true},
		{name: "current discovered", source: domain.PROwnershipDiscovered, current: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			st := sqlitetest.MustOpen(t)
			now := time.Date(2026, 1, 20, 12, 0, 0, 0, time.UTC)
			if err := st.UpsertProject(ctx, domain.ProjectRecord{ID: "p", Path: t.TempDir(), RepoOriginURL: "https://github.com/o/r.git", RegisteredAt: now}); err != nil {
				t.Fatal(err)
			}
			worker, err := st.CreateSession(ctx, domain.SessionRecord{ProjectID: "p", Kind: domain.KindWorker, CreatedAt: now, UpdatedAt: now, Metadata: domain.SessionMetadata{WorkspacePath: t.TempDir()}})
			if err != nil {
				t.Fatal(err)
			}
			created := now.Add(-24 * time.Hour)
			if tc.current {
				created = now
			}
			pr := domain.PullRequest{URL: "https://github.com/o/r/pull/1", SessionID: worker.ID, Number: 1, OwnershipSource: tc.source, CreatedAtProvider: created, UpdatedAt: now}
			if err := st.WriteSCMObservation(ctx, pr, nil, nil, nil, nil, ports.ReviewWritePreserve); err != nil {
				t.Fatal(err)
			}
			provider := ownershipReviewProvider{observation: ports.SCMObservation{
				Fetched: true, Provider: "github", Host: "github.com", Repo: "o/r",
				PR: ports.SCMPRObservation{URL: pr.URL, Number: 1, CreatedAtProvider: created, HeadSHA: "fresh-head"},
			}}
			sessions := sessionsvc.NewWithDeps(sessionsvc.Deps{Store: st, PRClaimer: st, SCM: provider})
			svc := New(nil, st, WithPRRefresher(ownershipPRRefresher{sessions: sessions}))
			triggered := false
			svc.engineTrigger = func(context.Context, domain.SessionID, reviewcore.TriggerOptions) (reviewcore.TriggerResult, error) {
				triggered = true
				return reviewcore.TriggerResult{Created: true}, nil
			}
			req := TriggerRequest{Source: domain.ReviewTriggerManual}
			want := tc.source
			if tc.named {
				req.PRURL, want = pr.URL, domain.PROwnershipClaimed
			}
			if _, err := svc.TriggerRequested(ctx, worker.ID, req); err != nil {
				t.Fatal(err)
			}
			got, found, err := st.GetPR(ctx, pr.URL)
			if err != nil || !found || !triggered || got.HeadSHA != "fresh-head" {
				t.Fatalf("review did not refresh facts: PR=%+v found=%v triggered=%v err=%v", got, found, triggered, err)
			}
			if got.OwnershipSource != want || got.AllowsAutomaticWork(worker) != (tc.named || tc.current || tc.source == domain.PROwnershipClaimed) {
				t.Fatalf("review changed ownership: source=%q allowed=%v, want source=%q", got.OwnershipSource, got.AllowsAutomaticWork(worker), want)
			}
		})
	}
}
