package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/telemetrymeta"
)

// The usage summary keeps count as direct human Chat messages and adds the
// other human sends and a per-client split, all drained and reset together.
func TestUsageMessageSummaryCountsSurfacesSteersAndSends(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "repo")
	allowed := true
	s.SetUsageTelemetryAllowed(func() bool { return allowed })
	record := sampleRecord("repo")
	record.Mode = domain.SessionModeChat
	session, err := s.CreateSession(ctx, record)
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := s.CreateConversation(ctx, "conversation", domain.ConversationScopeSession, session.ProjectID, session.ID, testNow)
	if err != nil {
		t.Fatal(err)
	}
	desktop := telemetrymeta.WithSurface(ctx, domain.RequestSurfaceDesktop)
	if created, err := s.AppendUserMessage(desktop, conversation.ID, session.ID, "generation",
		domain.ConversationMessage{ID: "m1", Origin: domain.MessageOriginHuman, Text: "private", ClientMessageID: "m1"}, "turn-m1", testNow); err != nil || !created {
		t.Fatalf("append: created=%v err=%v", created, err)
	}
	if err := s.RecordUsageHumanSend(telemetrymeta.WithSurface(ctx, domain.RequestSurfaceMobile), session.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordUsageHumanSend(telemetrymeta.WithSurface(ctx, domain.RequestSurfaceCLI), session.ID, false); err != nil {
		t.Fatal(err)
	}
	allowed = false
	if err := s.RecordUsageHumanSend(ctx, session.ID, true); err != nil {
		t.Fatal(err)
	}
	allowed = true

	batches, err := s.DrainUsageMessages(ctx, 10, false, "install-one")
	if err != nil || len(batches) != 1 {
		t.Fatalf("batches = %+v, err=%v", batches, err)
	}
	b := batches[0]
	if b.Count != 1 || b.SteerCount != 1 || b.SendCount != 1 || b.DesktopCount != 1 || b.MobileCount != 1 || b.CLICount != 1 {
		t.Fatalf("counts = %+v, want one of each (the send while off is not counted)", b)
	}
	if b.Kind != string(session.Kind) || b.Harness != string(session.Harness) {
		t.Fatalf("kind/harness = %q/%q, want %q/%q", b.Kind, b.Harness, session.Kind, session.Harness)
	}
	if more, err := s.DrainUsageMessages(ctx, 10, false, "install-one"); err != nil || len(more) != 0 {
		t.Fatalf("after drain = %+v, err=%v; every counter must reset", more, err)
	}

	// A session with only a steer still drains: it is a returning person.
	if err := s.RecordUsageHumanSend(ctx, session.ID, true); err != nil {
		t.Fatal(err)
	}
	batches, err = s.DrainUsageMessages(ctx, 10, false, "install-one")
	if err != nil || len(batches) != 1 || batches[0].Count != 0 || batches[0].SteerCount != 1 {
		t.Fatalf("steer-only batch = %+v, err=%v", batches, err)
	}
}

func TestUsageMessageOptOutDiscardsEveryCounter(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "repo")
	s.SetUsageTelemetryAllowed(func() bool { return true })
	session, err := s.CreateSession(ctx, sampleRecord("repo"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RecordUsageHumanSend(ctx, session.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := s.DiscardUsageMessages(ctx); err != nil {
		t.Fatal(err)
	}
	if batches, err := s.DrainUsageMessages(ctx, 10, false, "install-one"); err != nil || len(batches) != 0 {
		t.Fatalf("batches after opt-out = %+v, err=%v", batches, err)
	}
}

func TestUsagePRClosedOutcomeMergedViaAndDetail(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "repo")
	s.SetUsageTelemetryAllowed(func() bool { return true })
	worker, err := s.CreateSession(ctx, sampleRecord("repo"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	opened := now.Add(-time.Hour)
	write := func(pr domain.PullRequest, reviews []domain.PullRequestReview) {
		t.Helper()
		mode := ports.ReviewWritePreserve
		if reviews != nil {
			mode = ports.ReviewWriteReplace
		}
		if err := s.WriteSCMObservation(ctx, pr, nil, reviews, nil, nil, mode); err != nil {
			t.Fatal(err)
		}
	}
	merged := domain.PullRequest{URL: "https://github.com/org/repo/pull/1", SessionID: worker.ID, Provider: "github",
		Host: "github.com", Repo: "org/repo", Number: 1, ProviderID: "PR_1", UpdatedAt: now, CreatedAtProvider: opened}
	closed := domain.PullRequest{URL: "https://github.com/org/repo/pull/2", SessionID: worker.ID, Provider: "github",
		Host: "github.com", Repo: "org/repo", Number: 2, ProviderID: "PR_2", UpdatedAt: now, CreatedAtProvider: opened}
	write(merged, []domain.PullRequestReview{{ID: "r1", State: domain.ReviewChangesRequest, SubmittedAt: now}, {ID: "r2", State: domain.ReviewApproved, SubmittedAt: now}})
	write(closed, nil)

	assoc, mergedBefore, err := s.WorkerPRUsageHistory(ctx)
	if err != nil || assoc || mergedBefore {
		t.Fatalf("history before any report = %v/%v, err=%v", assoc, mergedBefore, err)
	}
	facts, err := s.PendingWorkerPRUsage(ctx, 10)
	if err != nil || len(facts) != 2 {
		t.Fatalf("facts = %+v, err=%v", facts, err)
	}
	for _, f := range facts {
		if err := s.MarkWorkerPRUsageReported(ctx, f.Key, true, false, false); err != nil {
			t.Fatal(err)
		}
	}
	if assoc, _, _ := s.WorkerPRUsageHistory(ctx); !assoc {
		t.Fatal("reported association not visible as history")
	}

	if err := s.RecordWorkerPRMergedVia(ctx, merged.URL, "mobile"); err != nil {
		t.Fatal(err)
	}
	merged.Merged, merged.MergedAtProvider = true, now
	write(merged, nil)
	closed.Closed, closed.ClosedAtProvider = true, now
	write(closed, nil)

	facts, err = s.PendingWorkerPRUsage(ctx, 10)
	if err != nil || len(facts) != 2 {
		t.Fatalf("outcome facts = %+v, err=%v", facts, err)
	}
	byNumber := map[string]bool{}
	for _, f := range facts {
		byNumber[f.Key] = true
		switch {
		case f.MergePending:
			if f.MergedVia != "mobile" || f.ClosePending || f.ReviewRounds != 2 || f.AOReviewUsed ||
				f.Provider != "github" || f.Harness != string(worker.Harness) || f.SessionKind != "worker" ||
				!f.PRCreatedAt.Equal(opened) || !f.MergedAt.Equal(now) || f.SessionCreatedAt.IsZero() {
				t.Fatalf("merge fact = %+v", f)
			}
		case f.ClosePending:
			if f.Merged || f.MergedVia != "" || !f.ClosedAt.Equal(now) {
				t.Fatalf("close fact = %+v", f)
			}
		default:
			t.Fatalf("unexpected fact %+v", f)
		}
		if err := s.MarkWorkerPRUsageReported(ctx, f.Key, false, f.MergePending, f.ClosePending); err != nil {
			t.Fatal(err)
		}
	}
	if facts, _ := s.PendingWorkerPRUsage(ctx, 10); len(facts) != 0 {
		t.Fatalf("outcomes reported twice: %+v", facts)
	}
	if _, mergedBefore, _ := s.WorkerPRUsageHistory(ctx); !mergedBefore {
		t.Fatal("reported merge not visible as history")
	}
	// merged_via is frozen once the merge is reported.
	if err := s.RecordWorkerPRMergedVia(ctx, merged.URL, "desktop"); err != nil {
		t.Fatal(err)
	}
}

func TestUsagePRDiscardMarksPendingCloses(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "repo")
	s.SetUsageTelemetryAllowed(func() bool { return true })
	worker, err := s.CreateSession(ctx, sampleRecord("repo"))
	if err != nil {
		t.Fatal(err)
	}
	pr := domain.PullRequest{URL: "https://github.com/org/repo/pull/3", SessionID: worker.ID, Provider: "github",
		Host: "github.com", Repo: "org/repo", Number: 3, Closed: true, UpdatedAt: time.Now().UTC()}
	if err := s.WriteSCMObservation(ctx, pr, nil, nil, nil, nil, ports.ReviewWritePreserve); err != nil {
		t.Fatal(err)
	}
	if err := s.DiscardPendingWorkerPRUsage(ctx); err != nil {
		t.Fatal(err)
	}
	if facts, err := s.PendingWorkerPRUsage(ctx, 10); err != nil || len(facts) != 0 {
		t.Fatalf("facts after opt-out = %+v, err=%v", facts, err)
	}
}
