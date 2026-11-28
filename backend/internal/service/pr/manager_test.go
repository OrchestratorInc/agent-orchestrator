package pr

import (
	"context"
	"testing"
	"time"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/domain"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/ports"
)

type fakeWriter struct {
	pr       map[domain.SessionID]domain.PullRequest
	comments map[string][]domain.PullRequestComment
	checks   []domain.PullRequestCheck
}

func (f *fakeWriter) WritePR(_ context.Context, pr domain.PullRequest, checks []domain.PullRequestCheck, comments []domain.PullRequestComment) error {
	f.pr[pr.SessionID] = pr
	f.checks = append(f.checks, checks...)
	f.comments[pr.URL] = comments
	return nil
}

func (f *fakeWriter) ClaimPR(_ context.Context, url string, sessionID domain.SessionID, observation ports.PRObservation, _ bool) (ports.ClaimOutcome, error) {
	pr := domain.PullRequest{URL: url, SessionID: sessionID, Number: observation.Number, Draft: observation.Draft, Merged: observation.Merged, Closed: observation.Closed, CI: observation.CI, Review: observation.Review, Mergeability: observation.Mergeability}
	f.pr[sessionID] = pr
	return ports.ClaimOutcome{}, nil
}

type fakeLifecycle struct {
	observed []ports.PRObservation
}

func (f *fakeLifecycle) ApplyPRObservation(_ context.Context, _ domain.SessionID, o ports.PRObservation) error {
	f.observed = append(f.observed, o)
	return nil
}

func newPRManager() (*Manager, *fakeWriter, *fakeLifecycle) {
	fw := &fakeWriter{pr: map[domain.SessionID]domain.PullRequest{}, comments: map[string][]domain.PullRequestComment{}}
	fl := &fakeLifecycle{}
	m := New(Deps{
		Writer:    fw,
		Lifecycle: fl,
		Clock:     func() time.Time { return time.Unix(1, 0).UTC() },
	})
	return m, fw, fl
}

func TestApplyObservation_WritesPRChecksAndComments(t *testing.T) {
	m, fw, fl := newPRManager()
	o := ports.PRObservation{
		Fetched: true, URL: "https://example/pr/1", Number: 1, CI: domain.CIFailing,
		Checks:   []ports.PRCheckObservation{{Name: "build", CommitHash: "c1", Status: domain.PRCheckFailed, LogTail: "boom"}},
		Comments: []ports.PRCommentObservation{{ID: "1", Author: "greptileai", Body: "use a constant here", AutoInjectReview: true}},
	}
	if err := m.ApplyObservation(context.Background(), "mer-1", o); err != nil {
		t.Fatal(err)
	}
	if got := fw.pr["mer-1"]; got.URL != o.URL || got.CI != domain.CIFailing {
		t.Fatalf("pr not written: %+v", got)
	}
	if len(fw.checks) != 1 || fw.checks[0].CreatedAt.IsZero() {
		t.Fatalf("checks not normalized: %+v", fw.checks)
	}
	if len(fw.comments[o.URL]) != 1 || fw.comments[o.URL][0].CreatedAt.IsZero() {
		t.Fatalf("comments not normalized: %+v", fw.comments)
	}
	if !fw.comments[o.URL][0].AutoInjectReview {
		t.Fatalf("comment injection decision was not persisted: %+v", fw.comments[o.URL][0])
	}
	if len(fl.observed) != 1 || fl.observed[0].URL != o.URL {
		t.Fatalf("PR observation should be forwarded to lifecycle, got %v", fl.observed)
	}
}

func TestApplyObservation_MergedForwardsToLifecycle(t *testing.T) {
	m, _, fl := newPRManager()
	if err := m.ApplyObservation(context.Background(), "mer-1", ports.PRObservation{Fetched: true, URL: "pr1", Number: 1, Merged: true}); err != nil {
		t.Fatal(err)
	}
	if len(fl.observed) != 1 || !fl.observed[0].Merged {
		t.Fatalf("merged PR should be forwarded to lifecycle, got %v", fl.observed)
	}
}

func TestApplyObservation_FailedFetchIsDropped(t *testing.T) {
	m, fw, fl := newPRManager()
	if err := m.ApplyObservation(context.Background(), "mer-1", ports.PRObservation{Fetched: false, URL: "pr1", CI: domain.CIFailing}); err != nil {
		t.Fatal(err)
	}
	if len(fw.pr) != 0 || len(fl.observed) != 0 {
		t.Fatalf("failed fetch must write nothing, pr=%v observed=%v", fw.pr, fl.observed)
	}
}
