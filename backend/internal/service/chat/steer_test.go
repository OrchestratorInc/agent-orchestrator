package chat_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/store"
)

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

type cancelAfterSteerRecorder struct {
	*steerRecorder
	cancel context.CancelFunc
}

func (s *cancelAfterSteerRecorder) Steer(
	ctx context.Context,
	providerTurnID string,
	msg ports.ChatUserMessage,
) (ports.ChatTurnRef, error) {
	ref, err := s.steerRecorder.Steer(ctx, providerTurnID, msg)
	s.cancel()
	return ref, err
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
	provider.emit(ports.ChatEvent{
		Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-1",
	})
	return h, provider
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
	if !recovered.Steered || !recovered.Duplicate || !recovered.Steer.Replayed || withoutReplay(recovered.Steer) != first.Steer {
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
	if first.Replayed || !replayed.Replayed {
		t.Fatalf("Replayed = %v then %v, want false then true", first.Replayed, replayed.Replayed)
	}
	replayed.Replayed = false
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
	if !replayed.Replayed || withoutReplay(replayed) != first {
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

func TestSteerIsRefusedWhenTheProviderDidNotAdvertiseIt(t *testing.T) {
	h, provider := steerHarness(t)
	provider.setCapabilities(productionCaps())
	ctx := context.Background()

	_, err := h.svc.Steer(ctx, testSession, ports.ChatUserMessage{
		Text: "guidance", ClientMessageID: "steer-unadvertised",
	})
	if !errors.Is(err, chatsvc.ErrSteerUnsupported) {
		t.Fatalf("Steer err = %v, want ErrSteerUnsupported", err)
	}

	queued, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "queued", ClientMessageID: "queued-1", Origin: domain.MessageOriginHuman,
	})
	if err != nil {
		t.Fatalf("queue: %v", err)
	}
	if _, err := h.svc.PromoteQueuedTurn(ctx, testSession, queued.ID); !errors.Is(err, chatsvc.ErrSteerUnsupported) {
		t.Fatalf("PromoteQueuedTurn err = %v, want ErrSteerUnsupported", err)
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

func TestSteerWaitsForTheProviderToAcknowledgeTheTurn(t *testing.T) {
	provider := newSteerRecorder()
	h := newHarnessWithConversation(t, provider)
	ctx := context.Background()

	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "do the long thing", ClientMessageID: "turn-1",
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}

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

func TestPromoteSelectedQueuedTurnIntoTheRunningTurn(t *testing.T) {
	h, provider := steerHarness(t)
	ctx := context.Background()

	first, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "first queued", ClientMessageID: "queued-1", Origin: domain.MessageOriginHuman,
	})
	if err != nil {
		t.Fatalf("queue first: %v", err)
	}
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
	if result.SourceTurnID != selected.ID || result.ProviderTurnID != "provider-turn-1" || result.ActivityID == "" {
		t.Fatalf("promotion result = %+v", result)
	}
	calls := provider.steers()
	if len(calls) != 1 {
		t.Fatalf("provider steers = %+v, want one", calls)
	}
	if calls[0].msg.Text != "second queued" || calls[0].msg.ClientMessageID != "queued-2" {
		t.Fatalf("provider message = %+v, want selected durable message", calls[0].msg)
	}
	if len(calls[0].msg.Content) != 1 || calls[0].msg.Content[0].MIMEType != "image/png" {
		t.Fatalf("provider content = %+v, want stored image", calls[0].msg.Content)
	}

	snapshot := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return len(steerMarkers(s)) == 1
	})
	for _, turn := range snapshot.Turns {
		if turn.ID == selected.ID {
			t.Fatalf("promoted source turn remains visible: %+v", turn)
		}
	}
	next, err := h.st.NextQueuedTurn(ctx, h.ctrl.ConversationID())
	if err != nil {
		t.Fatalf("remaining queue: %v", err)
	}
	if next.TurnID != first.ID {
		t.Fatalf("remaining queue head = %q, want %q", next.TurnID, first.ID)
	}
}

func TestPromoteQueuedTurnRefusalRestoresItsQueuePosition(t *testing.T) {
	h, provider := steerHarness(t)
	ctx := context.Background()
	queued, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "keep me queued", ClientMessageID: "queued-refused", Origin: domain.MessageOriginHuman,
	})
	if err != nil {
		t.Fatalf("queue: %v", err)
	}
	provider.failWith(ports.ErrChatTurnNotSteerable)

	_, err = h.svc.PromoteQueuedTurn(ctx, testSession, queued.ID)
	if !errors.Is(err, chatsvc.ErrTurnNotSteerable) {
		t.Fatalf("promotion error = %v, want ErrTurnNotSteerable", err)
	}
	next, err := h.st.NextQueuedTurn(ctx, h.ctrl.ConversationID())
	if err != nil || next.TurnID != queued.ID {
		t.Fatalf("restored queue head = %+v, %v; want %s", next, err, queued.ID)
	}
}

func TestPromoteQueuedTurnRejectsNonHumanSourceWithoutContactingProvider(t *testing.T) {
	h, provider := steerHarness(t)
	ctx := context.Background()
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
	if calls := provider.steers(); len(calls) != 0 {
		t.Fatalf("provider received %d steer attempts, want none", len(calls))
	}
	next, err := h.st.NextQueuedTurn(ctx, h.ctrl.ConversationID())
	if err != nil {
		t.Fatalf("load queue after rejection: %v", err)
	}
	if next.TurnID != queued.ID || next.Origin != domain.MessageOriginAutomation {
		t.Fatalf("queue head after rejection = %+v, want unchanged automation turn %s", next, queued.ID)
	}
}

func TestPromoteQueuedUserAuthoredAutomationTurnIntoTheRunningTurn(t *testing.T) {
	h, provider := steerHarness(t)
	ctx := context.Background()
	queued, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "<browser_annotations>fix the label</browser_annotations>", ClientMessageID: "queued-annotation",
		Origin: domain.MessageOriginAutomation, AuthoredByUser: true,
	})
	if err != nil {
		t.Fatalf("queue annotation turn: %v", err)
	}
	snapshot := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		for _, msg := range s.Messages {
			if msg.TurnID == queued.ID {
				return true
			}
		}
		return false
	})
	for _, msg := range snapshot.Messages {
		if msg.TurnID == queued.ID && (!msg.AuthoredByUser || msg.Origin != domain.MessageOriginAutomation) {
			t.Fatalf("queued annotation message = %+v, want automation origin authored by the user", msg)
		}
	}

	if _, err := h.svc.PromoteQueuedTurn(ctx, testSession, queued.ID); err != nil {
		t.Fatalf("PromoteQueuedTurn: %v", err)
	}
	calls := provider.steers()
	if len(calls) != 1 || calls[0].msg.ClientMessageID != "queued-annotation" ||
		calls[0].msg.Origin != domain.MessageOriginAutomation {
		t.Fatalf("provider steers = %+v, want the queued annotation as automation", calls)
	}
}

func TestQueuedUserAuthoredAutomationTurnCanBeCancelledButNotEdited(t *testing.T) {
	h, _ := steerHarness(t)
	ctx := context.Background()
	queued, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "<browser_annotations>fix the label</browser_annotations>", ClientMessageID: "queued-annotation-edit",
		Origin: domain.MessageOriginAutomation, AuthoredByUser: true,
	})
	if err != nil {
		t.Fatalf("queue annotation turn: %v", err)
	}
	if err := h.svc.EditQueuedTurn(ctx, testSession, queued.ID, chatsvc.QueuedMessageEdit{Text: "rewritten"}); err == nil {
		t.Fatal("EditQueuedTurn accepted a user-authored automation turn")
	}
	if err := h.svc.CancelQueuedTurn(ctx, testSession, queued.ID); err != nil {
		t.Fatalf("CancelQueuedTurn: %v", err)
	}
	if next, err := h.st.NextQueuedTurn(ctx, h.ctrl.ConversationID()); err == nil {
		t.Fatalf("queue head after cancel = %+v, want empty", next)
	}
}

func TestPromoteQueuedTurnAmbiguousProviderFailureSettlesUncertainWithoutRedelivery(t *testing.T) {
	requestCtx, cancelRequest := context.WithCancel(context.Background())
	t.Cleanup(cancelRequest)
	provider := &cancelAfterSteerRecorder{steerRecorder: newSteerRecorder(), cancel: cancelRequest}
	h := newHarnessWithConversation(t, provider)
	storeCtx := context.Background()
	if _, err := h.svc.Send(storeCtx, testSession, ports.ChatUserMessage{
		Text: "do the long thing", ClientMessageID: "turn-1", Origin: domain.MessageOriginHuman,
	}); err != nil {
		t.Fatalf("start running turn: %v", err)
	}
	provider.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-1"})
	queued, err := h.svc.Send(storeCtx, testSession, ports.ChatUserMessage{
		Text: "deliver me at most once", ClientMessageID: "queued-uncertain", Origin: domain.MessageOriginHuman,
	})
	if err != nil {
		t.Fatalf("queue: %v", err)
	}
	transportErr := errors.New("connection lost after request write")
	provider.failWith(transportErr)

	_, err = h.svc.PromoteQueuedTurn(requestCtx, testSession, queued.ID)
	if !errors.Is(err, chatsvc.ErrPromotionUncertain) {
		t.Fatalf("promotion error = %v, want ErrPromotionUncertain", err)
	}
	if !errors.Is(err, transportErr) {
		t.Fatalf("promotion error = %v, want transport cause", err)
	}

	snapshot, err := h.st.LoadConversationSnapshot(storeCtx, h.ctrl.ConversationID())
	if err != nil {
		t.Fatalf("load snapshot: %v", err)
	}
	var source *domain.ConversationTurn
	for index := range snapshot.Turns {
		if snapshot.Turns[index].ID == queued.ID {
			source = &snapshot.Turns[index]
			break
		}
	}
	if source == nil {
		t.Fatalf("uncertain source turn %s is not visible", queued.ID)
		return
	}
	if source.State != domain.TurnStateFailed || source.ErrorMessage != chatsvc.ErrPromotionUncertain.Error() {
		t.Fatalf("uncertain source = %+v, want failed with promotion-uncertain error", *source)
	}
	if _, err := h.st.NextQueuedTurn(storeCtx, h.ctrl.ConversationID()); !errors.Is(err, domain.ErrNoQueuedTurn) {
		t.Fatalf("uncertain source remained drainable: %v", err)
	}

	_, retryErr := h.svc.PromoteQueuedTurn(storeCtx, testSession, queued.ID)
	if !errors.Is(retryErr, chatsvc.ErrTurnNotQueued) {
		t.Fatalf("retry error = %v, want ErrTurnNotQueued", retryErr)
	}
	if calls := provider.steers(); len(calls) != 1 {
		t.Fatalf("provider received %d steer attempts, want one", len(calls))
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
				if !errors.Is(err, tc.wantError) || withoutReplay(recovered) != original {
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

func withoutReplay(r chatsvc.SteerResult) chatsvc.SteerResult {
	r.Replayed = false
	return r
}
