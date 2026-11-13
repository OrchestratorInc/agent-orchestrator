package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

func TestPRConsolidationPreservesExplicitOwnership(t *testing.T) {
	ctx := context.Background()
	st := sqlitetest.MustOpen(t)
	now := time.Date(2026, 1, 20, 12, 0, 0, 0, time.UTC)
	if err := st.UpsertProject(ctx, domain.ProjectRecord{ID: "p", Path: t.TempDir(), RegisteredAt: now}); err != nil {
		t.Fatal(err)
	}
	session, err := st.CreateSession(ctx, domain.SessionRecord{ProjectID: "p", Kind: domain.KindWorker, CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	canonical := domain.PullRequest{URL: "https://github.com/o/new/pull/1", SessionID: session.ID, Number: 1, Provider: "github", Host: "github.com", Repo: "o/new", CreatedAtProvider: now.Add(-18 * 24 * time.Hour), UpdatedAt: now}
	if err := st.WriteSCMObservation(ctx, canonical, nil, nil, nil, nil, ports.ReviewWritePreserve); err != nil {
		t.Fatal(err)
	}
	claimed := canonical
	claimed.URL, claimed.Repo, claimed.ProviderID = "https://github.com/o/old/pull/1", "o/old", "stable-1"
	if _, err := st.ClaimPR(ctx, claimed, nil, nil, nil, nil, ports.ReviewWritePreserve, false); err != nil {
		t.Fatal(err)
	}
	canonical.ProviderID = claimed.ProviderID
	for _, source := range []string{"", domain.PROwnershipDiscovered} {
		canonical.OwnershipSource = source
		if err := st.WriteSCMObservation(ctx, canonical, nil, nil, nil, nil, ports.ReviewWritePreserve); err != nil {
			t.Fatal(err)
		}
		for _, url := range []string{canonical.URL, claimed.URL} {
			got, found, err := st.GetPR(ctx, url)
			if err != nil || !found || got.URL != canonical.URL || got.OwnershipSource != domain.PROwnershipClaimed || !got.AllowsAutomaticWork(session) {
				t.Fatalf("claim lost for %s: %+v found=%v err=%v", url, got, found, err)
			}
		}
	}
}
