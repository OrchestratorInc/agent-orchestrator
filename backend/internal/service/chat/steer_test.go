package chat_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/store"
)

// Steering scenarios.
//
// The promise under test is not "the provider was called". It is that guidance goes
// to the turn the user is actually watching, that it shows up on the timeline so
// they can see it landed, and that every refusal is a typed answer rather than a
// failure — because the moment someone steers is the moment a turn is ending
// underneath them.

/* ---- a provider double that can be steered ----------------------------- */

type steerCall struct {
	turnID string
	msg    ports.ChatUserMessage
}

type steerRecorder struct {
	*fakeConversation

	mu     sync.Mutex
	calls  []steerCall
	err    error
	landed string
}

func newSteerRecorder() *steerRecorder {
	conv := newFakeConversation()
	caps := productionCaps()
	caps[ports.ChatCapabilitySteer] = true
	conv.setCapabilities(caps)
	return &steerRecorder{fakeConversation: conv}
}

func (s *steerRecorder) Steer(
	_ context.Context,
	providerTurnID string,
	msg ports.ChatUserMessage,
) (ports.ChatTurnRef, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, steerCall{turnID: providerTurnID, msg: msg})
	if s.err != nil {
		return ports.ChatTurnRef{}, s.err
	}
	landed := s.landed
	if landed == "" {
		landed = providerTurnID
	}
	return ports.ChatTurnRef{ProviderTurnID: landed}, nil
}

func (s *steerRecorder) steers() []steerCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]steerCall(nil), s.calls...)
}

func (s *steerRecorder) failWith(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.err = err
}

// steerHarness starts a session whose provider can be steered, and puts a turn in
// flight — the only state steering is meaningful in.
func steerHarness(t *testing.T) (*harness, *steerRecorder) {
	t.Helper()
	return steerHarnessWithStore(t, func(st *store.Store) chatsvc.Store { return st })
}

func steerHarnessWithStore(
	t *testing.T,
	wrapStore func(*store.Store) chatsvc.Store,
) (*harness, *steerRecorder) {
	t.Helper()
	provider := newSteerRecorder()
	h := newHarnessWithConversationAndStore(t, provider, wrapStore)

	if _, err := h.svc.Send(context.Background(), testSession, ports.ChatUserMessage{
		Text:            "do the long thing",
		ClientMessageID: "turn-1",
		Origin:          domain.MessageOriginHuman,
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	// The provider's own acknowledgement. Steering is refused for a turn the provider
	// has not announced, so nothing can be steered before this arrives.
	provider.emit(ports.ChatEvent{
		Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-1",
	})
	return h, provider
}

// interruptCounting replaces the fake's silent cancel with the two facts a real
// provider reports: which turn the cancel named, and how the cut turn settled.
type interruptCounting struct {
	*steerRecorder

	mu                sync.Mutex
	interrupts        []string
	interruptErr      error
	completionState   domain.TurnState
	settleOnInterrupt bool
}

func (s *interruptCounting) Interrupt(_ context.Context, providerTurnID string) error {
	s.mu.Lock()
	s.interrupts = append(s.interrupts, providerTurnID)
	interruptErr := s.interruptErr
	state := s.completionState
	noSettle := s.settleOnInterrupt
	s.mu.Unlock()
	if noSettle {
		return interruptErr
	}
	if state == "" {
		state = domain.TurnStateInterrupted
	}
	s.emit(ports.ChatEvent{
		Kind:           ports.ChatEventTurnCompleted,
		ProviderTurnID: providerTurnID,
		TurnState:      state,
	})
	return interruptErr
}

func (s *interruptCounting) failInterrupt(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.interruptErr = err
}

func (s *interruptCounting) interruptCalls() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.interrupts...)
}

func restartSteerService(
	t *testing.T,
	h *harness,
	provider *steerRecorder,
) *chatsvc.Service {
	t.Helper()
	if err := h.svc.Stop(context.Background(), testSession); err != nil {
		t.Fatalf("stop original service: %v", err)
	}
	var (
		idMu sync.Mutex
		id   int
	)
	svc := chatsvc.New(chatsvc.Options{
		Store: h.st, Sessions: h.st,
		Drivers: fakeRegistry{driver: fakeDriver{conv: provider}},
		Log:     slog.New(slog.DiscardHandler),
		NewID: func() string {
			idMu.Lock()
			defer idMu.Unlock()
			id++
			return fmt.Sprintf("restart-steer-%d", id)
		},
		Now: h.now,
	})
	if _, err := svc.Start(context.Background(), chatsvc.StartConfig{
		SessionID: testSession, ProjectID: testProject, Harness: domain.HarnessCodex,
		WorkspacePath: t.TempDir(), ProviderConversationID: "thread-1",
	}); err != nil {
		t.Fatalf("restart service: %v", err)
	}
	t.Cleanup(func() { _ = svc.Stop(context.Background(), testSession) })
	return svc
}

type failSteerCompletionStore struct {
	chatsvc.Store
}

func (s *failSteerCompletionStore) CompleteSteerDelivery(
	context.Context,
	string,
	string,
	string,
	domain.ConversationActivity,
	time.Time,
) error {
	return errors.New("injected steer completion failure")
}

// steerMarkers reads the steer entries out of a timeline the way a renderer must: by
// the discriminator in the detail payload. `system` is a general bucket, so the
// activity kind alone does not identify one.
func steerMarkers(s store.ConversationSnapshot) []struct {
	activity domain.ConversationActivity
	detail   struct {
		Event           string `json:"event"`
		Text            string `json:"text"`
		Origin          string `json:"origin"`
		ClientMessageID string `json:"clientMessageId"`
	}
} {
	type marker = struct {
		activity domain.ConversationActivity
		detail   struct {
			Event           string `json:"event"`
			Text            string `json:"text"`
			Origin          string `json:"origin"`
			ClientMessageID string `json:"clientMessageId"`
		}
	}
	var found []marker
	for _, a := range s.Activities {
		if a.Kind != domain.ActivityKindSystem || len(a.Detail) == 0 {
			continue
		}
		var m marker
		if err := json.Unmarshal(a.Detail, &m.detail); err != nil {
			continue
		}
		if m.detail.Event != "steer" {
			continue
		}
		m.activity = a
		found = append(found, m)
	}
	return found
}

/* ---- tests ------------------------------------------------------------- */

// The whole feature: guidance reaches the running turn and the timeline says so.
func TestSteerReachesTheRunningTurnAndLandsOnTheTimeline(t *testing.T) {
	h, provider := steerHarness(t)
	ctx := context.Background()

	result, err := h.svc.Steer(ctx, testSession, ports.ChatUserMessage{
		Text:            "actually, just summarize what you have",
		ClientMessageID: "steer-1",
		Origin:          domain.MessageOriginHuman,
	})
	if err != nil {
		t.Fatalf("Steer: %v", err)
	}
	if result.ProviderTurnID != "provider-turn-1" {
		t.Errorf("steered turn = %q, want provider-turn-1", result.ProviderTurnID)
	}
	if result.ActivityID == "" {
		t.Error("no activity id reported; a client cannot reconcile its own bubble")
	}

	calls := provider.steers()
	if len(calls) != 1 {
		t.Fatalf("provider saw %d steers, want 1", len(calls))
	}
	// The turn is named as a precondition rather than left to the provider to guess,
	// which is what stops a correction landing on work the user was not watching.
	if calls[0].turnID != "provider-turn-1" {
		t.Errorf("steered turn id = %q, want provider-turn-1", calls[0].turnID)
	}
	if calls[0].msg.Text != "actually, just summarize what you have" {
		t.Errorf("steer text = %q", calls[0].msg.Text)
	}
	if calls[0].msg.ClientMessageID != "steer-1" {
		t.Errorf("idempotency handle = %q, want steer-1", calls[0].msg.ClientMessageID)
	}

	snapshot := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return len(steerMarkers(s)) == 1
	})
	markers := steerMarkers(snapshot)
	if markers[0].detail.Text != "actually, just summarize what you have" {
		t.Errorf("recorded text = %q", markers[0].detail.Text)
	}
	if markers[0].detail.Origin != string(domain.MessageOriginHuman) {
		t.Errorf("recorded origin = %q, want human", markers[0].detail.Origin)
	}
	if markers[0].detail.ClientMessageID != "steer-1" {
		t.Errorf("recorded client message id = %q", markers[0].detail.ClientMessageID)
	}
	if markers[0].activity.Summary == "" {
		t.Error("the row has no summary; a collapsed timeline would show an empty entry")
	}

	// Bound to the turn it steered, not floating: an unattached row would leave the
	// guidance rendering outside the conversation it changed.
	var running string
	for _, turn := range snapshot.Turns {
		if turn.ProviderTurnID == "provider-turn-1" {
			running = turn.ID
		}
	}
	if running == "" {
		t.Fatalf("no turn row for provider-turn-1:\n%+v", snapshot.Turns)
	}
	if markers[0].activity.TurnID != running {
		t.Errorf("steer recorded on turn %q, want the running turn %q",
			markers[0].activity.TurnID, running)
	}

	// And it must not have opened a turn of its own. A second turn row would be
	// dispatched by the drain loop later, sending the correction twice.
	if len(snapshot.Turns) != 1 {
		t.Errorf("steering produced %d turns, want 1:\n%+v", len(snapshot.Turns), snapshot.Turns)
	}
}

func TestSteerOrSendSteersAndRecoversOneAtomicOutcome(t *testing.T) {
	h, provider := steerHarness(t)
	msg := ports.ChatUserMessage{
		Text: "correct the active work", ClientMessageID: "atomic-steer-1",
		Origin: domain.MessageOriginHuman,
	}

	first, err := h.svc.SteerOrSend(context.Background(), testSession, msg, false)
	if err != nil {
		t.Fatalf("SteerOrSend: %v", err)
	}
	if !first.Steered || first.Steer.ProviderTurnID != "provider-turn-1" {
		t.Fatalf("result = %+v, want steered active turn", first)
	}
	recovered, err := h.svc.SteerOrSend(context.Background(), testSession,
		ports.ChatUserMessage{ClientMessageID: msg.ClientMessageID}, true)
	if err != nil {
		t.Fatalf("recover SteerOrSend: %v", err)
	}
	if !recovered.Steered || !recovered.Duplicate || recovered.Steer != first.Steer {
		t.Fatalf("recovered = %+v, want %+v", recovered, first)
	}
	if calls := provider.steers(); len(calls) != 1 {
		t.Fatalf("provider received %d steers, want one", len(calls))
	}
}

func TestSteerOrSendSendsWhenIdleAndRecoversWithoutRedispatch(t *testing.T) {
	provider := newSteerRecorder()
	h := newHarnessWithConversation(t, provider)
	msg := ports.ChatUserMessage{
		Text: "start the next work", ClientMessageID: "atomic-send-1",
		Origin: domain.MessageOriginHuman,
	}

	first, err := h.svc.SteerOrSend(context.Background(), testSession, msg, false)
	if err != nil {
		t.Fatalf("SteerOrSend: %v", err)
	}
	if first.Steered || first.Turn.ID == "" || first.Turn.State != domain.TurnStateRunning {
		t.Fatalf("result = %+v, want running normal turn", first)
	}
	recovered, err := h.svc.SteerOrSend(context.Background(), testSession,
		ports.ChatUserMessage{ClientMessageID: msg.ClientMessageID}, true)
	if err != nil {
		t.Fatalf("recover SteerOrSend: %v", err)
	}
	if recovered.Steered || !recovered.Duplicate || recovered.Turn.ID != first.Turn.ID {
		t.Fatalf("recovered = %+v, want sent turn %s", recovered, first.Turn.ID)
	}
	for _, changed := range []ports.ChatUserMessage{
		{Text: "different work", ClientMessageID: msg.ClientMessageID, Origin: msg.Origin},
		{Text: msg.Text, ClientMessageID: msg.ClientMessageID, Origin: msg.Origin,
			Content: []ports.ChatContent{{Type: "image", Data: "aGVsbG8=", MIMEType: "image/png"}}},
		{Text: msg.Text, ClientMessageID: msg.ClientMessageID, Origin: msg.Origin, AuthoredByUser: true},
		{Text: msg.Text, ClientMessageID: msg.ClientMessageID, Origin: msg.Origin,
			Settings: ports.ChatTurnSettings{Model: "different-model"}},
	} {
		if _, err := h.svc.SteerOrSend(context.Background(), testSession, changed, false); !errors.Is(err, domain.ErrClientMessageConflict) {
			t.Fatalf("changed retry error = %v, want client message conflict", err)
		}
	}
	if calls := provider.sendCallCount(); calls != 1 {
		t.Fatalf("provider received %d sends, want one", calls)
	}
	if calls := provider.steers(); len(calls) != 0 {
		t.Fatalf("provider received %d steers, want none", len(calls))
	}
}

func TestSteerOrSendFallsBackWithoutLeavingAQueuedTurn(t *testing.T) {
	h, provider := steerHarness(t)
	provider.failWith(ports.ErrChatNoSteerableTurn)
	msg := ports.ChatUserMessage{
		Text: "continue as the next turn", ClientMessageID: "atomic-race-1",
		Origin: domain.MessageOriginHuman,
	}

	result, err := h.svc.SteerOrSend(context.Background(), testSession, msg, false)
	if err != nil {
		t.Fatalf("SteerOrSend: %v", err)
	}
	if result.Steered || result.Turn.ID == "" || result.Turn.State != domain.TurnStateRunning {
		t.Fatalf("result = %+v, want definitive running fallback", result)
	}
	recovered, err := h.svc.SteerOrSend(context.Background(), testSession,
		ports.ChatUserMessage{ClientMessageID: msg.ClientMessageID}, true)
	if err != nil {
		t.Fatalf("recover SteerOrSend: %v", err)
	}
	if recovered.Steered || recovered.Turn.ID != result.Turn.ID || !recovered.Duplicate {
		t.Fatalf("recovered = %+v, want sent turn %s", recovered, result.Turn.ID)
	}
	if calls := provider.sendCallCount(); calls != 2 {
		t.Fatalf("provider send calls = %d, want initial turn plus one fallback", calls)
	}
}

// A retry with the same handle is the same guidance, not a second piece of it.
func TestSteerIsIdempotentOnTheClientHandle(t *testing.T) {
	h, provider := steerHarness(t)
	ctx := context.Background()

	msg := ports.ChatUserMessage{Text: "narrow the search", ClientMessageID: "steer-retry"}
	first, err := h.svc.Steer(ctx, testSession, msg)
	if err != nil {
		t.Fatalf("first Steer: %v", err)
	}
	replayed, err := h.svc.Steer(ctx, testSession, msg)
	if err != nil {
		t.Fatalf("retried Steer: %v", err)
	}
	if replayed != first {
		t.Fatalf("replayed result = %+v, want original %+v", replayed, first)
	}
	if calls := provider.steers(); len(calls) != 1 {
		t.Fatalf("provider received %d steer attempts, want one", len(calls))
	}

	snapshot := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return len(steerMarkers(s)) >= 1
	})
	if got := len(steerMarkers(snapshot)); got != 1 {
		t.Errorf("a retried steer produced %d timeline entries, want 1", got)
	}
}

func TestAcceptedSteerReplaysAfterControllerRestartWithoutProviderRedispatch(t *testing.T) {
	h, provider := steerHarness(t)
	msg := ports.ChatUserMessage{Text: "narrow the search", ClientMessageID: "steer-restart"}

	first, err := h.svc.Steer(context.Background(), testSession, msg)
	if err != nil {
		t.Fatalf("first Steer: %v", err)
	}
	if calls := provider.steers(); len(calls) != 1 {
		t.Fatalf("original provider received %d steer attempts, want one", len(calls))
	}

	restartedProvider := newSteerRecorder()
	restarted := restartSteerService(t, h, restartedProvider)
	replayed, err := restarted.Steer(context.Background(), testSession, msg)
	if err != nil {
		t.Fatalf("Steer after restart: %v", err)
	}
	if replayed != first {
		t.Fatalf("replayed result = %+v, want original %+v", replayed, first)
	}
	if calls := restartedProvider.steers(); len(calls) != 0 {
		t.Fatalf("restarted provider received %d steer attempts, want none", len(calls))
	}
}

func TestReservedSteerStaysUncertainAcrossRetryAndRestart(t *testing.T) {
	var flaky *failSteerCompletionStore
	h, provider := steerHarnessWithStore(t, func(st *store.Store) chatsvc.Store {
		flaky = &failSteerCompletionStore{Store: st}
		return flaky
	})
	msg := ports.ChatUserMessage{Text: "narrow the search", ClientMessageID: "steer-unknown"}

	_, err := h.svc.Steer(context.Background(), testSession, msg)
	if !errors.Is(err, chatsvc.ErrSteerDeliveryUncertain) {
		t.Fatalf("first Steer error = %v, want ErrSteerDeliveryUncertain", err)
	}
	_, err = h.svc.Steer(context.Background(), testSession, msg)
	if !errors.Is(err, chatsvc.ErrSteerDeliveryUncertain) {
		t.Fatalf("same-process retry error = %v, want ErrSteerDeliveryUncertain", err)
	}
	if calls := provider.steers(); len(calls) != 1 {
		t.Fatalf("provider received %d steer attempts after retry, want one", len(calls))
	}

	restartedProvider := newSteerRecorder()
	restarted := restartSteerService(t, h, restartedProvider)
	_, err = restarted.Steer(context.Background(), testSession, msg)
	if !errors.Is(err, chatsvc.ErrSteerDeliveryUncertain) {
		t.Fatalf("restart retry error = %v, want ErrSteerDeliveryUncertain", err)
	}
	if calls := restartedProvider.steers(); len(calls) != 0 {
		t.Fatalf("restarted provider received %d steer attempts, want none", len(calls))
	}
}

func TestSteerClientHandleCannotBeReusedForDifferentGuidance(t *testing.T) {
	h, provider := steerHarness(t)
	if _, err := h.svc.Steer(context.Background(), testSession, ports.ChatUserMessage{
		Text: "narrow the search", ClientMessageID: "steer-collision",
	}); err != nil {
		t.Fatalf("first Steer: %v", err)
	}
	_, err := h.svc.Steer(context.Background(), testSession, ports.ChatUserMessage{
		Text: "search everything", ClientMessageID: "steer-collision",
	})
	if !errors.Is(err, chatsvc.ErrSteerIdempotencyConflict) {
		t.Fatalf("changed retry error = %v, want ErrSteerIdempotencyConflict", err)
	}
	if calls := provider.steers(); len(calls) != 1 {
		t.Fatalf("provider received %d steer attempts, want one", len(calls))
	}
}

// A handoff refusal belongs to the original delivery handle. If the 409 response
// is lost, retrying after the source controller reopens or a new controller starts
// must replay that refusal rather than steering whichever turn happens to be live.
func TestSteerDuringInterfaceTransitionDurablyReplaysWithoutProviderDispatch(t *testing.T) {
	h, provider := steerHarness(t)
	msg := ports.ChatUserMessage{
		Text: "guidance typed during the switch", ClientMessageID: "steer-handoff",
		Origin: domain.MessageOriginHuman,
	}
	if err := h.ctrl.ArmHandoff(
		context.Background(), domain.SessionInterfaceTransitionDrain); err != nil {
		t.Fatalf("ArmHandoff: %v", err)
	}

	_, err := h.svc.Steer(context.Background(), testSession, msg)
	if !errors.Is(err, chatsvc.ErrControllerHandoff) {
		t.Fatalf("Steer during handoff error = %v, want ErrControllerHandoff", err)
	}
	if calls := provider.steers(); len(calls) != 0 {
		t.Fatalf("provider received %d steers during handoff, want none", len(calls))
	}

	// Model a lost 409: the caller did not observe it and retries only after the
	// transition was abandoned. The durable result still wins over current state.
	h.svc.AbortChatHandoff(testSession)
	_, err = h.svc.Steer(context.Background(), testSession, msg)
	if !errors.Is(err, chatsvc.ErrControllerHandoff) {
		t.Fatalf("same-controller replay error = %v, want durable ErrControllerHandoff", err)
	}
	if calls := provider.steers(); len(calls) != 0 {
		t.Fatalf("provider received %d steers after handoff reopened, want none", len(calls))
	}

	restartedProvider := newSteerRecorder()
	restarted := restartSteerService(t, h, restartedProvider)
	_, err = restarted.Steer(context.Background(), testSession, msg)
	if !errors.Is(err, chatsvc.ErrControllerHandoff) {
		t.Fatalf("new-controller replay error = %v, want durable ErrControllerHandoff", err)
	}
	if calls := restartedProvider.steers(); len(calls) != 0 {
		t.Fatalf("new provider received %d steers for prior handoff refusal, want none", len(calls))
	}
}

// Nothing in flight is an ordinary outcome — the turn finished while the user was
// typing — and the provider must not be asked.
func TestSteerWithNothingInFlightIsTypedAndNeverReachesTheProvider(t *testing.T) {
	provider := newSteerRecorder()
	h := newHarnessWithConversation(t, provider)
	msg := ports.ChatUserMessage{Text: "too late", ClientMessageID: "steer-no-active"}

	_, err := h.svc.Steer(context.Background(), testSession, msg)
	if !errors.Is(err, chatsvc.ErrNoActiveTurn) {
		t.Fatalf("err = %v, want ErrNoActiveTurn", err)
	}
	if len(provider.steers()) != 0 {
		t.Error("asked the provider to steer with no turn in flight")
	}

	// Even if a different turn starts before recovery, the original handle owns the
	// durable refusal. A lost 409 must not turn into guidance for later work.
	if _, err := h.svc.Send(context.Background(), testSession, ports.ChatUserMessage{
		Text: "later work", ClientMessageID: "later-turn",
	}); err != nil {
		t.Fatalf("start later turn: %v", err)
	}
	provider.emit(ports.ChatEvent{
		Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-1",
	})
	_, err = h.svc.Steer(context.Background(), testSession, msg)
	if !errors.Is(err, chatsvc.ErrNoActiveTurn) {
		t.Fatalf("retry after later turn error = %v, want durable ErrNoActiveTurn", err)
	}
	if len(provider.steers()) != 0 {
		t.Error("a recovered refusal was delivered into a later turn")
	}
}

func TestSteerOrSendIdleCrossSessionUsesAutomationOrigin(t *testing.T) {
	provider := newSteerRecorder()
	h := newHarnessWithConversation(t, provider)

	_, err := h.svc.SteerOrSend(context.Background(), testSession, ports.ChatUserMessage{
		Text:              "[from worker-1] use the simpler approach",
		ClientMessageID:   "idle-cross-session-steer",
		Origin:            domain.MessageOriginHuman,
		SenderSessionID:   "worker-1",
		SenderProjectID:   "project-1",
		SenderDisplayName: "Backend worker",
	}, false)
	if err != nil {
		t.Fatalf("SteerOrSend: %v", err)
	}

	snapshot, err := h.st.LoadConversationSnapshot(context.Background(), h.ctrl.ConversationID())
	if err != nil {
		t.Fatalf("load snapshot: %v", err)
	}
	if len(snapshot.Messages) == 0 {
		t.Fatal("idle steer did not persist a message")
	}
	message := snapshot.Messages[len(snapshot.Messages)-1]
	if message.Origin != domain.MessageOriginAutomation {
		t.Fatalf("idle cross-session steer origin = %q, want automation", message.Origin)
	}
	if message.SenderSessionID != "worker-1" || message.SenderProjectID != "project-1" || message.SenderDisplayName != "Backend worker" {
		t.Fatalf("idle cross-session sender metadata = (%q, %q, %q)", message.SenderSessionID, message.SenderProjectID, message.SenderDisplayName)
	}
}

// The provider is the authority on whether its turn is still steerable, and losing
// that race must read as "nothing to steer", not as a failure.
func TestSteerRaceLostToTheProviderIsReportedAsNoActiveTurn(t *testing.T) {
	h, provider := steerHarness(t)
	provider.failWith(ports.ErrChatNoSteerableTurn)
	msg := ports.ChatUserMessage{Text: "guidance", ClientMessageID: "steer-refused"}

	_, err := h.svc.Steer(context.Background(), testSession, msg)
	if !errors.Is(err, chatsvc.ErrNoActiveTurn) {
		t.Fatalf("err = %v, want ErrNoActiveTurn", err)
	}
	_, err = h.svc.Steer(context.Background(), testSession, msg)
	if !errors.Is(err, chatsvc.ErrNoActiveTurn) {
		t.Fatalf("retried err = %v, want ErrNoActiveTurn", err)
	}
	if calls := provider.steers(); len(calls) != 1 {
		t.Fatalf("provider received %d refused steer attempts, want one", len(calls))
	}

	// Nothing recorded: a timeline claiming guidance the agent never received would
	// have the user waiting for an answer to something it never heard.
	snapshot, loadErr := h.st.LoadConversationSnapshot(context.Background(), h.ctrl.ConversationID())
	if loadErr != nil {
		t.Fatalf("load snapshot: %v", loadErr)
	}
	if got := len(steerMarkers(snapshot)); got != 0 {
		t.Errorf("recorded %d steers for a refused one", got)
	}

	restartedProvider := newSteerRecorder()
	restarted := restartSteerService(t, h, restartedProvider)
	_, err = restarted.Steer(context.Background(), testSession, msg)
	if !errors.Is(err, chatsvc.ErrNoActiveTurn) {
		t.Fatalf("restart retry error = %v, want ErrNoActiveTurn", err)
	}
	if calls := restartedProvider.steers(); len(calls) != 0 {
		t.Fatalf("restarted provider received %d refused steer attempts, want none", len(calls))
	}
}

// A turn that is running but cannot take guidance (a compaction, a review) is a
// different answer: retryable once it ends, so it keeps its own sentinel.
func TestSteerOfAnUnsteerableTurnKeepsItsOwnOutcome(t *testing.T) {
	h, provider := steerHarness(t)
	provider.failWith(ports.ErrChatTurnNotSteerable)

	_, err := h.svc.Steer(context.Background(), testSession,
		ports.ChatUserMessage{Text: "guidance"})
	if !errors.Is(err, chatsvc.ErrTurnNotSteerable) {
		t.Fatalf("err = %v, want ErrTurnNotSteerable", err)
	}
	if errors.Is(err, chatsvc.ErrNoActiveTurn) {
		t.Error("an unsteerable running turn was reported as no turn at all")
	}
}

// A definitive provider refusal (for example ACP JSON-RPC method-not-found for
// an optional steering extension) must settle the reserved delivery handle. Retry
// and recover-only must replay that refusal without another provider call.
func TestSteerProviderRefusalSettlesAndRecoversWithoutRedispatch(t *testing.T) {
	h, provider := steerHarness(t)
	provider.failWith(refusedError{msg: `"Method not found": _session/steering`})
	msg := ports.ChatUserMessage{Text: "guidance", ClientMessageID: "steer-refused-provider"}

	_, err := h.svc.Steer(context.Background(), testSession, msg)
	if !errors.Is(err, chatsvc.ErrProviderRefused) {
		t.Fatalf("err = %v, want ErrProviderRefused", err)
	}
	_, err = h.svc.Steer(context.Background(), testSession, msg)
	if !errors.Is(err, chatsvc.ErrProviderRefused) {
		t.Fatalf("retried err = %v, want ErrProviderRefused", err)
	}
	if calls := provider.steers(); len(calls) != 1 {
		t.Fatalf("provider received %d refused steer attempts, want one", len(calls))
	}

	recovered, recoverErr := h.svc.RecoverSteer(context.Background(), testSession, msg.ClientMessageID)
	if !errors.Is(recoverErr, chatsvc.ErrProviderRefused) {
		t.Fatalf("RecoverSteer error = %v, want ErrProviderRefused", recoverErr)
	}
	if recovered != (chatsvc.SteerResult{}) {
		t.Fatalf("RecoverSteer result = %+v, want empty settled refusal", recovered)
	}
	if calls := provider.steers(); len(calls) != 1 {
		t.Fatalf("provider received %d attempts after recover, want one", len(calls))
	}

	restartedProvider := newSteerRecorder()
	restarted := restartSteerService(t, h, restartedProvider)
	_, err = restarted.Steer(context.Background(), testSession, msg)
	if !errors.Is(err, chatsvc.ErrProviderRefused) {
		t.Fatalf("restart retry error = %v, want ErrProviderRefused", err)
	}
	if calls := restartedProvider.steers(); len(calls) != 0 {
		t.Fatalf("restarted provider received %d refused steer attempts, want none", len(calls))
	}
}

// Ambiguous transport failures must leave the reserved handle uncertain so AO
// neither claims delivery nor automatically resends guidance that may already
// have reached the provider.
func TestSteerTransportFailureStaysUncertain(t *testing.T) {
	h, provider := steerHarness(t)
	transportErr := errors.New("connection reset while waiting for steer reply")
	provider.failWith(transportErr)
	msg := ports.ChatUserMessage{Text: "guidance", ClientMessageID: "steer-transport"}

	_, err := h.svc.Steer(context.Background(), testSession, msg)
	if !errors.Is(err, chatsvc.ErrSteerDeliveryUncertain) {
		t.Fatalf("err = %v, want ErrSteerDeliveryUncertain", err)
	}
	if !errors.Is(err, transportErr) {
		t.Fatalf("err = %v, want transport cause preserved", err)
	}
	_, err = h.svc.Steer(context.Background(), testSession, msg)
	if !errors.Is(err, chatsvc.ErrSteerDeliveryUncertain) {
		t.Fatalf("retried err = %v, want ErrSteerDeliveryUncertain", err)
	}
	if calls := provider.steers(); len(calls) != 1 {
		t.Fatalf("provider received %d steer attempts after retry, want one", len(calls))
	}

	_, recoverErr := h.svc.RecoverSteer(context.Background(), testSession, msg.ClientMessageID)
	if !errors.Is(recoverErr, chatsvc.ErrSteerDeliveryUncertain) {
		t.Fatalf("RecoverSteer error = %v, want ErrSteerDeliveryUncertain", recoverErr)
	}
	if calls := provider.steers(); len(calls) != 1 {
		t.Fatalf("provider received %d attempts after recover, want one", len(calls))
	}
}

// A provider with no steering at all: a permanent answer, so a client hides the
// control instead of retrying.
func TestSteerIsRefusedWhenTheDriverCannotDoIt(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	msg := ports.ChatUserMessage{Text: "guidance", ClientMessageID: "steer-unsupported"}

	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "do the long thing", ClientMessageID: "turn-1",
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-1"})

	_, err := h.svc.Steer(ctx, testSession, msg)
	if !errors.Is(err, chatsvc.ErrSteerUnsupported) {
		t.Fatalf("err = %v, want ErrSteerUnsupported", err)
	}

	restartedProvider := newSteerRecorder()
	restarted := restartSteerService(t, h, restartedProvider)
	_, err = restarted.Steer(ctx, testSession, msg)
	if !errors.Is(err, chatsvc.ErrSteerUnsupported) {
		t.Fatalf("restart retry error = %v, want durable ErrSteerUnsupported", err)
	}
	if calls := restartedProvider.steers(); len(calls) != 0 {
		t.Fatalf("restarted capable provider received %d attempts for a prior refusal, want none", len(calls))
	}
}

// The ACP driver implements Steer for every agent, but only an agent that
// advertised the extension can take it. Without the capability AO refuses before
// reserving a delivery, so the agent is never called and the answer is not
// "uncertain".
func TestSteerIsRefusedWhenTheProviderDidNotAdvertiseIt(t *testing.T) {
	provider := &interruptCounting{steerRecorder: newSteerRecorder()}
	h := newHarnessWithConversation(t, provider)
	provider.setCapabilities(productionCaps())
	ctx := context.Background()

	_, err := h.svc.Steer(ctx, testSession, ports.ChatUserMessage{
		Text: "guidance", ClientMessageID: "steer-unadvertised",
	})
	if !errors.Is(err, chatsvc.ErrSteerUnsupported) {
		t.Fatalf("Steer err = %v, want ErrSteerUnsupported", err)
	}

	// Promoting a queued turn is NOT gated on the steering capability: it cuts
	// the running turn through the interrupt seam and dispatches the message as
	// the next real turn, on every driver.
	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "do the long thing", ClientMessageID: "turn-1", Origin: domain.MessageOriginHuman,
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	provider.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-1"})
	queued, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "queued", ClientMessageID: "queued-1", Origin: domain.MessageOriginHuman,
	})
	if err != nil {
		t.Fatalf("queue: %v", err)
	}
	result, err := h.svc.PromoteQueuedTurn(ctx, testSession, queued.ID)
	if err != nil {
		t.Fatalf("PromoteQueuedTurn: %v", err)
	}
	if result.InterruptedTurnID != "provider-turn-1" {
		t.Fatalf("promotion result = %+v, want the running turn cut", result)
	}
	if calls := provider.steers(); len(calls) != 0 {
		t.Fatalf("provider received %d steer attempts, want none", len(calls))
	}
}

func TestSteerRejectsEmptyText(t *testing.T) {
	h, provider := steerHarness(t)

	_, err := h.svc.Steer(context.Background(), testSession,
		ports.ChatUserMessage{Text: "  \n "})
	if !errors.Is(err, chatsvc.ErrSteerTextRequired) {
		t.Fatalf("err = %v, want ErrSteerTextRequired", err)
	}
	if len(provider.steers()) != 0 {
		t.Error("sent an empty steer to the provider")
	}
}

// The trap this waits for is real: the provider refuses a steer for a turn it has
// accepted but not yet announced, and steering is most useful in exactly that
// window. So a steer that arrives between dispatch and acknowledgement must WAIT for
// the acknowledgement rather than being refused or fired early.
func TestSteerWaitsForTheProviderToAcknowledgeTheTurn(t *testing.T) {
	provider := newSteerRecorder()
	h := newHarnessWithConversation(t, provider)
	ctx := context.Background()

	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "do the long thing", ClientMessageID: "turn-1",
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	// Dispatched but NOT acknowledged: no turn/started has arrived.

	done := make(chan error, 1)
	go func() {
		_, err := h.svc.Steer(ctx, testSession, ports.ChatUserMessage{Text: "guidance"})
		done <- err
	}()

	select {
	case err := <-done:
		t.Fatalf("steer resolved before the provider acknowledged the turn (err=%v); "+
			"the provider would have refused it", err)
	case <-time.After(150 * time.Millisecond):
	}

	provider.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-1"})

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Steer after acknowledgement: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("steer never completed after the turn was acknowledged")
	}

	calls := provider.steers()
	if len(calls) != 1 || calls[0].turnID != "provider-turn-1" {
		t.Fatalf("provider saw %+v, want one steer for provider-turn-1", calls)
	}
}

// The provider names the turn its guidance joined, and AO attributes the row to
// that turn rather than to the one it asked about. Same id in practice; asserted so
// a provider that answered differently could not be silently misfiled.
func TestSteerRecordsTheTurnTheProviderNames(t *testing.T) {
	h, provider := steerHarness(t)
	provider.mu.Lock()
	provider.landed = "provider-turn-1"
	provider.mu.Unlock()

	result, err := h.svc.Steer(context.Background(), testSession,
		ports.ChatUserMessage{Text: "guidance"})
	if err != nil {
		t.Fatalf("Steer: %v", err)
	}
	if result.ProviderTurnID != "provider-turn-1" {
		t.Errorf("reported turn = %q, want the one the provider named", result.ProviderTurnID)
	}
}

// Promoting a selected queued turn must use AO's durable content, attach it to
// the running provider turn, and remove only that source turn from the visible
// queue. If this regresses to queue-head-only behavior, the second message below
// is never the one the provider receives.
func TestSteerQueuedTurnCutsTheRunningTurnAndPromotesTheMessage(t *testing.T) {
	provider := &interruptCounting{steerRecorder: newSteerRecorder()}
	// Deliberately WITHOUT the steer capability: promotion rides the shared
	// interrupt seam every driver has, not the steering extension.
	provider.setCapabilities(productionCaps())
	h := newHarnessWithConversation(t, provider)
	ctx := context.Background()

	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "do the long thing", ClientMessageID: "turn-1", Origin: domain.MessageOriginHuman,
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	provider.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-1"})

	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "first queued", ClientMessageID: "queued-1", Origin: domain.MessageOriginHuman,
	}); err != nil {
		t.Fatalf("queue first: %v", err)
	}
	// Queue order is a timestamp ordering; the injected clock must move for the
	// promotion's reorder to be able to jump the queue.
	h.advance(time.Second)
	selected, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "second queued", ClientMessageID: "queued-2", Origin: domain.MessageOriginHuman,
		Content: []ports.ChatContent{{Type: "image", Data: "aGVsbG8=", MIMEType: "image/png"}},
	})
	if err != nil {
		t.Fatalf("queue selected: %v", err)
	}

	result, err := h.svc.PromoteQueuedTurn(ctx, testSession, selected.ID)
	if err != nil {
		t.Fatalf("PromoteQueuedTurn: %v", err)
	}
	if result.SourceTurnID != selected.ID ||
		result.InterruptedTurnID != "provider-turn-1" ||
		result.ProviderTurnID != "provider-turn-2" {
		t.Fatalf("promotion result = %+v", result)
	}
	if calls := provider.interruptCalls(); len(calls) != 1 || calls[0] != "provider-turn-1" {
		t.Fatalf("provider interrupts = %v, want exactly provider-turn-1", calls)
	}
	if len(provider.steers()) != 0 {
		t.Fatalf("promotion contacted the mid-turn steer seam: %+v", provider.steers())
	}

	snapshot := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		for _, turn := range s.Turns {
			if turn.ID == selected.ID {
				return turn.State == domain.TurnStateRunning
			}
		}
		return false
	})
	var cut *domain.ConversationTurn
	for index := range snapshot.Turns {
		if snapshot.Turns[index].ProviderTurnID == "provider-turn-1" {
			cut = &snapshot.Turns[index]
		}
	}
	if cut == nil || cut.State != domain.TurnStateInterrupted {
		t.Fatalf("cut turn = %+v, want provider-turn-1 interrupted", cut)
	}

	sent := provider.sentTexts()
	// The settlement drain claims the whole queue into one batched prompt, so the
	// promoted message and the rest of the queue go out together on the next turn.
	if len(sent) != 2 || !strings.Contains(sent[1], `"second queued"`) || !strings.Contains(sent[1], `"first queued"`) {
		t.Fatalf("provider received %q, want the promoted durable message in the drained batch", sent)
	}
	if next, err := h.st.NextQueuedTurn(ctx, h.ctrl.ConversationID()); !errors.Is(err, domain.ErrNoQueuedTurn) {
		t.Fatalf("queue after the drained promotion = %+v, %v; want empty", next, err)
	}
}

// The provider is the authority on whether a turn is still running. When it
// answers the cancel with "nothing active" — the turn settled on its own in the
// race window — the interrupt is a no-op and the request degrades to a plain
// promotion. Nothing the provider completed may be marked interrupted.
func TestSteerQueuedTurnDegradesToPlainPromotionWhenTheCancelLostTheRace(t *testing.T) {
	provider := &interruptCounting{steerRecorder: newSteerRecorder()}
	h := newHarnessWithConversation(t, provider)
	ctx := context.Background()

	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "do the long thing", ClientMessageID: "turn-1", Origin: domain.MessageOriginHuman,
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	provider.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-1"})
	selected, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "take me now", ClientMessageID: "queued-1", Origin: domain.MessageOriginHuman,
	})
	if err != nil {
		t.Fatalf("queue: %v", err)
	}

	// The cancel lands on a turn the provider has just completed on its own: the
	// refusal says so, and the completion that beat the cancel settles the turn
	// completed rather than interrupted.
	provider.mu.Lock()
	provider.completionState = domain.TurnStateCompleted
	provider.mu.Unlock()
	provider.failInterrupt(ports.ErrChatNoActiveTurn)

	result, err := h.svc.PromoteQueuedTurn(ctx, testSession, selected.ID)
	if err != nil {
		t.Fatalf("PromoteQueuedTurn: %v", err)
	}
	if result.InterruptedTurnID != "" {
		t.Fatalf("interruptedTurnId = %q, want empty: the turn completed on its own", result.InterruptedTurnID)
	}

	snapshot := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		for _, turn := range s.Turns {
			if turn.ID == selected.ID {
				return turn.State == domain.TurnStateRunning
			}
		}
		return false
	})
	for _, turn := range snapshot.Turns {
		if turn.ProviderTurnID == "provider-turn-1" && turn.State != domain.TurnStateCompleted {
			t.Fatalf("raced turn = %+v, want completed without an interrupted marker", turn)
		}
	}
	if sent := provider.sentTexts(); len(sent) != 2 || sent[1] != "take me now" {
		t.Fatalf("provider received %q, want the promoted message dispatched after the race", sent)
	}
}

// A queue can outlive the turn it was queued behind when that turn fails: the
// settle holds the queue on purpose, and there is nothing running to cut. Steer
// on the stranded head is then just "send this now".
func TestSteerQueuedTurnWithNothingRunningIsAPlainPromotion(t *testing.T) {
	provider := &interruptCounting{steerRecorder: newSteerRecorder()}
	h := newHarnessWithConversation(t, provider)
	ctx := context.Background()

	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "breaks", ClientMessageID: "turn-1", Origin: domain.MessageOriginHuman,
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	provider.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-1"})
	queued, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "stranded behind the failure", ClientMessageID: "queued-1", Origin: domain.MessageOriginHuman,
	})
	if err != nil {
		t.Fatalf("queue: %v", err)
	}
	provider.emit(ports.ChatEvent{
		Kind:           ports.ChatEventTurnCompleted,
		ProviderTurnID: "provider-turn-1",
		Err:            errors.New("provider exploded"),
	})

	result, err := h.svc.PromoteQueuedTurn(ctx, testSession, queued.ID)
	if err != nil {
		t.Fatalf("PromoteQueuedTurn: %v", err)
	}
	if result.InterruptedTurnID != "" || result.ProviderTurnID != "provider-turn-2" {
		t.Fatalf("promotion result = %+v, want a plain promotion onto provider-turn-2", result)
	}
	if calls := provider.interruptCalls(); len(calls) != 0 {
		t.Fatalf("provider received %v interrupts, want none: nothing was running", calls)
	}
}

// A provider that accepts the cancel but never settles the turn must not hang the
// request or leave a zombie Working bar. After the settle budget the durable rows
// are reconciled the same way Stop reconciles them, and the promotion still goes
// out.
func TestSteerQueuedTurnReconcilesWhenTheCancelNeverSettles(t *testing.T) {
	restore := chatsvc.SetQueuedTurnSettleWait(50 * time.Millisecond)
	t.Cleanup(restore)

	provider := &interruptCounting{steerRecorder: newSteerRecorder(), settleOnInterrupt: true}
	h := newHarnessWithConversation(t, provider)
	ctx := context.Background()

	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "do the long thing", ClientMessageID: "turn-1", Origin: domain.MessageOriginHuman,
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	provider.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-1"})
	selected, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "deliver me anyway", ClientMessageID: "queued-1", Origin: domain.MessageOriginHuman,
	})
	if err != nil {
		t.Fatalf("queue: %v", err)
	}

	result, err := h.svc.PromoteQueuedTurn(ctx, testSession, selected.ID)
	if err != nil {
		t.Fatalf("PromoteQueuedTurn: %v", err)
	}
	if result.InterruptedTurnID != "provider-turn-1" || result.ProviderTurnID != "provider-turn-2" {
		t.Fatalf("promotion result = %+v", result)
	}

	snapshot, err := h.st.LoadConversationSnapshot(ctx, h.ctrl.ConversationID())
	if err != nil {
		t.Fatalf("load snapshot: %v", err)
	}
	for _, turn := range snapshot.Turns {
		if turn.ProviderTurnID == "provider-turn-1" && turn.State != domain.TurnStateInterrupted {
			t.Fatalf("reconciled cut turn = %+v, want interrupted", turn)
		}
	}
	if sent := provider.sentTexts(); len(sent) != 2 || sent[1] != "deliver me anyway" {
		t.Fatalf("provider received %q, want the promoted message after reconciliation", sent)
	}
}

// A refused cancel has stopped nothing, so nothing may be dispatched and the
// addressed message stays queued for the user to retry.
func TestPromoteQueuedTurnInterruptFailureKeepsTheTurnQueued(t *testing.T) {
	provider := &interruptCounting{steerRecorder: newSteerRecorder()}
	provider.failInterrupt(errors.New("cancel refused"))
	h := newHarnessWithConversation(t, provider)
	ctx := context.Background()

	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "do the long thing", ClientMessageID: "turn-1", Origin: domain.MessageOriginHuman,
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	provider.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-1"})
	queued, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "keep me queued", ClientMessageID: "queued-refused", Origin: domain.MessageOriginHuman,
	})
	if err != nil {
		t.Fatalf("queue: %v", err)
	}

	_, err = h.svc.PromoteQueuedTurn(ctx, testSession, queued.ID)
	if err == nil {
		t.Fatal("promotion error = nil, want the interrupt refusal")
	}
	next, err := h.st.NextQueuedTurn(ctx, h.ctrl.ConversationID())
	if err != nil || next.TurnID != queued.ID {
		t.Fatalf("queue head = %+v, %v; want %s still queued", next, err, queued.ID)
	}
	if calls := provider.sendCallCount(); calls != 1 {
		t.Fatalf("provider send calls = %d, want only the original turn", calls)
	}
}

// Only human-originated queue items are steerable. The service must enforce that
// boundary even when a caller bypasses the frontend, without cutting the running
// turn or reordering the automation item.
func TestPromoteQueuedTurnRejectsNonHumanSourceWithoutContactingProvider(t *testing.T) {
	provider := &interruptCounting{steerRecorder: newSteerRecorder()}
	h := newHarnessWithConversation(t, provider)
	ctx := context.Background()

	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "do the long thing", ClientMessageID: "turn-1", Origin: domain.MessageOriginHuman,
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	provider.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-1"})
	queued, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "automation follow-up", ClientMessageID: "queued-automation", Origin: domain.MessageOriginAutomation,
	})
	if err != nil {
		t.Fatalf("queue automation turn: %v", err)
	}

	_, err = h.svc.PromoteQueuedTurn(ctx, testSession, queued.ID)
	if !errors.Is(err, chatsvc.ErrTurnNotQueued) {
		t.Fatalf("promotion error = %v, want ErrTurnNotQueued", err)
	}
	if calls := provider.interruptCalls(); len(calls) != 0 {
		t.Fatalf("provider received %v interrupts, want none", calls)
	}
	next, err := h.st.NextQueuedTurn(ctx, h.ctrl.ConversationID())
	if err != nil {
		t.Fatalf("load queue after rejection: %v", err)
	}
	if next.TurnID != queued.ID || next.Origin != domain.MessageOriginAutomation {
		t.Fatalf("queue head after rejection = %+v, want unchanged automation turn %s", next, queued.ID)
	}
}

// ao send --steer against a running turn on a driver WITHOUT the steering
// capability still delivers the guidance: the running turn is cut and the
// guidance dispatches as the next real turn, exactly the queued-steer contract.
func TestSteerOrSendOnASteerlessDriverCutsTheRunningTurnAndSends(t *testing.T) {
	provider := &interruptCounting{steerRecorder: newSteerRecorder()}
	// Deliberately WITHOUT the steer capability.
	provider.setCapabilities(productionCaps())
	h := newHarnessWithConversation(t, provider)
	ctx := context.Background()

	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "do the long thing", ClientMessageID: "turn-1", Origin: domain.MessageOriginHuman,
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	provider.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-1"})

	result, err := h.svc.SteerOrSend(ctx, testSession, ports.ChatUserMessage{
		Text: "pivot to the parser instead", ClientMessageID: "steer-1", Origin: domain.MessageOriginHuman,
	}, false)
	if err != nil {
		t.Fatalf("SteerOrSend: %v", err)
	}
	if result.Steered {
		t.Fatalf("steer-or-send = %+v, want an ordinary send (cut-and-reprompt), not a mid-turn steer", result)
	}
	if calls := provider.interruptCalls(); len(calls) != 1 || calls[0] != "provider-turn-1" {
		t.Fatalf("provider interrupts = %v, want exactly provider-turn-1", calls)
	}
	if len(provider.steers()) != 0 {
		t.Fatalf("provider received mid-turn steers: %+v, want none", provider.steers())
	}

	snapshot := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		for _, turn := range s.Turns {
			if turn.ProviderTurnID == "provider-turn-2" {
				return turn.State == domain.TurnStateRunning
			}
		}
		return false
	})
	var cut *domain.ConversationTurn
	for index := range snapshot.Turns {
		if snapshot.Turns[index].ProviderTurnID == "provider-turn-1" {
			cut = &snapshot.Turns[index]
		}
	}
	if cut == nil || cut.State != domain.TurnStateInterrupted {
		t.Fatalf("cut turn = %+v, want provider-turn-1 interrupted", cut)
	}
	sent := provider.sentTexts()
	if len(sent) != 2 || sent[1] != "pivot to the parser instead" {
		t.Fatalf("provider received %q, want the guidance dispatched as the next turn", sent)
	}
}
func TestRecoverImageSteerWithoutControllerNeverRedispatches(t *testing.T) {
	for _, tc := range []struct {
		name          string
		providerError error
		wantError     error
	}{
		{name: "accepted"},
		{name: "rejected", providerError: ports.ErrChatNoSteerableTurn, wantError: chatsvc.ErrNoActiveTurn},
		{name: "uncertain", providerError: errors.New("response lost"), wantError: chatsvc.ErrSteerDeliveryUncertain},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, provider := steerHarness(t)
			provider.failWith(tc.providerError)
			ctx := context.Background()
			original, err := h.svc.Steer(ctx, testSession, ports.ChatUserMessage{
				Text: "use this image", ClientMessageID: "image-steer",
				Content: []ports.ChatContent{{Type: "image", MIMEType: "image/png", Data: "aW1hZ2U="}},
			})
			if !errors.Is(err, tc.wantError) {
				t.Fatalf("steer error = %v, want %v", err, tc.wantError)
			}
			if err := h.svc.Stop(ctx, testSession); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				recovered, err := h.svc.RecoverSteer(ctx, testSession, "image-steer")
				if !errors.Is(err, tc.wantError) || recovered != original {
					t.Fatalf("recovery = %+v, %v; want %+v, %v", recovered, err, original, tc.wantError)
				}
			}
			for _, id := range []string{"", "never-reserved"} {
				if _, err := h.svc.RecoverSteer(ctx, testSession, id); !errors.Is(err, chatsvc.ErrSteerDeliveryUncertain) {
					t.Fatalf("missing receipt: %v", err)
				}
			}
			if len(provider.steers()) != 1 {
				t.Fatal("recovery redispatched guidance")
			}
		})
	}
}
