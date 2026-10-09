//go:build e2e

package cli_test

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

func TestE2E_DaemonBindsPortWithRecoverableChatReviewer(t *testing.T) {
	e := newEnv(t)
	st, err := sqlitetest.Open(e.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	now := time.Now().UTC()
	workspace := t.TempDir()
	if err := st.UpsertProject(ctx, domain.ProjectRecord{
		ID: "boot", Kind: domain.ProjectKindScratch, Path: workspace, RegisteredAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	worker, err := st.CreateSession(ctx, domain.SessionRecord{
		ProjectID: "boot", Kind: domain.KindWorker, Harness: domain.HarnessCodex,
		Mode: domain.SessionModeChat, HibernatedAt: &now,
		Activity:  domain.Activity{State: domain.ActivityIdle, LastActivityAt: now},
		Metadata:  domain.SessionMetadata{WorkspacePath: workspace, ProviderConversationID: "worker-thread"},
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertReview(ctx, domain.Review{
		ID: "boot-review", SessionID: worker.ID, ProjectID: worker.ProjectID,
		Harness: domain.ReviewerCodex, InterfaceMode: domain.ReviewerInterfaceChat,
		ProviderConversationID: "missing-review-thread", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	// Recovery can fail for this deliberately missing thread. It must never
	// prevent the listener from becoming ready or deadlock daemon shutdown.
	e.startDaemon(t)
}
