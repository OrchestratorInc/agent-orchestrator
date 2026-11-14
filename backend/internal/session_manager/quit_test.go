package sessionmanager

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestStopAgentsForQuitPreservesResumableSession(t *testing.T) {
	for _, state := range []domain.ActivityState{domain.ActivityIdle, domain.ActivityExited} {
		t.Run(string(state), func(t *testing.T) {
			manager, store, runtime, workspace := newManager()
			store.sessions["mer-1"] = domain.SessionRecord{
				ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessCodex,
				Activity: domain.Activity{State: state},
				Metadata: domain.SessionMetadata{
					WorkspacePath: "/ws/mer-1", Branch: "ao/mer-1", RuntimeHandleID: "tmux-mer-1",
					RuntimeLaunchID: "launch-current", AgentSessionID: "native-thread-1",
				},
			}
			runtime.aliveByHandle = map[string]bool{"tmux-mer-1": true}
			if err := manager.StopAgentsForQuit(context.Background()); err != nil {
				t.Fatal(err)
			}
			got := store.sessions["mer-1"]
			if runtime.destroyed != 1 || got.IsTerminated || got.Activity.State != domain.ActivityExited {
				t.Fatalf("destroyed=%d, session=%+v", runtime.destroyed, got)
			}
			if got.Metadata.WorkspacePath != "/ws/mer-1" || got.Metadata.Branch != "ao/mer-1" ||
				got.Metadata.AgentSessionID != "native-thread-1" || got.Metadata.RuntimeReleasedLaunchID != "launch-current" {
				t.Fatalf("resumable identity changed: %+v", got.Metadata)
			}
			if workspace.destroyed != 0 {
				t.Fatal("desktop quit removed a worktree")
			}
		})
	}
}

type quitStopLauncher struct {
	recordingLauncher
	store     *fakeStore
	manager   *Manager
	settled   []domain.SessionID
	settleErr error
	resumeErr error
}

func (l *quitStopLauncher) SettleAbandonedWork(ctx context.Context, id domain.SessionID) error {
	l.settled = append(l.settled, id)
	if l.resumeErr = l.manager.beginAgentResume(ctx, id); l.resumeErr == nil {
		l.manager.endAgentResume(id)
	}
	return l.settleErr
}

func (l *quitStopLauncher) StopChat(ctx context.Context, id domain.SessionID) error {
	rec := l.store.sessions[id]
	rec.HibernatedAt = nil
	l.store.sessions[id] = rec
	return l.recordingLauncher.StopChat(ctx, id)
}

func TestStopAgentsForQuitLeavesChatsAsleep(t *testing.T) {
	asleepSince := time.Unix(100, 0)
	for _, tc := range []struct {
		id           domain.SessionID
		kind         domain.SessionKind
		state        domain.ActivityState
		hibernatedAt *time.Time
		asleep       bool
	}{
		{"mer-busy", domain.KindWorker, domain.ActivityActive, nil, true},
		{"mer-asleep", domain.KindWorker, domain.ActivityIdle, &asleepSince, true},
		{"mer-exited", domain.KindWorker, domain.ActivityExited, nil, false},
		{"mer-orchestrator", domain.KindOrchestrator, domain.ActivityIdle, nil, false},
	} {
		t.Run(string(tc.id), func(t *testing.T) {
			manager, store, _, _ := newManager()
			launcher := &quitStopLauncher{store: store, manager: manager}
			manager.chat = launcher
			store.sessions[tc.id] = domain.SessionRecord{
				ID: tc.id, ProjectID: "mer", Kind: tc.kind, Harness: domain.HarnessClaudeCode, Mode: domain.SessionModeChat,
				Activity: domain.Activity{State: tc.state}, HibernatedAt: tc.hibernatedAt,
				Metadata: domain.SessionMetadata{
					WorkspacePath: "/ws/" + string(tc.id), Branch: "ao/" + string(tc.id),
					ProviderConversationID: "native-" + string(tc.id), ControllerGeneration: "gen-1",
				},
			}

			if err := manager.StopAgentsForQuit(context.Background()); err != nil {
				t.Fatal(err)
			}
			if len(launcher.stopped) != 1 {
				t.Fatalf("quit must still stop the chat provider: %v", launcher.stopped)
			}
			got := store.sessions[tc.id]
			if tc.asleep {
				if got.HibernatedAt == nil || got.Activity.State != domain.ActivityIdle || got.IsTerminated {
					t.Fatalf("must be asleep so opening it wakes it: %+v", got)
				}
				if len(launcher.settled) != 1 || !errors.Is(launcher.resumeErr, ErrResumeInProgress) {
					t.Fatalf("must settle open work while still holding the session: settled=%v resume=%v", launcher.settled, launcher.resumeErr)
				}
			} else if got.HibernatedAt != nil || got.Activity.State != domain.ActivityExited || got.IsTerminated {
				t.Fatalf("must stay stopped: %+v", got)
			}
		})
	}
}

func TestStopAgentsForQuitKeepsChatStoppedWhenItsOpenWorkCannotSettle(t *testing.T) {
	manager, store, _, _ := newManager()
	launcher := &quitStopLauncher{store: store, manager: manager, settleErr: errors.New("database is locked")}
	manager.chat = launcher
	store.sessions["mer-busy"] = domain.SessionRecord{
		ID: "mer-busy", ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode, Mode: domain.SessionModeChat,
		Activity: domain.Activity{State: domain.ActivityActive},
		Metadata: domain.SessionMetadata{ProviderConversationID: "native-mer-busy", ControllerGeneration: "gen-1"},
	}
	if err := manager.StopAgentsForQuit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := store.sessions["mer-busy"]; got.HibernatedAt != nil || got.Activity.State != domain.ActivityExited {
		t.Fatalf("a chat whose open work could not settle must stay stopped: %+v", got)
	}
}

type resumedAfterListStore struct{ *fakeStore }

func (s resumedAfterListStore) ListAllSessions(ctx context.Context) ([]domain.SessionRecord, error) {
	records, err := s.fakeStore.ListAllSessions(ctx)
	for _, rec := range records {
		resumed := s.sessions[rec.ID]
		resumed.Metadata.ControllerGeneration = "gen-2"
		s.sessions[rec.ID] = resumed
	}
	return records, err
}

func TestStopAgentsForQuitSleepsTheControllerItStopped(t *testing.T) {
	manager, store, _, _ := newManager()
	manager.store = resumedAfterListStore{store}
	manager.chat = &quitStopLauncher{store: store, manager: manager}
	store.sessions["mer-busy"] = domain.SessionRecord{
		ID: "mer-busy", ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode, Mode: domain.SessionModeChat,
		Activity: domain.Activity{State: domain.ActivityActive},
		Metadata: domain.SessionMetadata{ProviderConversationID: "native-mer-busy", ControllerGeneration: "gen-1"},
	}
	if err := manager.StopAgentsForQuit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := store.sessions["mer-busy"]; got.HibernatedAt == nil || got.Activity.State != domain.ActivityIdle {
		t.Fatalf("a chat resumed after the quit listed it must still end asleep and idle: state=%s hibernatedAt=%v", got.Activity.State, got.HibernatedAt)
	}
}

func TestStopAgentsForQuitKeepsChatStoppedWhenTheSleepMarkerCannotBeWritten(t *testing.T) {
	manager, store, _, _ := newManager()
	manager.chat = &quitStopLauncher{store: store, manager: manager}
	store.hibernationCASConflicts = 3
	store.sessions["mer-busy"] = domain.SessionRecord{
		ID: "mer-busy", ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode, Mode: domain.SessionModeChat,
		Activity: domain.Activity{State: domain.ActivityActive},
		Metadata: domain.SessionMetadata{ProviderConversationID: "native-mer-busy", ControllerGeneration: "gen-1"},
	}
	if err := manager.StopAgentsForQuit(context.Background()); err == nil {
		t.Fatal("quit hid a failed sleep marker write")
	}
	if got := store.sessions["mer-busy"]; got.HibernatedAt != nil || got.Activity.State != domain.ActivityExited {
		t.Fatalf("a chat without a sleep marker must stay stopped: state=%s hibernatedAt=%v", got.Activity.State, got.HibernatedAt)
	}
}
