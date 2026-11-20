package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Break caught: a PR whose commit has no checks could not be stored as such. It
// stayed 'unknown' (not yet observed), so the PR panel never stopped waiting
// (#7044). The state must round-trip and replace 'unknown' on the next write.
func TestPRCIStateNoneRoundTripsAndReplacesUnknown(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "repo")
	session, err := s.CreateSession(ctx, sampleRecord("repo"))
	if err != nil {
		t.Fatal(err)
	}
	url := "https://github.com/acme/repo/pull/1"
	at := time.Date(2026, 10, 22, 10, 0, 0, 0, time.UTC)
	pr := domain.PullRequest{
		URL: url, SessionID: session.ID, Number: 1, Provider: "github", Host: "github.com", Repo: "acme/repo",
		HeadSHA: "abc123", CI: domain.CIUnknown, Mergeability: domain.MergeMergeable, UpdatedAt: at,
	}
	write := func(p domain.PullRequest) {
		t.Helper()
		if err := s.WriteSCMObservation(ctx, p, nil, nil, nil, nil, ports.ReviewWritePreserve); err != nil {
			t.Fatal(err)
		}
	}
	read := func() domain.CIState {
		t.Helper()
		got, ok, err := s.GetPR(ctx, url)
		if err != nil || !ok {
			t.Fatalf("GetPR ok=%v err=%v", ok, err)
		}
		return got.CI
	}

	write(pr)
	if got := read(); got != domain.CIUnknown {
		t.Fatalf("first write CI = %q, want unknown", got)
	}
	pr.CI = domain.CINone
	pr.UpdatedAt = at.Add(time.Minute)
	write(pr)
	if got := read(); got != domain.CINone {
		t.Fatalf("CI after observing no checks = %q, want none", got)
	}
	pr.CI = domain.CIPending
	pr.UpdatedAt = at.Add(2 * time.Minute)
	write(pr)
	if got := read(); got != domain.CIPending {
		t.Fatalf("CI after checks appear = %q, want pending", got)
	}
}
