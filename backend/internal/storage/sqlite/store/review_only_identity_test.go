package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestReviewOnlySurvivesCanonicalURLChange(t *testing.T) {
	for _, canonicalExists := range []bool{false, true} {
		name := "new canonical row"
		if canonicalExists {
			name = "existing canonical row"
		}
		t.Run(name, func(t *testing.T) {
			s := newTestStore(t)
			ctx := context.Background()
			seedProject(t, s, "repo")
			session, err := s.CreateSession(ctx, sampleRecord("repo"))
			if err != nil {
				t.Fatal(err)
			}
			old := domain.PullRequest{URL: "https://github.com/old/repo/pull/7", SessionID: session.ID, Number: 7, Provider: "github", Host: "github.com", ProviderID: "PR_7", UpdatedAt: time.Now().UTC()}
			if err := s.WriteSCMObservation(ctx, old, nil, nil, nil, nil, ports.ReviewWritePreserve); err != nil {
				t.Fatal(err)
			}
			if ok, err := s.SetPRReviewOnly(ctx, old.URL, session.ID, true); err != nil || !ok {
				t.Fatalf("set review-only: %v, %v", ok, err)
			}
			canonical := old
			canonical.URL = "https://github.com/new/repo/pull/7"
			if canonicalExists {
				canonical.ProviderID = ""
				if err := s.WriteSCMObservation(ctx, canonical, nil, nil, nil, nil, ports.ReviewWritePreserve); err != nil {
					t.Fatal(err)
				}
				canonical.ProviderID = old.ProviderID
			}
			if err := s.WriteSCMObservation(ctx, canonical, nil, nil, nil, nil, ports.ReviewWritePreserve); err != nil {
				t.Fatal(err)
			}
			got, ok, err := s.GetPR(ctx, canonical.URL)
			if err != nil || !ok || !got.ReviewOnly {
				t.Fatalf("canonical PR = %+v, found=%v err=%v; want review-only", got, ok, err)
			}
		})
	}
}

func TestReviewOnlyClaimKeepsExistingOwnerAcrossURLChange(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "repo")
	session, err := s.CreateSession(ctx, sampleRecord("repo"))
	if err != nil {
		t.Fatal(err)
	}
	pr := domain.PullRequest{URL: "https://github.com/old/repo/pull/7", SessionID: session.ID, Number: 7, Provider: "github", Host: "github.com", ProviderID: "PR_7", UpdatedAt: time.Now().UTC()}
	if err := s.WriteSCMObservation(ctx, pr, nil, nil, nil, nil, ports.ReviewWritePreserve); err != nil {
		t.Fatal(err)
	}
	pr.URL, pr.ReviewOnly = "https://github.com/new/repo/pull/7", true
	if _, err := s.ClaimPR(ctx, pr, nil, nil, nil, nil, ports.ReviewWritePreserve, false); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.GetPR(ctx, pr.URL)
	if err != nil || !ok || got.ReviewOnly {
		t.Fatalf("claim disowned an existing PR: reviewOnly=%v found=%v err=%v", got.ReviewOnly, ok, err)
	}
}
