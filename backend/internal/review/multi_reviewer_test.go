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

func TestTriggerFailingReviewerAloneOwingWorkIsReportedNotErrored(t *testing.T) {
	// claude-code already approved the head; codex owes a review but cannot
	// start. Nothing new starts, yet the trigger reports codex's failure
	// rather than erroring.
	store := &fakeStore{runs: []domain.ReviewRun{{ID: "run-cc", SessionID: "mer-1", PRURL: multiPRURL, TargetSHA: "sha1",
		Harness: domain.ReviewerClaudeCode, Status: domain.ReviewRunComplete, Verdict: domain.VerdictApproved}}}
	failing := &failingHarnessLauncher{fakeLauncher: perHarnessLauncher(), fail: domain.ReviewerCodex}
	eng := newEngineForTest(store, fakeSessions{rec: liveWorker(), ok: true}, prAt("sha1"), twoReviewerProject(), failing)

	res, err := trigger(context.Background(), eng, "mer-1", "", domain.AgentConfig{})
	if err != nil {
		t.Fatalf("Trigger err = %v, want partial success", err)
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
