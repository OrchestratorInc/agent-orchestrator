package review

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const requestedPRURL = "https://github.com/o/r/pull/1"

func requestedRun(id string, harness domain.ReviewerHarness, status domain.ReviewRunStatus, verdict domain.ReviewVerdict, created int64) domain.ReviewRun {
	return domain.ReviewRun{
		ID: id, ReviewID: "rev-" + string(harness), SessionID: "mer-1", Harness: harness,
		PRURL: requestedPRURL, TargetSHA: "sha1", Status: status, Verdict: verdict,
		CreatedAt: time.Unix(created, 0).UTC(),
	}
}

// A worker asking for its own review must hear that the head is already being
// reviewed rather than silently get the existing pass back.
func TestTriggerRejectReviewedHeadReportsRunningReview(t *testing.T) {
	store := &fakeStore{
		reviews: map[domain.ReviewerHarness]domain.Review{
			domain.ReviewerClaudeCode: {ID: "rev-claude-code", SessionID: "mer-1", Harness: domain.ReviewerClaudeCode, ReviewerHandleID: "claude-pane"},
		},
		runs: []domain.ReviewRun{requestedRun("run-1", domain.ReviewerClaudeCode, domain.ReviewRunRunning, domain.VerdictNone, 1)},
	}
	launcher := &fakeLauncher{alive: true}
	eng := newEngineForTest(store, fakeSessions{rec: liveWorker(), ok: true}, prAt("sha1"), fakeProjects{}, launcher)

	_, err := eng.TriggerWithOptions(context.Background(), "mer-1", TriggerOptions{Source: domain.ReviewTriggerAgent, RejectReviewedHead: true})
	if !errors.Is(err, ErrReviewAlreadyRunning) || !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v, want ErrReviewAlreadyRunning", err)
	}
	if !strings.Contains(err.Error(), "claude-code is already reviewing PR #1 head sha1") || !strings.Contains(err.Error(), "--rerun") {
		t.Fatalf("err = %q, want reviewer, PR, head, and the --rerun hint", err)
	}
	if launcher.spawnCount != 0 || launcher.notified || len(store.runs) != 1 {
		t.Fatalf("a rejected trigger must start nothing: launcher=%+v runs=%+v", launcher, store.runs)
	}
}

// "Any review on that head" counts every reviewer, even when a later pass by
// another reviewer failed and so is the newest run on the head.
func TestTriggerRejectReviewedHeadReportsAnyCompletedReview(t *testing.T) {
	store := &fakeStore{
		runs: []domain.ReviewRun{
			requestedRun("run-1", domain.ReviewerClaudeCode, domain.ReviewRunComplete, domain.VerdictApproved, 1),
			requestedRun("run-2", domain.ReviewerCodex, domain.ReviewRunFailed, domain.VerdictNone, 2),
		},
	}
	launcher := &fakeLauncher{}
	eng := newEngineForTest(store, fakeSessions{rec: liveWorker(), ok: true}, prAt("sha1"), fakeProjects{}, launcher)

	_, err := eng.TriggerWithOptions(context.Background(), "mer-1", TriggerOptions{Source: domain.ReviewTriggerAgent, RejectReviewedHead: true})
	if !errors.Is(err, ErrHeadAlreadyReviewed) {
		t.Fatalf("err = %v, want ErrHeadAlreadyReviewed", err)
	}
	if !strings.Contains(err.Error(), "PR #1 head sha1 was already reviewed") || !strings.Contains(err.Error(), "push new commits, or pass --rerun") {
		t.Fatalf("err = %q, want the already-reviewed explanation", err)
	}
	if launcher.spawnCount != 0 || len(store.runs) != 2 {
		t.Fatalf("a rejected trigger must start nothing: launcher=%+v runs=%+v", launcher, store.runs)
	}
}

func TestTriggerRejectReviewedHeadStartsAnUnreviewedHeadAsAgent(t *testing.T) {
	store := &fakeStore{
		runs: []domain.ReviewRun{requestedRun("run-old", domain.ReviewerClaudeCode, domain.ReviewRunComplete, domain.VerdictChangesRequested, 1)},
	}
	store.runs[0].TargetSHA = "sha0"
	launcher := &fakeLauncher{handle: "claude-pane"}
	eng := newEngineForTest(store, fakeSessions{rec: liveWorker(), ok: true}, prAt("sha1"), fakeProjects{}, launcher)

	res, err := eng.TriggerWithOptions(context.Background(), "mer-1", TriggerOptions{Source: domain.ReviewTriggerAgent, RejectReviewedHead: true})
	if err != nil {
		t.Fatalf("TriggerWithOptions: %v", err)
	}
	if !res.Created || len(res.CreatedRuns) != 1 {
		t.Fatalf("result = %+v, want one new pass", res)
	}
	if got := res.CreatedRuns[0]; got.TargetSHA != "sha1" || got.TriggerSource != domain.ReviewTriggerAgent {
		t.Fatalf("run = %+v, want the new head recorded as agent-requested", got)
	}
}

// --rerun really reviews the same commit again with the same reviewer. The
// SQLite unique index used to make this collide with the approved pass.
func TestTriggerRerunReviewsAnApprovedHeadAgainWithTheSameReviewer(t *testing.T) {
	ctx := context.Background()
	store := newSQLiteReviewStore(t)
	worker := liveWorker()
	seedReviewWorker(t, store, worker)
	now := time.Unix(1, 0).UTC()
	if err := store.UpsertReview(ctx, domain.Review{ID: "rev-claude-code", SessionID: worker.ID, ProjectID: worker.ProjectID, Harness: domain.ReviewerClaudeCode, InterfaceMode: domain.ReviewerInterfaceTUI, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("seed review: %v", err)
	}
	approved := requestedRun("run-approved", domain.ReviewerClaudeCode, domain.ReviewRunRunning, domain.VerdictNone, 1)
	if err := store.InsertReviewRun(ctx, approved); err != nil {
		t.Fatalf("seed run: %v", err)
	}
	if _, err := store.UpdateReviewRunResult(ctx, approved.ID, domain.ReviewRunComplete, domain.VerdictApproved, "", "", "", true); err != nil {
		t.Fatalf("approve seeded run: %v", err)
	}
	launcher := &fakeLauncher{handle: "claude-pane"}
	eng := newEngineForTest(store, fakeSessions{rec: worker, ok: true}, prAt("sha1"), fakeProjects{}, launcher)

	if _, err := eng.TriggerWithOptions(ctx, worker.ID, TriggerOptions{Source: domain.ReviewTriggerAgent, RejectReviewedHead: true}); !errors.Is(err, ErrHeadAlreadyReviewed) {
		t.Fatalf("without --rerun: err = %v, want ErrHeadAlreadyReviewed", err)
	}
	res, err := eng.TriggerWithOptions(ctx, worker.ID, TriggerOptions{Source: domain.ReviewTriggerAgent, Rerun: true})
	if err != nil {
		t.Fatalf("rerun: %v", err)
	}
	if !res.Created || len(res.CreatedRuns) != 1 || res.CreatedRuns[0].Harness != domain.ReviewerClaudeCode || res.CreatedRuns[0].TargetSHA != "sha1" {
		t.Fatalf("rerun result = %+v, want a new claude-code pass on sha1", res)
	}
	runs, err := store.ListReviewRunsBySession(ctx, worker.ID)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("runs = %+v, want the approved pass plus the rerun", runs)
	}
}

// A worker may ask several reviewers at once. Adding one must not cancel or
// tear down another that is still working on the same head.
func TestTriggerRerunAddsAParallelReviewerWithoutStoppingTheRunningOne(t *testing.T) {
	store := &fakeStore{
		reviews: map[domain.ReviewerHarness]domain.Review{
			domain.ReviewerClaudeCode: {ID: "rev-claude-code", SessionID: "mer-1", Harness: domain.ReviewerClaudeCode, ReviewerHandleID: "claude-pane", ReviewerActivityState: domain.ActivityActive},
		},
		runs: []domain.ReviewRun{requestedRun("run-1", domain.ReviewerClaudeCode, domain.ReviewRunRunning, domain.VerdictNone, 1)},
	}
	launcher := &fakeLauncher{alive: true, handle: "codex-pane"}
	eng := newEngineForTest(store, fakeSessions{rec: liveWorker(), ok: true}, prAt("sha1"), fakeProjects{}, launcher)

	res, err := eng.TriggerWithOptions(context.Background(), "mer-1", TriggerOptions{Harness: domain.ReviewerCodex, Source: domain.ReviewTriggerAgent, Rerun: true})
	if err != nil {
		t.Fatalf("TriggerWithOptions: %v", err)
	}
	if !res.Created || len(res.CreatedRuns) != 1 || res.CreatedRuns[0].Harness != domain.ReviewerCodex {
		t.Fatalf("result = %+v, want a new codex pass", res)
	}
	if launcher.destroyCalls != 0 {
		t.Fatalf("the running claude-code reviewer was destroyed: %+v", launcher)
	}
	if got := store.runs[0]; got.Status != domain.ReviewRunRunning {
		t.Fatalf("claude-code run = %+v, want still running", got)
	}
	if review := store.reviews[domain.ReviewerClaudeCode]; review.ReviewerHandleID != "claude-pane" {
		t.Fatalf("claude-code reviewer row = %+v, want its pane kept", review)
	}

	list, err := eng.List(context.Background(), "mer-1")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	harnesses := map[domain.ReviewerHarness]bool{}
	for _, surface := range list.ActiveReviewers {
		harnesses[surface.Harness] = true
	}
	if !harnesses[domain.ReviewerClaudeCode] || !harnesses[domain.ReviewerCodex] {
		t.Fatalf("active reviewers = %+v, want both claude-code and codex", list.ActiveReviewers)
	}
	if len(list.Reviews) != 1 || list.Reviews[0].Status != ReviewStateRunning {
		t.Fatalf("reviews = %+v, want the head shown as running", list.Reviews)
	}
}

// An idle pane of another reviewer is still released, as before, so parallel
// support does not leak reviewer processes.
func TestTriggerReleasesAnIdleOtherReviewer(t *testing.T) {
	store := &fakeStore{
		reviews: map[domain.ReviewerHarness]domain.Review{
			domain.ReviewerClaudeCode: {ID: "rev-claude-code", SessionID: "mer-1", Harness: domain.ReviewerClaudeCode, ReviewerHandleID: "claude-pane"},
		},
		runs: []domain.ReviewRun{requestedRun("run-1", domain.ReviewerClaudeCode, domain.ReviewRunComplete, domain.VerdictApproved, 1)},
	}
	launcher := &fakeLauncher{alive: true, handle: "codex-pane"}
	eng := newEngineForTest(store, fakeSessions{rec: liveWorker(), ok: true}, prAt("sha1"), fakeProjects{}, launcher)

	if _, err := eng.TriggerWithOptions(context.Background(), "mer-1", TriggerOptions{Harness: domain.ReviewerCodex, Source: domain.ReviewTriggerAgent, Rerun: true}); err != nil {
		t.Fatalf("TriggerWithOptions: %v", err)
	}
	if launcher.destroyCalls != 1 || launcher.destroyedHandle != "claude-pane" {
		t.Fatalf("launcher = %+v, want the idle claude-code pane released", launcher)
	}
}

func TestTriggerRerunNeverStartsTheSameReviewerTwiceOnOneHead(t *testing.T) {
	store := &fakeStore{
		reviews: map[domain.ReviewerHarness]domain.Review{
			domain.ReviewerClaudeCode: {ID: "rev-claude-code", SessionID: "mer-1", Harness: domain.ReviewerClaudeCode, ReviewerHandleID: "claude-pane"},
		},
		runs: []domain.ReviewRun{requestedRun("run-1", domain.ReviewerClaudeCode, domain.ReviewRunRunning, domain.VerdictNone, 1)},
	}
	launcher := &fakeLauncher{alive: true}
	eng := newEngineForTest(store, fakeSessions{rec: liveWorker(), ok: true}, prAt("sha1"), fakeProjects{}, launcher)

	_, err := eng.TriggerWithOptions(context.Background(), "mer-1", TriggerOptions{Source: domain.ReviewTriggerAgent, Rerun: true})
	if !errors.Is(err, ErrReviewAlreadyRunning) || !strings.Contains(err.Error(), "--agent") {
		t.Fatalf("err = %v, want ErrReviewAlreadyRunning suggesting another --agent", err)
	}
	if launcher.spawnCount != 0 || launcher.notified || len(store.runs) != 1 {
		t.Fatalf("nothing should start: launcher=%+v runs=%+v", launcher, store.runs)
	}
}

func TestTriggerRejectsAutomaticRerunAndUnknownSource(t *testing.T) {
	eng := newEngineForTest(&fakeStore{}, fakeSessions{rec: liveWorker(), ok: true}, prAt("sha1"), fakeProjects{}, &fakeLauncher{})
	for _, opts := range []TriggerOptions{
		{Source: domain.ReviewTriggerAuto, Rerun: true},
		{Source: domain.ReviewTriggerAuto, RejectReviewedHead: true},
		{Source: "robot"},
	} {
		if _, err := eng.TriggerWithOptions(context.Background(), "mer-1", opts); !errors.Is(err, ErrInvalid) {
			t.Fatalf("opts %+v: err = %v, want ErrInvalid", opts, err)
		}
	}
}

func TestCancelStopsEveryRunningReviewer(t *testing.T) {
	store := &fakeStore{
		reviews: map[domain.ReviewerHarness]domain.Review{
			domain.ReviewerClaudeCode: {ID: "rev-claude-code", SessionID: "mer-1", Harness: domain.ReviewerClaudeCode, ReviewerHandleID: "claude-pane"},
			domain.ReviewerCodex:      {ID: "rev-codex", SessionID: "mer-1", Harness: domain.ReviewerCodex, ReviewerHandleID: "codex-pane"},
		},
		runs: []domain.ReviewRun{
			requestedRun("run-1", domain.ReviewerClaudeCode, domain.ReviewRunRunning, domain.VerdictNone, 1),
			requestedRun("run-2", domain.ReviewerCodex, domain.ReviewRunRunning, domain.VerdictNone, 2),
		},
	}
	launcher := &fakeLauncher{alive: true}
	eng := newEngineForTest(store, fakeSessions{rec: liveWorker(), ok: true}, prAt("sha1"), fakeProjects{}, launcher)

	res, err := eng.Cancel(context.Background(), "mer-1")
	if err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if len(res.CancelledRuns) != 2 {
		t.Fatalf("cancelled = %+v, want both reviewers' runs", res.CancelledRuns)
	}
	for _, run := range store.runs {
		if run.Status != domain.ReviewRunCancelled {
			t.Fatalf("run %s = %q, want cancelled", run.ID, run.Status)
		}
	}
	if res.ReviewerHandleID != "claude-pane" {
		t.Fatalf("reported handle = %q, want the selected claude-code reviewer", res.ReviewerHandleID)
	}
}

// A dead pane must only fail its own reviewer's runs, never another reviewer's
// pass that is still running on the same worker.
func TestTriggerStaleReviewerCancelIsScopedToThatReviewer(t *testing.T) {
	store := &fakeStore{
		reviews: map[domain.ReviewerHarness]domain.Review{
			domain.ReviewerCodex: {ID: "rev-codex", SessionID: "mer-1", Harness: domain.ReviewerCodex, ReviewerHandleID: "codex-pane"},
		},
		runs: []domain.ReviewRun{
			requestedRun("run-1", domain.ReviewerCodex, domain.ReviewRunRunning, domain.VerdictNone, 1),
		},
	}
	store.runs[0].PRURL = "https://github.com/o/r/pull/2"
	launcher := &fakeLauncher{handle: "claude-pane"}
	prs := fakePRs{prs: []domain.PullRequest{{URL: requestedPRURL, Number: 1, HeadSHA: "sha1"}, {URL: "https://github.com/o/r/pull/2", Number: 2, HeadSHA: "sha1"}}}
	eng := newEngineForTest(store, fakeSessions{rec: liveWorker(), ok: true}, prs, fakeProjects{}, launcher)

	if _, err := eng.TriggerWithOptions(context.Background(), "mer-1", TriggerOptions{Source: domain.ReviewTriggerAgent, RejectReviewedHead: true}); err != nil {
		t.Fatalf("TriggerWithOptions: %v", err)
	}
	if got := store.runs[0]; got.Status != domain.ReviewRunRunning {
		t.Fatalf("codex run = %+v, want untouched by the claude-code trigger", got)
	}
}

func TestReviewerDefaultsToTheProjectDefaultWorkerConfig(t *testing.T) {
	worker := liveWorker()
	worker.Harness = domain.HarnessClaudeCode
	projects := fakeProjects{cfg: domain.ProjectConfig{
		AgentConfig: domain.AgentConfig{Permissions: "bypass-permissions"},
		Worker:      domain.RoleOverride{Harness: domain.HarnessCodex, AgentConfig: domain.AgentConfig{Model: "gpt-5.5", Effort: "high", Permissions: "bypass-permissions"}},
	}}
	launcher := &fakeLauncher{handle: "codex-pane"}
	eng := newEngineForTest(&fakeStore{}, fakeSessions{rec: worker, ok: true}, prAt("sha1"), projects, launcher)

	res, err := eng.TriggerWithOptions(context.Background(), "mer-1", TriggerOptions{Source: domain.ReviewTriggerAgent})
	if err != nil {
		t.Fatalf("TriggerWithOptions: %v", err)
	}
	if res.Run.Harness != domain.ReviewerCodex {
		t.Fatalf("harness = %q, want the project's default worker agent", res.Run.Harness)
	}
	if got := launcher.gotSpec.AgentConfig; got.Model != "gpt-5.5" || got.Effort != "high" || got.Permissions != "" {
		t.Fatalf("reviewer config = %+v, want worker model/effort and never worker permissions", got)
	}
}

// Restoring a worker restores its selected reviewer. Another reviewer running
// alongside it keeps running when its pane survived, and is cancelled with an
// accurate reason when it did not, instead of being reported as "switched".
func TestRestoreReviewerKeepsALiveParallelReviewerAndCancelsADeadOne(t *testing.T) {
	for _, tc := range []struct {
		name       string
		handle     string
		wantStatus domain.ReviewRunStatus
	}{
		{"live", "opencode-pane", domain.ReviewRunRunning},
		{"torn down", "", domain.ReviewRunCancelled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeStore{
				reviews: map[domain.ReviewerHarness]domain.Review{
					domain.ReviewerClaudeCode: {ID: "rev-claude-code", SessionID: "mer-1", Harness: domain.ReviewerClaudeCode},
					domain.ReviewerOpenCode:   {ID: "rev-opencode", SessionID: "mer-1", Harness: domain.ReviewerOpenCode, ReviewerHandleID: tc.handle},
				},
				runs: []domain.ReviewRun{
					requestedRun("run-claude", domain.ReviewerClaudeCode, domain.ReviewRunComplete, domain.VerdictApproved, 1),
					requestedRun("run-opencode", domain.ReviewerOpenCode, domain.ReviewRunRunning, domain.VerdictNone, 2),
				},
			}
			launcher := &fakeLauncher{alive: true, handle: "claude-pane"}
			eng := newEngineForTest(store, fakeSessions{rec: liveWorker(), ok: true}, prAt("sha1"), fakeProjects{}, launcher)

			if _, err := eng.RestoreReviewer(context.Background(), "mer-1"); err != nil {
				t.Fatalf("RestoreReviewer: %v", err)
			}
			var got domain.ReviewRun
			for _, run := range store.runs {
				if run.ID == "run-opencode" {
					got = run
				}
			}
			if got.Status != tc.wantStatus {
				t.Fatalf("opencode run = %+v, want %s", got, tc.wantStatus)
			}
			if strings.Contains(got.Body, "switched") {
				t.Fatalf("opencode run body = %q, must not claim the reviewer was switched", got.Body)
			}
			if tc.handle != "" && launcher.destroyCalls != 0 {
				t.Fatalf("a live parallel reviewer was destroyed: %+v", launcher)
			}
		})
	}
}

// When an agent asks a reviewer other than the selected one, the selected
// reviewer fields keep meaning the session's selected reviewer; the working
// reviewer is reported through ActiveReviewers.
func TestListKeepsTheSelectedReviewerAndListsTheWorkingOneAsActive(t *testing.T) {
	store := &fakeStore{
		reviews: map[domain.ReviewerHarness]domain.Review{
			domain.ReviewerClaudeCode: {ID: "rev-claude-code", SessionID: "mer-1", Harness: domain.ReviewerClaudeCode},
			domain.ReviewerCodex:      {ID: "rev-codex", SessionID: "mer-1", Harness: domain.ReviewerCodex, ReviewerHandleID: "codex-pane", ReviewerActivityState: domain.ActivityActive},
		},
		runs: []domain.ReviewRun{
			requestedRun("run-claude", domain.ReviewerClaudeCode, domain.ReviewRunComplete, domain.VerdictApproved, 1),
			requestedRun("run-codex", domain.ReviewerCodex, domain.ReviewRunRunning, domain.VerdictNone, 2),
		},
	}
	store.runs[1].ReviewID = "rev-codex"
	eng := newEngineForTest(store, fakeSessions{rec: liveWorker(), ok: true}, prAt("sha1"), fakeProjects{}, &fakeLauncher{alive: true})

	got, err := eng.List(context.Background(), "mer-1")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if got.ReviewerHarness != domain.ReviewerClaudeCode || got.ReviewerSurface.ReviewID != "rev-claude-code" {
		t.Fatalf("selected reviewer = %s/%q/%+v, want the selected claude-code reviewer", got.ReviewerHarness, got.ReviewerHandleID, got.ReviewerSurface)
	}
	// Fork #6064: the live deterministic terminal pane is reported through the
	// selected reviewer's handle, so the inspector's kill control stays
	// visible for an orphaned pane whose DB handle was cleared.
	if got.ReviewerHandleID != "review-mer-1" {
		t.Fatalf("handle = %q, want the live deterministic pane id", got.ReviewerHandleID)
	}
	if len(got.ActiveReviewers) != 1 || got.ActiveReviewers[0].Harness != domain.ReviewerCodex {
		t.Fatalf("active = %+v, want only codex", got.ActiveReviewers)
	}
}

// A Chat reviewer running alongside the selected one has no pane by design and
// is recovered by RecoverChatReviewers; worker restore must not cancel it.
func TestRestoreReviewerLeavesARunningChatReviewerToChatRecovery(t *testing.T) {
	store := &fakeStore{
		reviews: map[domain.ReviewerHarness]domain.Review{
			domain.ReviewerClaudeCode: {ID: "rev-claude-code", SessionID: "mer-1", Harness: domain.ReviewerClaudeCode},
			domain.ReviewerCodex:      {ID: "rev-codex", SessionID: "mer-1", Harness: domain.ReviewerCodex, InterfaceMode: domain.ReviewerInterfaceChat},
		},
		runs: []domain.ReviewRun{
			requestedRun("run-claude", domain.ReviewerClaudeCode, domain.ReviewRunComplete, domain.VerdictApproved, 1),
			requestedRun("run-codex", domain.ReviewerCodex, domain.ReviewRunRunning, domain.VerdictNone, 2),
		},
	}
	eng := newEngineForTest(store, fakeSessions{rec: liveWorker(), ok: true}, prAt("sha1"), fakeProjects{}, &fakeLauncher{alive: true, handle: "claude-pane"})

	if _, err := eng.RestoreReviewer(context.Background(), "mer-1"); err != nil {
		t.Fatalf("RestoreReviewer: %v", err)
	}
	if got := store.runs[1]; got.Status != domain.ReviewRunRunning {
		t.Fatalf("codex chat run = %+v, want still running", got)
	}
}

func TestTriggerPRURLReviewsOnlyTheNamedPR(t *testing.T) {
	prs := fakePRs{prs: []domain.PullRequest{
		{URL: "https://api/pr/1", HTMLURL: "https://github.com/o/r/pull/1", Number: 1, HeadSHA: "sha1"},
		{URL: "https://api/pr/2", HTMLURL: "https://github.com/o/r/pull/2", Number: 2, HeadSHA: "sha2"},
	}}
	eng := newEngineForTest(&fakeStore{}, fakeSessions{rec: liveWorker(), ok: true}, prs, fakeProjects{}, &fakeLauncher{handle: "pane"})

	res, err := eng.TriggerWithOptions(context.Background(), "mer-1", TriggerOptions{Source: domain.ReviewTriggerAgent, PRURL: "https://github.com/o/r/pull/2"})
	if err != nil {
		t.Fatalf("TriggerWithOptions: %v", err)
	}
	if len(res.CreatedRuns) != 1 || res.CreatedRuns[0].PRURL != "https://api/pr/2" {
		t.Fatalf("created = %+v, want only PR #2", res.CreatedRuns)
	}
	if _, err := eng.TriggerWithOptions(context.Background(), "mer-1", TriggerOptions{Source: domain.ReviewTriggerAgent, PRURL: "https://github.com/o/r/pull/7"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("untracked PR: err = %v, want ErrNotFound", err)
	}
}

func TestTriggerWithNoTrackedPRPointsAtThePRFlag(t *testing.T) {
	eng := newEngineForTest(&fakeStore{}, fakeSessions{rec: liveWorker(), ok: true}, fakePRs{}, fakeProjects{}, &fakeLauncher{})
	_, err := eng.TriggerWithOptions(context.Background(), "mer-1", TriggerOptions{Source: domain.ReviewTriggerAgent})
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "ao review trigger --pr <url>") {
		t.Fatalf("err = %v, want a pointer to --pr for a PR AO has not picked up", err)
	}
}

// trigger, triggerWithSource and triggerWithSourceAndMode keep the older
// engine tests readable now that TriggerWithOptions is the only entry point.
func trigger(ctx context.Context, e *Engine, workerID domain.SessionID, harness domain.ReviewerHarness, config domain.AgentConfig) (TriggerResult, error) {
	return e.TriggerWithOptions(ctx, workerID, TriggerOptions{Harness: harness, Config: config})
}

func triggerWithSource(ctx context.Context, e *Engine, workerID domain.SessionID, harness domain.ReviewerHarness, config domain.AgentConfig, source domain.ReviewTriggerSource) (TriggerResult, error) {
	return e.TriggerWithOptions(ctx, workerID, TriggerOptions{Harness: harness, Config: config, Source: source})
}

func triggerWithSourceAndMode(ctx context.Context, e *Engine, workerID domain.SessionID, harness domain.ReviewerHarness, config domain.AgentConfig, source domain.ReviewTriggerSource, mode domain.ReviewerInterfaceMode) (TriggerResult, error) {
	return e.TriggerWithOptions(ctx, workerID, TriggerOptions{Harness: harness, Config: config, Source: source, InterfaceMode: mode})
}
