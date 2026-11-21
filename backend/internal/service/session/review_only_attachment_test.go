package session

import (
	"context"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestReviewOnlyAttachmentIsMarkedBeforeClaimWrite(t *testing.T) {
	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker, Metadata: domain.SessionMetadata{WorkspacePath: "/ws"}}
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", RepoOriginURL: "https://github.com/acme/repo"}
	claimer := &fakePRClaimer{}
	svc := NewWithDeps(Deps{Store: st, PRClaimer: claimer, SCM: fakeSCM{obs: ports.SCMObservation{
		Fetched: true, Provider: "github", Host: "github.com", Repo: "acme/repo",
		PR: ports.SCMPRObservation{URL: "https://github.com/acme/repo/pull/7", Number: 7},
	}}})
	if _, err := svc.ClaimPR(context.Background(), "mer-1", "7", ClaimPROptions{KeepReviewOnly: true, ReviewOnly: true}); err != nil {
		t.Fatal(err)
	}
	if !claimer.called || !claimer.gotPR.ReviewOnly {
		t.Fatal("review-only target was visible as owned when the claim was written")
	}
}
