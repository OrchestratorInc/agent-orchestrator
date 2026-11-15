package review

import (
	"context"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

var bothReviewers = []domain.ReviewerHarness{domain.ReviewerClaudeCode, domain.ReviewerCodex}

func headRun(id string, harness domain.ReviewerHarness, sha string, status domain.ReviewRunStatus, verdict domain.ReviewVerdict) domain.ReviewRun {
	return domain.ReviewRun{ID: id, SessionID: "mer-1", PRURL: multiPRURL, TargetSHA: sha, Harness: harness, Status: status, Verdict: verdict}
}

func TestPlanReviewersCombinedVerdict(t *testing.T) {
	approved := func(id string, h domain.ReviewerHarness, sha string) domain.ReviewRun {
		return headRun(id, h, sha, domain.ReviewRunComplete, domain.VerdictApproved)
	}
	tests := []struct {
		name string
		runs []domain.ReviewRun
		want StateStatus
		per  map[domain.ReviewerHarness]StateStatus
	}{
		{name: "nobody reviewed", want: ReviewStateNeedsReview,
			per: map[domain.ReviewerHarness]StateStatus{domain.ReviewerClaudeCode: ReviewStateNeedsReview, domain.ReviewerCodex: ReviewStateNeedsReview}},
		{name: "every reviewer approved head", want: ReviewStateUpToDate,
			runs: []domain.ReviewRun{approved("a", domain.ReviewerClaudeCode, "sha1"), approved("b", domain.ReviewerCodex, "sha1")},
			per:  map[domain.ReviewerHarness]StateStatus{domain.ReviewerClaudeCode: ReviewStateUpToDate, domain.ReviewerCodex: ReviewStateUpToDate}},
		{name: "one approval is not enough", want: ReviewStateNeedsReview,
			runs: []domain.ReviewRun{approved("a", domain.ReviewerClaudeCode, "sha1")},
			per:  map[domain.ReviewerHarness]StateStatus{domain.ReviewerClaudeCode: ReviewStateUpToDate, domain.ReviewerCodex: ReviewStateNeedsReview}},
		{name: "any changes requested blocks approval", want: ReviewStateChangesRequested,
			runs: []domain.ReviewRun{approved("a", domain.ReviewerClaudeCode, "sha1"), headRun("b", domain.ReviewerCodex, "sha1", domain.ReviewRunComplete, domain.VerdictChangesRequested)},
			per:  map[domain.ReviewerHarness]StateStatus{domain.ReviewerClaudeCode: ReviewStateUpToDate, domain.ReviewerCodex: ReviewStateChangesRequested}},
		{name: "changes requested outranks a running reviewer", want: ReviewStateChangesRequested,
			runs: []domain.ReviewRun{headRun("a", domain.ReviewerClaudeCode, "sha1", domain.ReviewRunRunning, domain.VerdictNone), headRun("b", domain.ReviewerCodex, "sha1", domain.ReviewRunComplete, domain.VerdictChangesRequested)}},
		{name: "still running", want: ReviewStateRunning,
			runs: []domain.ReviewRun{approved("a", domain.ReviewerClaudeCode, "sha1"), headRun("b", domain.ReviewerCodex, "sha1", domain.ReviewRunRunning, domain.VerdictNone)}},
		{name: "failed reviewer is never approval", want: ReviewStateNeedsReview,
			runs: []domain.ReviewRun{approved("a", domain.ReviewerClaudeCode, "sha1"), headRun("b", domain.ReviewerCodex, "sha1", domain.ReviewRunFailed, domain.VerdictNone)}},
		{name: "cancelled or timed-out reviewer is never approval", want: ReviewStateNeedsReview,
			runs: []domain.ReviewRun{approved("a", domain.ReviewerClaudeCode, "sha1"), headRun("b", domain.ReviewerCodex, "sha1", domain.ReviewRunCancelled, domain.VerdictNone)}},
		{name: "approvals on an older head do not carry over", want: ReviewStateNeedsReview,
			runs: []domain.ReviewRun{approved("a", domain.ReviewerClaudeCode, "old"), approved("b", domain.ReviewerCodex, "old")},
			per:  map[domain.ReviewerHarness]StateStatus{domain.ReviewerClaudeCode: ReviewStateNeedsReview, domain.ReviewerCodex: ReviewStateNeedsReview}},
		{name: "mixed heads: only head approvals count", want: ReviewStateNeedsReview,
			runs: []domain.ReviewRun{approved("a", domain.ReviewerClaudeCode, "sha1"), approved("b", domain.ReviewerCodex, "old")}},
		{name: "unattributed legacy approval counts for nobody", want: ReviewStateNeedsReview,
			runs: []domain.ReviewRun{approved("a", domain.ReviewerClaudeCode, "sha1"), approved("b", "", "sha1")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := PlanReviewers(prAt("sha1").prs, tt.runs, bothReviewers)
			if len(got) != 1 {
				t.Fatalf("states = %+v, want one PR", got)
			}
			if got[0].Status != tt.want {
				t.Fatalf("combined status = %q, want %q (reviewers %+v)", got[0].Status, tt.want, got[0].Reviewers)
			}
			if len(got[0].Reviewers) != 2 || got[0].Reviewers[0].Harness != domain.ReviewerClaudeCode || got[0].Reviewers[1].Harness != domain.ReviewerCodex {
				t.Fatalf("reviewers = %+v, want claude-code then codex attributed", got[0].Reviewers)
			}
			for _, r := range got[0].Reviewers {
				if want, ok := tt.per[r.Harness]; ok && r.Status != want {
					t.Fatalf("%s status = %q, want %q", r.Harness, r.Status, want)
				}
			}
		})
	}
}

func TestPlanReviewersSingleReviewerIsPlan(t *testing.T) {
	runs := []domain.ReviewRun{headRun("a", domain.ReviewerCodex, "sha1", domain.ReviewRunComplete, domain.VerdictApproved)}
	got := PlanReviewers(prAt("sha1").prs, runs, []domain.ReviewerHarness{domain.ReviewerCodex})
	if got[0].Status != ReviewStateUpToDate || got[0].Reviewers != nil {
		t.Fatalf("single reviewer state = %+v, want plain Plan output", got[0])
	}
}

func TestPlanReviewersIneligiblePR(t *testing.T) {
	prs := prAt("sha1").prs
	prs[0].Merged = true
	if got := PlanReviewers(prs, nil, bothReviewers); got[0].Status != ReviewStateIneligible {
		t.Fatalf("status = %q, want ineligible", got[0].Status)
	}
}

func TestListReportsCombinedVerdictForConfiguredReviewers(t *testing.T) {
	store := &fakeStore{runs: []domain.ReviewRun{
		headRun("a", domain.ReviewerClaudeCode, "sha1", domain.ReviewRunComplete, domain.VerdictApproved),
	}}
	eng := newEngineForTest(store, fakeSessions{rec: liveWorker(), ok: true}, prAt("sha1"), twoReviewerProject(), &fakeLauncher{})

	res, err := eng.List(context.Background(), "mer-1")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if res.Reviews[0].Status != ReviewStateNeedsReview || len(res.Reviews[0].Reviewers) != 2 {
		t.Fatalf("reviews = %+v, want needs_review until codex approves", res.Reviews)
	}

	store.runs = append(store.runs, headRun("b", domain.ReviewerCodex, "sha1", domain.ReviewRunComplete, domain.VerdictApproved))
	res, err = eng.List(context.Background(), "mer-1")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if res.Reviews[0].Status != ReviewStateUpToDate {
		t.Fatalf("status = %q, want up_to_date once every reviewer approved", res.Reviews[0].Status)
	}
}

func TestListSingleReviewerHasNoReviewerBreakdown(t *testing.T) {
	store := &fakeStore{runs: []domain.ReviewRun{headRun("a", domain.ReviewerClaudeCode, "sha1", domain.ReviewRunComplete, domain.VerdictApproved)}}
	eng := newEngineForTest(store, fakeSessions{rec: liveWorker(), ok: true}, prAt("sha1"), fakeProjects{}, &fakeLauncher{})
	res, err := eng.List(context.Background(), "mer-1")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if res.Reviews[0].Status != ReviewStateUpToDate || res.Reviews[0].Reviewers != nil {
		t.Fatalf("reviews = %+v, want unchanged single-reviewer output", res.Reviews)
	}
}

func TestTriggerResultCarriesCombinedVerdict(t *testing.T) {
	store := &fakeStore{runs: []domain.ReviewRun{
		headRun("a", domain.ReviewerClaudeCode, "sha1", domain.ReviewRunComplete, domain.VerdictApproved),
	}}
	eng := newEngineForTest(store, fakeSessions{rec: liveWorker(), ok: true}, prAt("sha1"), twoReviewerProject(), perHarnessLauncher())

	res, err := trigger(context.Background(), eng, "mer-1", "", domain.AgentConfig{})
	if err != nil {
		t.Fatalf("Trigger: %v", err)
	}
	if len(res.Reviews) != 1 || res.Reviews[0].Status != ReviewStateRunning {
		t.Fatalf("reviews = %+v, want one combined PR state, running while codex reviews", res.Reviews)
	}
	per := res.Reviews[0].Reviewers
	if len(per) != 2 || per[0].Status != ReviewStateUpToDate || per[1].Status != ReviewStateRunning {
		t.Fatalf("reviewers = %+v, want claude-code up_to_date and codex running", per)
	}
}
