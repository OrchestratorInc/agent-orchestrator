package session

import (
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

var statusNow = time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)

func statusRec(activity domain.ActivityState, terminated bool) domain.SessionRecord {
	return domain.SessionRecord{
		Activity:      domain.Activity{State: activity, LastActivityAt: statusNow},
		FirstSignalAt: statusNow,
		IsTerminated:  terminated,
	}
}

func silentRec(age time.Duration) domain.SessionRecord {
	return domain.SessionRecord{
		Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: statusNow.Add(-age)},
	}
}

func chatRec(rec domain.SessionRecord) domain.SessionRecord {
	rec.Mode = domain.SessionModeChat
	return rec
}

func statusPR(facts domain.PRFacts) []domain.PRFacts { return []domain.PRFacts{facts} }

func TestServiceDerivesStatusFromSessionFactsAndPR(t *testing.T) {
	tests := []struct {
		name     string
		rec      domain.SessionRecord
		pr       []domain.PRFacts
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

		{"no-signal-after-grace", silentRec(noSignalGrace + time.Second), nil, false, domain.StatusNoSignal},
		{"hookless-silent-stays-idle", silentRec(2 * noSignalGrace), nil, true, domain.StatusIdle},
		{"chat-silent-stays-idle", chatRec(silentRec(4 * noSignalGrace)), nil, false, domain.StatusIdle},
		{"silent-within-grace-is-idle", silentRec(10 * time.Second), nil, false, domain.StatusIdle},
		{"silent-at-grace-boundary-is-idle", silentRec(noSignalGrace), nil, false, domain.StatusIdle},
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
		{"blocked-child-mergeable-suppressed", []domain.PRFacts{parent, child(domain.PRFacts{Mergeability: domain.MergeMergeable})}, domain.StatusMergeable},
		{"blocked-child-approved-suppressed", []domain.PRFacts{parent, child(domain.PRFacts{Review: domain.ReviewApproved})}, domain.StatusMergeable},
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
