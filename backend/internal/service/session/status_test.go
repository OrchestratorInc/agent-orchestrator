package session

import (
	"context"
	"testing"
	"time"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/domain"
)

func TestChangesRequestedPushReviewAndRestart(t *testing.T) {
	const id, url = domain.SessionID("mer-1"), "https://github.com/acme/repo/pull/1"
	st := newFakeStore()
	rec := statusRec(domain.ActivityIdle, false)
	rec.ID, rec.ProjectID = id, "mer"
	st.sessions[id] = rec
	pr := domain.PRFacts{URL: url, Number: 1, HeadSHA: "old-head", Review: domain.ReviewChangesRequest, ExternalChangesRequested: true, UpdatedAt: statusNow}
	st.prFacts[id] = []domain.PRFacts{pr}

	read := func(wantReview domain.ReviewDecision, wantStatus domain.SessionStatus, wantColumn, wantDisplay string) {
		t.Helper()
		// A fresh service models daemon restart: only durable store facts survive.
		svc := &Service{store: st, clock: func() time.Time { return statusNow }}
		for _, get := range []func() (domain.Session, error){
			func() (domain.Session, error) { return svc.Get(context.Background(), id) },
			func() (domain.Session, error) {
				list, err := svc.List(context.Background(), ListFilter{})
				if err != nil || len(list) != 1 {
					return domain.Session{}, err
				}
				return list[0], nil
			},
		} {
			sess, err := get()
			if err != nil {
				t.Fatal(err)
			}
			if len(sess.PRs) != 1 || sess.PRs[0].Review != wantReview || sess.SCMStatus != wantStatus || string(sess.DisplayStatus) != wantDisplay || string(sess.KanbanColumn) != wantColumn {
				t.Fatalf("session after read = review %+v, scm %q, column %q, display %q; want review %q, scm %q, column %q/display %q", sess.PRs, sess.SCMStatus, sess.KanbanColumn, sess.DisplayStatus, wantReview, wantStatus, wantColumn, wantDisplay)
			}
		}
	}

	read(domain.ReviewChangesRequest, domain.StatusChangesRequested, "needs_review", "Changes requested")
	active := rec
	active.Activity.State = domain.ActivityActive
	st.sessions[id] = active
	working, err := (&Service{store: st, clock: func() time.Time { return statusNow }}).Get(context.Background(), id)
	if err != nil || working.KanbanColumn != "needs_review" || string(working.DisplayStatus) != "Addressing comments" {
		t.Fatalf("active worker with requested changes = column %q, display %q, err %v", working.KanbanColumn, working.DisplayStatus, err)
	}
	st.sessions[id] = rec
	// GitHub retains its aggregate verdict, while the review row targeted the old head.
	pr.HeadSHA, pr.ExternalChangesRequested, pr.StaleChangesRequested = "new-head", false, true
	st.prFacts[id] = []domain.PRFacts{pr}
	read(domain.ReviewRequired, domain.StatusReviewPending, "needs_review", "Awaiting review")
	st.reviewRuns[id] = []domain.CurrentHeadReviewRun{{PRURL: url, Verdict: domain.VerdictChangesRequested}}
	read(domain.ReviewChangesRequest, domain.StatusChangesRequested, "validating", "Changes requested")
	st.reviewRuns[id] = []domain.CurrentHeadReviewRun{{PRURL: url + "-other", Verdict: domain.VerdictChangesRequested}}
	read(domain.ReviewRequired, domain.StatusReviewPending, "needs_review", "Awaiting review")
	delete(st.reviewRuns, id)
	if st.prFacts[id][0].Review != domain.ReviewChangesRequest {
		t.Fatal("read changed durable provider decision")
	}
	// A current-head request and then approval each replace the awaiting state.
	pr.ExternalChangesRequested = true
	st.prFacts[id] = []domain.PRFacts{pr}
	read(domain.ReviewChangesRequest, domain.StatusChangesRequested, "needs_review", "Changes requested")
	pr.Review, pr.ExternalChangesRequested = domain.ReviewApproved, false
	st.prFacts[id] = []domain.PRFacts{pr}
	approved, err := (&Service{store: st, clock: func() time.Time { return statusNow }}).Get(context.Background(), id)
	if err != nil || approved.PRs[0].Review != domain.ReviewApproved {
		t.Fatalf("current-head approval = %+v, err %v", approved.PRs, err)
	}
	// An old provider row without a head SHA has no evidence of staleness.
	pr.HeadSHA, pr.Review = "", domain.ReviewChangesRequest
	st.prFacts[id] = []domain.PRFacts{pr}
	unknownHead, err := (&Service{store: st, clock: func() time.Time { return statusNow }}).Get(context.Background(), id)
	if err != nil || unknownHead.PRs[0].Review != domain.ReviewChangesRequest {
		t.Fatalf("unknown-head decision = %+v, err %v", unknownHead.PRs, err)
	}
}

func TestTerminatedSessionGetKeepsCurrentHeadReviewDecision(t *testing.T) {
	const id, url = domain.SessionID("mer-terminated"), "pr/terminated"
	st := newFakeStore()
	rec := statusRec(domain.ActivityExited, true)
	rec.ID, rec.ProjectID = id, "mer"
	st.sessions[id] = rec
	st.prFacts[id] = []domain.PRFacts{{URL: url, HeadSHA: "head", Review: domain.ReviewChangesRequest, StaleChangesRequested: true}}
	st.reviewRuns[id] = []domain.CurrentHeadReviewRun{{PRURL: url, Verdict: domain.VerdictChangesRequested}}
	svc := &Service{store: st, clock: func() time.Time { return statusNow }}
	got, err := svc.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	listed, err := svc.List(context.Background(), ListFilter{})
	if err != nil || len(listed) != 1 {
		t.Fatalf("list = %+v, err %v", listed, err)
	}
	if got.PRs[0].Review != domain.ReviewChangesRequest || listed[0].PRs[0].Review != domain.ReviewChangesRequest {
		t.Fatalf("Get review = %q, List review = %q; want current-head changes requested", got.PRs[0].Review, listed[0].PRs[0].Review)
	}
}

func TestLaterSameHeadRunWithoutVerdictKeepsPostedChangesRequest(t *testing.T) {
	const id, url = domain.SessionID("mer-rerun"), "pr/rerun"
	st := newFakeStore()
	rec := statusRec(domain.ActivityIdle, false)
	rec.ID, rec.ProjectID = id, "mer"
	st.sessions[id] = rec
	st.prFacts[id] = []domain.PRFacts{{URL: url, HeadSHA: "head", Review: domain.ReviewChangesRequest, StaleChangesRequested: true}}
	st.reviewRuns[id] = []domain.CurrentHeadReviewRun{
		{ID: "posted", PRURL: url, Harness: "codex", Verdict: domain.VerdictChangesRequested, CreatedAt: statusNow},
		{ID: "rerun", PRURL: url, Harness: "codex", Status: domain.ReviewRunRunning, CreatedAt: statusNow.Add(time.Minute)},
	}
	svc := &Service{store: st, clock: func() time.Time { return statusNow.Add(2 * time.Minute) }}
	got, err := svc.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	listed, err := svc.List(context.Background(), ListFilter{})
	if err != nil || len(listed) != 1 {
		t.Fatalf("list = %+v, err %v", listed, err)
	}
	if got.PRs[0].Review != domain.ReviewChangesRequest || listed[0].PRs[0].Review != domain.ReviewChangesRequest {
		t.Fatalf("Get review = %q, List review = %q; want posted current-head changes request", got.PRs[0].Review, listed[0].PRs[0].Review)
	}
}

func TestChangesRequestedWithoutProofOfOlderHeadStaysRequested(t *testing.T) {
	for _, tc := range []struct {
		name string
		host string
	}{
		{"GitLab requested_changes", "gitlab.com"},
		{"GitHub bot current-head request", "github.com"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pr := domain.PRFacts{URL: "pr/1", Host: tc.host, HeadSHA: "head", Review: domain.ReviewChangesRequest}
			got := clearStaleChangesRequested([]domain.PRFacts{pr}, nil)
			if got[0].Review != domain.ReviewChangesRequest {
				t.Fatalf("review = %q, want provider changes_requested without proof of an older-head request", got[0].Review)
			}
		})
	}
}

func TestCanonicalizeAliasRunsKeepsLatestSettledVerdict(t *testing.T) {
	oldURL := "https://github.com/AgentWrapper/agent-orchestrator/pull/9"
	newURL := "https://github.com/Untrivial-ai/agent-orchestrator/pull/9"
	prs := []domain.PRFacts{
		{URL: oldURL, Number: 9, SourceBranch: "fix", TargetBranch: "main", HeadSHA: "head", UpdatedAt: statusNow},
		{URL: newURL, Number: 9, SourceBranch: "fix", TargetBranch: "main", HeadSHA: "head", UpdatedAt: statusNow.Add(time.Minute)},
	}
	runs := []domain.CurrentHeadReviewRun{
		{ID: "old-pending", PRURL: oldURL, Harness: "codex", CreatedAt: statusNow, LastVerdictChangesRequested: true},
		{ID: "new-pending", PRURL: newURL, Harness: "codex", CreatedAt: statusNow.Add(time.Minute)},
	}
	got := canonicalizeCurrentHeadReviewRuns(prs, runs)
	if len(got) != 1 || got[0].PRURL != newURL || !got[0].LastVerdictChangesRequested {
		t.Fatalf("pending alias runs = %+v, want inherited current-head changes request", got)
	}
	runs = append(runs, domain.CurrentHeadReviewRun{ID: "approved", PRURL: oldURL, Harness: "codex", CreatedAt: statusNow.Add(2 * time.Minute), Verdict: domain.VerdictApproved})
	runs = append(runs, domain.CurrentHeadReviewRun{ID: "after-approval", PRURL: newURL, Harness: "codex", CreatedAt: statusNow.Add(3 * time.Minute)})
	got = canonicalizeCurrentHeadReviewRuns(prs, runs)
	if len(got) != 1 || got[0].LastVerdictChangesRequested {
		t.Fatalf("alias runs after approval = %+v, want request cleared by later verdict", got)
	}
}

var statusNow = time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)

// statusRec builds a session whose agent HAS delivered a hook signal; the
// no-signal cases below zero FirstSignalAt explicitly.
func statusRec(activity domain.ActivityState, terminated bool) domain.SessionRecord {
	return domain.SessionRecord{
		Activity:      domain.Activity{State: activity, LastActivityAt: statusNow},
		FirstSignalAt: statusNow,
		IsTerminated:  terminated,
	}
}

// silentRec builds a live session that has never delivered a hook signal,
// seeded (spawned/restored) `age` before the derivation time.
func silentRec(age time.Duration) domain.SessionRecord {
	return domain.SessionRecord{
		Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: statusNow.Add(-age)},
	}
}

// chatRec marks a record as a Chat-mode session, whose activity reaches AO
// through its own controller rather than an agent hook pipeline.
func chatRec(rec domain.SessionRecord) domain.SessionRecord {
	rec.Mode = domain.SessionModeChat
	return rec
}

func statusPR(facts domain.PRFacts) []domain.PRFacts { return []domain.PRFacts{facts} }

func TestServiceDerivesStatusFromSessionFactsAndPR(t *testing.T) {
	tests := []struct {
		name string
		rec  domain.SessionRecord
		pr   []domain.PRFacts
		// hookless marks a harness with no activity pipeline (signalCapable
		// false): silence is its permanent normal state, never no_signal.
		hookless bool
		want     domain.SessionStatus
	}{
		{"terminated", statusRec(domain.ActivityExited, true), nil, false, domain.StatusTerminated},
		{"merged-pr", statusRec(domain.ActivityIdle, true), statusPR(domain.PRFacts{Merged: true}), false, domain.StatusMerged},
		{"needs-input", statusRec(domain.ActivityWaitingInput, false), statusPR(domain.PRFacts{CI: domain.CIFailing}), false, domain.StatusNeedsInput},
		{"needs-input-blocked", statusRec(domain.ActivityBlocked, false), statusPR(domain.PRFacts{CI: domain.CIFailing}), false, domain.StatusNeedsInput},
		{"exited", statusRec(domain.ActivityExited, false), statusPR(domain.PRFacts{Mergeability: domain.MergeMergeable}), false, domain.StatusExited},
		{"ci-failed", statusRec(domain.ActivityIdle, false), statusPR(domain.PRFacts{CI: domain.CIFailing}), false, domain.StatusCIFailed},
		{"draft", statusRec(domain.ActivityIdle, false), statusPR(domain.PRFacts{Draft: true}), false, domain.StatusDraft},
		{"changes-requested", statusRec(domain.ActivityIdle, false), statusPR(domain.PRFacts{Review: domain.ReviewChangesRequest}), false, domain.StatusChangesRequested},
		{"mergeable", statusRec(domain.ActivityIdle, false), statusPR(domain.PRFacts{Mergeability: domain.MergeMergeable}), false, domain.StatusMergeable},
		{"approved", statusRec(domain.ActivityIdle, false), statusPR(domain.PRFacts{Review: domain.ReviewApproved}), false, domain.StatusApproved},
		{"merge-blocked-approved", statusRec(domain.ActivityIdle, false), statusPR(domain.PRFacts{Mergeability: domain.MergeBlocked, Review: domain.ReviewApproved}), false, domain.StatusPROpen},
		{"merge-blocked-no-review", statusRec(domain.ActivityIdle, false), statusPR(domain.PRFacts{Mergeability: domain.MergeBlocked}), false, domain.StatusPROpen},
		{"review-pending", statusRec(domain.ActivityIdle, false), statusPR(domain.PRFacts{Review: domain.ReviewRequired}), false, domain.StatusReviewPending},
		{"pr-open", statusRec(domain.ActivityIdle, false), statusPR(domain.PRFacts{}), false, domain.StatusPROpen},
		{"working", statusRec(domain.ActivityActive, false), nil, false, domain.StatusWorking},
		{"idle", statusRec(domain.ActivityIdle, false), nil, false, domain.StatusIdle},

		// A live session whose hook-capable agent never signaled is no_signal
		// once the grace passes — never a confident idle.
		{"no-signal-after-grace", silentRec(noSignalGrace + time.Second), nil, false, domain.StatusNoSignal},
		// A hook-less harness can never signal: its silence stays idle forever
		// instead of degrading into a false "needs you".
		{"hookless-silent-stays-idle", silentRec(2 * noSignalGrace), nil, true, domain.StatusIdle},
		// A chat session's silence is not evidence of anything. AO owns the
		// provider connection, so a lost controller arrives as exited; an idle
		// chat session is simply waiting on the user and must not read as broken.
		{"chat-silent-stays-idle", chatRec(silentRec(4 * noSignalGrace)), nil, false, domain.StatusIdle},
		// Right after spawn the agent legitimately hasn't called back yet.
		{"silent-within-grace-is-idle", silentRec(10 * time.Second), nil, false, domain.StatusIdle},
		{"silent-at-grace-boundary-is-idle", silentRec(noSignalGrace), nil, false, domain.StatusIdle},
		// Termination and PR facts outrank the missing-signal downgrade.
		{
			"no-signal-terminated-wins",
			domain.SessionRecord{Activity: domain.Activity{State: domain.ActivityExited, LastActivityAt: statusNow.Add(-2 * noSignalGrace)}, IsTerminated: true},
			nil,
			false,
			domain.StatusTerminated,
		},
		{"no-signal-pr-wins", silentRec(2 * noSignalGrace), statusPR(domain.PRFacts{}), false, domain.StatusPROpen},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := deriveStatus(tt.rec, tt.pr, statusNow, !tt.hookless); got != tt.want {
				t.Fatalf("got %q want %q", got, tt.want)
			}
		})
	}
}

func TestDeriveStatusActivityPrecedence(t *testing.T) {
	tests := []struct {
		name     string
		activity domain.ActivityState
		prs      []domain.PRFacts
		want     domain.SessionStatus
	}{
		{"active-without-pr", domain.ActivityActive, nil, domain.StatusWorking},
		{"active-with-open-pr", domain.ActivityActive, statusPR(domain.PRFacts{}), domain.StatusWorking},
		{"active-with-draft-pr", domain.ActivityActive, statusPR(domain.PRFacts{Draft: true}), domain.StatusWorking},
		{"active-with-review-pending", domain.ActivityActive, statusPR(domain.PRFacts{Review: domain.ReviewRequired}), domain.StatusWorking},
		{"active-with-ci-failure", domain.ActivityActive, statusPR(domain.PRFacts{CI: domain.CIFailing}), domain.StatusWorking},
		{"active-with-changes-requested", domain.ActivityActive, statusPR(domain.PRFacts{Review: domain.ReviewChangesRequest}), domain.StatusWorking},
		{"active-with-approved-pr", domain.ActivityActive, statusPR(domain.PRFacts{Review: domain.ReviewApproved}), domain.StatusWorking},
		{"active-with-mergeable-pr", domain.ActivityActive, statusPR(domain.PRFacts{Mergeability: domain.MergeMergeable}), domain.StatusWorking},
		{"active-with-merged-pr", domain.ActivityActive, statusPR(domain.PRFacts{Merged: true}), domain.StatusWorking},
		{"exited-without-pr", domain.ActivityExited, nil, domain.StatusExited},
		{"exited-with-open-pr", domain.ActivityExited, statusPR(domain.PRFacts{}), domain.StatusExited},
		{"exited-with-merged-pr", domain.ActivityExited, statusPR(domain.PRFacts{Merged: true}), domain.StatusExited},
		{"waiting-with-pr", domain.ActivityWaitingInput, statusPR(domain.PRFacts{Mergeability: domain.MergeMergeable}), domain.StatusNeedsInput},
		{"blocked-with-pr", domain.ActivityBlocked, statusPR(domain.PRFacts{Mergeability: domain.MergeMergeable}), domain.StatusNeedsInput},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := deriveStatus(statusRec(tt.activity, false), tt.prs, statusNow, true); got != tt.want {
				t.Fatalf("got %q want %q", got, tt.want)
			}
		})
	}
}

func TestDeriveSCMStatusRemainsAvailableWhileAgentIsActive(t *testing.T) {
	tests := []struct {
		name string
		prs  []domain.PRFacts
		want domain.SessionStatus
	}{
		{"without-pr", nil, ""},
		{"open", statusPR(domain.PRFacts{}), domain.StatusPROpen},
		{"draft", statusPR(domain.PRFacts{Draft: true}), domain.StatusDraft},
		{"ci-failed", statusPR(domain.PRFacts{CI: domain.CIFailing}), domain.StatusCIFailed},
		{"changes-requested", statusPR(domain.PRFacts{Review: domain.ReviewChangesRequest}), domain.StatusChangesRequested},
		{"review-pending", statusPR(domain.PRFacts{Review: domain.ReviewRequired}), domain.StatusReviewPending},
		{"approved", statusPR(domain.PRFacts{Review: domain.ReviewApproved}), domain.StatusApproved},
		{"mergeable", statusPR(domain.PRFacts{Mergeability: domain.MergeMergeable}), domain.StatusMergeable},
		{"merge-blocked", statusPR(domain.PRFacts{Mergeability: domain.MergeBlocked}), domain.StatusPROpen},
		{"merged", statusPR(domain.PRFacts{Merged: true}), domain.StatusMerged},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := deriveSCMStatus(tt.prs); got != tt.want {
				t.Fatalf("deriveSCMStatus() = %q, want %q", got, tt.want)
			}
			if tt.want != "" {
				if got := deriveStatus(statusRec(domain.ActivityActive, false), tt.prs, statusNow, true); got != domain.StatusWorking {
					t.Fatalf("deriveStatus(active) = %q, want working", got)
				}
			}
		})
	}
}

// A blocked stacked child cannot merge until its parent does, so its readiness
// signals are suppressed, but its problem signals (failing CI, draft,
// requested-changes/unresolved-comments) must still surface for the session.
func TestAggregateStackedChildSignals(t *testing.T) {
	parent := domain.PRFacts{URL: "parent", SourceBranch: "feat", Mergeability: domain.MergeMergeable}
	child := func(f domain.PRFacts) domain.PRFacts {
		f.URL = "child"
		f.SourceBranch = "feat/child"
		f.TargetBranch = "feat"
		return f
	}
	tests := []struct {
		name string
		prs  []domain.PRFacts
		want domain.SessionStatus
	}{
		{"blocked-child-ci-failing-surfaces", []domain.PRFacts{parent, child(domain.PRFacts{CI: domain.CIFailing})}, domain.StatusCIFailed},
		{"blocked-child-draft-surfaces", []domain.PRFacts{parent, child(domain.PRFacts{Draft: true})}, domain.StatusDraft},
		{"blocked-child-changes-requested-surfaces", []domain.PRFacts{parent, child(domain.PRFacts{Review: domain.ReviewChangesRequest})}, domain.StatusChangesRequested},
		{"blocked-child-unresolved-comments-surfaces", []domain.PRFacts{parent, child(domain.PRFacts{ReviewComments: true})}, domain.StatusCommented},
		// A blocked child's readiness signals stay hidden: only the parent's
		// mergeable state drives the session.
		{"blocked-child-mergeable-suppressed", []domain.PRFacts{parent, child(domain.PRFacts{Mergeability: domain.MergeMergeable})}, domain.StatusMergeable},
		{"blocked-child-approved-suppressed", []domain.PRFacts{parent, child(domain.PRFacts{Review: domain.ReviewApproved})}, domain.StatusMergeable},
		// Degenerate set where every open PR is blocked and none is actionable:
		// fall back to the raw aggregate so the session never goes dark.
		{
			"all-blocked-no-actionable-falls-back",
			[]domain.PRFacts{
				{URL: "a", SourceBranch: "feat/a", TargetBranch: "feat/b", Mergeability: domain.MergeMergeable},
				{URL: "b", SourceBranch: "feat/b", TargetBranch: "feat/a", Mergeability: domain.MergeMergeable},
			},
			domain.StatusMergeable,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := deriveStatus(statusRec(domain.ActivityIdle, false), tt.prs, statusNow, true); got != tt.want {
				t.Fatalf("got %q want %q", got, tt.want)
			}
		})
	}
}

// Without an injected capability predicate the service must never claim
// no_signal; with one, capability follows the predicate per harness.
func TestHarnessSignalsCapabilityGate(t *testing.T) {
	if (&Service{}).harnessSignals(domain.HarnessCodex) {
		t.Fatal("zero-value Service reports signal-capable; want incapable (never no_signal)")
	}
	s := NewWithDeps(Deps{SignalCapable: func(h domain.AgentHarness) bool { return h == domain.HarnessCodex }})
	if !s.harnessSignals(domain.HarnessCodex) {
		t.Fatal("harnessSignals(codex) = false with codex-capable predicate")
	}
	if s.harnessSignals(domain.HarnessAmp) {
		t.Fatal("harnessSignals(amp) = true with codex-only predicate")
	}
}
