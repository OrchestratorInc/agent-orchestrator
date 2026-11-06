package store_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

func TestCompleteSessionInterfaceTransitionKeepsNoticeAtomicAndRecoverable(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	st := sqlitetest.MustOpenAt(t, dataDir)
	seedProject(t, st, "atomic-switch")
	rec := sampleRecord("atomic-switch")
	rec.Harness = domain.HarnessHermes
	session, err := st.CreateSession(ctx, rec)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	transition, created, err := st.CreateSessionInterfaceTransition(ctx, domain.SessionInterfaceTransition{
		ID: "completion-atomic", SessionID: session.ID,
		SourceMode: domain.SessionModeTUI, TargetMode: domain.SessionModeChat,
		Policy: domain.SessionInterfaceTransitionDrain, HistoryPolicy: domain.SessionInterfaceTransitionHistoryStrict,
		Phase: domain.SessionInterfaceTransitionActivating, DegradedKind: domain.SessionInterfaceTransitionDegradedIdentityReset,
		NativeConversationID: "native-hermes", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil || !created {
		t.Fatalf("create transition: created=%v err=%v", created, err)
	}
	raw, err := sql.Open("sqlite", "file:"+filepath.Join(dataDir, "ao.db")+failureStorePragmas)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = raw.Close() }()
	if _, err := raw.ExecContext(ctx, `CREATE TRIGGER reject_handoff_notice
BEFORE INSERT ON session_interface_transition_messages
BEGIN SELECT RAISE(ABORT, 'injected notice failure'); END;`); err != nil {
		t.Fatal(err)
	}
	const notice = "Chat started fresh."
	moved, err := st.CompleteSessionInterfaceTransition(ctx, transition.ID, transition.Phase, notice, now.Add(time.Second))
	if err == nil || moved {
		t.Fatalf("completion escaped failed notice insert: moved=%v err=%v", moved, err)
	}
	current, found, err := st.GetSessionInterfaceTransition(ctx, transition.ID)
	if err != nil || !found || current.Phase != transition.Phase || !current.CompletedAt.IsZero() {
		t.Fatalf("completion escaped rollback: transition=%+v found=%v err=%v", current, found, err)
	}
	pending, err := st.ListPendingSessionInterfaceTransitionMessages(ctx, transition.ID)
	if err != nil || len(pending) != 0 {
		t.Fatalf("failed completion left messages: %+v err=%v", pending, err)
	}
	if _, err := raw.ExecContext(ctx, "DROP TRIGGER reject_handoff_notice"); err != nil {
		t.Fatal(err)
	}
	moved, err = st.CompleteSessionInterfaceTransition(ctx, transition.ID, domain.SessionInterfaceTransitionRequested, notice, now.Add(time.Second))
	if err != nil || moved {
		t.Fatalf("stale phase completed transition: moved=%v err=%v", moved, err)
	}
	completedAt := now.Add(2 * time.Second)
	moved, err = st.CompleteSessionInterfaceTransition(ctx, transition.ID, transition.Phase, notice, completedAt)
	if err != nil || !moved {
		t.Fatalf("retry completion: moved=%v err=%v", moved, err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = sqlite.OpenPreMigrated(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("close reopened store: %v", err)
		}
	})
	current, found, err = st.GetSessionInterfaceTransition(ctx, transition.ID)
	if err != nil || !found || current.Phase != domain.SessionInterfaceTransitionCompleted ||
		!current.CompletedAt.Equal(completedAt) || current.NativeConversationID != "native-hermes" {
		t.Fatalf("reopened completion: transition=%+v found=%v err=%v", current, found, err)
	}
	deliverable, err := st.ListDeliverableSessionInterfaceTransitions(ctx)
	if err != nil || len(deliverable) != 1 || deliverable[0].ID != transition.ID {
		t.Fatalf("restart cannot discover notice: transitions=%+v err=%v", deliverable, err)
	}
	moved, err = st.CompleteSessionInterfaceTransition(ctx, transition.ID, transition.Phase, notice, completedAt)
	if err != nil || moved {
		t.Fatalf("duplicate completion: moved=%v err=%v", moved, err)
	}
	pending, err = st.ListPendingSessionInterfaceTransitionMessages(ctx, transition.ID)
	if err != nil || len(pending) != 1 || pending[0].Message != notice ||
		pending[0].ClientMessageID != "interface-transition:completion-atomic:context-boundary" ||
		pending[0].AuthoredByUser || !pending[0].DeliveredAt.IsZero() {
		t.Fatalf("reopened outbox: messages=%+v err=%v", pending, err)
	}
}
