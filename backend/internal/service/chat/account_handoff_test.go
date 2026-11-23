package chat_test

import (
	"context"
	"encoding/json"
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

const (
	handoffSource = "acct-a"
	handoffTarget = "acct-b"
)

type fakeHandoffAccounts struct {
	st       *sqlite.Store
	provider domain.AgentAccountProvider
	mu       sync.Mutex
	target   string
	calls    int
	onTarget func()
}

func (a *fakeHandoffAccounts) SessionAccountID(ctx context.Context, id domain.SessionID, _ domain.AgentHarness) (string, error) {
	account, _, err := a.st.GetSessionAgentAccount(ctx, id, a.provider)
	return account, err
}

func (a *fakeHandoffAccounts) HandoffTarget(_ context.Context, _ domain.SessionID, _ domain.AgentHarness, _ string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls++
	if a.onTarget != nil {
		a.onTarget()
	}
	return a.target, nil
}

type fakeHandoffMover struct {
	st       *sqlite.Store
	now      func() time.Time
	provider domain.AgentAccountProvider
	mu       sync.Mutex
	started  []domain.SessionInterfaceTransition
	err      error
}

func (m *fakeHandoffMover) StartAccountTransition(ctx context.Context, id domain.SessionID, accountID string, policy domain.SessionInterfaceTransitionPolicy) (domain.SessionInterfaceTransition, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return domain.SessionInterfaceTransition{}, m.err
	}
	source, _, err := m.st.GetSessionAgentAccount(ctx, id, m.provider)
	if err != nil {
		return domain.SessionInterfaceTransition{}, err
	}
	now := m.now()
	rec, created, err := m.st.CreateSessionInterfaceTransition(ctx, domain.SessionInterfaceTransition{
		ID: uuid.NewString(), SessionID: id, SourceMode: domain.SessionModeChat, TargetMode: domain.SessionModeChat,
		Policy: policy, HistoryPolicy: domain.SessionInterfaceTransitionHistoryStrict, Phase: domain.SessionInterfaceTransitionRequested,
		SourceAccountID: source, TargetAccountID: accountID, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		return domain.SessionInterfaceTransition{}, err
	}
	if !created {
		return rec, errors.New("transition in progress")
	}
	m.started = append(m.started, rec)
	return rec, nil
}

func (m *fakeHandoffMover) moves() []domain.SessionInterfaceTransition {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]domain.SessionInterfaceTransition(nil), m.started...)
}

func (m *fakeHandoffMover) finish(t *testing.T, phase domain.SessionInterfaceTransitionPhase) {
	t.Helper()
	ctx := context.Background()
	moves := m.moves()
	if len(moves) == 0 {
		t.Fatal("no account move was started")
	}
	last := moves[len(moves)-1]
	if phase == domain.SessionInterfaceTransitionCompleted {
		if err := m.st.BindSessionAgentAccount(ctx, last.SessionID, m.provider, last.TargetAccountID, m.now()); err != nil {
			t.Fatal(err)
		}
	}
	ok, err := m.st.AdvanceSessionInterfaceTransition(ctx, last.ID, domain.SessionInterfaceTransitionRequested, phase, "", "", "rolled back in test", m.now())
	if err != nil || !ok {
		t.Fatalf("finish move: %v %v", ok, err)
	}
}

type handoffHarness struct {
	*harness
	provider domain.AgentAccountProvider
	accounts *fakeHandoffAccounts
	mover    *fakeHandoffMover
	enabled  atomic.Bool
}

func newHandoffHarness(t *testing.T, agentHarness domain.AgentHarness) *handoffHarness {
	t.Helper()
	return handoffHarnessFor(t, newHarnessForHarness(t, agentHarness), agentHarness)
}

func handoffHarnessFor(t *testing.T, h *harness, agentHarness domain.AgentHarness) *handoffHarness {
	t.Helper()
	provider, _ := domain.AgentAccountProviderFor(agentHarness)
	if err := h.st.BindSessionAgentAccount(context.Background(), testSession, provider, handoffSource, h.now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	hh := &handoffHarness{
		harness: h, provider: provider,
		accounts: &fakeHandoffAccounts{st: h.st, provider: provider, target: handoffTarget},
		mover:    &fakeHandoffMover{st: h.st, now: h.now, provider: provider},
	}
	hh.enabled.Store(true)
	hh.wire(h.svc)
	return hh
}

func (hh *handoffHarness) wire(svc *chatsvc.Service) {
	svc.SetAccountHandoff(func(context.Context) bool { return hh.enabled.Load() }, hh.accounts, hh.mover)
}

func (hh *handoffHarness) tick(t *testing.T) {
	t.Helper()
	if err := hh.svc.HandoffUsageLimitedTurns(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func (hh *handoffHarness) boundAccount(t *testing.T) string {
	t.Helper()
	account, _, err := hh.st.GetSessionAgentAccount(context.Background(), testSession, hh.provider)
	if err != nil {
		t.Fatal(err)
	}
	return account
}

func usageLimitMessage(agentHarness domain.AgentHarness, knownReset bool, now time.Time) string {
	if !knownReset {
		return "You've hit your usage limit. Please try again later."
	}
	if agentHarness == domain.HarnessCodex {
		return fmt.Sprintf("Usage limit exceeded\n\nYou've hit your usage limit.\n\nResets at %s", now.Add(time.Minute).Format(time.RFC3339))
	}
	return timedUsageLimit
}

func handoffNotes(t *testing.T, h *harness, turnID string) (notes int, retry string) {
	t.Helper()
	conversation, err := h.st.ConversationForSession(context.Background(), testSession)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := h.st.LoadConversationSnapshot(context.Background(), conversation.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, activity := range rows.Activities {
		if activity.ProviderItemID != "account-handoff:"+turnID {
			continue
		}
		var detail struct{ Event, AccountID, Retry string }
		if err := json.Unmarshal(activity.Detail, &detail); err != nil || detail.Event != chatsvc.AccountHandoffEvent || detail.AccountID != handoffTarget {
			t.Fatalf("handoff note detail = %s (%v)", activity.Detail, err)
		}
		notes, retry = notes+1, detail.Retry
	}
	return notes, retry
}

var bothAccountHarnesses = []domain.AgentHarness{domain.HarnessClaudeCode, domain.HarnessCodex}

func TestAccountHandoffMovesAndRetriesExactlyOnce(t *testing.T) {
	for _, agentHarness := range bothAccountHarnesses {
		t.Run(string(agentHarness), func(t *testing.T) {
			hh := newHandoffHarness(t, agentHarness)
			ctx := context.Background()
			source := failUsageLimitedTurn(t, hh.harness, usageLimitMessage(agentHarness, true, hh.now()))

			hh.tick(t)
			if moves := hh.mover.moves(); len(moves) != 1 || moves[0].TargetAccountID != handoffTarget || moves[0].Policy != domain.SessionInterfaceTransitionDrain {
				t.Fatalf("moves = %+v", moves)
			}
			hh.tick(t)
			if got := len(hh.conv.sentMessages()); got != 1 {
				t.Fatalf("dispatches before commit = %d", got)
			}

			hh.mover.finish(t, domain.SessionInterfaceTransitionCompleted)
			var ticks sync.WaitGroup
			for range 8 {
				ticks.Go(func() { _ = hh.svc.HandoffUsageLimitedTurns(ctx) })
			}
			ticks.Wait()
			hh.tick(t)
			hh.tick(t)

			if got := len(hh.conv.sentMessages()); got != 2 {
				t.Fatalf("dispatches = %d, want the original and one retry", got)
			}
			retryID, found, err := hh.st.RetryTurnIDForSource(ctx, source.ConversationID, source.ID)
			if err != nil || !found {
				t.Fatalf("retry link: %v %v", found, err)
			}
			if notes, retry := handoffNotes(t, hh.harness, source.ID); notes != 1 || retry != "sent" {
				t.Fatalf("handoff notes = %d, retry = %q", notes, retry)
			}
			if len(hh.mover.moves()) != 1 || hh.boundAccount(t) != handoffTarget {
				t.Fatalf("moves=%d account=%s", len(hh.mover.moves()), hh.boundAccount(t))
			}
			retry, err := hh.st.TurnByID(ctx, retryID)
			if err != nil || retry.RetryOfTurnID != source.ID {
				t.Fatalf("retry lineage = %+v, %v", retry, err)
			}
		})
	}
}

func TestAccountHandoffNoCandidateKeepsAccount(t *testing.T) {
	for _, agentHarness := range bothAccountHarnesses {
		for _, knownReset := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/known-reset=%v", agentHarness, knownReset), func(t *testing.T) {
				hh := newHandoffHarness(t, agentHarness)
				hh.accounts.target = ""
				source := failUsageLimitedTurn(t, hh.harness, usageLimitMessage(agentHarness, knownReset, hh.now()))
				hh.tick(t)
				if len(hh.mover.moves()) != 0 || hh.boundAccount(t) != handoffSource {
					t.Fatal("moved without a candidate")
				}
				stored, err := hh.st.TurnByID(context.Background(), source.ID)
				if err != nil {
					t.Fatal(err)
				}
				if stored.UsageLimit.AutoResume != knownReset {
					t.Fatalf("auto-resume = %v, want %v (known reset)", stored.UsageLimit.AutoResume, knownReset)
				}
				if got := len(hh.conv.sentMessages()); got != 1 {
					t.Fatalf("dispatches = %d", got)
				}
			})
		}
	}
}

func TestAccountHandoffRollbackKeepsAccount(t *testing.T) {
	for _, agentHarness := range bothAccountHarnesses {
		for _, phase := range []domain.SessionInterfaceTransitionPhase{domain.SessionInterfaceTransitionFailed, domain.SessionInterfaceTransitionCancelled} {
			t.Run(fmt.Sprintf("%s/%s", agentHarness, phase), func(t *testing.T) {
				hh := newHandoffHarness(t, agentHarness)
				source := failUsageLimitedTurn(t, hh.harness, usageLimitMessage(agentHarness, true, hh.now()))
				hh.tick(t)
				hh.mover.finish(t, phase)
				hh.tick(t)
				hh.tick(t)
				if hh.boundAccount(t) != handoffSource {
					t.Fatalf("account = %s", hh.boundAccount(t))
				}
				if got := len(hh.conv.sentMessages()); got != 1 {
					t.Fatalf("dispatches after rollback = %d", got)
				}
				stored, _ := hh.st.TurnByID(context.Background(), source.ID)
				if want := phase == domain.SessionInterfaceTransitionFailed; stored.UsageLimit.AutoResume != want {
					t.Fatalf("auto-resume = %v, want %v", stored.UsageLimit.AutoResume, want)
				}
				if notes, _ := handoffNotes(t, hh.harness, source.ID); notes != 0 {
					t.Fatal("a rolled-back move wrote a note")
				}
			})
		}
	}
}

func TestAccountHandoffOffClaimsNothing(t *testing.T) {
	hh := newHandoffHarness(t, domain.HarnessCodex)
	hh.enabled.Store(false)
	failUsageLimitedTurn(t, hh.harness, usageLimitMessage(domain.HarnessCodex, true, hh.now()))
	hh.tick(t)
	if hh.accounts.calls != 0 {
		t.Fatal("claimed with the setting off")
	}
	hh.enabled.Store(true)
	hh.advance(11 * time.Minute)
	hh.tick(t)
	if hh.accounts.calls != 0 || len(hh.mover.moves()) != 0 {
		t.Fatal("an old failure was handed off")
	}
}

func TestAccountHandoffTurnedOffDuringSelectionDoesNotMove(t *testing.T) {
	hh := newHandoffHarness(t, domain.HarnessCodex)
	hh.accounts.onTarget = func() { hh.enabled.Store(false) }
	failUsageLimitedTurn(t, hh.harness, usageLimitMessage(domain.HarnessCodex, true, hh.now()))
	hh.tick(t)
	if hh.accounts.calls != 1 || len(hh.mover.moves()) != 0 {
		t.Fatal("moved after the setting was turned off")
	}
}

func TestAccountHandoffDoesNotPingPong(t *testing.T) {
	hh := newHandoffHarness(t, domain.HarnessCodex)
	ctx := context.Background()
	source := failUsageLimitedTurn(t, hh.harness, usageLimitMessage(domain.HarnessCodex, true, hh.now()))
	hh.tick(t)
	hh.mover.finish(t, domain.SessionInterfaceTransitionCompleted)
	hh.tick(t)
	retryID, found, err := hh.st.RetryTurnIDForSource(ctx, source.ConversationID, source.ID)
	if err != nil || !found {
		t.Fatalf("retry: %v %v", found, err)
	}
	retry, err := hh.st.TurnByID(ctx, retryID)
	if err != nil {
		t.Fatal(err)
	}
	hh.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: retry.ProviderTurnID,
		TurnState: domain.TurnStateFailed, Err: errors.New(usageLimitMessage(domain.HarnessCodex, true, hh.now()))})
	failedTurnSnapshot(t, hh.harness, retryID)
	hh.accounts.target = handoffSource
	hh.tick(t)
	if moves := hh.mover.moves(); len(moves) != 1 {
		t.Fatalf("moves = %d, want the first only", len(moves))
	}
	if got := len(hh.conv.sentMessages()); got != 2 {
		t.Fatalf("dispatches = %d", got)
	}
}

func TestAccountHandoffWithdrawsRetryForNewerPrompt(t *testing.T) {
	hh := newHandoffHarness(t, domain.HarnessClaudeCode)
	ctx := context.Background()
	source := failUsageLimitedTurn(t, hh.harness, timedUsageLimit)
	hh.tick(t)
	if _, err := hh.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "never mind", ClientMessageID: uuid.NewString(), Origin: domain.MessageOriginHuman}); err != nil {
		t.Fatal(err)
	}
	hh.mover.finish(t, domain.SessionInterfaceTransitionCompleted)
	hh.tick(t)
	if _, found, _ := hh.st.RetryTurnIDForSource(ctx, source.ConversationID, source.ID); found {
		t.Fatal("retried a superseded prompt")
	}
	if got := len(hh.conv.sentMessages()); got != 2 {
		t.Fatalf("dispatches = %d", got)
	}
	if _, got := handoffNotes(t, hh.harness, source.ID); got != "skipped" {
		t.Fatalf("handoff note retry = %q", got)
	}
}

func TestAccountHandoffRestartLeavesThePromptToThePerson(t *testing.T) {
	for _, agentHarness := range bothAccountHarnesses {
		t.Run(string(agentHarness), func(t *testing.T) {
			hh := newHandoffHarness(t, agentHarness)
			ctx := context.Background()
			source := failUsageLimitedTurn(t, hh.harness, usageLimitMessage(agentHarness, true, hh.now()))
			hh.tick(t)
			if err := hh.svc.Stop(ctx, testSession); err != nil {
				t.Fatal(err)
			}
			hh.mover.finish(t, domain.SessionInterfaceTransitionCompleted)

			hh.advance(time.Second)
			provider := newFakeConversation()
			provider.turnSeq = 100
			svc := chatsvc.New(chatsvc.Options{
				Store: hh.st, Reader: fullSnapshotReader(hh.st), PageReader: boundedSnapshotReader(hh.st), Sessions: hh.st,
				Drivers: fakeRegistry{driver: fakeDriver{conv: provider}}, NewID: uuid.NewString, Now: hh.now,
			})
			if _, err := svc.Start(ctx, chatsvc.StartConfig{SessionID: testSession, ProjectID: testProject,
				Harness: agentHarness, WorkspacePath: t.TempDir(), ProviderConversationID: "thread-1"}); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = svc.Stop(ctx, testSession) })
			hh.wire(svc)
			for range 3 {
				if err := svc.HandoffUsageLimitedTurns(ctx); err != nil {
					t.Fatal(err)
				}
			}
			if got := len(provider.sentMessages()); got != 0 {
				t.Fatalf("retries after restart = %d", got)
			}
			if len(hh.mover.moves()) != 1 || hh.boundAccount(t) != handoffTarget {
				t.Fatalf("moves=%d account=%s", len(hh.mover.moves()), hh.boundAccount(t))
			}
			if _, found, _ := hh.st.RetryTurnIDForSource(ctx, source.ConversationID, source.ID); found {
				t.Fatal("retried a turn from before the restart")
			}
		})
	}
}

func TestAccountHandoffSkipsRetryWhenTheSessionLeftTheTarget(t *testing.T) {
	hh := newHandoffHarness(t, domain.HarnessCodex)
	ctx := context.Background()
	source := failUsageLimitedTurn(t, hh.harness, usageLimitMessage(domain.HarnessCodex, true, hh.now()))
	hh.tick(t)
	hh.mover.finish(t, domain.SessionInterfaceTransitionCompleted)
	if err := hh.st.BindSessionAgentAccount(ctx, testSession, hh.provider, "acct-c", hh.now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	hh.tick(t)
	if _, found, _ := hh.st.RetryTurnIDForSource(ctx, source.ConversationID, source.ID); found {
		t.Fatal("retried after the session left the target account")
	}
}

func TestAccountHandoffDeferredRetrySkipsAfterTheSessionLeftTheTarget(t *testing.T) {
	h, _ := hibernationHarnessWith(t, "", func(opts *chatsvc.Options) { opts.HibernationEnabled = func() bool { return true } })
	hh := handoffHarnessFor(t, h, domain.HarnessCodex)
	ctx := context.Background()
	source := failUsageLimitedTurn(t, h, usageLimitMessage(domain.HarnessCodex, true, h.now()))
	hh.tick(t)
	hh.mover.finish(t, domain.SessionInterfaceTransitionCompleted)
	if slept, err := h.svc.HibernateChat(ctx, testSession); err != nil || !slept {
		t.Fatalf("hibernate = %v, %v", slept, err)
	}
	var wakes atomic.Int32
	h.svc.SetWakeCallback(func(context.Context, domain.SessionID) error {
		wakes.Add(1)
		return errors.New("native resume refused")
	})
	if err := h.svc.HandoffUsageLimitedTurns(ctx); err == nil {
		t.Fatal("a failed wake reported success")
	}
	if err := hh.st.BindSessionAgentAccount(ctx, testSession, hh.provider, "acct-c", h.now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	h.advance(time.Minute)
	hh.tick(t)
	if wakes.Load() != 1 {
		t.Fatalf("wakes = %d, want no retry attempt after the session left the target", wakes.Load())
	}
	if _, retry := handoffNotes(t, h, source.ID); retry != "skipped" {
		t.Fatalf("handoff note retry = %q", retry)
	}
}

func TestAccountHandoffPendingRetrySurvivesHibernation(t *testing.T) {
	resumed := newFakeConversation()
	resumed.turnSeq = 100
	h, old := hibernationHarnessWith(t, "", func(opts *chatsvc.Options) {
		opts.HibernationEnabled = func() bool { return true }
		driver := opts.Drivers.(fakeRegistry).driver.(fakeDriver)
		driver.resume = func(ports.ChatResumeConfig) (ports.ChatConversation, error) { return resumed, nil }
		opts.Drivers = fakeRegistry{driver: driver}
	})
	hh := handoffHarnessFor(t, h, domain.HarnessCodex)
	ctx := context.Background()
	source := failUsageLimitedTurn(t, h, usageLimitMessage(domain.HarnessCodex, true, h.now()))
	hh.tick(t)
	hh.mover.finish(t, domain.SessionInterfaceTransitionCompleted)
	if slept, err := h.svc.HibernateChat(ctx, testSession); err != nil || !slept {
		t.Fatalf("hibernate = %v, %v", slept, err)
	}
	var wakes atomic.Int32
	h.svc.SetWakeCallback(func(ctx context.Context, id domain.SessionID) error {
		if wakes.Add(1) == 1 {
			return errors.New("native resume refused")
		}
		record, _, err := hh.st.GetSession(ctx, id)
		if err != nil {
			return err
		}
		if applied, err := hh.st.SetSessionHibernated(ctx, id, record.Revision, nil); err != nil || !applied {
			return errors.New("could not clear hibernation marker")
		}
		_, err = h.svc.Start(ctx, chatsvc.StartConfig{SessionID: id, ProjectID: testProject,
			Harness: domain.HarnessCodex, WorkspacePath: t.TempDir(), ProviderConversationID: old.ProviderConversationID()})
		return err
	})

	if err := h.svc.HandoffUsageLimitedTurns(ctx); err == nil {
		t.Fatal("a failed wake reported success")
	}
	if notes, _ := handoffNotes(t, h, source.ID); notes != 1 {
		t.Fatalf("handoff notes = %d", notes)
	}
	hh.tick(t)
	if wakes.Load() != 1 {
		t.Fatalf("wakes inside backoff = %d", wakes.Load())
	}
	h.advance(time.Minute)
	hh.tick(t)
	hh.tick(t)
	if wakes.Load() != 2 || len(resumed.sentMessages()) != 1 {
		t.Fatalf("wakes=%d sends=%d, want one retry after the second wake", wakes.Load(), len(resumed.sentMessages()))
	}
	if _, found, err := hh.st.RetryTurnIDForSource(ctx, source.ConversationID, source.ID); err != nil || !found {
		t.Fatalf("retry link: %v %v", found, err)
	}
	if notes, _ := handoffNotes(t, h, source.ID); notes != 1 {
		t.Fatalf("handoff notes after retry = %d", notes)
	}
}
