package review

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type fakeSummaryPoster struct {
	requests []ports.SCMReviewSummaryRequest
	err      error
}

func (f *fakeSummaryPoster) PostReviewSummary(_ context.Context, req ports.SCMReviewSummaryRequest) (string, error) {
	f.requests = append(f.requests, req)
	if f.err != nil {
		return "", f.err
	}
	return "9001", nil
}

type fakeDeliverer struct{ prs []string }

func (f *fakeDeliverer) DeliverReviewRuns(_ context.Context, _ domain.SessionID, prURL string) error {
	f.prs = append(f.prs, prURL)
	return nil
}

const findingsPR = "https://github.com/acme/app/pull/5"

func findingsStore() *fakeStore {
	return &fakeStore{ok: true,
		run: domain.ReviewRun{ID: "run-1", SessionID: "mer-1", PRURL: findingsPR, TargetSHA: "abcdef123", Harness: "claude-code", Status: domain.ReviewRunRunning},
		prs: []domain.PullRequest{{URL: findingsPR, Provider: "github", Repo: "acme/app", Number: 5, HeadSHA: "abcdef123"}},
	}
}

func TestSubmitFilesFindingsPostsOneSummaryAndDelivers(t *testing.T) {
	st := findingsStore()
	poster := &fakeSummaryPoster{}
	deliverer := &fakeDeliverer{}
	svc := New(nil, st, WithReviewSummaryPoster(poster), WithReviewDeliverer(deliverer))
	review := SubmittedReview{RunID: "run-1", Verdict: domain.VerdictChangesRequested, Body: "One issue.",
		Findings: []domain.ReviewFindingInput{{Path: "greet.js", Line: 12, Body: "add tests"}}}

	runs, err := svc.SubmitMany(context.Background(), "mer-1", []SubmittedReview{review})
	if err != nil {
		t.Fatalf("SubmitMany: %v", err)
	}
	if len(st.findings) != 1 || st.findings[0].Path != "greet.js" || st.findings[0].Status != domain.ReviewFindingOpen {
		t.Fatalf("findings = %+v", st.findings)
	}
	if len(poster.requests) != 1 || poster.requests[0].CommitSHA != "abcdef123" || poster.requests[0].PR.Number != 5 {
		t.Fatalf("summary posts = %+v", poster.requests)
	}
	if body := poster.requests[0].Body; !strings.Contains(body, "**AO review: changes requested**") || !strings.Contains(body, "`greet.js:12`: add tests") {
		t.Fatalf("summary body = %q", body)
	}
	if runs[0].GithubReviewID != "9001" || st.providerPost["run-1"][0] != "9001" {
		t.Fatalf("posted review id not recorded: run=%+v store=%v", runs[0], st.providerPost)
	}
	if len(deliverer.prs) != 1 || deliverer.prs[0] != findingsPR {
		t.Fatalf("delivered PRs = %v", deliverer.prs)
	}

	// A retried identical submit is a no-op; different findings are rejected.
	if _, err := svc.SubmitMany(context.Background(), "mer-1", []SubmittedReview{review}); err != nil {
		t.Fatalf("identical resubmit: %v", err)
	}
	if len(st.findings) != 1 || len(poster.requests) != 1 {
		t.Fatalf("resubmit duplicated work: findings=%d posts=%d", len(st.findings), len(poster.requests))
	}
	review.Findings[0].Body = "something else"
	if _, err := svc.SubmitMany(context.Background(), "mer-1", []SubmittedReview{review}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("conflicting resubmit err = %v, want ErrInvalid", err)
	}
}

func TestSubmitRecordsAFailedSummaryPostWithoutFailing(t *testing.T) {
	st := findingsStore()
	poster := &fakeSummaryPoster{err: errors.New("github scm: 403 forbidden")}
	svc := New(nil, st, WithReviewSummaryPoster(poster))
	runs, err := svc.SubmitMany(context.Background(), "mer-1", []SubmittedReview{{RunID: "run-1", Verdict: domain.VerdictApproved}})
	if err != nil {
		t.Fatalf("SubmitMany: %v", err)
	}
	if runs[0].ProviderPostError == "" || !strings.Contains(st.providerPost["run-1"][1], "403") {
		t.Fatalf("post failure not recorded: run=%+v store=%v", runs[0], st.providerPost)
	}
	if len(poster.requests) != 1 {
		t.Fatalf("a failed post must not be retried: %d attempts", len(poster.requests))
	}
}

func TestSubmitValidatesFindings(t *testing.T) {
	for name, review := range map[string]SubmittedReview{
		"changes without findings": {Verdict: domain.VerdictChangesRequested, Body: "x"},
		"approval with findings":   {Verdict: domain.VerdictApproved, Findings: []domain.ReviewFindingInput{{Body: "x"}}},
		"empty finding body":       {Verdict: domain.VerdictChangesRequested, Body: "x", Findings: []domain.ReviewFindingInput{{Path: "a.go"}}},
		"line without a path":      {Verdict: domain.VerdictChangesRequested, Body: "x", Findings: []domain.ReviewFindingInput{{Line: 3, Body: "x"}}},
	} {
		t.Run(name, func(t *testing.T) {
			review.RunID = "run-1"
			svc := New(nil, findingsStore())
			if _, err := svc.SubmitMany(context.Background(), "mer-1", []SubmittedReview{review}); !errors.Is(err, ErrInvalid) {
				t.Fatalf("err = %v, want ErrInvalid", err)
			}
		})
	}
	// A reviewer started before findings moved into AO already posted its
	// own provider review, so its changes_requested result needs no findings.
	svc := New(nil, findingsStore())
	if _, err := svc.SubmitMany(context.Background(), "mer-1", []SubmittedReview{{RunID: "run-1", Verdict: domain.VerdictChangesRequested, Body: "x", GithubReviewID: "77"}}); err != nil {
		t.Fatalf("legacy submit: %v", err)
	}
}

func TestResolveFindingsAuthorizesWorkerOrchestratorAndPerson(t *testing.T) {
	now := time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)
	newStore := func() *fakeStore {
		return &fakeStore{
			sessions: map[domain.SessionID]domain.SessionRecord{
				"mer-1":   {ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker},
				"mer-2":   {ID: "mer-2", ProjectID: "mer", Kind: domain.KindWorker},
				"mer-orc": {ID: "mer-orc", ProjectID: "mer", Kind: domain.KindOrchestrator},
				"oth-orc": {ID: "oth-orc", ProjectID: "other", Kind: domain.KindOrchestrator},
			},
			findings: []domain.ReviewFinding{
				{ID: "f-open", SessionID: "mer-1", Status: domain.ReviewFindingOpen, CreatedAt: now},
				{ID: "f-old", SessionID: "mer-1", Status: domain.ReviewFindingSuperseded, CreatedAt: now},
			},
		}
	}
	for _, tc := range []struct {
		actor domain.SessionID
		ids   []string
		want  error
	}{
		{actor: "mer-1", ids: []string{"f-open"}},
		{actor: "mer-orc", ids: []string{"f-open"}},
		{actor: "", ids: []string{"f-open"}},
		{actor: "mer-2", ids: []string{"f-open"}, want: ErrFindingForbidden},
		{actor: "oth-orc", ids: []string{"f-open"}, want: ErrFindingForbidden},
		{actor: "mer-1", ids: []string{"f-old"}, want: ErrFindingSuperseded},
		{actor: "mer-1", ids: []string{"f-missing"}, want: ErrNotFound},
	} {
		st := newStore()
		svc := New(nil, st, WithClock(func() time.Time { return now }))
		got, err := svc.ResolveFindings(context.Background(), "mer-1", ResolveFindingsRequest{FindingIDs: tc.ids, Note: "fixed", ActorSessionID: tc.actor})
		if tc.want != nil {
			if !errors.Is(err, tc.want) {
				t.Fatalf("actor %q ids %v: err = %v, want %v", tc.actor, tc.ids, err, tc.want)
			}
			continue
		}
		if err != nil {
			t.Fatalf("actor %q: %v", tc.actor, err)
		}
		if got[0].Status != domain.ReviewFindingResolved || got[0].ResolvedBySessionID != tc.actor || got[0].ResolutionNote != "fixed" {
			t.Fatalf("actor %q resolved = %+v", tc.actor, got[0])
		}
		// Resolving again keeps the first resolution.
		again, err := svc.ResolveFindings(context.Background(), "mer-1", ResolveFindingsRequest{FindingIDs: tc.ids, Note: "other", ActorSessionID: "mer-1"})
		if err != nil || again[0].ResolutionNote != "fixed" {
			t.Fatalf("idempotent resolve = %+v err=%v", again, err)
		}
	}
}
