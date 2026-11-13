package scm

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

func TestPoll_CrossInstallOwnership(t *testing.T) {
	now := time.Date(2026, 1, 20, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name, branch, temporary, source string
		created                         time.Time
		want                            bool
	}{
		{"older root namespace", "ao/example-42/root", "", "ao/example-42/topic", now.Add(-18 * 24 * time.Hour), false},
		{"older temporary namespace", "ao/renamed/root", "ao/example-42/root", "ao/example-42/topic", now.Add(-18 * 24 * time.Hour), false},
		{"older exact branch", "ao/example-42/root", "", "ao/example-42/root", now.Add(-18 * 24 * time.Hour), false},
		{"unknown creation time", "ao/example-42/root", "", "ao/example-42/topic", time.Time{}, false},
		{"current PR", "ao/example-42/root", "", "ao/example-42/topic", now.Add(time.Minute), true},
		{"current stack", "ao/example-42/root", "", "ao/example-42/topic/child", now.Add(time.Minute), true},
		{"current temporary namespace", "ao/renamed/root", "ao/example-42/root", "ao/example-42/topic", now.Add(time.Minute), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := testStoreWithSession()
			store.sessions[0].ID = "example-42"
			store.sessions[0].CreatedAt = now
			store.sessions[0].Metadata.Branch = tc.branch
			store.sessions[0].TemporaryBranch = tc.temporary
			obs := testObs(101)
			obs.PR.SourceBranch, obs.PR.HeadRepo, obs.PR.Author = tc.source, "o/r", "alice"
			obs.PR.CreatedAtProvider = tc.created
			obs.Mergeability.State = string(domain.MergeConflicting)
			provider := &fakeProvider{
				openPRs:      map[string][]ports.SCMPRObservation{prKey(testRepo, 0): {obs.PR}},
				observations: map[string]ports.SCMObservation{prKey(testRepo, 101): obs},
			}
			lifecycle := &fakeLifecycle{}
			observer := newTestObserver(store, provider, lifecycle, now)
			if err := observer.Poll(context.Background()); err != nil {
				t.Fatal(err)
			}
			if (len(store.writes) > 0) != tc.want || (len(lifecycle.observed) > 0) != tc.want {
				t.Fatalf("writes=%d lifecycle=%d, want attachment=%v", len(store.writes), len(lifecycle.observed), tc.want)
			}
		})
	}
}

func TestPoll_StoredOlderAssociationRequiresExplicitClaim(t *testing.T) {
	ctx := context.Background()
	st := sqlitetest.MustOpen(t)
	now := time.Date(2026, 1, 20, 12, 0, 0, 0, time.UTC)
	if err := st.UpsertProject(ctx, domain.ProjectRecord{ID: "p", Path: t.TempDir(), RepoOriginURL: "https://github.com/o/r.git", RegisteredAt: now}); err != nil {
		t.Fatal(err)
	}
	sess, err := st.CreateSession(ctx, domain.SessionRecord{ProjectID: "p", Kind: domain.KindWorker, CreatedAt: now, UpdatedAt: now, Metadata: domain.SessionMetadata{Branch: "ao/example-42/root"}})
	if err != nil {
		t.Fatal(err)
	}
	obs := testObs(101)
	obs.PR.CreatedAtProvider = now.Add(-18 * 24 * time.Hour)
	obs.PR.SourceBranch, obs.PR.HeadRepo, obs.PR.Author = "ao/example-42/topic", "o/r", "alice"
	obs.PR.ProviderID = "node-101"
	old := domain.PullRequest{URL: obs.PR.URL, SessionID: sess.ID, Number: 101, Provider: "github", Host: "github.com", Repo: "o/r", ProviderID: obs.PR.ProviderID, CreatedAtProvider: obs.PR.CreatedAtProvider, SourceBranch: obs.PR.SourceBranch, UpdatedAt: now}
	if err := st.WriteSCMObservation(ctx, old, nil, nil, nil, nil, ports.ReviewWritePreserve); err != nil {
		t.Fatal(err)
	}
	provider := &fakeProvider{observations: map[string]ports.SCMObservation{prKey(testRepo, 101): obs}}
	lifecycle := &fakeLifecycle{}
	poll := func() {
		t.Helper()
		observer := New(provider, st, lifecycle, Config{Logger: quietSlog(), Clock: func() time.Time { return now }, ScopedIdentityResolver: provider})
		if err := observer.Poll(ctx); err != nil {
			t.Fatal(err)
		}
	}
	poll()
	if len(lifecycle.observed) != 0 {
		t.Fatal("legacy association triggered automatic work")
	}
	if _, err := st.ClaimPR(ctx, old, nil, nil, nil, nil, ports.ReviewWritePreserve, false); err != nil {
		t.Fatal(err)
	}
	poll()
	if len(lifecycle.observed) != 1 {
		t.Fatalf("explicit claim delivered %d observations", len(lifecycle.observed))
	}
	got, found, err := st.GetPR(ctx, old.URL)
	if err != nil || !found || got.OwnershipSource != domain.PROwnershipClaimed {
		t.Fatalf("claim provenance lost: %+v %v", got, err)
	}
}

func TestMatchSession_TokenizedWorkspaceBranches(t *testing.T) {
	for _, prefix := range []string{"ao/", "ao/dev/"} {
		branch := prefix + "example-42-89eb684a-28d9-4f8c-bf0b-3ff0891663a0"
		candidate := sessionRepo{session: domain.SessionRecord{ID: "example-42"}, branch: branch, workspace: true}
		for _, suffix := range []string{"", "-2"} {
			candidate.branch = branch + suffix
			if _, ok := matchSession([]sessionRepo{candidate}, candidate.branch+"-topic"); !ok {
				t.Fatalf("workspace PR not matched under %q", candidate.branch)
			}
		}
		if _, ok := matchSession([]sessionRepo{candidate}, branch+"extra-topic"); ok {
			t.Fatal("unrelated namespace matched")
		}
	}
}
