package daemon

import (
	"context"
	"errors"
	"strings"
	"testing"

	scmmulti "github.com/aoagents/agent-orchestrator/backend/internal/adapters/scm/multi"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	reviewcore "github.com/aoagents/agent-orchestrator/backend/internal/review"
	sessionsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/session"
)

type fakeClaimer struct {
	err  error
	opts sessionsvc.ClaimPROptions
}

func (f *fakeClaimer) ClaimPR(_ context.Context, _ domain.SessionID, _ string, opts sessionsvc.ClaimPROptions) (sessionsvc.ClaimPRResult, error) {
	f.opts = opts
	return sessionsvc.ClaimPRResult{}, f.err
}

func (f *fakeClaimer) FindBranchPRs(context.Context, domain.SessionID) ([]string, error) {
	return nil, nil
}

// The review refresher reuses the session claim, which never takes a PR over
// from another active session, and reports claim failures as review errors the
// trigger can explain.
func TestReviewPRRefresherClaimsWithoutTakeoverAndTranslatesErrors(t *testing.T) {
	cases := []struct {
		err  error
		want error
		text string
	}{
		{nil, nil, ""},
		{ports.PRClaimedByActiveSessionError{Owner: "mer-2"}, reviewcore.ErrPROwnedElsewhere, "belongs to active session mer-2"},
		{sessionsvc.ErrPRNotFound, reviewcore.ErrNotFound, "not found on the provider"},
		{sessionsvc.ErrPRNotOpen, reviewcore.ErrInvalid, "is not open"},
		{sessionsvc.ErrProjectMismatch, reviewcore.ErrInvalid, "this project's repository"},
		{sessionsvc.ErrSCMUnavailable, reviewcore.ErrInvalid, "could not fetch"},
	}
	for _, tc := range cases {
		claimer := &fakeClaimer{err: tc.err}
		err := reviewPRRefresher{sessions: claimer}.RefreshPR(context.Background(), "mer-1", "https://github.com/o/r/pull/1")
		if claimer.opts.AllowTakeover {
			t.Fatal("refresh must never take a PR over from an active session")
		}
		if tc.want == nil {
			if err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			continue
		}
		if !errors.Is(err, tc.want) || !strings.Contains(err.Error(), tc.text) {
			t.Fatalf("claim err %v -> %v, want %v containing %q", tc.err, err, tc.want, tc.text)
		}
	}
}

// The session service finds a just-opened branch PR only when the daemon's
// SCM provider can list a branch's open PRs; keep that capability wired.
func TestDaemonSCMProviderListsBranchPRs(t *testing.T) {
	var provider any = (*scmmulti.Provider)(nil)
	if _, ok := provider.(interface {
		ListOpenPRsByBranch(ctx context.Context, repo ports.SCMRepo, branch string) ([]ports.SCMPRObservation, error)
	}); !ok {
		t.Fatal("daemon SCM provider cannot list a branch's open PRs")
	}
}
