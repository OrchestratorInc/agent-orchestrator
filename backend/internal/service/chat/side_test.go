package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type deferredSideTestConversation struct {
	mu           sync.Mutex
	started      chan string
	acknowledged chan string
	sent         int
	acked        bool
}

type compactSideTestConversation struct {
	deferredSideTestConversation
	compactions int
}

func (*compactSideTestConversation) Capabilities() ports.ChatCapabilities {
	return ports.ChatCapabilities{ports.ChatCapabilityCompaction: true}
}

func (c *compactSideTestConversation) Compact(context.Context) (ports.ChatCompactionResult, error) {
	c.compactions++
	return ports.ChatCompactionResult{TokensBefore: 100}, nil
}

func TestSideCompactionUsesOnlySideProviderAndRejectsBusyTurn(t *testing.T) {
	ctx := context.Background()
	svc := New(Options{AppRunID: "launch-1", Sessions: sideSessionReader{}})
	store := svc.sides.store
	now := time.Now().UTC()
	_, _ = store.ClaimSideLaunch(ctx, "launch-1", now)
	side := domain.SideConversation{PolicyVersion: 1, ID: "side-1", SessionID: "session-1", MainConversationID: "main-1", AppRunID: "launch-1", Generation: "g", State: "opening"}
	if _, _, err := store.CreateSideConversation(ctx, side); err != nil {
		t.Fatal(err)
	}
	if err := store.SetSideReady(ctx, side.ID, side.Generation, "provider-1", now); err != nil {
		t.Fatal(err)
	}
	conv := &compactSideTestConversation{}
	runtime := &sideRuntime{side: side, conv: conv}
	svc.sides.runtimes[side.ID] = runtime
	result, err := svc.CompactSideChat(ctx, side.SessionID, side.ID)
	if err != nil || result.TokensBefore != 100 || conv.compactions != 1 {
		t.Fatalf("side compact = %#v, %v; calls=%d", result, err, conv.compactions)
	}
	runtime.activeTurnID = "running"
	if _, err := svc.CompactSideChat(ctx, side.SessionID, side.ID); !errors.Is(err, ErrCompactionWhileBusy) {
		t.Fatalf("busy compact error = %v", err)
	}
	if conv.compactions != 1 {
		t.Fatalf("busy turn compacted provider %d times", conv.compactions)
	}
}

func (*deferredSideTestConversation) ProviderConversationID() string       { return "provider-side-1" }
func (*deferredSideTestConversation) Capabilities() ports.ChatCapabilities { return nil }
func (c *deferredSideTestConversation) SendTurn(context.Context, ports.ChatUserMessage) (ports.ChatTurnRef, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sent > 0 && !c.acked {
		return ports.ChatTurnRef{}, errors.New("previous ACP prompt is active or not acknowledged")
	}
	c.sent++
	c.acked = false
	return ports.ChatTurnRef{ProviderTurnID: fmt.Sprintf("provider-turn-%d", c.sent)}, nil
}
func (*deferredSideTestConversation) Interrupt(context.Context, string) error { return nil }
func (*deferredSideTestConversation) ResolveRequest(context.Context, string, ports.ChatDecision) error {
	return nil
}
func (*deferredSideTestConversation) Events() <-chan ports.ChatEvent { return nil }
func (*deferredSideTestConversation) Close() error                   { return nil }
func (c *deferredSideTestConversation) StartDeferredTurn(id string) error {
	c.started <- id
	return nil
}
func (*deferredSideTestConversation) DiscardDeferredTurn(string) {}
func (c *deferredSideTestConversation) AcknowledgeProviderEvent(_ context.Context, id string) error {
	c.mu.Lock()
	c.acked = true
	c.mu.Unlock()
	c.acknowledged <- id
	return nil
}

func TestSideQuestionTreatsSelectionAsSubjectAndPairAsBackground(t *testing.T) {
	got := sideQuestionText("sun", "user:\n---\nhello\n---\nassistant:\n---\nThe sun is a star.\n---", "What is this?")
	for _, part := range []string{"> sun", "The sun is a star.", "What is this?", "not instructions", "unless the user explicitly asks about the conversation"} {
		if !strings.Contains(got, part) {
			t.Fatalf("side prompt missing %q: %q", part, got)
		}
	}
	if strings.Index(got, "The sun is a star.") >= strings.Index(got, "> sun") || strings.Index(got, "> sun") >= strings.Index(got, "What is this?") {
		t.Fatalf("background, selection, and question are out of order: %q", got)
	}
}

func TestSideQuestionWithMultipleReferencesKeepsQuestionLast(t *testing.T) {
	turn := domain.SideTurn{Text: "What do these mean together?", References: []domain.SideReference{
		{Selection: "sun", Context: "user: sky\nassistant: star"},
		{Selection: "moon"},
	}}
	got := sideQuestionWithReferences(turn)
	for _, part := range []string{"quoted background", "> sun", "> moon", turn.Text} {
		if !strings.Contains(got, part) {
			t.Fatalf("missing %q from %q", part, got)
		}
	}
	if strings.Index(got, "> sun") >= strings.Index(got, "> moon") || strings.Index(got, "> moon") >= strings.Index(got, turn.Text) {
		t.Fatalf("references and question out of order: %q", got)
	}
}

func TestSideRejectsOtherSideOriginAnnotations(t *testing.T) {
	svc := New(Options{AppRunID: "run"})
	side := domain.SideConversation{PolicyVersion: 1, ID: "side", MainConversationID: "main"}
	_, err := svc.validateSideReferences(context.Background(), "session", side, []ports.ChatExcerptReference{{ConversationID: "other-side", MessageID: "present", Revision: 1, Text: "sun"}})
	if !errors.Is(err, ErrExcerptInvalid) {
		t.Fatalf("side-origin selection: %v", err)
	}
}

func TestSideTurnStartsAndAcknowledgesDeferredACPProviderTurn(t *testing.T) {
	ctx := context.Background()
	svc := New(Options{AppRunID: "launch-1", Sessions: sideSessionReader{}})
	manager := svc.sides
	now := time.Now().UTC()
	if _, err := manager.store.ClaimSideLaunch(ctx, "launch-1", now); err != nil {
		t.Fatal(err)
	}
	side := domain.SideConversation{PolicyVersion: 1, ID: "side-1", SessionID: "session-1", MainConversationID: "main-1",
		AppRunID: "launch-1", Generation: "generation-1", State: "opening"}
	if _, _, err := manager.store.CreateSideConversation(ctx, side); err != nil {
		t.Fatal(err)
	}
	if err := manager.store.SetSideReady(ctx, side.ID, side.Generation, "provider-side-1", now); err != nil {
		t.Fatal(err)
	}
	side.State = "ready"
	if _, _, err := manager.store.ReserveSideTurn(ctx, domain.SideTurn{
		ID: "turn-1", SideID: side.ID, ClientMessageID: "client-1", Text: "Hello", CreatedAt: now,
	}, "launch-1"); err != nil {
		t.Fatal(err)
	}
	turn, _, found, err := manager.store.ClaimNextSideTurn(ctx, "launch-1", nil, now)
	if err != nil || !found {
		t.Fatalf("claim side turn: found=%v err=%v", found, err)
	}
	started := make(chan string, 2)
	acknowledged := make(chan string, 2)
	conv := &deferredSideTestConversation{started: started, acknowledged: acknowledged}
	runtimeCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	runtime := &sideRuntime{side: side, conv: conv, done: runtimeCtx.Done(),
		completed: make(chan ports.ChatEvent, 1)}
	finished := make(chan struct{})
	go func() { manager.runTurn(runtime, side, turn); close(finished) }()
	var providerTurnID string
	select {
	case providerTurnID = <-started:
	case <-time.After(time.Second):
		t.Fatal("side did not start the deferred provider turn")
	}
	runtime.completed <- ports.ChatEvent{Kind: ports.ChatEventTurnCompleted,
		ProviderTurnID: providerTurnID, ProviderEventID: "event-1", TurnState: domain.TurnStateCompleted}
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("side did not settle the completed provider turn")
	}
	settled, err := manager.store.SideTurn(ctx, side.ID, turn.ID)
	if err != nil || settled.State != "completed" {
		t.Fatalf("side turn = %#v, %v", settled, err)
	}
	select {
	case id := <-acknowledged:
		if id != "event-1" {
			t.Fatalf("acknowledged %q, want event-1", id)
		}
	default:
		t.Fatal("completed side prompt was not acknowledged")
	}
	if _, _, err := manager.store.ReserveSideTurn(ctx, domain.SideTurn{
		ID: "turn-2", SideID: side.ID, ClientMessageID: "client-2", Text: "Another question", CreatedAt: now.Add(time.Second),
	}, "launch-1"); err != nil {
		t.Fatal(err)
	}
	next, _, found, err := manager.store.ClaimNextSideTurn(ctx, "launch-1", nil, now.Add(time.Second))
	if err != nil || !found || next.ID != "turn-2" {
		t.Fatalf("claim second side turn: %#v, found=%v err=%v", next, found, err)
	}
	finished = make(chan struct{})
	go func() { manager.runTurn(runtime, side, next); close(finished) }()
	select {
	case providerTurnID = <-started:
		if providerTurnID != "provider-turn-2" {
			t.Fatalf("second provider turn = %q", providerTurnID)
		}
	case <-time.After(time.Second):
		t.Fatal("second side turn was rejected by the ACP host")
	}
	runtime.completed <- ports.ChatEvent{Kind: ports.ChatEventTurnCompleted,
		ProviderTurnID: providerTurnID, ProviderEventID: "event-2", TurnState: domain.TurnStateCompleted}
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("second side turn did not finish")
	}
	settled, err = manager.store.SideTurn(ctx, side.ID, next.ID)
	if err != nil || settled.State != "completed" {
		t.Fatalf("second side turn = %#v, %v", settled, err)
	}
}

func TestSideSchedulerStartsOtherSideBeforeCompletion(t *testing.T) {
	ctx := context.Background()
	svc := New(Options{AppRunID: "run"})
	m := svc.sides
	defer m.cancel()
	_, _ = m.store.ClaimSideLaunch(ctx, "run", time.Now())
	runtimes := map[string]*sideRuntime{}
	for _, id := range []string{"a", "b"} {
		side := domain.SideConversation{PolicyVersion: 1, ID: id, MainConversationID: id, AppRunID: "run", State: "ready", Generation: "g"}
		_, _, _ = m.store.CreateSideConversation(ctx, side)
		conv := &deferredSideTestConversation{started: make(chan string, 2), acknowledged: make(chan string, 2)}
		rt := &sideRuntime{side: side, conv: conv, completed: make(chan ports.ChatEvent, 2), messageText: map[string]string{}, messageIDs: map[string]string{}}
		m.runtimes[id] = rt
		runtimes[id] = rt
		_, _, _ = m.store.ReserveSideTurn(ctx, domain.SideTurn{ID: id, SideID: id, ClientMessageID: id, Text: "q", CreatedAt: time.Now()}, "run")
	}
	go m.schedule()
	m.signal()
	for _, id := range []string{"a", "b"} {
		select {
		case <-runtimes[id].conv.(*deferredSideTestConversation).started:
		case <-time.After(time.Second):
			t.Fatalf("side %s blocked by another side", id)
		}
	}
}

func TestSideRecoveryTimeoutKeepsDraftAndFails(t *testing.T) {
	svc := New(Options{AppRunID: "run"})
	m := svc.sides
	defer m.cancel()
	now := time.Now()
	_, _ = m.store.ClaimSideLaunch(context.Background(), "run", now)
	side := domain.SideConversation{PolicyVersion: 1, ID: "side", AppRunID: "run", SessionID: "missing", Generation: "g", State: "recovering"}
	_, _, _ = m.store.CreateSideConversation(context.Background(), side)
	_ = m.store.SetSideDraft(context.Background(), side.ID, "run", "draft", now)
	ctx, cancel := context.WithTimeout(m.ctx, 5*time.Millisecond)
	defer cancel()
	m.restore(ctx, side)
	after, err := m.store.SideConversation(context.Background(), side.ID)
	if err != nil || after.State != "failed" || after.ErrorMessage == "" {
		t.Fatalf("timed-out recovery: %+v %v", after, err)
	}
	draft, _ := m.store.SideDraft(context.Background(), side.ID)
	if draft != "draft" {
		t.Fatal("recovery discarded draft")
	}
}

func TestSideDisconnectSettlesRunningTurn(t *testing.T) {
	svc := New(Options{AppRunID: "run"})
	m := svc.sides
	defer m.cancel()
	now := time.Now()
	_, _ = m.store.ClaimSideLaunch(context.Background(), "run", now)
	side := domain.SideConversation{PolicyVersion: 1, ID: "side", AppRunID: "run", State: "ready", Generation: "g"}
	_, _, _ = m.store.CreateSideConversation(context.Background(), side)
	_, _, _ = m.store.ReserveSideTurn(context.Background(), domain.SideTurn{ID: "turn", SideID: "side", ClientMessageID: "client", Text: "q", CreatedAt: now}, "run")
	turn, _, _, _ := m.store.ClaimNextSideTurn(context.Background(), "run", nil, now)
	ctx, cancel := context.WithCancel(m.ctx)
	conv := &deferredSideTestConversation{started: make(chan string, 1), acknowledged: make(chan string, 1)}
	rt := &sideRuntime{side: side, conv: conv, ctx: ctx, done: ctx.Done(), completed: make(chan ports.ChatEvent)}
	finished := make(chan struct{})
	go func() { m.runTurn(rt, side, turn); close(finished) }()
	select {
	case <-conv.started:
	case <-time.After(time.Second):
		t.Fatal("turn not dispatched")
	}
	cancel()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("disconnected turn not stopped")
	}
	after, _ := m.store.SideTurn(context.Background(), side.ID, turn.ID)
	if after.State != "failed" {
		t.Fatalf("disconnected turn still %s", after.State)
	}
}

type sideSessionReader struct{}

func (sideSessionReader) GetSession(_ context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	return domain.SessionRecord{ID: id, Mode: domain.SessionModeChat}, true, nil
}

type recoveredSideTestConversation struct {
	deferredSideTestConversation
	events chan ports.ChatEvent
}

func (c *recoveredSideTestConversation) Events() <-chan ports.ChatEvent { return c.events }

func TestSideRecoveryResumesAcceptedTurnWithoutResending(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	before := newMemorySideStore()
	_, _ = before.ClaimSideLaunch(ctx, "launch", now)
	side := domain.SideConversation{PolicyVersion: 1, ID: "side", SessionID: "session", MainConversationID: "main", AppRunID: "launch", ProviderHostID: "btw-launch-side", ProviderForkID: "provider-side-1", State: "ready", Generation: "g"}
	_, _, _ = before.CreateSideConversation(ctx, side)
	_, _, _ = before.ReserveSideTurn(ctx, domain.SideTurn{ID: "turn", SideID: side.ID, Text: "question", ClientMessageID: "receipt", CreatedAt: now}, "launch")
	_, _, _, _ = before.ClaimNextSideTurn(ctx, "launch", nil, now)
	if err := before.SetSideTurnProviderID(ctx, side.ID, "turn", "g", "provider-turn-1"); err != nil {
		t.Fatal(err)
	}
	_ = before.UpsertSideMessage(ctx, domain.SideMessage{ID: "answer", SideID: side.ID, TurnID: "turn", ProviderItemID: "native-answer", Role: "assistant", Text: "partial", Streaming: true}, "g")
	raw, err := json.Marshal(before.export("launch"))
	if err != nil {
		t.Fatal(err)
	}
	var records []SideRecoveryRecord
	if err := json.Unmarshal(raw, &records); err != nil {
		t.Fatal(err)
	}
	svc := New(Options{AppRunID: "launch", Sessions: sideSessionReader{}})
	m := svc.sides
	defer m.stopAll()
	_, _ = m.store.ClaimSideLaunch(ctx, "launch", now)
	recovered, err := m.store.(*memorySideStore).recover("launch", records, now)
	if err != nil {
		t.Fatal(err)
	}
	conv := &recoveredSideTestConversation{events: make(chan ports.ChatEvent, 4)}
	if !m.attach(recovered[0], conv) {
		t.Fatal("recovered side did not attach")
	}
	conv.events <- ports.ChatEvent{Kind: ports.ChatEventMessageCompleted, ProviderTurnID: "provider-turn-1", ProviderItemID: "native-answer", Text: "complete answer"}
	conv.events <- ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "provider-turn-1", TurnState: domain.TurnStateCompleted}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		turn, _ := m.store.SideTurn(ctx, side.ID, "turn")
		if turn.State == "completed" {
			messages, _ := m.store.SideMessages(ctx, side.ID, []string{"turn"})
			if len(messages) != 2 || messages[1].ID != "answer" || messages[1].Text != "complete answer" {
				t.Fatalf("replayed projection duplicated or lost text: %+v", messages)
			}
			if conv.sent != 0 {
				t.Fatal("accepted turn was resent")
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("recovered provider completion did not settle accepted turn")
}

func TestSideSelfReferencesValidateAuthoritativePairedTurn(t *testing.T) {
	ctx := context.Background()
	svc := New(Options{AppRunID: "run"})
	store := svc.sides.store.(*memorySideStore)
	_, _ = store.ClaimSideLaunch(ctx, "run", time.Now())
	side, _, err := store.CreateSideConversation(ctx, domain.SideConversation{PolicyVersion: 1, ID: "side", AppRunID: "run", SessionID: "session", MainConversationID: "main", State: "ready", Generation: "g"})
	if err != nil {
		t.Fatal(err)
	}
	store.turns[side.ID] = []domain.SideTurn{{ID: "turn", SideID: side.ID, State: "completed"}}
	store.messages[side.ID] = []domain.SideMessage{{ID: "user", TurnID: "turn", Role: "user", Text: "Explain alpha", Revision: 1}, {ID: "assistant", TurnID: "turn", Role: "assistant", Text: "**Alpha** is [first](https://example.test).", Revision: 2}}
	for _, tc := range []struct {
		name, message, text string
		revision            int64
		want                error
	}{
		{"assistant markdown", "assistant", "Alpha is first", 2, nil}, {"user", "user", "alpha", 1, nil}, {"stale", "assistant", "Alpha", 1, ErrExcerptStale}, {"missing", "missing", "Alpha", 2, ErrExcerptStale}, {"mismatch", "assistant", "Beta", 2, ErrExcerptInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			refs, err := svc.validateSideReferences(ctx, "session", side, []ports.ChatExcerptReference{{ConversationID: side.ID, MessageID: tc.message, Revision: tc.revision, Text: tc.text}})
			if !errors.Is(err, tc.want) {
				t.Fatalf("error=%v want=%v", err, tc.want)
			}
			if err == nil && (len(refs) != 1 || !strings.Contains(refs[0].Context, "Explain alpha") || !strings.Contains(refs[0].Context, "**Alpha**")) {
				t.Fatalf("paired context=%+v", refs)
			}
		})
	}
	store.turns[side.ID][0].State = "running"
	_, err = svc.validateSideReferences(ctx, "session", side, []ports.ChatExcerptReference{{ConversationID: side.ID, MessageID: "assistant", Revision: 2, Text: "Alpha"}})
	if !errors.Is(err, ErrExcerptStale) {
		t.Fatalf("incomplete turn accepted: %v", err)
	}
}
func TestSideIdentityInstructionsAreStable(t *testing.T) {
	inherited := "Original system instructions"
	got := sideIdentityPrompt(inherited)
	if !strings.HasPrefix(got, inherited) || !strings.Contains(got, "frozen at the fork point") || !strings.Contains(got, sideConversationBoundary) {
		t.Fatal(got)
	}
	if sideIdentityPrompt(got) != got {
		t.Fatal("identity duplicated on reconnect")
	}
}
func TestSideNumberingResetsAfterAllTabsCloseAndIgnoresLegacyCounters(t *testing.T) {
	ctx := context.Background()
	svc := New(Options{AppRunID: "run"})
	store := svc.sides.store.(*memorySideStore)
	_, _ = store.ClaimSideLaunch(ctx, "run", time.Now())
	create := func(id string) domain.SideConversation {
		t.Helper()
		side, _, err := store.CreateSideConversation(ctx, domain.SideConversation{PolicyVersion: 1, ID: id, SessionID: "session", MainConversationID: "main", AppRunID: "run", ForceNew: true, State: "ready"})
		if err != nil {
			t.Fatal(err)
		}
		return side
	}
	one := create("one")
	if one.Label != "Side Chat" {
		t.Fatal(one.Label)
	}
	_, _ = store.CloseSideConversation(ctx, one.ID, time.Now())
	two := create("two")
	if two.Label != "Side Chat" {
		t.Fatal(two.Label)
	}
	_, _, _ = store.ReserveSideTurn(ctx, domain.SideTurn{ID: "q", SideID: two.ID, Text: "Do not rename me", ClientMessageID: "q"}, "run")
	two, _ = store.SideConversation(ctx, two.ID)
	if two.Label != "Side Chat" {
		t.Fatal(two.Label)
	}
	_ = store.RenameSide(ctx, two.ID, "Side Chat 99", time.Now())
	two, _ = store.SideConversation(ctx, two.ID)
	if two.Label != "Side Chat 99" {
		t.Fatal(two.Label)
	}
	_, _ = store.CloseSideConversation(ctx, two.ID, time.Now())
	numbers := svc.SideChatNumbers("run", nil)
	recovered := New(Options{AppRunID: "run"})
	_, _ = recovered.sides.store.ClaimSideLaunch(ctx, "run", time.Now())
	recovered.SideChatNumbers("run", numbers)
	third, _, err := recovered.sides.store.CreateSideConversation(ctx, domain.SideConversation{PolicyVersion: 1, ID: "third", SessionID: "session", MainConversationID: "main", AppRunID: "run", ForceNew: true})
	if err != nil || third.Label != "Side Chat" {
		t.Fatalf("recovery=%+v %v", third, err)
	}
}
