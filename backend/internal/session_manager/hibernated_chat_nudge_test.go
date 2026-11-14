package sessionmanager

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/lifecycle"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	browsersvc "github.com/aoagents/agent-orchestrator/backend/internal/service/browser"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
	"github.com/aoagents/agent-orchestrator/backend/internal/sessionguard"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

type testLogWriter struct{ t *testing.T }

func (w testLogWriter) Write(p []byte) (int, error) {
	w.t.Log(strings.TrimRight(string(p), "\n"))
	return len(p), nil
}

type managerMessenger struct{ m *Manager }

func (s managerMessenger) Send(ctx context.Context, id domain.SessionID, message string) error {
	return s.m.Send(ctx, id, message, nil)
}

// A lifecycle nudge reaches a sleeping Chat session through the session guard,
// which holds the session's input lease for the whole write. The relay has to
// wake the session first, and the resume that wakes it waits for that same
// lease to drain. Routine nudges (failing CI) and urgent ones (a merge
// conflict) share that path.
func TestLifecycleNudgeWakesHibernatedChat(t *testing.T) {
	for _, tc := range []struct {
		name    string
		message string
		nudge   func(*sessionguard.Guard, context.Context, domain.SessionID, string) (sessionguard.Outcome, error)
	}{
		{"ci-failure nudge", "CI is failing on your PR", (*sessionguard.Guard).Nudge},
		{"merge-conflict urgent nudge", "There are merge conflicts on your PR", func(g *sessionguard.Guard, ctx context.Context, id domain.SessionID, msg string) (sessionguard.Outcome, error) {
			return g.NudgeUrgent(ctx, id, msg, nil)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testLifecycleNudgeWakesHibernatedChat(t, tc.message, tc.nudge)
		})
	}
}

func testLifecycleNudgeWakesHibernatedChat(
	t *testing.T,
	message string,
	nudge func(*sessionguard.Guard, context.Context, domain.SessionID, string) (sessionguard.Outcome, error),
) {
	ctx := context.Background()
	dataDir := t.TempDir()
	st := sqlitetest.MustOpenAt(t, dataDir)
	if err := st.UpsertProject(ctx, domain.ProjectRecord{
		ID: string(chatTestProject), Path: dataDir, Config: testRoleAgents(), RegisteredAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(testLogWriter{t}, &slog.HandlerOptions{Level: slog.LevelInfo}))
	var nextID atomic.Int64
	var resumed atomic.Pointer[integrationChatConversation]
	newChat := func() *chatsvc.Service {
		return chatsvc.New(chatsvc.Options{
			Store: st, Sessions: st,
			Drivers: integrationChatRegistry{domain.HarnessCodex: integrationChatDriver{
				harness: domain.HarnessCodex,
				start:   func() ports.ChatConversation { return newIntegrationChatConversation("provider-thread") },
				resume: func() ports.ChatConversation {
					conversation := newIntegrationChatConversation("provider-thread")
					resumed.Store(conversation)
					return conversation
				},
			}},
			Log:   logger,
			NewID: func() string { return fmt.Sprintf("nudge-%d", nextID.Add(1)) },
		})
	}
	newManager := func(chat *chatsvc.Service) *Manager {
		m := New(Deps{
			Runtime: &fakeRuntime{}, Agents: fakeAgents{}, Workspace: &fakeWorkspace{path: t.TempDir()},
			Store: st, Messenger: &fakeMessenger{}, Chat: integrationChatLauncher{service: chat},
			Lifecycle: lifecycle.New(st, nil), DataDir: dataDir,
			LookPath: func(string) (string, error) { return "/bin/true", nil },
			Logger:   logger,
		})
		m.browserCapabilities = browsersvc.NewAuthority()
		return m
	}

	// A worker that finished its turn and went to sleep: no controller, the
	// durable hibernation marker set, the provider conversation kept.
	firstChat := newChat()
	first := newManager(firstChat)
	deferred := deferredBackground(first)
	rec, _, _, err := first.Spawn(ctx, asyncChatSpawnConfig(""))
	if err != nil {
		t.Fatal(err)
	}
	for _, work := range *deferred {
		work()
	}
	firstChat.StopAll(ctx)
	current, _, err := st.GetSession(ctx, rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC()
	if _, err := st.SetSessionHibernated(ctx, rec.ID, current.Revision, &at); err != nil {
		t.Fatal(err)
	}
	asleep, _, err := st.GetSession(ctx, rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if asleep.HibernatedAt == nil || asleep.Activity.State == domain.ActivityExited || asleep.Metadata.ProviderConversationID == "" {
		t.Fatalf("setup did not produce a sleeping chat worker: hibernated=%v activity=%q provider=%q",
			asleep.HibernatedAt, asleep.Activity.State, asleep.Metadata.ProviderConversationID)
	}

	// Chat wakes through Session Manager, as daemon.go wires it. The guard
	// holds Session Manager's input lease, as lifecycle's guard once did: a
	// relay must not deadlock under any caller that holds it.
	chat := newChat()
	t.Cleanup(func() { chat.StopAll(context.Background()) })
	mgr := newManager(chat)
	chat.SetWakeCallback(mgr.WakeHibernatedChat)
	guard := sessionguard.New(st, managerMessenger{mgr}, logger)
	guard.SetInputLease(mgr)

	var took time.Duration
	nudged := make(chan error, 1)
	go func() {
		started := time.Now()
		_, err := nudge(guard, ctx, rec.ID, message)
		took = time.Since(started)
		nudged <- err
	}()
	// The user opens the worker and asks for a status while that wake runs.
	time.Sleep(time.Second)
	asked, err := chat.Send(ctx, rec.ID, ports.ChatUserMessage{Text: "status?", Origin: domain.MessageOriginHuman})
	if err != nil {
		t.Fatal(err)
	}
	nudgeErr := <-nudged
	reply := settledTurn(t, st, rec.ID, asked.ID)
	t.Logf("nudge took=%s err=%v; user turn state=%s error=%q",
		took.Round(time.Millisecond), nudgeErr, reply.State, reply.ErrorMessage)
	if nudgeErr != nil {
		t.Errorf("nudge into a sleeping chat worker failed after %s: %v", took.Round(time.Millisecond), nudgeErr)
	}
	// The input lease blocks every exclusive operation on the session, so the
	// nudge must not hold it through the provider start.
	if took > 10*time.Second {
		t.Errorf("nudge held the session's input lease for %s", took.Round(time.Millisecond))
	}
	if conversation := resumed.Load(); conversation == nil {
		t.Error("the sleeping chat worker was never resumed")
	} else {
		conversation.mu.Lock()
		sent := append([]ports.ChatUserMessage(nil), conversation.sent...)
		conversation.mu.Unlock()
		if len(sent) == 0 || sent[0].Text != message || sent[0].Origin != domain.MessageOriginAutomation {
			t.Errorf("resumed provider received %+v, want the nudge first", sent)
		}
	}
	if reply.State == domain.TurnStateFailed {
		t.Errorf("user message sent during the wake failed: %s", reply.ErrorMessage)
	}
	if !chat.HasLiveChatController(rec.ID) {
		t.Error("the sleeping chat worker did not wake")
	}
}

// settledTurn waits briefly for a queued turn to leave the queue.
func settledTurn(t *testing.T, st *sqlite.Store, id domain.SessionID, turnID string) domain.ConversationTurn {
	t.Helper()
	ctx := context.Background()
	conversation, err := st.ConversationForSession(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		snapshot, err := st.LoadConversationSnapshot(ctx, conversation.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, turn := range snapshot.Turns {
			if turn.ID == turnID && (turn.State != domain.TurnStateQueued || time.Now().After(deadline)) {
				return turn
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("turn %s not found", turnID)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
