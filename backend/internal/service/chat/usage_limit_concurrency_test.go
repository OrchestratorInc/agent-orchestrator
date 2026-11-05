package chat_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

type usageLimitWakeFixture struct {
	h       *harness
	svc     *chatsvc.Service
	source  domain.ConversationTurn
	resumed *fakeConversation
	other   *fakeConversation
	entered chan struct{}
	release func()
}

func pausedUsageLimitWake(t *testing.T) usageLimitWakeFixture {
	t.Helper()
	h, old := settledHibernationHarness(t, "")
	ctx := context.Background()
	source := failUsageLimitedTurn(t, h, timedUsageLimit)
	if err := h.svc.SetTurnAutoResume(ctx, testSession, source.ID, true); err != nil {
		t.Fatal(err)
	}
	if slept, err := h.svc.HibernateChat(ctx, testSession); err != nil || !slept {
		t.Fatalf("hibernate: %v, %v", slept, err)
	}
	resumed, other := newFakeConversation(), newFakeConversation()
	resumed.turnSeq = 1
	selectConversation := func(id domain.SessionID) ports.ChatConversation {
		if id == testSession {
			return resumed
		}
		return other
	}
	svc := chatsvc.New(chatsvc.Options{
		Store: h.st, Reader: fullSnapshotReader(h.st), Sessions: h.st,
		Drivers: fakeRegistry{driver: fakeDriver{conv: resumed,
			start: func(cfg ports.ChatStartConfig) (ports.ChatConversation, error) {
				return selectConversation(cfg.SessionID), nil
			},
			resume: func(cfg ports.ChatResumeConfig) (ports.ChatConversation, error) {
				return selectConversation(cfg.SessionID), nil
			},
		}}, NewID: uuid.NewString, Now: h.now,
	})
	t.Cleanup(func() { svc.StopAll(ctx) })
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	releaseWake := func() { once.Do(func() { close(release) }) }
	t.Cleanup(releaseWake)
	workspace := t.TempDir()
	svc.SetWakeCallback(func(ctx context.Context, id domain.SessionID) error {
		close(entered)
		select {
		case <-release:
		case <-ctx.Done():
			return ctx.Err()
		}
		record, _, err := h.st.GetSession(ctx, id)
		if err != nil {
			return err
		}
		applied, err := h.st.SetSessionHibernated(ctx, id, record.Revision, nil)
		if err != nil {
			return fmt.Errorf("clear hibernation: %w", err)
		}
		if !applied {
			return errors.New("hibernation changed before fixture wake")
		}
		_, err = svc.Start(ctx, chatsvc.StartConfig{SessionID: id, ProjectID: testProject,
			Harness: domain.HarnessCodex, WorkspacePath: workspace, ProviderConversationID: old.ProviderConversationID()})
		return err
	})
	h.advance(time.Minute)
	return usageLimitWakeFixture{h: h, svc: svc, source: source, resumed: resumed, other: other, entered: entered, release: releaseWake}
}

func awaitUsageLimitResult(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("usage-limit operation blocked behind another session")
		return nil
	}
}

func seedUsageLimitOtherSession(t *testing.T, h *harness, reset time.Time) (domain.SessionID, domain.ConversationTurn) {
	t.Helper()
	ctx := context.Background()
	now := h.now()
	record, err := h.st.CreateSession(ctx, domain.SessionRecord{ProjectID: testProject,
		Kind: domain.KindWorker, Harness: domain.HarnessCodex, Mode: domain.SessionModeChat, CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := h.st.CreateConversation(ctx, uuid.NewString(), domain.ConversationScopeSession, testProject, record.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	turnID := uuid.NewString()
	if _, err := h.st.AppendUserMessage(ctx, conversation.ID, record.ID, "other-generation",
		domain.ConversationMessage{ID: uuid.NewString(), Origin: domain.MessageOriginHuman, Text: "other task"}, turnID, now); err != nil {
		t.Fatal(err)
	}
	if err := h.st.BindTurnToProvider(ctx, turnID, "other-provider-turn", now); err != nil {
		t.Fatal(err)
	}
	if err := h.st.SettleTurnByID(ctx, turnID, domain.TurnStateFailed, "Usage limit reached. Resets at "+reset.Format(time.RFC3339), now); err != nil {
		t.Fatal(err)
	}
	turn, err := h.st.TurnByID(ctx, turnID)
	if err != nil {
		t.Fatal(err)
	}
	return record.ID, turn
}

func TestUsageLimitAutoResumeDoesNotBlockConsentDuringWake(t *testing.T) {
	for _, action := range []string{"cancel source", "cancel other", "arm other"} {
		t.Run(action, func(t *testing.T) {
			f := pausedUsageLimitWake(t)
			ctx := context.Background()
			id, source, enabled := testSession, f.source, false
			if action != "cancel source" {
				id, source = seedUsageLimitOtherSession(t, f.h, f.h.now().Add(2*time.Hour))
				enabled = action == "arm other"
				if !enabled {
					if err := f.svc.SetTurnAutoResume(ctx, id, source.ID, true); err != nil {
						t.Fatal(err)
					}
				}
			}
			swept := make(chan error, 1)
			go func() { swept <- f.svc.ResumeUsageLimitedTurns(ctx) }()
			joined := false
			defer func() {
				f.release()
				if !joined {
					_ = awaitUsageLimitResult(t, swept)
				}
			}()
			select {
			case <-f.entered:
			case err := <-swept:
				joined = true
				t.Fatalf("sweep ended before wake: %v", err)
			case <-time.After(5 * time.Second):
				t.Fatal("automatic retry did not wake its provider")
			}
			changed := make(chan error, 1)
			go func() { changed <- f.svc.SetTurnAutoResume(ctx, id, source.ID, enabled) }()
			if err := awaitUsageLimitResult(t, changed); err != nil {
				t.Fatal(err)
			}
			stored, err := f.h.st.TurnByID(ctx, source.ID)
			if err != nil || stored.UsageLimit.AutoResume != enabled {
				t.Fatalf("consent while wake paused = %+v, %v", stored.UsageLimit, err)
			}
			f.release()
			if err := awaitUsageLimitResult(t, swept); err != nil {
				joined = true
				t.Fatal(err)
			}
			joined = true
			if action == "cancel source" && len(f.resumed.sentMessages()) != 0 {
				t.Fatal("cancelled consent dispatched after wake")
			}
		})
	}
}

func TestUsageLimitAutoResumeFindsNewDeadlineWhileAnotherWakeRuns(t *testing.T) {
	f := pausedUsageLimitWake(t)
	ctx := context.Background()
	record, err := f.h.st.CreateSession(ctx, domain.SessionRecord{ProjectID: testProject,
		Kind: domain.KindWorker, Harness: domain.HarnessCodex, Mode: domain.SessionModeChat, CreatedAt: f.h.now(), UpdatedAt: f.h.now()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Start(ctx, chatsvc.StartConfig{SessionID: record.ID, ProjectID: testProject,
		Harness: domain.HarnessCodex, WorkspacePath: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	turn, err := f.svc.Send(ctx, record.ID, ports.ChatUserMessage{Text: "independent task", Origin: domain.MessageOriginHuman})
	if err != nil {
		t.Fatal(err)
	}
	f.other.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: turn.ProviderTurnID,
		TurnState: domain.TurnStateFailed, Err: fmt.Errorf("Usage limit reached. Resets at %s", f.h.now().Add(time.Second).Format(time.RFC3339))})
	deadline := time.Now().Add(5 * time.Second)
	for {
		stored, err := f.h.st.TurnByID(ctx, turn.ID)
		if err != nil {
			t.Fatal(err)
		}
		if stored.State == domain.TurnStateFailed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("other provider failure was not projected")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := f.svc.SetTurnAutoResume(ctx, record.ID, turn.ID, true); err != nil {
		t.Fatal(err)
	}
	swept := make(chan error, 1)
	go func() { swept <- f.svc.ResumeUsageLimitedTurns(ctx) }()
	defer func() { f.release(); _ = awaitUsageLimitResult(t, swept) }()
	select {
	case <-f.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("first provider did not wake")
	}
	f.h.advance(2 * time.Second)
	for range 3 {
		otherSweep := make(chan error, 1)
		go func() { otherSweep <- f.svc.ResumeUsageLimitedTurns(ctx) }()
		if err := awaitUsageLimitResult(t, otherSweep); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.other.sentMessages()) != 2 || len(f.resumed.sentMessages()) != 0 {
		t.Fatalf("other sends=%d paused sends=%d", len(f.other.sentMessages()), len(f.resumed.sentMessages()))
	}
}

func TestUsageLimitAutoResumeBoundsOverlappingProviderProbes(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	for range 5 {
		id, source := seedUsageLimitOtherSession(t, h, h.now().Add(-time.Second))
		if err := h.svc.SetTurnAutoResume(ctx, id, source.ID, true); err != nil {
			t.Fatal(err)
		}
	}
	entered, release := make(chan struct{}, 5), make(chan struct{})
	var once sync.Once
	releaseProbes := func() { once.Do(func() { close(release) }) }
	defer releaseProbes()
	var calls atomic.Int32
	h.svc.SetUsageLimitAccounts(usageLimitAccountReaderFunc(func(ctx context.Context, _ domain.SessionID, _ domain.AgentHarness, _ bool) (string, domain.AgentAccountLimits, error) {
		calls.Add(1)
		entered <- struct{}{}
		select {
		case <-release:
			return "account", domain.AgentAccountLimits{}, nil
		case <-ctx.Done():
			return "", domain.AgentAccountLimits{}, ctx.Err()
		}
	}))
	swept := make(chan error, 1)
	go func() { swept <- h.svc.ResumeUsageLimitedTurns(ctx) }()
	defer func() { releaseProbes(); _ = awaitUsageLimitResult(t, swept) }()
	for range 4 {
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("bounded provider workers did not start")
		}
	}
	for range 5 {
		if err := h.svc.ResumeUsageLimitedTurns(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 4 {
		t.Fatalf("concurrent provider probes=%d, want 4", calls.Load())
	}
}

func TestUsageLimitAutoResumeShutdownDuringWakePreservesConsent(t *testing.T) {
	f := pausedUsageLimitWake(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	swept := make(chan error, 1)
	go func() { swept <- f.svc.ResumeUsageLimitedTurns(ctx) }()
	select {
	case <-f.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("automatic retry did not wake its provider")
	}
	cancel()
	f.release()
	if err := awaitUsageLimitResult(t, swept); !errors.Is(err, context.Canceled) {
		t.Fatalf("shutdown sweep=%v", err)
	}
	stored, err := f.h.st.TurnByID(context.Background(), f.source.ID)
	if err != nil || !stored.UsageLimit.AutoResume || len(f.resumed.sentMessages()) != 0 {
		t.Fatalf("shutdown consent=%+v sends=%d err=%v", stored.UsageLimit, len(f.resumed.sentMessages()), err)
	}
}

type usageLimitAppendHookStore struct {
	*sqlite.Store
	beforeAppend func()
}

func (s *usageLimitAppendHookStore) AppendAutoResumeRetryUserMessage(ctx context.Context, conversation string, session domain.SessionID, generation string, msg domain.ConversationMessage, turnID, sourceID string, now time.Time) (bool, error) {
	s.beforeAppend()
	return s.Store.AppendAutoResumeRetryUserMessage(ctx, conversation, session, generation, msg, turnID, sourceID, now)
}

func TestUsageLimitAutoResumeRechecksConsentInRetryTransaction(t *testing.T) {
	for _, action := range []string{"cancel", "change account", "postpone deadline"} {
		t.Run(action, func(t *testing.T) {
			var wrapped *usageLimitAppendHookStore
			h := newHarnessWithConversationAndStore(t, nil, func(st *sqlite.Store) chatsvc.Store {
				wrapped = &usageLimitAppendHookStore{Store: st}
				return wrapped
			})
			ctx := context.Background()
			if err := h.st.BindSessionAgentAccount(ctx, testSession, domain.AgentAccountProviderCodex, "account-a", h.now().Add(-time.Minute)); err != nil {
				t.Fatal(err)
			}
			source := failUsageLimitedTurn(t, h, timedUsageLimit)
			if err := h.svc.SetTurnAutoResume(ctx, testSession, source.ID, true); err != nil {
				t.Fatal(err)
			}
			h.advance(time.Minute)
			wrapped.beforeAppend = func() {
				var err error
				switch action {
				case "cancel":
					err = h.svc.SetTurnAutoResume(ctx, testSession, source.ID, false)
				case "change account":
					err = h.st.BindSessionAgentAccount(ctx, testSession, domain.AgentAccountProviderCodex, "account-b", h.now())
				case "postpone deadline":
					_, err = h.st.PostponeTurnAutoResume(ctx, source.ConversationID, source.ID, h.now().Add(time.Hour))
				}
				if err != nil {
					t.Error(err)
				}
			}
			if err := h.svc.ResumeUsageLimitedTurns(ctx); err != nil {
				t.Fatal(err)
			}
			if _, found, err := h.st.RetryTurnIDForSource(ctx, source.ConversationID, source.ID); err != nil || found {
				t.Fatalf("revoked consent created a retry: found=%v err=%v", found, err)
			}
			if len(h.conv.sentMessages()) != 1 {
				t.Fatal("revoked or postponed consent reached the provider")
			}
			stored, err := h.st.TurnByID(ctx, source.ID)
			if err != nil || stored.UsageLimit.AutoResume != (action == "postpone deadline") || stored.UsageLimit.ResumeError != "" {
				t.Fatalf("consent after pre-insert change = %+v, %v", stored.UsageLimit, err)
			}
		})
	}
}
