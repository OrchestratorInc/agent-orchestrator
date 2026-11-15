package review

import (
	"context"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const multiPRURL = "https://github.com/o/r/pull/1"

func twoReviewerProject() fakeProjects {
	return fakeProjects{cfg: domain.ProjectConfig{Reviewers: []domain.ReviewerConfig{
		{Harness: domain.ReviewerClaudeCode, AgentConfig: domain.AgentConfig{Model: "claude-x"}},
		{Harness: domain.ReviewerCodex, AgentConfig: domain.AgentConfig{Model: "gpt-x"}},
	}}}
}

// perHarnessLauncher gives each reviewer harness its own terminal handle.
func perHarnessLauncher() *fakeLauncher {
	l := &fakeLauncher{}
	l.onSpawn = func(spec LaunchSpec) { l.handle = "review-" + string(spec.Harness) }
	return l
}

func runsFor(runs []domain.ReviewRun, harness domain.ReviewerHarness, status domain.ReviewRunStatus) []domain.ReviewRun {
	var out []domain.ReviewRun
	for _, r := range runs {
		if r.Harness == harness && r.Status == status {
			out = append(out, r)
		}
	}
	return out
}

func spawnedHarnesses(l *fakeLauncher) []domain.ReviewerHarness {
	out := make([]domain.ReviewerHarness, 0, len(l.specs))
	for _, s := range l.specs {
		out = append(out, s.Harness)
	}
	return out
}

func TestProjectReviewerSelectionsReturnsEveryConfiguredReviewer(t *testing.T) {
	projects := fakeProjects{cfg: domain.ProjectConfig{Reviewers: []domain.ReviewerConfig{
		{Harness: domain.ReviewerCodex, AgentConfig: domain.AgentConfig{Model: "gpt-x"}},
		{Harness: domain.ReviewerClaudeCode},
		{Harness: domain.ReviewerCodex, AgentConfig: domain.AgentConfig{Model: "dup"}},
	}}}
	eng := newEngineForTest(&fakeStore{}, fakeSessions{rec: liveWorker(), ok: true}, prAt("sha1"), projects, &fakeLauncher{})

	got, err := eng.projectReviewerSelections(context.Background(), liveWorker())
	if err != nil {
		t.Fatalf("projectReviewerSelections: %v", err)
	}
	if len(got) != 2 || got[0].Harness != domain.ReviewerCodex || got[0].AgentConfig.Model != "gpt-x" || got[1].Harness != domain.ReviewerClaudeCode {
		t.Fatalf("selections = %+v, want [codex(gpt-x), claude-code] with duplicate dropped", got)
	}
}

func TestProjectReviewerSelectionsFallsBackToWorkerHarness(t *testing.T) {
	eng := newEngineForTest(&fakeStore{}, fakeSessions{rec: liveWorker(), ok: true}, prAt("sha1"), fakeProjects{}, &fakeLauncher{})
	got, err := eng.projectReviewerSelections(context.Background(), liveWorker())
	if err != nil {
		t.Fatalf("projectReviewerSelections: %v", err)
	}
	if len(got) != 1 || got[0].Harness != domain.ReviewerClaudeCode {
		t.Fatalf("selections = %+v, want single inherited claude-code", got)
	}
}

func TestTriggerSingleConfiguredReviewerUnchanged(t *testing.T) {
	store := &fakeStore{}
	launcher := perHarnessLauncher()
	projects := fakeProjects{cfg: domain.ProjectConfig{Reviewers: []domain.ReviewerConfig{{Harness: domain.ReviewerCodex}}}}
	eng := newEngineForTest(store, fakeSessions{rec: liveWorker(), ok: true}, prAt("sha1"), projects, launcher)

	res, err := trigger(context.Background(), eng, "mer-1", "", domain.AgentConfig{})
	if err != nil {
		t.Fatalf("Trigger: %v", err)
	}
	if !res.Created || len(res.CreatedRuns) != 1 || res.CreatedRuns[0].Harness != domain.ReviewerCodex {
		t.Fatalf("result = %+v, want exactly one codex run", res)
	}
	if got := spawnedHarnesses(launcher); len(got) != 1 || got[0] != domain.ReviewerCodex {
		t.Fatalf("spawned = %v, want [codex]", got)
	}
}

func TestTriggerRunsEveryConfiguredReviewerOnSameHead(t *testing.T) {
	store := &fakeStore{}
	launcher := perHarnessLauncher()
	eng := newEngineForTest(store, fakeSessions{rec: liveWorker(), ok: true}, prAt("sha1"), twoReviewerProject(), launcher)

	res, err := trigger(context.Background(), eng, "mer-1", "", domain.AgentConfig{})
	if err != nil {
		t.Fatalf("Trigger: %v", err)
	}
	if !res.Created || len(res.CreatedRuns) != 2 {
		t.Fatalf("created runs = %+v, want one per reviewer", res.CreatedRuns)
	}
	for _, h := range []domain.ReviewerHarness{domain.ReviewerClaudeCode, domain.ReviewerCodex} {
		running := runsFor(store.runs, h, domain.ReviewRunRunning)
		if len(running) != 1 || running[0].TargetSHA != "sha1" {
			t.Fatalf("%s running runs = %+v, want one on sha1", h, running)
		}
		if store.reviews[h].ReviewerHandleID != "review-"+string(h) {
			t.Fatalf("%s handle = %q, want its own terminal", h, store.reviews[h].ReviewerHandleID)
		}
	}
	got := spawnedHarnesses(launcher)
	if len(got) != 2 || got[0] != domain.ReviewerClaudeCode || got[1] != domain.ReviewerCodex {
		t.Fatalf("spawn order = %v, want claude-code then codex", got)
	}
	if launcher.specs[0].AgentConfig.Model != "claude-x" || launcher.specs[1].AgentConfig.Model != "gpt-x" {
		t.Fatalf("spawn configs = %+v / %+v, want per-reviewer configs", launcher.specs[0].AgentConfig, launcher.specs[1].AgentConfig)
	}
	if launcher.destroyCalls != 0 {
		t.Fatalf("destroy calls = %d, one reviewer must not tear down another", launcher.destroyCalls)
	}
}

func TestTriggerOneReviewerApprovalDoesNotSatisfyTheOther(t *testing.T) {
	// claude-code already approved sha1; codex has not reviewed it. The
	// approval must not make the head look up to date for codex.
	store := &fakeStore{runs: []domain.ReviewRun{{
		ID: "run-cc", SessionID: "mer-1", PRURL: multiPRURL, TargetSHA: "sha1",
		Harness: domain.ReviewerClaudeCode, Status: domain.ReviewRunComplete, Verdict: domain.VerdictApproved,
	}}}
	launcher := perHarnessLauncher()
	eng := newEngineForTest(store, fakeSessions{rec: liveWorker(), ok: true}, prAt("sha1"), twoReviewerProject(), launcher)

	res, err := trigger(context.Background(), eng, "mer-1", "", domain.AgentConfig{})
	if err != nil {
		t.Fatalf("Trigger: %v", err)
	}
	if len(res.CreatedRuns) != 1 || res.CreatedRuns[0].Harness != domain.ReviewerCodex {
		t.Fatalf("created runs = %+v, want only a codex run", res.CreatedRuns)
	}
}

func TestTriggerReRunsOnlyReviewerThatRequestedChanges(t *testing.T) {
	store := &fakeStore{runs: []domain.ReviewRun{
		{ID: "run-cc", SessionID: "mer-1", PRURL: multiPRURL, TargetSHA: "sha1",
			Harness: domain.ReviewerClaudeCode, Status: domain.ReviewRunComplete, Verdict: domain.VerdictApproved},
		{ID: "run-cx", SessionID: "mer-1", PRURL: multiPRURL, TargetSHA: "sha1",
			Harness: domain.ReviewerCodex, Status: domain.ReviewRunComplete, Verdict: domain.VerdictChangesRequested},
	}}
	launcher := perHarnessLauncher()
	eng := newEngineForTest(store, fakeSessions{rec: liveWorker(), ok: true}, prAt("sha1"), twoReviewerProject(), launcher)

	res, err := trigger(context.Background(), eng, "mer-1", "", domain.AgentConfig{})
	if err != nil {
		t.Fatalf("Trigger: %v", err)
	}
	if len(res.CreatedRuns) != 1 || res.CreatedRuns[0].Harness != domain.ReviewerCodex {
		t.Fatalf("created runs = %+v, want only the changes-requesting codex reviewer re-run", res.CreatedRuns)
	}
}

func TestTriggerNewHeadReReviewsWithEveryReviewer(t *testing.T) {
	store := &fakeStore{runs: []domain.ReviewRun{
		{ID: "run-cc", SessionID: "mer-1", PRURL: multiPRURL, TargetSHA: "sha1",
			Harness: domain.ReviewerClaudeCode, Status: domain.ReviewRunComplete, Verdict: domain.VerdictApproved},
		{ID: "run-cx", SessionID: "mer-1", PRURL: multiPRURL, TargetSHA: "sha1",
			Harness: domain.ReviewerCodex, Status: domain.ReviewRunComplete, Verdict: domain.VerdictApproved},
	}}
	launcher := perHarnessLauncher()
	eng := newEngineForTest(store, fakeSessions{rec: liveWorker(), ok: true}, prAt("sha2"), twoReviewerProject(), launcher)

	res, err := trigger(context.Background(), eng, "mer-1", "", domain.AgentConfig{})
	if err != nil {
		t.Fatalf("Trigger: %v", err)
	}
	if len(res.CreatedRuns) != 2 {
		t.Fatalf("created runs = %+v, want both reviewers on the new head", res.CreatedRuns)
	}
	for _, r := range res.CreatedRuns {
		if r.TargetSHA != "sha2" {
			t.Fatalf("run %+v targets %q, want sha2", r, r.TargetSHA)
		}
	}
}

func TestTriggerNewHeadDuringReviewSupersedesEveryReviewer(t *testing.T) {
	// Both reviewers are mid-review on sha1 when sha2 lands. Each must
	// supersede its stale pass and start on sha2.
	store := &fakeStore{
		reviews: map[domain.ReviewerHarness]domain.Review{
			domain.ReviewerClaudeCode: {ID: "rev-cc", SessionID: "mer-1", Harness: domain.ReviewerClaudeCode, ReviewerHandleID: "review-claude-code"},
			domain.ReviewerCodex:      {ID: "rev-cx", SessionID: "mer-1", Harness: domain.ReviewerCodex, ReviewerHandleID: "review-codex"},
		},
		runs: []domain.ReviewRun{
			{ID: "run-cc", ReviewID: "rev-cc", SessionID: "mer-1", PRURL: multiPRURL, TargetSHA: "sha1",
				Harness: domain.ReviewerClaudeCode, Status: domain.ReviewRunRunning, Verdict: domain.VerdictNone},
			{ID: "run-cx", ReviewID: "rev-cx", SessionID: "mer-1", PRURL: multiPRURL, TargetSHA: "sha1",
				Harness: domain.ReviewerCodex, Status: domain.ReviewRunRunning, Verdict: domain.VerdictNone},
		},
	}
	launcher := &fakeLauncher{alive: true}
	eng := newEngineForTest(store, fakeSessions{rec: liveWorker(), ok: true}, prAt("sha2"), twoReviewerProject(), launcher)

	res, err := trigger(context.Background(), eng, "mer-1", "", domain.AgentConfig{})
	if err != nil {
		t.Fatalf("Trigger: %v", err)
	}
	if len(res.CreatedRuns) != 2 {
		t.Fatalf("created runs = %+v, want both reviewers on sha2", res.CreatedRuns)
	}
	for _, r := range store.runs {
		if r.TargetSHA == "sha1" && r.Status == domain.ReviewRunRunning {
			t.Fatalf("stale sha1 run %s still running", r.ID)
		}
		if r.TargetSHA == "sha1" && r.Verdict == domain.VerdictApproved {
			t.Fatalf("stale sha1 run %s counted as approval", r.ID)
		}
	}
	if launcher.destroyCalls != 0 {
		t.Fatalf("destroy calls = %d, want both live reviewer terminals kept", launcher.destroyCalls)
	}
}

func TestTriggerDoesNotCancelOtherReviewersRunningPass(t *testing.T) {
	// claude-code is mid-review with a live terminal; codex has none yet.
	// Starting codex must not treat claude-code's run as stale.
	store := &fakeStore{
		reviews: map[domain.ReviewerHarness]domain.Review{
			domain.ReviewerClaudeCode: {ID: "rev-cc", SessionID: "mer-1", Harness: domain.ReviewerClaudeCode, ReviewerHandleID: "review-claude-code"},
		},
		runs: []domain.ReviewRun{{ID: "run-cc", ReviewID: "rev-cc", SessionID: "mer-1", PRURL: multiPRURL, TargetSHA: "sha1",
			Harness: domain.ReviewerClaudeCode, Status: domain.ReviewRunRunning, Verdict: domain.VerdictNone}},
	}
	launcher := &fakeLauncher{alive: true, handle: "review-codex"}
	eng := newEngineForTest(store, fakeSessions{rec: liveWorker(), ok: true}, prAt("sha1"), twoReviewerProject(), launcher)

	res, err := trigger(context.Background(), eng, "mer-1", "", domain.AgentConfig{})
	if err != nil {
		t.Fatalf("Trigger: %v", err)
	}
	if len(res.CreatedRuns) != 1 || res.CreatedRuns[0].Harness != domain.ReviewerCodex {
		t.Fatalf("created runs = %+v, want only codex", res.CreatedRuns)
	}
	if got := runsFor(store.runs, domain.ReviewerClaudeCode, domain.ReviewRunRunning); len(got) != 1 {
		t.Fatalf("claude-code running runs = %+v, want its pass untouched", got)
	}
}

func TestTriggerReviewerFailureDoesNotBlockOthersOrApprove(t *testing.T) {
	store := &fakeStore{}
	launcher := perHarnessLauncher()
	failing := &failingHarnessLauncher{fakeLauncher: launcher, fail: domain.ReviewerClaudeCode}
	eng := newEngineForTest(store, fakeSessions{rec: liveWorker(), ok: true}, prAt("sha1"), twoReviewerProject(), failing)

	res, err := trigger(context.Background(), eng, "mer-1", "", domain.AgentConfig{})
	if err != nil {
		t.Fatalf("Trigger err = %v, want success because codex started", err)
	}
	if len(res.ReviewerFailures) != 1 || res.ReviewerFailures[0].Harness != domain.ReviewerClaudeCode || res.ReviewerFailures[0].Error == "" {
		t.Fatalf("reviewer failures = %+v, want claude-code reported", res.ReviewerFailures)
	}
	if !res.Created || res.Reviews[0].Status != ReviewStateRunning {
		t.Fatalf("result = %+v, want created with codex running", res)
	}
	if len(res.CreatedRuns) != 1 || res.CreatedRuns[0].Harness != domain.ReviewerCodex {
		t.Fatalf("created runs = %+v, want codex to still start", res.CreatedRuns)
	}
	failed := runsFor(store.runs, domain.ReviewerClaudeCode, domain.ReviewRunFailed)
	if len(failed) != 1 || failed[0].Verdict != domain.VerdictNone {
		t.Fatalf("claude-code runs = %+v, want one failed run with no verdict", store.runs)
	}
}

func TestTriggerSessionReviewerPreferenceKeepsSingleReviewer(t *testing.T) {
	worker := liveWorker()
	worker.ReviewerHarness = domain.ReviewerCodex
	store := &fakeStore{}
	launcher := perHarnessLauncher()
	eng := newEngineForTest(store, fakeSessions{rec: worker, ok: true}, prAt("sha1"), twoReviewerProject(), launcher)

	res, err := trigger(context.Background(), eng, "mer-1", "", domain.AgentConfig{})
	if err != nil {
		t.Fatalf("Trigger: %v", err)
	}
	if len(res.CreatedRuns) != 1 || res.CreatedRuns[0].Harness != domain.ReviewerCodex {
		t.Fatalf("created runs = %+v, want only the session's chosen codex reviewer", res.CreatedRuns)
	}
}

var errSpawnBoom = errors.New("spawn boom")

type failingHarnessLauncher struct {
	*fakeLauncher
	fail domain.ReviewerHarness
}

func (f *failingHarnessLauncher) Spawn(ctx context.Context, spec LaunchSpec) (LaunchResult, error) {
	if spec.Harness == f.fail {
		return LaunchResult{}, errSpawnBoom
	}
	return f.fakeLauncher.Spawn(ctx, spec)
}

func TestTriggerErrorsWhenEveryReviewerFails(t *testing.T) {
	store := &fakeStore{}
	launcher := perHarnessLauncher()
	launcher.preflightErr = errSpawnBoom
	eng := newEngineForTest(store, fakeSessions{rec: liveWorker(), ok: true}, prAt("sha1"), twoReviewerProject(), launcher)

	if _, err := trigger(context.Background(), eng, "mer-1", "", domain.AgentConfig{}); !errors.Is(err, errSpawnBoom) {
		t.Fatalf("Trigger err = %v, want failure when no reviewer started", err)
	}
	for _, r := range store.runs {
		if r.Status != domain.ReviewRunFailed || r.Verdict != domain.VerdictNone {
			t.Fatalf("run %+v, want failed with no verdict", r)
		}
	}
}

// approvedByClaudeCode is a store where claude-code already approved the head,
// so only codex owes a review.
func approvedByClaudeCode() *fakeStore {
	return &fakeStore{runs: []domain.ReviewRun{{ID: "run-cc", SessionID: "mer-1", PRURL: multiPRURL, TargetSHA: "sha1",
		Harness: domain.ReviewerClaudeCode, Status: domain.ReviewRunComplete, Verdict: domain.VerdictApproved}}}
}

func TestTriggerErrorsWhenOnlyOwingReviewerFails(t *testing.T) {
	// claude-code is up to date and codex cannot start, so no review is
	// happening: a manual trigger must say so, not answer as a reuse.
	store := approvedByClaudeCode()
	failing := &failingHarnessLauncher{fakeLauncher: perHarnessLauncher(), fail: domain.ReviewerCodex}
	eng := newEngineForTest(store, fakeSessions{rec: liveWorker(), ok: true}, prAt("sha1"), twoReviewerProject(), failing)

	if _, err := trigger(context.Background(), eng, "mer-1", "", domain.AgentConfig{}); !errors.Is(err, errSpawnBoom) {
		t.Fatalf("Trigger err = %v, want codex's launch failure", err)
	}
	failed := runsFor(store.runs, domain.ReviewerCodex, domain.ReviewRunFailed)
	if len(failed) != 1 || failed[0].Verdict != domain.VerdictNone {
		t.Fatalf("codex runs = %+v, want one failed run with no verdict", store.runs)
	}
}

func TestTriggerReportsFailureWhenOtherReviewerStillRunning(t *testing.T) {
	// claude-code is still reviewing the head, so the review is happening:
	// codex's failure is reported alongside the reused pass.
	store := &fakeStore{
		reviews: map[domain.ReviewerHarness]domain.Review{
			domain.ReviewerClaudeCode: {ID: "rev-cc", SessionID: "mer-1", Harness: domain.ReviewerClaudeCode, ReviewerHandleID: "review-claude-code"},
		},
		runs: []domain.ReviewRun{{ID: "run-cc", ReviewID: "rev-cc", SessionID: "mer-1", PRURL: multiPRURL, TargetSHA: "sha1",
			Harness: domain.ReviewerClaudeCode, Status: domain.ReviewRunRunning, Verdict: domain.VerdictNone}},
	}
	launcher := perHarnessLauncher()
	launcher.alive = true
	failing := &failingHarnessLauncher{fakeLauncher: launcher, fail: domain.ReviewerCodex}
	eng := newEngineForTest(store, fakeSessions{rec: liveWorker(), ok: true}, prAt("sha1"), twoReviewerProject(), failing)

	res, err := trigger(context.Background(), eng, "mer-1", "", domain.AgentConfig{})
	if err != nil {
		t.Fatalf("Trigger err = %v, want the running claude-code pass reused", err)
	}
	if res.Created || len(res.ReviewerFailures) != 1 || res.ReviewerFailures[0].Harness != domain.ReviewerCodex {
		t.Fatalf("result = %+v, want not created with codex failure reported", res)
	}
}

func TestAutoTriggerReportsFailingReviewerAloneOwingWork(t *testing.T) {
	// Automatic sweeps keep the partial result so the coordinator can report
	// reviewer_failed instead of erroring.
	store := approvedByClaudeCode()
	failing := &failingHarnessLauncher{fakeLauncher: perHarnessLauncher(), fail: domain.ReviewerCodex}
	eng := newEngineForTest(store, fakeSessions{rec: liveWorker(), ok: true}, prAt("sha1"), twoReviewerProject(), failing)

	res, err := triggerWithSource(context.Background(), eng, "mer-1", "", domain.AgentConfig{}, domain.ReviewTriggerAuto)
	if err != nil {
		t.Fatalf("Trigger err = %v, want partial result for auto review", err)
	}
	if res.Created || len(res.ReviewerFailures) != 1 || res.ReviewerFailures[0].Harness != domain.ReviewerCodex {
		t.Fatalf("result = %+v, want not created with codex failure reported", res)
	}
	if res.Reviews[0].Status != ReviewStateNeedsReview {
		t.Fatalf("combined status = %q, want needs_review (failure is never approval)", res.Reviews[0].Status)
	}
}

func TestCancelStopsEveryRunningConfiguredReviewer(t *testing.T) {
	store := &fakeStore{
		reviews: map[domain.ReviewerHarness]domain.Review{
			domain.ReviewerClaudeCode: {ID: "rev-cc", SessionID: "mer-1", Harness: domain.ReviewerClaudeCode, ReviewerHandleID: "review-claude-code"},
			domain.ReviewerCodex:      {ID: "rev-cx", SessionID: "mer-1", Harness: domain.ReviewerCodex, ReviewerHandleID: "review-codex"},
		},
		runs: []domain.ReviewRun{
			{ID: "run-cc", ReviewID: "rev-cc", SessionID: "mer-1", PRURL: multiPRURL, TargetSHA: "sha1",
				Harness: domain.ReviewerClaudeCode, Status: domain.ReviewRunRunning, Verdict: domain.VerdictNone},
			{ID: "run-cx", ReviewID: "rev-cx", SessionID: "mer-1", PRURL: multiPRURL, TargetSHA: "sha1",
				Harness: domain.ReviewerCodex, Status: domain.ReviewRunRunning, Verdict: domain.VerdictNone},
		},
	}
	launcher := &cancelRecordingLauncher{fakeLauncher: &fakeLauncher{alive: true}}
	eng := newEngineForTest(store, fakeSessions{rec: liveWorker(), ok: true}, prAt("sha1"), twoReviewerProject(), launcher)

	res, err := eng.Cancel(context.Background(), "mer-1")
	if err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if len(launcher.cancelledHandles) != 2 {
		t.Fatalf("cancelled handles = %v, want both reviewer panes interrupted", launcher.cancelledHandles)
	}
	if len(res.CancelledRuns) != 2 {
		t.Fatalf("cancelled runs = %+v, want both", res.CancelledRuns)
	}
	for _, r := range store.runs {
		if r.Status != domain.ReviewRunCancelled {
			t.Fatalf("run %s status = %q, want cancelled", r.ID, r.Status)
		}
	}
	if res.Reviews[0].Status != ReviewStateNeedsReview {
		t.Fatalf("combined status = %q, want needs_review after cancelling every reviewer", res.Reviews[0].Status)
	}
}

func TestCancelSingleReviewerUnchanged(t *testing.T) {
	store := &fakeStore{
		reviews: map[domain.ReviewerHarness]domain.Review{
			domain.ReviewerClaudeCode: {ID: "rev-cc", SessionID: "mer-1", Harness: domain.ReviewerClaudeCode, ReviewerHandleID: "review-claude-code"},
		},
		runs: []domain.ReviewRun{{ID: "run-cc", ReviewID: "rev-cc", SessionID: "mer-1", PRURL: multiPRURL, TargetSHA: "sha1",
			Harness: domain.ReviewerClaudeCode, Status: domain.ReviewRunRunning, Verdict: domain.VerdictNone}},
	}
	launcher := &cancelRecordingLauncher{fakeLauncher: &fakeLauncher{alive: true}}
	eng := newEngineForTest(store, fakeSessions{rec: liveWorker(), ok: true}, prAt("sha1"), fakeProjects{}, launcher)

	res, err := eng.Cancel(context.Background(), "mer-1")
	if err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if len(launcher.cancelledHandles) != 1 || res.ReviewerHandleID != "review-claude-code" || res.Reviews[0].Reviewers != nil {
		t.Fatalf("result = %+v handles=%v, want unchanged single-reviewer cancel", res, launcher.cancelledHandles)
	}
}

func TestAutoTriggerIgnoresLegacyRunsWithSeveralReviewers(t *testing.T) {
	// A legacy approval with no recorded harness belongs to no configured
	// reviewer, so an automatic pass must still start both.
	store := &fakeStore{runs: []domain.ReviewRun{{ID: "legacy", SessionID: "mer-1", PRURL: multiPRURL, TargetSHA: "sha1",
		Status: domain.ReviewRunComplete, Verdict: domain.VerdictApproved, TriggerSource: domain.ReviewTriggerAuto}}}
	eng := newEngineForTest(store, fakeSessions{rec: liveWorker(), ok: true}, prAt("sha1"), twoReviewerProject(), perHarnessLauncher())

	res, err := triggerWithSource(context.Background(), eng, "mer-1", "", domain.AgentConfig{}, domain.ReviewTriggerAuto)
	if err != nil {
		t.Fatalf("TriggerWithSource: %v", err)
	}
	if len(res.CreatedRuns) != 2 {
		t.Fatalf("created runs = %+v, want both reviewers despite the legacy run", res.CreatedRuns)
	}
}

type cancelRecordingLauncher struct {
	*fakeLauncher
	cancelledHandles []string
}

func (c *cancelRecordingLauncher) Cancel(ctx context.Context, handleID string, harness domain.ReviewerHarness) error {
	c.cancelledHandles = append(c.cancelledHandles, handleID)
	return c.fakeLauncher.Cancel(ctx, handleID, harness)
}

// approvedByBoth is a store where both configured reviewers approved the head.
func approvedByBoth() *fakeStore {
	return &fakeStore{runs: []domain.ReviewRun{
		{ID: "run-cc", SessionID: "mer-1", PRURL: multiPRURL, TargetSHA: "sha1", Harness: domain.ReviewerClaudeCode, Status: domain.ReviewRunComplete, Verdict: domain.VerdictApproved},
		{ID: "run-cx", SessionID: "mer-1", PRURL: multiPRURL, TargetSHA: "sha1", Harness: domain.ReviewerCodex, Status: domain.ReviewRunComplete, Verdict: domain.VerdictApproved},
	}}
}

func TestRerunFansOutToEveryConfiguredReviewer(t *testing.T) {
	store := approvedByBoth()
	launcher := perHarnessLauncher()
	eng := newEngineForTest(store, fakeSessions{rec: liveWorker(), ok: true}, prAt("sha1"), twoReviewerProject(), launcher)

	res, err := eng.TriggerWithOptions(context.Background(), "mer-1", TriggerOptions{Rerun: true})
	if err != nil {
		t.Fatalf("rerun: %v", err)
	}
	if !res.Created || len(res.CreatedRuns) != 2 || res.CreatedRuns[0].Harness == res.CreatedRuns[1].Harness {
		t.Fatalf("created runs = %+v, want a fresh pass from each reviewer", res.CreatedRuns)
	}
}

func TestRejectReviewedHeadStartsOnlyReviewerThatOwesReview(t *testing.T) {
	// claude-code already reviewed the head; that is not a failure, and codex,
	// which has not, still starts.
	store := approvedByClaudeCode()
	launcher := perHarnessLauncher()
	eng := newEngineForTest(store, fakeSessions{rec: liveWorker(), ok: true}, prAt("sha1"), twoReviewerProject(), launcher)

	res, err := eng.TriggerWithOptions(context.Background(), "mer-1", TriggerOptions{Source: domain.ReviewTriggerAgent, RejectReviewedHead: true})
	if err != nil {
		t.Fatalf("trigger: %v", err)
	}
	if len(res.CreatedRuns) != 1 || res.CreatedRuns[0].Harness != domain.ReviewerCodex || len(res.ReviewerFailures) != 0 {
		t.Fatalf("result = %+v, want only codex started and no failures", res)
	}
}

func TestRejectReviewedHeadErrorsWhenEveryReviewerReviewed(t *testing.T) {
	store := approvedByBoth()
	eng := newEngineForTest(store, fakeSessions{rec: liveWorker(), ok: true}, prAt("sha1"), twoReviewerProject(), perHarnessLauncher())

	_, err := eng.TriggerWithOptions(context.Background(), "mer-1", TriggerOptions{Source: domain.ReviewTriggerAgent, RejectReviewedHead: true})
	if !errors.Is(err, ErrHeadAlreadyReviewed) {
		t.Fatalf("trigger err = %v, want ErrHeadAlreadyReviewed", err)
	}
	if len(store.runs) != 2 {
		t.Fatalf("runs = %+v, want no new pass", store.runs)
	}
}

func TestTargetedPRFansOutToEveryConfiguredReviewer(t *testing.T) {
	const otherPR = "https://github.com/o/r/pull/2"
	prs := fakePRs{prs: []domain.PullRequest{
		{URL: multiPRURL, Number: 1, HeadSHA: "sha1"},
		{URL: otherPR, Number: 2, HeadSHA: "sha2"},
	}}
	store := &fakeStore{}
	eng := newEngineForTest(store, fakeSessions{rec: liveWorker(), ok: true}, prs, twoReviewerProject(), perHarnessLauncher())

	res, err := eng.TriggerWithOptions(context.Background(), "mer-1", TriggerOptions{PRURL: otherPR})
	if err != nil {
		t.Fatalf("trigger: %v", err)
	}
	if len(res.CreatedRuns) != 2 {
		t.Fatalf("created runs = %+v, want one per reviewer", res.CreatedRuns)
	}
	for _, run := range res.CreatedRuns {
		if run.PRURL != otherPR {
			t.Fatalf("run %+v, want only the targeted PR reviewed", run)
		}
	}
}

func TestInterfaceModeFansOutToEveryConfiguredReviewer(t *testing.T) {
	launcher := perHarnessLauncher()
	eng := newEngineForTest(&fakeStore{}, fakeSessions{rec: liveWorker(), ok: true}, prAt("sha1"), twoReviewerProject(), launcher)

	if _, err := eng.TriggerWithOptions(context.Background(), "mer-1", TriggerOptions{InterfaceMode: domain.ReviewerInterfaceTUI}); err != nil {
		t.Fatalf("trigger: %v", err)
	}
	if len(launcher.specs) != 2 {
		t.Fatalf("specs = %+v, want both reviewers launched", launcher.specs)
	}
	for _, spec := range launcher.specs {
		if spec.InterfaceMode != domain.ReviewerInterfaceTUI {
			t.Fatalf("%s launched in %q, want terminal for every reviewer", spec.Harness, spec.InterfaceMode)
		}
	}
}

func TestCancelStopsPerCallReviewerOutsideConfiguredSet(t *testing.T) {
	// A one-off copilot pass in a project that configures claude-code and
	// codex must still be cancellable.
	store := &fakeStore{
		reviews: map[domain.ReviewerHarness]domain.Review{
			domain.ReviewerCopilot: {ID: "rev-cp", SessionID: "mer-1", Harness: domain.ReviewerCopilot, ReviewerHandleID: "review-copilot"},
		},
		runs: []domain.ReviewRun{{ID: "run-cp", ReviewID: "rev-cp", SessionID: "mer-1", PRURL: multiPRURL, TargetSHA: "sha1",
			Harness: domain.ReviewerCopilot, Status: domain.ReviewRunRunning, Verdict: domain.VerdictNone}},
	}
	launcher := &cancelRecordingLauncher{fakeLauncher: &fakeLauncher{alive: true}}
	eng := newEngineForTest(store, fakeSessions{rec: liveWorker(), ok: true}, prAt("sha1"), twoReviewerProject(), launcher)

	res, err := eng.Cancel(context.Background(), "mer-1")
	if err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if len(launcher.cancelledHandles) != 1 || launcher.cancelledHandles[0] != "review-copilot" || len(res.CancelledRuns) != 1 {
		t.Fatalf("cancelled handles=%v runs=%+v, want the copilot pass stopped", launcher.cancelledHandles, res.CancelledRuns)
	}
	if store.runs[0].Status != domain.ReviewRunCancelled {
		t.Fatalf("copilot run status = %q, want cancelled", store.runs[0].Status)
	}
}

func TestTriggerKeepsAnotherConfiguredReviewersIdlePane(t *testing.T) {
	// claude-code finished and its pane is idle; codex starting must not
	// release it, since configured reviewers run side by side.
	store := approvedByClaudeCode()
	store.reviews = map[domain.ReviewerHarness]domain.Review{
		domain.ReviewerClaudeCode: {ID: "rev-cc", SessionID: "mer-1", Harness: domain.ReviewerClaudeCode, ReviewerHandleID: "review-claude-code"},
	}
	launcher := perHarnessLauncher()
	eng := newEngineForTest(store, fakeSessions{rec: liveWorker(), ok: true}, prAt("sha1"), twoReviewerProject(), launcher)

	res, err := trigger(context.Background(), eng, "mer-1", "", domain.AgentConfig{})
	if err != nil {
		t.Fatalf("Trigger: %v", err)
	}
	if len(res.CreatedRuns) != 1 || res.CreatedRuns[0].Harness != domain.ReviewerCodex {
		t.Fatalf("created runs = %+v, want only codex", res.CreatedRuns)
	}
	if launcher.destroyCalls != 0 {
		t.Fatalf("destroy calls = %d, want claude-code's idle pane kept", launcher.destroyCalls)
	}
}

func TestTriggerReportsReviewerSurfaceForConfiguredReviewers(t *testing.T) {
	eng := newEngineForTest(&fakeStore{}, fakeSessions{rec: liveWorker(), ok: true}, prAt("sha1"), twoReviewerProject(), perHarnessLauncher())

	res, err := trigger(context.Background(), eng, "mer-1", "", domain.AgentConfig{})
	if err != nil {
		t.Fatalf("Trigger: %v", err)
	}
	if res.ReviewerSurface.ReviewID == "" || res.ReviewerSurface.Harness != domain.ReviewerClaudeCode {
		t.Fatalf("surface = %+v, want the first started reviewer's surface", res.ReviewerSurface)
	}
}

// preflightHookLauncher runs hook before each reviewer's preflight, which is
// after the trigger read the runs and before that reviewer launches.
type preflightHookLauncher struct {
	*cancelRecordingLauncher
	hook func(domain.ReviewerHarness)
}

func (p *preflightHookLauncher) Preflight(ctx context.Context, harness domain.ReviewerHarness, workspacePath string, modes ...domain.ReviewerInterfaceMode) error {
	if p.hook != nil {
		p.hook(harness)
	}
	return p.cancelRecordingLauncher.Preflight(ctx, harness, workspacePath, modes...)
}

func TestCancelDuringFirstReviewerLaunchStopsFanOut(t *testing.T) {
	// Cancel does not take the worker lock, so it lands while claude-code is
	// launching. Codex must never start after it returns.
	store := &fakeStore{}
	inner := perHarnessLauncher()
	inner.alive = true
	launcher := &cancelRecordingLauncher{fakeLauncher: inner}
	var eng *Engine
	inner.onSpawn = func(spec LaunchSpec) {
		inner.handle = "review-" + string(spec.Harness)
		if spec.Harness == domain.ReviewerClaudeCode {
			if _, err := eng.Cancel(context.Background(), "mer-1"); err != nil {
				t.Errorf("Cancel: %v", err)
			}
		}
	}
	eng = newEngineForTest(store, fakeSessions{rec: liveWorker(), ok: true}, prAt("sha1"), twoReviewerProject(), launcher)

	if _, err := trigger(context.Background(), eng, "mer-1", "", domain.AgentConfig{}); err != nil {
		t.Fatalf("Trigger: %v", err)
	}
	if got := spawnedHarnesses(inner); len(got) != 1 || got[0] != domain.ReviewerClaudeCode {
		t.Fatalf("spawned = %v, want only claude-code before the cancel", got)
	}
	for _, r := range store.runs {
		if r.Harness == domain.ReviewerCodex || r.Status != domain.ReviewRunCancelled {
			t.Fatalf("run %+v, want only claude-code's pass, cancelled", r)
		}
	}
}

func TestCancelBeforeNextReviewerLaunchStopsThatReviewer(t *testing.T) {
	// Cancel lands after the trigger read the runs for codex but before codex
	// launched, so codex's pane starts after Cancel returned. The trigger must
	// interrupt it and leave no pass running.
	store := &fakeStore{}
	inner := perHarnessLauncher()
	inner.alive = true
	launcher := &preflightHookLauncher{cancelRecordingLauncher: &cancelRecordingLauncher{fakeLauncher: inner}}
	var eng *Engine
	launcher.hook = func(harness domain.ReviewerHarness) {
		if harness == domain.ReviewerCodex {
			if _, err := eng.Cancel(context.Background(), "mer-1"); err != nil {
				t.Errorf("Cancel: %v", err)
			}
		}
	}
	eng = newEngineForTest(store, fakeSessions{rec: liveWorker(), ok: true}, prAt("sha1"), twoReviewerProject(), launcher)

	if _, err := trigger(context.Background(), eng, "mer-1", "", domain.AgentConfig{}); err != nil {
		t.Fatalf("Trigger: %v", err)
	}
	interrupted := false
	for _, h := range launcher.cancelledHandles {
		interrupted = interrupted || h == "review-codex"
	}
	if !interrupted {
		t.Fatalf("cancelled handles = %v, want codex's late pane interrupted", launcher.cancelledHandles)
	}
	for _, r := range store.runs {
		if r.Status == domain.ReviewRunRunning {
			t.Fatalf("run %+v still running after cancel", r)
		}
	}
}

func TestAgentRerunReportsFailureWhileOtherReviewerStillRunning(t *testing.T) {
	// An agent rerun finds claude-code still reviewing (ErrReviewAlreadyRunning
	// for it) and codex fails to start. A review is in progress, so codex's
	// failure is reported with the result rather than returned as an error.
	store := &fakeStore{
		reviews: map[domain.ReviewerHarness]domain.Review{
			domain.ReviewerClaudeCode: {ID: "rev-cc", SessionID: "mer-1", Harness: domain.ReviewerClaudeCode, ReviewerHandleID: "review-claude-code"},
		},
		runs: []domain.ReviewRun{{ID: "run-cc", ReviewID: "rev-cc", SessionID: "mer-1", PRURL: multiPRURL, TargetSHA: "sha1",
			Harness: domain.ReviewerClaudeCode, Status: domain.ReviewRunRunning, Verdict: domain.VerdictNone}},
	}
	launcher := perHarnessLauncher()
	launcher.alive = true
	failing := &failingHarnessLauncher{fakeLauncher: launcher, fail: domain.ReviewerCodex}
	eng := newEngineForTest(store, fakeSessions{rec: liveWorker(), ok: true}, prAt("sha1"), twoReviewerProject(), failing)

	res, err := eng.TriggerWithOptions(context.Background(), "mer-1", TriggerOptions{Source: domain.ReviewTriggerAgent, Rerun: true})
	if err != nil {
		t.Fatalf("rerun err = %v, want codex's failure reported while claude-code runs", err)
	}
	if res.Created || len(res.ReviewerFailures) != 1 || res.ReviewerFailures[0].Harness != domain.ReviewerCodex {
		t.Fatalf("result = %+v, want not created with codex failure reported", res)
	}
}
