package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/domain"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/ports"
)

// ListPRFactsForSession is the real-SQLite batch read the multi-PR status
// aggregator builds stacks from: every owned PR returned newest-first with its
// state flags and branch pair projected (the stack model needs both).
//
// The branch pair is written via WriteSCMObservation (the observer path, the
// source of truth for tracked PRs). The other writer, WritePR, deliberately
// omits source/target branch (UpsertLegacyPR), so the stack model depends on the
// observer having populated the row.
func TestListPRFactsForSessionProjectsAllPRsNewestFirst(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	r, _ := s.CreateSession(ctx, sampleRecord("mer"))
	now := time.Now().UTC().Truncate(time.Second)

	// A stack: root (open) -> child targets the root branch (open) -> a merged
	// historical PR. Distinct updated_at so newest-first ordering is observable.
	write := func(pr domain.PullRequest) {
		t.Helper()
		if err := s.WriteSCMObservation(ctx, pr, nil, nil, nil, nil, ports.ReviewWritePreserve); err != nil {
			t.Fatalf("write %s: %v", pr.URL, err)
		}
	}
	write(domain.PullRequest{URL: "root", SessionID: r.ID, Number: 1, CI: domain.CIPassing, SourceBranch: "feat/x", TargetBranch: "main", UpdatedAt: now, ObservedAt: now})
	write(domain.PullRequest{URL: "child", SessionID: r.ID, Number: 2, Draft: true, SourceBranch: "feat/x/child", TargetBranch: "feat/x", UpdatedAt: now.Add(time.Second), ObservedAt: now})
	write(domain.PullRequest{URL: "old", SessionID: r.ID, Number: 3, Merged: true, SourceBranch: "feat/old", TargetBranch: "main", UpdatedAt: now.Add(2 * time.Second), ObservedAt: now})

	facts, err := s.ListPRFactsForSession(ctx, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 3 {
		t.Fatalf("ListPRFactsForSession = %d, want 3", len(facts))
	}
	// Newest-first by updated_at: old, child, root.
	if facts[0].URL != "old" || facts[1].URL != "child" || facts[2].URL != "root" {
		t.Fatalf("order = [%s %s %s], want [old child root]", facts[0].URL, facts[1].URL, facts[2].URL)
	}
	byURL := map[string]domain.PRFacts{}
	for _, f := range facts {
		byURL[f.URL] = f
	}
	if !byURL["old"].Merged || byURL["old"].Closed || byURL["old"].Draft {
		t.Fatalf("merged PR flags wrong: %+v", byURL["old"])
	}
	if !byURL["child"].Draft || byURL["child"].Merged {
		t.Fatalf("draft child flags wrong: %+v", byURL["child"])
	}
	// The stack model is derived from the source/target branch pair, so it must
	// survive the projection.
	if byURL["child"].SourceBranch != "feat/x/child" || byURL["child"].TargetBranch != "feat/x" {
		t.Fatalf("child branch pair lost: %+v", byURL["child"])
	}
	if byURL["root"].SourceBranch != "feat/x" || byURL["root"].TargetBranch != "main" {
		t.Fatalf("root branch pair lost: %+v", byURL["root"])
	}
	if byURL["root"].CI != domain.CIPassing {
		t.Fatalf("root CI = %q, want passing", byURL["root"].CI)
	}

	// A session with no PRs returns an empty (non-nil) slice, never an error.
	empty, _ := s.CreateSession(ctx, sampleRecord("mer"))
	got, err := s.ListPRFactsForSession(ctx, empty.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("no-PR session = %d facts, want 0", len(got))
	}
}

func TestPRStateChangedAtPersistsOnlyOnStateTransitions(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	r, _ := s.CreateSession(ctx, sampleRecord("mer"))
	createdAt := time.Date(2026, 6, 4, 9, 0, 0, 0, time.UTC)
	updatedAt := time.Date(2026, 6, 4, 9, 30, 0, 0, time.UTC)
	readyAt := time.Date(2026, 6, 4, 10, 0, 0, 0, time.UTC)
	laterAt := time.Date(2026, 6, 4, 11, 0, 0, 0, time.UTC)
	pr := domain.PullRequest{
		URL:               "https://github.com/acme/repo/pull/7",
		SessionID:         r.ID,
		Number:            7,
		Draft:             true,
		CreatedAtProvider: createdAt,
		UpdatedAtProvider: updatedAt,
		UpdatedAt:         updatedAt,
		ObservedAt:        updatedAt,
	}
	if err := s.WriteSCMObservation(ctx, pr, nil, nil, nil, nil, ports.ReviewWritePreserve); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.GetPR(ctx, pr.URL)
	if err != nil || !ok {
		t.Fatalf("GetPR after draft write: ok=%v err=%v", ok, err)
	}
	if !got.StateChangedAt.Equal(createdAt) {
		t.Fatalf("draft stateChangedAt = %s, want created time %s", got.StateChangedAt, createdAt)
	}

	pr.Title = "metadata-only update"
	pr.UpdatedAtProvider = laterAt
	pr.UpdatedAt = laterAt
	pr.ObservedAt = laterAt
	if err := s.WriteSCMObservation(ctx, pr, nil, nil, nil, nil, ports.ReviewWritePreserve); err != nil {
		t.Fatal(err)
	}
	got, ok, err = s.GetPR(ctx, pr.URL)
	if err != nil || !ok {
		t.Fatalf("GetPR after same-state write: ok=%v err=%v", ok, err)
	}
	if !got.StateChangedAt.Equal(createdAt) {
		t.Fatalf("same-state stateChangedAt = %s, want preserved %s", got.StateChangedAt, createdAt)
	}

	pr.Draft = false
	pr.UpdatedAtProvider = readyAt
	pr.UpdatedAt = readyAt
	pr.ObservedAt = readyAt
	if err := s.WriteSCMObservation(ctx, pr, nil, nil, nil, nil, ports.ReviewWritePreserve); err != nil {
		t.Fatal(err)
	}
	got, ok, err = s.GetPR(ctx, pr.URL)
	if err != nil || !ok {
		t.Fatalf("GetPR after ready write: ok=%v err=%v", ok, err)
	}
	if !got.StateChangedAt.Equal(readyAt) {
		t.Fatalf("ready stateChangedAt = %s, want ready time %s", got.StateChangedAt, readyAt)
	}
}

func TestPRStateChangedAtFillsWhenProviderCreatedAtArrives(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	r, _ := s.CreateSession(ctx, sampleRecord("mer"))
	discoveredAt := time.Date(2026, 6, 4, 9, 30, 0, 0, time.UTC)
	createdAt := time.Date(2026, 6, 4, 8, 45, 0, 0, time.UTC)
	laterAt := time.Date(2026, 6, 4, 10, 0, 0, 0, time.UTC)
	pr := domain.PullRequest{
		URL:        "https://github.com/acme/repo/pull/8",
		SessionID:  r.ID,
		Number:     8,
		UpdatedAt:  discoveredAt,
		ObservedAt: discoveredAt,
	}
	if err := s.WriteSCMObservation(ctx, pr, nil, nil, nil, nil, ports.ReviewWritePreserve); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.GetPR(ctx, pr.URL)
	if err != nil || !ok {
		t.Fatalf("GetPR after discovery write: ok=%v err=%v", ok, err)
	}
	if !got.StateChangedAt.IsZero() {
		t.Fatalf("discovery stateChangedAt = %s, want unset without provider creation time", got.StateChangedAt)
	}

	pr.CreatedAtProvider = createdAt
	pr.UpdatedAt = laterAt
	pr.ObservedAt = laterAt
	if err := s.WriteSCMObservation(ctx, pr, nil, nil, nil, nil, ports.ReviewWritePreserve); err != nil {
		t.Fatal(err)
	}
	got, ok, err = s.GetPR(ctx, pr.URL)
	if err != nil || !ok {
		t.Fatalf("GetPR after provider-created write: ok=%v err=%v", ok, err)
	}
	if !got.StateChangedAt.Equal(createdAt) {
		t.Fatalf("same-state stateChangedAt = %s, want provider creation time %s", got.StateChangedAt, createdAt)
	}
}

func TestListPRFactsForSessionsBatchesBySession(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	first, _ := s.CreateSession(ctx, sampleRecord("mer"))
	second, _ := s.CreateSession(ctx, sampleRecord("mer"))
	now := time.Now().UTC().Truncate(time.Second)

	write := func(sessionID domain.SessionID, url string, updatedAt time.Time) {
		t.Helper()
		if err := s.WriteSCMObservation(ctx, domain.PullRequest{
			URL: url, SessionID: sessionID, Number: 1, HeadSHA: "head", UpdatedAt: updatedAt, ObservedAt: updatedAt,
		}, nil, nil, nil, nil, ports.ReviewWritePreserve); err != nil {
			t.Fatalf("write %s: %v", url, err)
		}
	}
	write(first.ID, "pr/1a", now)
	write(first.ID, "pr/1b", now.Add(time.Second))
	write(second.ID, "pr/2a", now.Add(2*time.Second))

	got, err := s.ListPRFactsForSessions(ctx, []domain.SessionID{first.ID, second.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(got[first.ID]) != 2 || len(got[second.ID]) != 1 {
		t.Fatalf("batched facts = %+v", got)
	}
	if got[first.ID][0].URL != "pr/1b" || got[first.ID][1].URL != "pr/1a" {
		t.Fatalf("first session order = %+v, want newest first", got[first.ID])
	}
	if got[second.ID][0].URL != "pr/2a" {
		t.Fatalf("second session facts = %+v", got[second.ID])
	}
}

// The session list orders mobile's workers by when something last changed, so
// both PR-facts reads project the change timestamps: lifecycle, CI, and the
// newest review by provider submission time. The review join must pick exactly
// one review per PR, never fan a PR out into one row per review.
func TestListPRFactsProjectsChangeTimestamps(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	r, _ := s.CreateSession(ctx, sampleRecord("mer"))
	openedAt := time.Date(2026, 6, 4, 9, 0, 0, 0, time.UTC)
	ciAt := time.Date(2026, 6, 4, 9, 30, 0, 0, time.UTC)
	firstReviewAt := time.Date(2026, 6, 4, 10, 0, 0, 0, time.UTC)
	lastReviewAt := time.Date(2026, 6, 4, 11, 0, 0, 0, time.UTC)

	reviewed := domain.PullRequest{
		URL: "reviewed", SessionID: r.ID, Number: 1, CI: domain.CIFailing,
		CreatedAtProvider: openedAt, UpdatedAt: ciAt, ObservedAt: ciAt, CIObservedAt: ciAt,
	}
	reviews := []domain.PullRequestReview{
		{ID: "r1", Author: "alice", State: domain.ReviewChangesRequest, SubmittedAt: firstReviewAt},
		{ID: "r2", Author: "bob", State: domain.ReviewApproved, SubmittedAt: lastReviewAt},
	}
	if err := s.WriteSCMObservation(ctx, reviewed, nil, reviews, nil, nil, ports.ReviewWriteReplace); err != nil {
		t.Fatal(err)
	}
	bare := domain.PullRequest{URL: "bare", SessionID: r.ID, Number: 2, CreatedAtProvider: openedAt, UpdatedAt: openedAt, ObservedAt: openedAt}
	if err := s.WriteSCMObservation(ctx, bare, nil, nil, nil, nil, ports.ReviewWritePreserve); err != nil {
		t.Fatal(err)
	}

	single, err := s.ListPRFactsForSession(ctx, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	batch, err := s.ListPRFactsForSessions(ctx, []domain.SessionID{r.ID})
	if err != nil {
		t.Fatal(err)
	}
	for name, facts := range map[string][]domain.PRFacts{"single": single, "batch": batch[r.ID]} {
		if len(facts) != 2 {
			t.Fatalf("%s: %d facts, want 2 (one row per PR)", name, len(facts))
		}
		byURL := map[string]domain.PRFacts{}
		for _, f := range facts {
			byURL[f.URL] = f
		}
		got := byURL["reviewed"]
		if !got.StateChangedAt.Equal(openedAt) || !got.CIChangedAt.Equal(ciAt) || !got.LastReviewAt.Equal(lastReviewAt) {
			t.Fatalf("%s: reviewed timestamps = state %s ci %s review %s, want %s %s %s",
				name, got.StateChangedAt, got.CIChangedAt, got.LastReviewAt, openedAt, ciAt, lastReviewAt)
		}
		if !byURL["bare"].LastReviewAt.IsZero() {
			t.Fatalf("%s: unreviewed PR LastReviewAt = %s, want zero", name, byURL["bare"].LastReviewAt)
		}
	}
}

// A PR whose base branch is another open PR's branch is stacked on it, even
// when the parent belongs to a different session. The read projects the
// parent's number so the board does not call the child ready to merge.
func TestListPRFactsProjectsTheOpenStackParent(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	parentSession, _ := s.CreateSession(ctx, sampleRecord("mer"))
	childSession, _ := s.CreateSession(ctx, sampleRecord("mer"))
	now := time.Now().UTC().Truncate(time.Second)
	write := func(pr domain.PullRequest) {
		t.Helper()
		pr.Host, pr.UpdatedAt, pr.ObservedAt = "github.com", now, now
		if pr.Repo == "" {
			pr.Repo = "acme/app"
		}
		if err := s.WriteSCMObservation(ctx, pr, nil, nil, nil, nil, ports.ReviewWritePreserve); err != nil {
			t.Fatalf("write %s: %v", pr.URL, err)
		}
	}
	// Open parent in another session, and a fork PR opened from its own main.
	write(domain.PullRequest{URL: "parent", SessionID: parentSession.ID, Number: 8277, SourceBranch: "dev-warm", TargetBranch: "main"})
	write(domain.PullRequest{URL: "fork", SessionID: parentSession.ID, Number: 9000, SourceBranch: "main", TargetBranch: "main"})
	// A merged parent and a same-named branch in another repository.
	write(domain.PullRequest{URL: "landed", SessionID: parentSession.ID, Number: 8100, Merged: true, SourceBranch: "old-base", TargetBranch: "main"})
	write(domain.PullRequest{URL: "elsewhere", SessionID: parentSession.ID, Number: 50, Repo: "acme/other", SourceBranch: "other-base", TargetBranch: "main"})
	// Long-lived heads: a main -> release sync PR would parent every PR into
	// main, and a fork's develop says nothing about this repository's develop.
	write(domain.PullRequest{URL: "sync", SessionID: parentSession.ID, Number: 9100, SourceBranch: "main", TargetBranch: "release", HeadIsDefaultBranch: true})
	write(domain.PullRequest{URL: "fork-develop", SessionID: parentSession.ID, Number: 9200, SourceBranch: "develop", TargetBranch: "main", HeadFromFork: true})
	// An archived session's PR still reads open, but nothing polls it any more,
	// so that state may be stale and it must not hold a PR back.
	archivedSession, _ := s.CreateSession(ctx, sampleRecord("mer"))
	write(domain.PullRequest{URL: "parked", SessionID: archivedSession.ID, Number: 7000, SourceBranch: "parked-base", TargetBranch: "main"})
	archivedSession.IsTerminated = true
	archivedSession.UpdatedAt = now
	if err := s.UpdateSession(ctx, archivedSession); err != nil {
		t.Fatal(err)
	}

	write(domain.PullRequest{URL: "stacked", SessionID: childSession.ID, Number: 8487, SourceBranch: "fast", TargetBranch: "dev-warm"})
	write(domain.PullRequest{URL: "on-main", SessionID: childSession.ID, Number: 8484, SourceBranch: "icons", TargetBranch: "main"})
	write(domain.PullRequest{URL: "on-landed", SessionID: childSession.ID, Number: 8101, SourceBranch: "follow-up", TargetBranch: "old-base"})
	write(domain.PullRequest{URL: "on-other-repo", SessionID: childSession.ID, Number: 8102, SourceBranch: "cross", TargetBranch: "other-base"})
	write(domain.PullRequest{URL: "on-archived", SessionID: childSession.ID, Number: 8103, SourceBranch: "late", TargetBranch: "parked-base"})
	write(domain.PullRequest{URL: "on-develop", SessionID: childSession.ID, Number: 8104, SourceBranch: "feature", TargetBranch: "develop"})

	want := map[string]int{"stacked": 8277, "on-main": 0, "on-landed": 0, "on-other-repo": 0, "on-archived": 0, "on-develop": 0}
	check := func(label string, facts []domain.PRFacts) {
		t.Helper()
		if len(facts) != len(want) {
			t.Fatalf("%s: %d facts, want %d", label, len(facts), len(want))
		}
		for _, f := range facts {
			if f.StackParent != want[f.URL] {
				t.Fatalf("%s: %s stack parent = %d, want %d", label, f.URL, f.StackParent, want[f.URL])
			}
		}
	}
	single, err := s.ListPRFactsForSession(ctx, childSession.ID)
	if err != nil {
		t.Fatal(err)
	}
	check("single", single)
	batch, err := s.ListPRFactsForSessions(ctx, []domain.SessionID{childSession.ID})
	if err != nil {
		t.Fatal(err)
	}
	check("batch", batch[childSession.ID])

	// The flags survive a read, so a poll that rebuilds the row from storage
	// keeps them.
	for url, want := range map[string][2]bool{"sync": {false, true}, "fork-develop": {true, false}, "parent": {false, false}} {
		got, ok, err := s.GetPR(ctx, url)
		if err != nil || !ok {
			t.Fatalf("get %s: ok=%v err=%v", url, ok, err)
		}
		if got.HeadFromFork != want[0] || got.HeadIsDefaultBranch != want[1] {
			t.Fatalf("%s: fork=%v default=%v, want %v", url, got.HeadFromFork, got.HeadIsDefaultBranch, want)
		}
	}
}
