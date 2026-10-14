package session

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

func TestAdmissionWriteDoesNotDowngradeTerminalSwitch(t *testing.T) {
	for _, state := range []domain.AgentSwitchState{domain.AgentSwitchCompleted, domain.AgentSwitchFailed} {
		t.Run(string(state), func(t *testing.T) {
			db := sqlitetest.MustOpen(t)
			ctx := context.Background()
			now := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
			if err := db.UpsertProject(ctx, domain.ProjectRecord{ID: "mer", Path: "/tmp/mer", RegisteredAt: now}); err != nil {
				t.Fatal(err)
			}
			created, err := db.CreateSession(ctx, domain.SessionRecord{
				ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessCodex, Mode: domain.SessionModeChat,
				Activity:  domain.Activity{State: domain.ActivityActive, LastActivityAt: now},
				Metadata:  domain.SessionMetadata{Branch: "feat/x", WorkspacePath: "/ws"},
				CreatedAt: now, UpdatedAt: now,
			})
			if err != nil {
				t.Fatal(err)
			}
			conversation, err := db.CreateConversation(ctx, "conv-1", domain.ConversationScopeSession, "mer", created.ID, now)
			if err != nil {
				t.Fatal(err)
			}

			var svc *Service
			fc := &fakeCommander{switchRecord: domain.AgentSwitch{
				ID: "switch-1", SessionID: created.ID, State: domain.AgentSwitchPreparingHandoff,
				FromHarness: domain.HarnessCodex, TargetHarness: domain.HarnessClaudeCode,
			}}
			svc = NewWithDeps(Deps{Manager: fc, Store: db, Clock: func() time.Time { return now }})
			fc.beforeSwitchReturn = func(ctx context.Context, id domain.SessionID) {
				settled := fc.switchRecord
				settled.SessionID = id
				settled.State = state
				if state == domain.AgentSwitchFailed {
					settled.ErrorCode = domain.AgentSwitchErrorTargetBinaryMissing
				}
				svc.observeSettledAgentSwitch(ctx, settled)
			}
			if _, err := svc.SwitchAgent(ctx, created.ID, SwitchAgentInput{TargetHarness: domain.HarnessClaudeCode}); err != nil {
				t.Fatal(err)
			}
			snapshot, err := db.LoadConversationSnapshot(ctx, conversation.ID)
			if err != nil {
				t.Fatal(err)
			}
			want := domain.ActivityStatusCompleted
			if state == domain.AgentSwitchFailed {
				want = domain.ActivityStatusFailed
			}
			if len(snapshot.Activities) != 1 || snapshot.Activities[0].Status != want {
				t.Fatalf("activity = %+v, want one %s row", snapshot.Activities, want)
			}
		})
	}
}

func TestAdmissionWriteAfterStatusReadDoesNotDowngrade(t *testing.T) {
	for _, state := range []domain.AgentSwitchState{domain.AgentSwitchCompleted, domain.AgentSwitchFailed} {
		t.Run(string(state), func(t *testing.T) {
			db := sqlitetest.MustOpen(t)
			ctx := context.Background()
			now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
			if err := db.UpsertProject(ctx, domain.ProjectRecord{ID: "mer", Path: "/tmp/mer", RegisteredAt: now}); err != nil {
				t.Fatal(err)
			}
			created, err := db.CreateSession(ctx, domain.SessionRecord{
				ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessCodex, Mode: domain.SessionModeChat,
				Activity:  domain.Activity{State: domain.ActivityActive, LastActivityAt: now},
				Metadata:  domain.SessionMetadata{Branch: "feat/x", WorkspacePath: "/ws"},
				CreatedAt: now, UpdatedAt: now,
			})
			if err != nil {
				t.Fatal(err)
			}
			conversation, err := db.CreateConversation(ctx, "conv-1", domain.ConversationScopeSession, "mer", created.ID, now)
			if err != nil {
				t.Fatal(err)
			}
			fc := &fakeCommander{switchRecord: domain.AgentSwitch{
				ID: "switch-1", SessionID: created.ID, State: domain.AgentSwitchPreparingHandoff,
				FromHarness: domain.HarnessCodex, TargetHarness: domain.HarnessClaudeCode,
			}}
			svc := NewWithDeps(Deps{Manager: fc, Store: db, Clock: func() time.Time { return now }})
			svc.afterActionStatusCheck = func() {
				settled := fc.switchRecord
				settled.SessionID = created.ID
				settled.State = state
				if state == domain.AgentSwitchFailed {
					settled.ErrorCode = domain.AgentSwitchErrorTargetBinaryMissing
				}
				svc.observeSettledAgentSwitch(ctx, settled)
			}
			if _, err := svc.SwitchAgent(ctx, created.ID, SwitchAgentInput{TargetHarness: domain.HarnessClaudeCode}); err != nil {
				t.Fatal(err)
			}
			snapshot, err := db.LoadConversationSnapshot(ctx, conversation.ID)
			if err != nil {
				t.Fatal(err)
			}
			want := domain.ActivityStatusCompleted
			if state == domain.AgentSwitchFailed {
				want = domain.ActivityStatusFailed
			}
			if len(snapshot.Activities) != 1 || snapshot.Activities[0].Status != want {
				t.Fatalf("activity = %+v, want one %s row", snapshot.Activities, want)
			}
		})
	}
}

func TestRenameOperationIDsSurviveDaemonRestart(t *testing.T) {
	dir := t.TempDir()
	db, err := sqlitetest.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	now := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	sessionID := seedRenameSession(t, db, now)
	first := NewWithDeps(Deps{Store: db, Clock: func() time.Time { return now }})
	if err := first.Rename(ctx, sessionID, "Ship"); err != nil {
		t.Fatal(err)
	}
	if err := first.Rename(ctx, sessionID, "Reviewer"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := sqlite.OpenPreMigrated(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	second := NewWithDeps(Deps{Store: reopened, Clock: func() time.Time { return now.Add(time.Minute) }})
	if err := second.Rename(ctx, sessionID, "Ship"); err != nil {
		t.Fatal(err)
	}
	if err := second.Rename(ctx, sessionID, "Ship"); err != nil {
		t.Fatal(err)
	}
	conversation, err := reopened.ConversationForSession(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := reopened.LoadConversationSnapshot(ctx, conversation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Activities) != 3 {
		t.Fatalf("activities = %d, want 3 after restart: %+v", len(snapshot.Activities), snapshot.Activities)
	}
}

func seedRenameSession(t *testing.T, db *sqlite.Store, now time.Time) domain.SessionID {
	t.Helper()
	ctx := context.Background()
	if err := db.UpsertProject(ctx, domain.ProjectRecord{ID: "mer", Path: "/tmp/mer", RegisteredAt: now}); err != nil {
		t.Fatal(err)
	}
	created, err := db.CreateSession(ctx, domain.SessionRecord{
		ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessCodex, Mode: domain.SessionModeChat,
		DisplayName: "Reviewer",
		Activity:    domain.Activity{State: domain.ActivityActive, LastActivityAt: now},
		Metadata:    domain.SessionMetadata{Branch: "feat/x", WorkspacePath: "/ws"},
		CreatedAt:   now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateConversation(ctx, "conv-rename", domain.ConversationScopeSession, "mer", created.ID, now); err != nil {
		t.Fatal(err)
	}
	return created.ID
}
