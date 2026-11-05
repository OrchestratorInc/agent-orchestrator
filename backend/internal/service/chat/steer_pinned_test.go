package chat_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/store"
)

// Pinned steering.
//
// The ordinary steer resolves the turn itself, which is right for a control acting
// on whatever the user is watching. A gateway answering a turn it observed needs
// the opposite: the guidance must join exactly that turn, or nothing may happen.
// Redirecting it into a successor would apply a correction to work that never made
// the mistake, and silently converting it into a new message would replay finished
// guidance as a fresh instruction. Every test here is about which of those two
// things AO refuses to do.

const pinnedTurn = "provider-turn-1"

// The feature: guidance lands on the turn the caller named.
func TestSteerPinnedDeliversIntoTheNamedTurn(t *testing.T) {
	h, provider := steerHarness(t)
	ctx := context.Background()

	result, err := h.svc.SteerPinned(ctx, testSession, h.ctrl.Generation(), pinnedTurn,
		ports.ChatUserMessage{
			Text:            "skip the second file",
			ClientMessageID: "pinned-1",
			Origin:          domain.MessageOriginAutomation,
		})
	if err != nil {
		t.Fatalf("SteerPinned: %v", err)
	}
	if result.ProviderTurnID != pinnedTurn {
		t.Errorf("steered turn = %q, want %q", result.ProviderTurnID, pinnedTurn)
	}
	if result.ActivityID == "" {
		t.Error("no activity id reported; the gateway cannot attribute the steer")
	}
	calls := provider.steers()
	if len(calls) != 1 {
		t.Fatalf("provider saw %d steers, want 1", len(calls))
	}
	if calls[0].turnID != pinnedTurn {
		t.Errorf("provider steered turn = %q, want %q", calls[0].turnID, pinnedTurn)
	}
}

// The refusal that gives the pin its meaning: a different turn is in flight, so
// the guidance is about work that has already moved on.
func TestSteerPinnedRefusesWhenAnotherTurnIsInFlight(t *testing.T) {
	h, provider := steerHarness(t)
	ctx := context.Background()

	_, err := h.svc.SteerPinned(ctx, testSession, h.ctrl.Generation(), "provider-turn-stale",
		ports.ChatUserMessage{
			Text:            "too late",
			ClientMessageID: "pinned-wrong-turn",
			Origin:          domain.MessageOriginAutomation,
		})
	if !errors.Is(err, chatsvc.ErrNoActiveTurn) {
		t.Fatalf("error = %v, want ErrNoActiveTurn", err)
	}
	if calls := provider.steers(); len(calls) != 0 {
		t.Fatalf("provider saw %d steers, want none", len(calls))
	}
	if calls := provider.sendCallCount(); calls != 1 {
		t.Fatalf("provider sends = %d, want the original only", calls)
	}

	// The handle was settled as a durable no-active-turn refusal, not left reserved.
	// A reserved row would report the guidance as uncertain and block the caller
	// from ever delivering it once its evidence is correct again.
	conversation, err := h.st.ConversationForSession(ctx, testSession)
	if err != nil {
		t.Fatalf("load conversation: %v", err)
	}
	delivery, found, err := h.st.SteerDelivery(ctx, conversation.ID, "pinned-wrong-turn")
	if err != nil {
		t.Fatalf("load steer delivery: %v", err)
	}
	if found {
		if delivery.State != domain.ConversationSteerRejected {
			t.Errorf("delivery state = %q, want rejected", delivery.State)
		}
		if delivery.RejectionKind != domain.ConversationSteerRejectedNoActiveTurn {
			t.Errorf("rejection kind = %q, want no_active_turn", delivery.RejectionKind)
		}
	}
}

// A controller that replaced the caller's evidence must not act on it.
func TestSteerPinnedRefusesStaleGeneration(t *testing.T) {
	h, provider := steerHarness(t)
	ctx := context.Background()

	_, err := h.svc.SteerPinned(ctx, testSession, "generation-from-a-dead-controller", pinnedTurn,
		ports.ChatUserMessage{
			Text:            "from an older generation",
			ClientMessageID: "pinned-stale",
			Origin:          domain.MessageOriginAutomation,
		})
	if !errors.Is(err, chatsvc.ErrStaleControllerGeneration) {
		t.Fatalf("error = %v, want ErrStaleControllerGeneration", err)
	}
	if calls := provider.steers(); len(calls) != 0 {
		t.Fatalf("provider saw %d steers, want none", len(calls))
	}
	// Nothing was reserved: the refusal is about the caller's evidence, so the same
	// handle must still be deliverable.
	conversation, err := h.st.ConversationForSession(ctx, testSession)
	if err != nil {
		t.Fatalf("load conversation: %v", err)
	}
	if _, found, err := h.st.SteerDelivery(ctx, conversation.ID, "pinned-stale"); err != nil {
		t.Fatalf("load steer delivery: %v", err)
	} else if found {
		t.Error("a stale-generation refusal left a durable delivery row")
	}
}

// An absent pin is a caller bug, and it must never degrade to "pick one".
func TestSteerPinnedRequiresATargetTurn(t *testing.T) {
	h, provider := steerHarness(t)

	_, err := h.svc.SteerPinned(context.Background(), testSession, "", "",
		ports.ChatUserMessage{Text: "no turn named", Origin: domain.MessageOriginAutomation})
	if !errors.Is(err, chatsvc.ErrSteerTargetRequired) {
		t.Fatalf("error = %v, want ErrSteerTargetRequired", err)
	}
	if calls := provider.steers(); len(calls) != 0 {
		t.Fatalf("provider saw %d steers, want none", len(calls))
	}
}

// The durable contract survives pinning: one client handle is one delivery.
func TestSteerPinnedReplaysOnTheClientHandle(t *testing.T) {
	h, provider := steerHarness(t)
	ctx := context.Background()
	msg := ports.ChatUserMessage{
		Text:            "narrow the search",
		ClientMessageID: "pinned-replay",
		Origin:          domain.MessageOriginAutomation,
	}

	first, err := h.svc.SteerPinned(ctx, testSession, h.ctrl.Generation(), pinnedTurn, msg)
	if err != nil {
		t.Fatalf("first SteerPinned: %v", err)
	}
	replayed, err := h.svc.SteerPinned(ctx, testSession, h.ctrl.Generation(), pinnedTurn, msg)
	if err != nil {
		t.Fatalf("replayed SteerPinned: %v", err)
	}
	if first.Replayed || !replayed.Replayed {
		t.Fatalf("Replayed = %v then %v, want false then true", first.Replayed, replayed.Replayed)
	}
	if withoutReplay(replayed) != first {
		t.Errorf("replayed result = %+v, want original %+v", replayed, first)
	}
	if calls := provider.steers(); len(calls) != 1 {
		t.Fatalf("provider saw %d steers, want exactly one", len(calls))
	}
}

// The provider is the authority on which turn absorbed the guidance. Reporting its
// answer rather than the requested one is what lets a gateway detect a mismatch
// instead of assuming its pin held.
func TestSteerPinnedReportsTheProvidersTurnWhenItDiffers(t *testing.T) {
	h, provider := steerHarness(t)
	provider.mu.Lock()
	provider.landed = "provider-turn-somebody-else"
	provider.mu.Unlock()

	result, err := h.svc.SteerPinned(context.Background(), testSession,
		h.ctrl.Generation(), pinnedTurn,
		ports.ChatUserMessage{
			Text:            "this should not have landed there",
			ClientMessageID: "pinned-mismatch",
			Origin:          domain.MessageOriginAutomation,
		})
	if err != nil {
		t.Fatalf("SteerPinned: %v", err)
	}
	if result.ProviderTurnID != "provider-turn-somebody-else" {
		t.Fatalf("ProviderTurnID = %q, want the provider's own answer",
			result.ProviderTurnID)
	}
	receipt := ports.MessageReceipt{
		ProviderTurnID:       result.ProviderTurnID,
		ControllerGeneration: h.ctrl.Generation(),
		Steered:              true,
	}
	if receipt.MatchesSteer(ports.MessageRequest{
		SessionID: testSession, Delivery: ports.MessageDeliverySteer,
		TargetProviderTurnID: pinnedTurn, ControllerGeneration: h.ctrl.Generation(),
	}) {
		t.Error("a receipt for a different turn matched the requested pin")
	}
}

// A provider that cannot absorb guidance must produce a typed refusal, never a
// result that reads as applied.
func TestSteerPinnedSurfacesProviderRefusalsAsTypedErrors(t *testing.T) {
	for _, tc := range []struct {
		name   string
		refuse error
		want   error
	}{
		{name: "no steerable turn", refuse: ports.ErrChatNoSteerableTurn, want: chatsvc.ErrNoActiveTurn},
		{name: "turn not steerable", refuse: ports.ErrChatTurnNotSteerable, want: chatsvc.ErrTurnNotSteerable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, provider := steerHarness(t)
			provider.failWith(tc.refuse)

			result, err := h.svc.SteerPinned(context.Background(), testSession,
				h.ctrl.Generation(), pinnedTurn,
				ports.ChatUserMessage{
					Text:            "guidance the provider will not take",
					ClientMessageID: "pinned-refused",
					Origin:          domain.MessageOriginAutomation,
				})
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
			if result.ProviderTurnID != "" || result.ActivityID != "" {
				t.Fatalf("result = %+v, want nothing that reads as applied", result)
			}
		})
	}
}

// A call that may or may not have reached the provider stays uncertain, so the
// handle can never be reused to deliver the same guidance twice.
func TestSteerPinnedKeepsUncertainDeliveryUncertain(t *testing.T) {
	var flaky *failSteerCompletionStore
	h, provider := steerHarnessWithStore(t, func(st *store.Store) chatsvc.Store {
		flaky = &failSteerCompletionStore{Store: st}
		return flaky
	})
	msg := ports.ChatUserMessage{
		Text:            "narrow the search",
		ClientMessageID: "pinned-uncertain",
		Origin:          domain.MessageOriginAutomation,
	}

	_, err := h.svc.SteerPinned(context.Background(), testSession, h.ctrl.Generation(), pinnedTurn, msg)
	if !errors.Is(err, chatsvc.ErrSteerDeliveryUncertain) {
		t.Fatalf("error = %v, want ErrSteerDeliveryUncertain", err)
	}
	_, err = h.svc.SteerPinned(context.Background(), testSession, h.ctrl.Generation(), pinnedTurn, msg)
	if !errors.Is(err, chatsvc.ErrSteerDeliveryUncertain) {
		t.Fatalf("retry error = %v, want ErrSteerDeliveryUncertain", err)
	}
	if calls := provider.steers(); len(calls) != 1 {
		t.Fatalf("provider saw %d steer attempts, want one", len(calls))
	}
}

/* ---- in-flight turn accessor ------------------------------------------- */

func TestRunningProviderTurnReportsTheDispatchedTurn(t *testing.T) {
	h, _ := steerHarness(t)

	turn, generation, ok := h.svc.RunningProviderTurn(context.Background(), testSession)
	if !ok {
		t.Fatal("ok = false, want the dispatched turn reported")
	}
	if turn != pinnedTurn {
		t.Errorf("turn = %q, want %q", turn, pinnedTurn)
	}
	if generation == "" || generation != h.ctrl.Generation() {
		t.Errorf("generation = %q, want the controller's %q", generation, h.ctrl.Generation())
	}
}

func TestRunningProviderTurnReportsNothingWithoutAController(t *testing.T) {
	h := newHarness(t)
	if err := h.svc.Stop(context.Background(), testSession); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	turn, generation, ok := h.svc.RunningProviderTurn(context.Background(), testSession)
	if ok || turn != "" || generation != "" {
		t.Fatalf("RunningProviderTurn = (%q, %q, %v), want nothing", turn, generation, ok)
	}
}

func TestRunningProviderTurnReportsNothingWhenIdle(t *testing.T) {
	h := newHarness(t)

	turn, _, ok := h.svc.RunningProviderTurn(context.Background(), testSession)
	if ok || turn != "" {
		t.Fatalf("RunningProviderTurn = (%q, %v), want no in-flight turn", turn, ok)
	}
}

/* ---- confirmed evidence from live operations ---------------------------- */

// recordingCapabilityEvidence is the chat service's view of the capability
// recorder: it records what a provider actually did, and nothing else.
type recordingCapabilityEvidence struct {
	mu       sync.Mutex
	recorded []domain.CapabilityFeature
	sessions []domain.SessionID
	gens     []string
	err      error
}

func (r *recordingCapabilityEvidence) RecordConfirmed(
	_ context.Context,
	_ domain.AgentHarness,
	sessionID domain.SessionID,
	generation string,
	features ...domain.CapabilityFeature,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return r.err
	}
	r.recorded = append(r.recorded, features...)
	r.sessions = append(r.sessions, sessionID)
	r.gens = append(r.gens, generation)
	return nil
}

func (r *recordingCapabilityEvidence) features() []domain.CapabilityFeature {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]domain.CapabilityFeature(nil), r.recorded...)
}

// capabilityHarness is newHarnessWithConversation plus the recorder, because the
// option is what is under test.
func capabilityHarness(
	t *testing.T,
	conv ports.ChatConversation,
	recorder chatsvc.CapabilityEvidenceRecorder,
) (*harness, *sqlite.Store) {
	t.Helper()
	st := openStore(t)
	base := newFakeConversation()
	if conv == nil {
		conv = base
	} else if recorderConv, ok := conv.(*steerRecorder); ok {
		base = recorderConv.fakeConversation
	}
	var (
		counterMu sync.Mutex
		counter   int
	)
	svc := chatsvc.New(chatsvc.Options{
		Store:              st,
		Reader:             fullSnapshotReader(st),
		PageReader:         boundedSnapshotReader(st),
		Sessions:           st,
		Drivers:            fakeRegistry{driver: fakeDriver{conv: conv}},
		Activity:           &recordingActivity{},
		Log:                slog.New(slog.DiscardHandler),
		CapabilityEvidence: recorder,
		NewID: func() string {
			counterMu.Lock()
			defer counterMu.Unlock()
			counter++
			return fmt.Sprintf("cap-id-%03d", counter)
		},
		Now: func() time.Time { return time.Date(2026, 8, 2, 10, 0, 0, 0, time.UTC) },
	})
	ctrl, err := svc.Start(context.Background(), chatsvc.StartConfig{
		SessionID: testSession, ProjectID: testProject, Harness: domain.HarnessCodex,
		WorkspacePath: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = svc.Stop(context.Background(), testSession) })
	return &harness{svc: svc, st: st, conv: base, ctrl: ctrl, activity: &recordingActivity{}}, st
}

// A steer that reached the provider is the only proof steer works.
func TestSteerPinnedRecordsConfirmedSteerEvidence(t *testing.T) {
	recorder := &recordingCapabilityEvidence{}
	provider := newSteerRecorder()
	h, _ := capabilityHarness(t, provider, recorder)

	if _, err := h.svc.Send(context.Background(), testSession, ports.ChatUserMessage{
		Text: "do the long thing", ClientMessageID: "cap-turn-1", Origin: domain.MessageOriginHuman,
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	provider.emit(ports.ChatEvent{
		Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-1",
	})

	if _, err := h.svc.SteerPinned(context.Background(), testSession,
		h.ctrl.Generation(), "provider-turn-1", ports.ChatUserMessage{
			Text: "narrow it", ClientMessageID: "cap-steer-1",
			Origin: domain.MessageOriginAutomation,
		}); err != nil {
		t.Fatalf("SteerPinned: %v", err)
	}

	features := recorder.features()
	if !slices.Contains(features, domain.CapabilityFeatureSteer) {
		t.Fatalf("recorded features = %v, want steer", features)
	}
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	if len(recorder.sessions) == 0 || recorder.sessions[len(recorder.sessions)-1] != testSession {
		t.Errorf("recorded sessions = %v, want %q", recorder.sessions, testSession)
	}
	if recorder.gens[len(recorder.gens)-1] != h.ctrl.Generation() {
		t.Errorf("recorded generation = %q, want %q",
			recorder.gens[len(recorder.gens)-1], h.ctrl.Generation())
	}
}

// A refused steer proves nothing, so it must record nothing: confirmed evidence is
// the one grade a gate will act on.
func TestRefusedSteerRecordsNoEvidence(t *testing.T) {
	recorder := &recordingCapabilityEvidence{}
	provider := newSteerRecorder()
	h, _ := capabilityHarness(t, provider, recorder)

	if _, err := h.svc.Send(context.Background(), testSession, ports.ChatUserMessage{
		Text: "do the long thing", ClientMessageID: "cap-turn-2", Origin: domain.MessageOriginHuman,
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	provider.emit(ports.ChatEvent{
		Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-1",
	})
	// The dispatch above is a streaming confirmation; clear it so this case is
	// only about the refusal.
	recorder.mu.Lock()
	recorder.recorded = nil
	recorder.mu.Unlock()

	provider.failWith(ports.ErrChatNoSteerableTurn)
	if _, err := h.svc.SteerPinned(context.Background(), testSession,
		h.ctrl.Generation(), "provider-turn-1", ports.ChatUserMessage{
			Text: "narrow it", ClientMessageID: "cap-steer-refused",
			Origin: domain.MessageOriginAutomation,
		}); !errors.Is(err, chatsvc.ErrNoActiveTurn) {
		t.Fatalf("error = %v, want ErrNoActiveTurn", err)
	}
	if features := recorder.features(); len(features) != 0 {
		t.Fatalf("recorded %v after a refusal, want nothing", features)
	}
}

// An acknowledged dispatch proves the harness streams.
func TestSendRecordsConfirmedStreamingEvidence(t *testing.T) {
	recorder := &recordingCapabilityEvidence{}
	h, _ := capabilityHarness(t, nil, recorder)

	if _, err := h.svc.Send(context.Background(), testSession, ports.ChatUserMessage{
		Text: "start work", ClientMessageID: "cap-send-1", Origin: domain.MessageOriginHuman,
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if features := recorder.features(); !slices.Contains(features, domain.CapabilityFeatureStreaming) {
		t.Fatalf("recorded features = %v, want streaming", features)
	}
}

// Wiring no recorder must change nothing: the option is optional by contract.
func TestNoCapabilityRecorderChangesNothing(t *testing.T) {
	h, provider := steerHarness(t)

	if _, err := h.svc.SteerPinned(context.Background(), testSession,
		h.ctrl.Generation(), pinnedTurn, ports.ChatUserMessage{
			Text: "narrow it", ClientMessageID: "cap-none-1",
			Origin: domain.MessageOriginAutomation,
		}); err != nil {
		t.Fatalf("SteerPinned: %v", err)
	}
	if calls := provider.steers(); len(calls) != 1 {
		t.Fatalf("provider saw %d steers, want one", len(calls))
	}
}
