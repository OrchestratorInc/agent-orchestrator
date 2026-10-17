package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/lifecycle"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

type lazyChatDriver struct {
	integrationChatDriver
	resumes       atomic.Int64
	entered       chan struct{}
	release       chan struct{}
	err           error
	mu            sync.Mutex
	configs       []ports.ChatResumeConfig
	conversations []*integrationChatConversation
}

type chatRestoreWaitContext struct {
	context.Context
	waiting chan struct{}
	once    sync.Once
}

func (ctx *chatRestoreWaitContext) Done() <-chan struct{} {
	ctx.once.Do(func() { close(ctx.waiting) })
	return ctx.Context.Done()
}

// The fake represents a surviving host for startup continuity checks.
func (d *lazyChatDriver) Reconnect(ctx context.Context, cfg ports.ChatResumeConfig) (ports.ChatConversation, error) {
	cfg.ReconnectOnly = true
	return d.Resume(ctx, cfg)
}

func (d *lazyChatDriver) Resume(ctx context.Context, cfg ports.ChatResumeConfig) (ports.ChatConversation, error) {
	d.resumes.Add(1)
	d.mu.Lock()
	d.configs = append(d.configs, cfg)
	d.mu.Unlock()
	if d.entered != nil {
		d.entered <- struct{}{}
	}
	if d.release != nil {
		select {
		case <-d.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if d.err != nil {
		return nil, d.err
	}
	conversation := newIntegrationChatConversation(cfg.ProviderConversationID)
	d.mu.Lock()
	d.conversations = append(d.conversations, conversation)
	d.mu.Unlock()
	return conversation, nil
}

func lazyChatFixture(t *testing.T) (*Manager, *chatsvc.Service, *sqlite.Store, *lazyChatDriver, domain.SessionRecord) {
	t.Helper()
	ctx := context.Background()
	dataDir := t.TempDir()
	st := sqlitetest.MustOpenAt(t, dataDir)
	if err := st.UpsertProject(ctx, domain.ProjectRecord{
		ID: string(chatTestProject), Path: dataDir, Config: testRoleAgents(), RegisteredAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	rec, err := st.CreateSession(ctx, domain.SessionRecord{
		ProjectID: chatTestProject, Kind: domain.KindWorker, Harness: domain.HarnessCodex, Mode: domain.SessionModeChat,
		Activity: domain.Activity{State: domain.ActivityIdle}, CreatedAt: time.Now(), UpdatedAt: time.Now(),
		Metadata: domain.SessionMetadata{WorkspacePath: dataDir, Branch: "ao/lazy", ProviderConversationID: "saved-thread", ControllerGeneration: "old-generation"},
	})
	if err != nil {
		t.Fatal(err)
	}
	rec, _, err = st.GetSession(ctx, rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	driver := &lazyChatDriver{integrationChatDriver: integrationChatDriver{harness: domain.HarnessCodex}}
	var nextID atomic.Int64
	lcm := lifecycle.New(st, nil)
	svc := chatsvc.New(chatsvc.Options{
		Store: st, Sessions: st, Activity: lcm,
		Drivers: integrationChatRegistry{domain.HarnessCodex: driver}, Log: slog.New(slog.DiscardHandler),
		NewID: func() string { return fmt.Sprintf("lazy-%d", nextID.Add(1)) },
		Reader: chatsvc.SnapshotReaderFunc(func(ctx context.Context, id string) (chatsvc.ConversationRows, error) {
			rows, err := st.LoadConversationSnapshot(ctx, id)
			return chatsvc.ConversationRows{Conversation: rows.Conversation, Turns: rows.Turns, Messages: rows.Messages, Activities: rows.Activities}, err
		}),
	})
	t.Cleanup(func() { svc.StopAll(context.Background()) })
	manager := New(Deps{
		Runtime: &fakeRuntime{}, Agents: fakeAgents{}, Workspace: &fakeWorkspace{path: dataDir}, Store: st,
		Messenger: &fakeMessenger{}, Chat: integrationChatLauncher{service: svc}, Lifecycle: lcm, DataDir: dataDir,
		LookPath: func(string) (string, error) { return "/bin/true", nil }, Logger: slog.New(slog.DiscardHandler),
	})
	svc.SetControllerRestorer(manager.EnsureChatController)
	return manager, svc, st, driver, rec
}

func TestStartupIdleChatSessionsStayCold(t *testing.T) {
	m, svc, st, driver, first := lazyChatFixture(t)
	ctx := context.Background()
	conversation, err := st.CreateConversation(ctx, "saved-conversation", domain.ConversationScopeSession, first.ProjectID, first.ID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AppendUserMessage(ctx, conversation.ID, first.ID, first.Metadata.ControllerGeneration,
		domain.ConversationMessage{ID: "saved-message", Text: "saved transcript", Origin: domain.MessageOriginHuman}, "saved-turn", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := st.SettleTurnByID(ctx, "saved-turn", domain.TurnStateCompleted, "", time.Now()); err != nil {
		t.Fatal(err)
	}
	for range 24 {
		rec := first
		rec.ID = ""
		if _, err := st.CreateSession(ctx, rec); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	recs, err := st.ListAllSessions(ctx)
	if err != nil || len(recs) != 25 {
		t.Fatalf("sessions = %d, err=%v", len(recs), err)
	}
	for _, rec := range recs {
		if svc.HasLiveChatController(rec.ID) || m.SessionMutationInProgress(rec.ID) || m.SessionStatusReadiness(rec) != "ready" {
			t.Fatalf("idle session warmed or fenced: %s", rec.ID)
		}
		if rec.Metadata.ProviderConversationID != first.Metadata.ProviderConversationID || rec.Metadata.WorkspacePath != first.Metadata.WorkspacePath || rec.IsTerminated || rec.Activity.State != domain.ActivityIdle {
			t.Fatalf("cold session lost durable facts: %+v", rec)
		}
		snapshot, err := svc.Snapshot(ctx, rec.ID)
		if err != nil || snapshot.Controller != ports.ChatControllerCold {
			t.Fatalf("cold snapshot = %s, err=%v", snapshot.Controller, err)
		}
		if rec.ID == first.ID && (len(snapshot.Messages) != 1 || snapshot.Messages[0].Text != "saved transcript" || snapshot.Turns[0].State != domain.TurnStateCompleted) {
			t.Fatalf("cold history lost completed work: %+v", snapshot)
		}
	}
	if driver.resumes.Load() != 0 || len(m.workspace.(*fakeWorkspace).restoreConfigs) != 0 {
		t.Fatal("idle startup performed provider or workspace I/O")
	}
}

func TestStartupColdStatePreservesHibernationIntent(t *testing.T) {
	m, svc, st, driver, rec := lazyChatFixture(t)
	ctx := context.Background()
	at := time.Now()
	changed, err := st.SetSessionHibernated(ctx, rec.ID, rec.Revision, &at)
	if err != nil || !changed {
		t.Fatalf("set hibernation intent: changed=%v err=%v", changed, err)
	}
	if err := m.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	for _, read := range []func() (chatsvc.Snapshot, error){
		func() (chatsvc.Snapshot, error) { return svc.Snapshot(ctx, rec.ID) },
		func() (chatsvc.Snapshot, error) { return svc.SnapshotPage(ctx, rec.ID, 0, 50) },
	} {
		snapshot, err := read()
		if err != nil || snapshot.Controller != ports.ChatControllerHibernated {
			t.Fatalf("hibernated snapshot=%s err=%v", snapshot.Controller, err)
		}
	}
	current, found, err := st.GetSession(ctx, rec.ID)
	if err != nil || !found || current.HibernatedAt == nil || driver.resumes.Load() != 0 {
		t.Fatalf("startup consumed hibernation intent: session=%+v resumes=%d err=%v", current, driver.resumes.Load(), err)
	}
}

func TestStartupChatRestoresContinuityOnly(t *testing.T) {
	for _, state := range []domain.ActivityState{domain.ActivityActive, domain.ActivityBlocked, domain.ActivityWaitingInput, domain.ActivityExited} {
		t.Run(string(state), func(t *testing.T) {
			m, svc, st, driver, rec := lazyChatFixture(t)
			rec.Activity.State = state
			if err := st.UpdateSession(context.Background(), rec); err != nil {
				t.Fatal(err)
			}
			if err := m.Reconcile(context.Background()); err != nil {
				t.Fatal(err)
			}
			want := state == domain.ActivityActive || state == domain.ActivityBlocked
			if svc.HasLiveChatController(rec.ID) != want || (driver.resumes.Load() == 1) != want {
				t.Fatalf("state=%s resumes=%d live=%v", state, driver.resumes.Load(), svc.HasLiveChatController(rec.ID))
			}
			if want && !driver.configs[0].ReconnectOnly {
				t.Fatal("startup continuity launched a replacement instead of reconnecting")
			}
		})
	}
}

func TestStartupChatPreservesAwakeOrchestrator(t *testing.T) {
	m, svc, st, driver, rec := lazyChatFixture(t)
	rec.Kind = domain.KindOrchestrator
	ctx := context.Background()
	if err := st.UpdateSession(ctx, rec); err != nil {
		t.Fatal(err)
	}
	if err := m.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if !svc.HasLiveChatController(rec.ID) || driver.resumes.Load() != 1 {
		t.Fatalf("orchestrator lost continuity: resumes=%d live=%v", driver.resumes.Load(), svc.HasLiveChatController(rec.ID))
	}
}

func TestIdleChatWithDurableWorkRestoresAtStartup(t *testing.T) {
	for _, work := range []string{"queued", "running", "approval", "user_input"} {
		t.Run(work, func(t *testing.T) {
			m, svc, st, driver, rec := lazyChatFixture(t)
			ctx := context.Background()
			conversation, err := st.CreateConversation(ctx, "pending-conversation", domain.ConversationScopeSession, rec.ProjectID, rec.ID, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if work == "queued" || work == "running" {
				if _, err := st.AppendUserMessage(ctx, conversation.ID, rec.ID, rec.Metadata.ControllerGeneration, domain.ConversationMessage{ID: "pending-message", Text: "accepted work", Origin: domain.MessageOriginHuman}, "pending-turn", time.Now()); err != nil {
					t.Fatal(err)
				}
				if work == "running" {
					if err := st.MarkTurnDispatching(ctx, "pending-turn"); err != nil {
						t.Fatal(err)
					}
				}
			} else {
				if err := st.UpsertActivity(ctx, conversation.ID, "", domain.ConversationActivity{
					ID: "pending-request", Kind: domain.ActivityKind(work), Status: domain.ActivityStatusPending, RequestID: "request-1",
				}, time.Now()); err != nil {
					t.Fatal(err)
				}
			}
			if err := m.Reconcile(ctx); err != nil {
				t.Fatal(err)
			}
			if driver.resumes.Load() != 1 || !svc.HasLiveChatController(rec.ID) {
				t.Fatalf("durable %s was left cold", work)
			}
		})
	}
}

func TestColdChatRemainsReadyWhileActiveChatRecoveryWaits(t *testing.T) {
	m, _, st, driver, cold := lazyChatFixture(t)
	ctx := context.Background()
	hot := cold
	hot.ID = ""
	hot.Activity.State = domain.ActivityActive
	if _, err := st.CreateSession(ctx, hot); err != nil {
		t.Fatal(err)
	}
	driver.entered = make(chan struct{}, 1)
	driver.release = make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- m.Reconcile(ctx) }()
	select {
	case <-driver.entered:
	case <-time.After(5 * time.Second):
		close(driver.release)
		t.Fatal("active recovery did not start")
	}
	ready := m.SessionStatusReadiness(cold)
	fenced := m.SessionMutationInProgress(cold.ID)
	close(driver.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if ready != "ready" || fenced {
		t.Fatalf("cold session waiting on active restore: readiness=%s fenced=%v", ready, fenced)
	}
}

func TestLazyChatConcurrentHumanAndAutomationSend(t *testing.T) {
	m, svc, st, driver, rec := lazyChatFixture(t)
	driver.entered = make(chan struct{}, 1)
	driver.release = make(chan struct{})
	ctx := context.Background()
	results := make(chan error, 9)
	go func() {
		_, err := svc.Send(ctx, rec.ID, ports.ChatUserMessage{Text: "human input", ClientMessageID: "human-1", Origin: domain.MessageOriginHuman})
		results <- err
	}()
	select {
	case <-driver.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("first send did not resume")
	}
	// Duplicate human retries and automation all meet the same blocked resume.
	for range 4 {
		go func() {
			_, err := svc.Send(ctx, rec.ID, ports.ChatUserMessage{Text: "human input", ClientMessageID: "human-1", Origin: domain.MessageOriginHuman})
			results <- err
		}()
		go func() { results <- m.SendSemantic(ctx, rec.ID, "automation input", "automation-1") }()
	}
	close(driver.release)
	for range 9 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if driver.resumes.Load() != 1 || driver.configs[0].ProviderConversationID != rec.Metadata.ProviderConversationID {
		t.Fatalf("resumes=%d configs=%+v", driver.resumes.Load(), driver.configs)
	}
	conversation, err := st.ConversationForSession(ctx, rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := st.LoadConversationSnapshot(ctx, conversation.ID)
	if err != nil || len(rows.Messages) != 2 || len(rows.Turns) != 2 || rows.Turns[0].State != domain.TurnStateRunning || rows.Turns[1].State != domain.TurnStateQueued {
		t.Fatalf("duplicate or reordered delivery: turns=%+v messages=%+v err=%v", rows.Turns, rows.Messages, err)
	}
	if rows.Messages[0].ClientMessageID != "human-1" || rows.Messages[1].ClientMessageID != "automation-1" {
		t.Fatalf("idempotency keys = %+v", rows.Messages)
	}
	provider := driver.conversations[0]
	provider.mu.Lock()
	if len(provider.sent) != 1 || provider.sent[0].Text != "human input" {
		t.Errorf("first provider delivery = %+v", provider.sent)
	}
	provider.mu.Unlock()
	provider.events <- ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "provider-turn-1", TurnState: domain.TurnStateCompleted}
	deadline := time.Now().Add(5 * time.Second)
	for {
		provider.mu.Lock()
		delivered := len(provider.sent) == 2
		provider.mu.Unlock()
		if delivered {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("accepted automation input did not drain")
		}
		time.Sleep(time.Millisecond)
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if provider.sent[1].Text != "automation input" || provider.sent[1].ClientMessageID != "automation-1" {
		t.Fatalf("second provider delivery = %+v", provider.sent)
	}
}

func TestColdChatSteerOrSendPreservesAutomationSender(t *testing.T) {
	_, svc, st, driver, rec := lazyChatFixture(t)
	ctx := context.Background()
	sender, err := st.CreateSession(ctx, domain.SessionRecord{
		ProjectID: rec.ProjectID, Kind: domain.KindWorker, Harness: domain.HarnessCodex,
		Mode: domain.SessionModeTUI, DisplayName: "Source worker",
		Activity: domain.Activity{State: domain.ActivityIdle}, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := svc.SteerOrSend(ctx, rec.ID, ports.ChatUserMessage{
		Text: "guidance", ClientMessageID: "cold-steer-1", Origin: domain.MessageOriginHuman,
		SenderSessionID: string(sender.ID),
	}, false)
	if err != nil || result.Steered || driver.resumes.Load() != 1 {
		t.Fatalf("cold steer-or-send = %+v, resumes=%d, err=%v", result, driver.resumes.Load(), err)
	}
	conversation, err := st.ConversationForSession(ctx, rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := st.LoadConversationSnapshot(ctx, conversation.ID)
	if err != nil || len(rows.Messages) != 1 {
		t.Fatalf("cold delivery messages=%+v, err=%v", rows.Messages, err)
	}
	message := rows.Messages[0]
	if message.Origin != domain.MessageOriginAutomation || message.SenderSessionID != string(sender.ID) ||
		message.SenderProjectID != string(sender.ProjectID) || message.SenderDisplayName != sender.DisplayName {
		t.Fatalf("cold delivery lost automation attribution: %+v", message)
	}
}

func TestLazyChatRestoreWaiterCancellationDoesNotDuplicateResume(t *testing.T) {
	m, _, _, driver, rec := lazyChatFixture(t)
	driver.entered = make(chan struct{}, 1)
	driver.release = make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- m.EnsureChatController(context.Background(), rec.ID) }()
	select {
	case <-driver.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("first restore did not start")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	waiterCtx := &chatRestoreWaitContext{Context: ctx, waiting: make(chan struct{})}
	waiterDone := make(chan error, 1)
	go func() { waiterDone <- m.EnsureChatController(waiterCtx, rec.ID) }()
	select {
	case <-waiterCtx.waiting:
	case <-time.After(5 * time.Second):
		t.Fatal("restore waiter did not join the shared attempt")
	}
	cancel()
	if err := <-waiterDone; !errors.Is(err, context.Canceled) {
		t.Errorf("resume waiter error = %v", err)
	}
	close(driver.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if driver.resumes.Load() != 1 || m.SessionMutationInProgress(rec.ID) {
		t.Fatalf("waiter cancelled owner or duplicated resume: resumes=%d", driver.resumes.Load())
	}
}

func TestLazyChatInputWaiterGetsRetryableExplicitResumeFailure(t *testing.T) {
	m, _, _, driver, rec := lazyChatFixture(t)
	driver.entered = make(chan struct{}, 1)
	driver.release = make(chan struct{})
	providerErr := errors.New("provider temporarily unavailable")
	driver.err = providerErr
	ownerDone := make(chan error, 1)
	go func() {
		_, err := m.ResumeAgentWithMode(context.Background(), rec.ID)
		ownerDone <- err
	}()
	select {
	case <-driver.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("explicit resume did not start")
	}
	waiterCtx := &chatRestoreWaitContext{Context: context.Background(), waiting: make(chan struct{})}
	waiterDone := make(chan error, 1)
	go func() { waiterDone <- m.EnsureChatController(waiterCtx, rec.ID) }()
	select {
	case <-waiterCtx.waiting:
	case <-time.After(5 * time.Second):
		t.Fatal("input waiter did not join explicit resume")
	}
	close(driver.release)
	if err := <-ownerDone; !errors.Is(err, providerErr) || errors.Is(err, ports.ErrChatControllerRestore) {
		t.Errorf("explicit resume error changed: %v", err)
	}
	if err := <-waiterDone; !errors.Is(err, providerErr) || !errors.Is(err, ports.ErrChatControllerRestore) {
		t.Errorf("input restore error is not retryable: %v", err)
	}
	if driver.resumes.Load() != 1 {
		t.Fatalf("shared failure resumed provider %d times", driver.resumes.Load())
	}
}

func TestLazyChatResumeFailurePreservesIdentityAndCanRetry(t *testing.T) {
	m, svc, st, driver, rec := lazyChatFixture(t)
	driver.err = errors.New("provider temporarily unavailable")
	ctx := context.Background()
	_, err := svc.Send(ctx, rec.ID, ports.ChatUserMessage{Text: "continue", ClientMessageID: "retry-1"})
	if !errors.Is(err, ports.ErrChatControllerRestore) {
		t.Fatalf("send error = %v", err)
	}
	current, _, err := st.GetSession(ctx, rec.ID)
	if err != nil || current.Metadata.ProviderConversationID != rec.Metadata.ProviderConversationID || current.Metadata.WorkspacePath != rec.Metadata.WorkspacePath || current.IsTerminated || current.Activity != rec.Activity {
		t.Fatalf("failed restore changed durable identity: %+v err=%v", current, err)
	}
	if m.SessionMutationInProgress(rec.ID) {
		t.Fatal("failed restore retained operation gate")
	}
	driver.err = nil
	if _, err := svc.Send(ctx, rec.ID, ports.ChatUserMessage{Text: "continue", ClientMessageID: "retry-1"}); err != nil {
		t.Fatal(err)
	}
	if driver.resumes.Load() != 2 {
		t.Fatalf("retry resumes = %d", driver.resumes.Load())
	}
}

func TestLazyChatSuccessfulRestoreWithoutControllerIsRetryable(t *testing.T) {
	_, svc, _, _, rec := lazyChatFixture(t)
	var attempts atomic.Int64
	svc.SetControllerRestorer(func(context.Context, domain.SessionID) error {
		attempts.Add(1)
		return nil // The provider stopped before delivery could be admitted.
	})
	_, err := svc.Send(context.Background(), rec.ID, ports.ChatUserMessage{Text: "continue", ClientMessageID: "stopped-restore"})
	if !errors.Is(err, ports.ErrChatControllerRestore) || attempts.Load() != 1 {
		t.Fatalf("stopped restoration must be retryable once: attempts=%d err=%v", attempts.Load(), err)
	}
}

func TestKillColdChatPreventsLazyRecreation(t *testing.T) {
	m, svc, _, driver, rec := lazyChatFixture(t)
	ctx := context.Background()
	if err := m.beginAgentOperation(ctx, rec.ID, agentOperationKill); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Send(ctx, rec.ID, ports.ChatUserMessage{Text: "racing kill"}); !errors.Is(err, ports.ErrChatControllerRestore) {
		t.Fatalf("input bypassed kill gate: %v", err)
	}
	m.endAgentOperation(rec.ID, agentOperationKill)
	if _, err := m.Kill(ctx, rec.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.EnsureChatController(ctx, rec.ID); !errors.Is(err, ErrTerminated) && !errors.Is(err, ports.ErrSessionNotFound) {
		t.Fatalf("terminated session resumed: %v", err)
	}
	if driver.resumes.Load() != 0 {
		t.Fatal("kill allowed a provider to be recreated")
	}
}

func TestLazyChatRestoreAndKillShareOperationGate(t *testing.T) {
	m, _, _, driver, rec := lazyChatFixture(t)
	driver.entered = make(chan struct{}, 1)
	driver.release = make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- m.EnsureChatController(context.Background(), rec.ID) }()
	select {
	case <-driver.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("lazy restore did not start")
	}
	_, killErr := m.Kill(context.Background(), rec.ID)
	close(driver.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !errors.Is(killErr, ErrSwitchInProgress) {
		t.Fatalf("kill bypassed in-flight restore gate: %v", killErr)
	}
	if _, err := m.Kill(context.Background(), rec.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.EnsureChatController(context.Background(), rec.ID); !errors.Is(err, ErrTerminated) && !errors.Is(err, ports.ErrSessionNotFound) {
		t.Fatalf("lazy restore reopened killed controller: %v", err)
	}
	if driver.resumes.Load() != 1 {
		t.Fatalf("provider resumes after kill = %d", driver.resumes.Load())
	}
}
