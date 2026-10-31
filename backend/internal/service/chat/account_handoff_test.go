package chat_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/store"
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
	models   []string
	excluded []map[string]bool
}

func (a *fakeHandoffAccounts) SessionAccountID(ctx context.Context, id domain.SessionID, _ domain.AgentHarness) (string, error) {
	account, _, err := a.st.GetSessionAgentAccount(ctx, id, a.provider)
	return account, err
}

func (a *fakeHandoffAccounts) HandoffTarget(_ context.Context, _ domain.SessionID, _ domain.AgentHarness, model string, exclude map[string]bool) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.models = append(a.models, model)
	a.excluded = append(a.excluded, exclude)
	if exclude[a.target] {
		return "", nil
	}
	return a.target, nil
}

// fakeHandoffMover stands in for Session Manager: it claims a real transition
// row, and the test commits (binding the target) or rolls it back.
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
	h := newHarnessForHarness(t, agentHarness)
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

func (hh *handoffHarness) handoff(t *testing.T, turnID string) domain.ChatAccountHandoff {
	t.Helper()
	rec, found, err := hh.st.ChatAccountHandoff(context.Background(), turnID)
	if err != nil || !found {
		t.Fatalf("handoff for %s: found=%v err=%v", turnID, found, err)
	}
	return rec
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

func handoffNotes(t *testing.T, h *harness, turnID string) int {
	t.Helper()
	conversation, err := h.st.ConversationForSession(context.Background(), testSession)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := h.st.LoadConversationSnapshot(context.Background(), conversation.ID)
	if err != nil {
		t.Fatal(err)
	}
	notes := 0
	for _, activity := range rows.Activities {
		if activity.ProviderItemID == "account-handoff:"+turnID {
			if !strings.Contains(string(activity.Detail), chatsvc.AccountHandoffEvent) || !strings.Contains(string(activity.Detail), handoffTarget) {
				t.Fatalf("handoff note detail = %s", activity.Detail)
			}
			notes++
		}
	}
	return notes
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
			if rec := hh.handoff(t, source.ID); rec.Phase != domain.ChatAccountHandoffMoving || rec.SourceAccountID != handoffSource {
				t.Fatalf("handoff while moving = %+v", rec)
			}
			if !hh.accounts.excluded[0][handoffSource] {
				t.Fatalf("the refused account was a candidate: %v", hh.accounts.excluded[0])
			}
			// A running move only waits; nothing retries before the commit.
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
			if rec := hh.handoff(t, source.ID); rec.Phase != domain.ChatAccountHandoffRetried || rec.TargetAccountID != handoffTarget {
				t.Fatalf("handoff after retry = %+v", rec)
			}
			if notes := handoffNotes(t, hh.harness, source.ID); notes != 1 {
				t.Fatalf("handoff notes = %d", notes)
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
				if rec := hh.handoff(t, source.ID); rec.Phase != domain.ChatAccountHandoffStayed {
					t.Fatalf("handoff = %+v", rec)
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
				rec := hh.handoff(t, source.ID)
				if rec.Phase != domain.ChatAccountHandoffRolledBack || hh.boundAccount(t) != handoffSource {
					t.Fatalf("handoff=%+v account=%s", rec, hh.boundAccount(t))
				}
				if got := len(hh.conv.sentMessages()); got != 1 {
					t.Fatalf("dispatches after rollback = %d", got)
				}
				stored, _ := hh.st.TurnByID(context.Background(), source.ID)
				// Only a move that failed on its own falls back to auto-resume.
				if want := phase == domain.SessionInterfaceTransitionFailed; stored.UsageLimit.AutoResume != want {
					t.Fatalf("auto-resume = %v, want %v", stored.UsageLimit.AutoResume, want)
				}
				if handoffNotes(t, hh.harness, source.ID) != 0 {
					t.Fatal("a rolled-back move wrote a note")
				}
			})
		}
	}
}

func TestAccountHandoffRespectsStayOnAccount(t *testing.T) {
	hh := newHandoffHarness(t, domain.HarnessClaudeCode)
	if err := hh.st.SetSessionAccountPinned(context.Background(), testSession, true, hh.now()); err != nil {
		t.Fatal(err)
	}
	source := failUsageLimitedTurn(t, hh.harness, timedUsageLimit)
	hh.tick(t)
	if len(hh.mover.moves()) != 0 || len(hh.accounts.models) != 0 {
		t.Fatal("a pinned session was considered for a move")
	}
	rec := hh.handoff(t, source.ID)
	stored, _ := hh.st.TurnByID(context.Background(), source.ID)
	if rec.Phase != domain.ChatAccountHandoffStayed || !stored.UsageLimit.AutoResume {
		t.Fatalf("handoff=%+v autoResume=%v", rec, stored.UsageLimit.AutoResume)
	}
}

func TestAccountHandoffOffClaimsNothing(t *testing.T) {
	hh := newHandoffHarness(t, domain.HarnessCodex)
	hh.enabled.Store(false)
	source := failUsageLimitedTurn(t, hh.harness, usageLimitMessage(domain.HarnessCodex, true, hh.now()))
	hh.tick(t)
	if _, found, _ := hh.st.ChatAccountHandoff(context.Background(), source.ID); found {
		t.Fatal("claimed with the setting off")
	}
	// Turned on later, a failure older than the window stays the person's.
	hh.enabled.Store(true)
	hh.advance(11 * time.Minute)
	hh.tick(t)
	if _, found, _ := hh.st.ChatAccountHandoff(context.Background(), source.ID); found || len(hh.mover.moves()) != 0 {
		t.Fatal("an old failure was handed off")
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
	// The retry on the new account is refused too: the prompt does not move again.
	hh.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: retry.ProviderTurnID,
		TurnState: domain.TurnStateFailed, Err: errors.New(usageLimitMessage(domain.HarnessCodex, true, hh.now()))})
	failedTurnSnapshot(t, hh.harness, retryID)
	hh.accounts.target = handoffSource
	hh.tick(t)
	if moves := hh.mover.moves(); len(moves) != 1 {
		t.Fatalf("moves = %d, want the first only", len(moves))
	}
	if rec := hh.handoff(t, retryID); rec.Phase != domain.ChatAccountHandoffStayed || !strings.Contains(rec.Detail, "refused") {
		t.Fatalf("second refusal handoff = %+v", rec)
	}
	if got := len(hh.conv.sentMessages()); got != 2 {
		t.Fatalf("dispatches = %d", got)
	}
}

func TestAccountHandoffExcludesRecentlyLeftAccount(t *testing.T) {
	hh := newHandoffHarness(t, domain.HarnessCodex)
	first := failUsageLimitedTurn(t, hh.harness, usageLimitMessage(domain.HarnessCodex, true, hh.now()))
	hh.tick(t)
	hh.mover.finish(t, domain.SessionInterfaceTransitionCompleted)
	hh.tick(t)
	// A new prompt on the target account is refused before acct-a resets.
	retryID, _, _ := hh.st.RetryTurnIDForSource(context.Background(), first.ConversationID, first.ID)
	retry, _ := hh.st.TurnByID(context.Background(), retryID)
	hh.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: retry.ProviderTurnID, TurnState: domain.TurnStateCompleted})
	hh.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		turn, ok := turnByID(s, retryID)
		return ok && turn.State == domain.TurnStateCompleted
	})
	second := failUsageLimitedTurn(t, hh.harness, usageLimitMessage(domain.HarnessCodex, true, hh.now()))
	hh.accounts.target = handoffSource
	hh.tick(t)
	last := hh.accounts.excluded[len(hh.accounts.excluded)-1]
	if !last[handoffSource] || !last[handoffTarget] {
		t.Fatalf("exclusions = %v, want both the current and the recently left account", last)
	}
	if rec := hh.handoff(t, second.ID); rec.Phase != domain.ChatAccountHandoffStayed {
		t.Fatalf("handoff = %+v", rec)
	}
}

func TestAccountHandoffWithdrawsRetryForNewerPrompt(t *testing.T) {
	hh := newHandoffHarness(t, domain.HarnessClaudeCode)
	ctx := context.Background()
	source := failUsageLimitedTurn(t, hh.harness, timedUsageLimit)
	hh.tick(t)
	// The person sends something else while the move runs.
	if _, err := hh.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "never mind", ClientMessageID: uuid.NewString(), Origin: domain.MessageOriginHuman}); err != nil {
		t.Fatal(err)
	}
	hh.mover.finish(t, domain.SessionInterfaceTransitionCompleted)
	hh.tick(t)
	if rec := hh.handoff(t, source.ID); rec.Phase != domain.ChatAccountHandoffSkipped {
		t.Fatalf("handoff = %+v", rec)
	}
	if _, found, _ := hh.st.RetryTurnIDForSource(ctx, source.ConversationID, source.ID); found {
		t.Fatal("retried a superseded prompt")
	}
	if got := len(hh.conv.sentMessages()); got != 2 {
		t.Fatalf("dispatches = %d", got)
	}
}

// A daemon restart drops every in-memory guard; the durable phase resumes the
// saga and the retry still happens once.
func TestAccountHandoffResumesAfterRestart(t *testing.T) {
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

			provider := newFakeConversation()
			provider.turnSeq = 100 // the provider's turn ids continue after a restart
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
			if got := len(provider.sentMessages()); got != 1 {
				t.Fatalf("retries after restart = %d", got)
			}
			if rec := hh.handoff(t, source.ID); rec.Phase != domain.ChatAccountHandoffRetried {
				t.Fatalf("handoff = %+v", rec)
			}
		})
	}
}

// A crash between starting the move and recording it must adopt that move.
func TestAccountHandoffAdoptsMoveStartedBeforeCrash(t *testing.T) {
	hh := newHandoffHarness(t, domain.HarnessCodex)
	ctx := context.Background()
	source := failUsageLimitedTurn(t, hh.harness, usageLimitMessage(domain.HarnessCodex, true, hh.now()))
	now := hh.now()
	claimed, err := hh.st.ClaimChatAccountHandoff(ctx, domain.ChatAccountHandoff{
		SourceTurnID: source.ID, SessionID: testSession, ConversationID: source.ConversationID,
		SourceAccountID: handoffSource, Phase: domain.ChatAccountHandoffSelecting, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil || !claimed {
		t.Fatal(claimed, err)
	}
	if _, err := hh.mover.StartAccountTransition(ctx, testSession, handoffTarget, domain.SessionInterfaceTransitionDrain); err != nil {
		t.Fatal(err)
	}
	hh.tick(t)
	if moves := hh.mover.moves(); len(moves) != 1 {
		t.Fatalf("moves = %d, want the adopted one only", len(moves))
	}
	if rec := hh.handoff(t, source.ID); rec.Phase != domain.ChatAccountHandoffMoving || rec.TransitionID != hh.mover.moves()[0].ID {
		t.Fatalf("handoff = %+v", rec)
	}
}

func TestAccountHandoffRetryConsentIsSingleUse(t *testing.T) {
	hh := newHandoffHarness(t, domain.HarnessCodex)
	ctx := context.Background()
	source := failUsageLimitedTurn(t, hh.harness, usageLimitMessage(domain.HarnessCodex, true, hh.now()))
	hh.tick(t)
	// The session leaves the target before the retry: the move's consent no
	// longer applies, and the store refuses it inside the retry transaction.
	hh.mover.finish(t, domain.SessionInterfaceTransitionCompleted)
	rec := hh.handoff(t, source.ID)
	rec.Phase, rec.UpdatedAt = domain.ChatAccountHandoffMoved, hh.now()
	if ok, err := hh.st.AdvanceChatAccountHandoff(ctx, rec, domain.ChatAccountHandoffMoving); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if err := hh.st.BindSessionAgentAccount(ctx, testSession, hh.provider, "acct-c", hh.now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	hh.tick(t)
	if _, found, _ := hh.st.RetryTurnIDForSource(ctx, source.ConversationID, source.ID); found {
		t.Fatal("retried after the session left the target account")
	}
	if rec := hh.handoff(t, source.ID); rec.Phase != domain.ChatAccountHandoffSkipped {
		t.Fatalf("handoff = %+v", rec)
	}
}
