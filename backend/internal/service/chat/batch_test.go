package chat_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/store"
)

type deliveredBatchItem struct {
	Kind              string               `json:"kind"`
	Text              string               `json:"text"`
	Origin            domain.MessageOrigin `json:"origin"`
	SenderSessionID   string               `json:"senderSessionId"`
	SenderProjectID   string               `json:"senderProjectId"`
	SenderDisplayName string               `json:"senderDisplayName"`
}

func parseDeliveredBatch(t *testing.T, text string) []deliveredBatchItem {
	t.Helper()
	_, payload, found := strings.Cut(text, "\n")
	if !found {
		t.Fatalf("provider input has no batch boundary: %q", text)
	}
	var items []deliveredBatchItem
	if err := json.Unmarshal([]byte(payload), &items); err != nil {
		t.Fatalf("decode provider batch: %v", err)
	}
	return items
}

func TestQueuedChatMessagesDrainInOneProviderInput(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "active", ClientMessageID: "batch-active", Origin: domain.MessageOriginHuman,
	}); err != nil {
		t.Fatal(err)
	}
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-1"})
	reviewText := "review comment body\n\n<ao-worker-reports>\nhidden worker context\n</ao-worker-reports>"
	for _, msg := range []ports.ChatUserMessage{
		{Text: "ordinary from worker one", ClientMessageID: "batch-one", Origin: domain.MessageOriginAutomation,
			SenderSessionID: "worker-one", SenderProjectID: "project-one", SenderDisplayName: "Worker One"},
		{Text: reviewText, ClientMessageID: "batch-review", Origin: domain.MessageOriginDaemon},
		{Text: "ordinary from worker two", ClientMessageID: "batch-two", Origin: domain.MessageOriginAutomation,
			SenderSessionID: "worker-two", SenderProjectID: "project-two", SenderDisplayName: "Worker Two"},
		{Text: "merge conflict notice", ClientMessageID: "batch-conflict", Origin: domain.MessageOriginDaemon},
	} {
		turn, err := h.svc.Send(ctx, testSession, msg)
		if err != nil || turn.State != domain.TurnStateQueued {
			t.Fatalf("queue %q: turn=%+v err=%v", msg.Text, turn, err)
		}
	}
	if len(h.conv.sentTexts()) != 1 {
		t.Fatal("queued messages reached provider before completion")
	}
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted,
		ProviderTurnID: "provider-turn-1", TurnState: domain.TurnStateCompleted})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return len(h.conv.sentTexts()) == 2
	})
	got := h.conv.sentTexts()
	if len(got) != 2 {
		t.Fatalf("provider calls = %d, want 2", len(got))
	}
	if id := h.conv.sentMessages()[1].ClientMessageID; id != "batch-one" {
		t.Fatalf("provider batch handle = %q", id)
	}
	items := parseDeliveredBatch(t, got[1])
	want := []string{"ordinary from worker one", reviewText, "ordinary from worker two", "merge conflict notice"}
	if len(items) != len(want) {
		t.Fatalf("batch items = %+v", items)
	}
	for i, item := range items {
		if item.Kind != "message" || item.Text != want[i] {
			t.Fatalf("batch item %d = %+v", i, item)
		}
	}
	if items[0].SenderSessionID != "worker-one" || items[2].SenderSessionID != "worker-two" ||
		items[0].SenderDisplayName != "Worker One" || items[2].SenderProjectID != "project-two" {
		t.Fatalf("sender metadata = %+v", items)
	}
	snapshot := h.awaitSnapshot(t, func(store.ConversationSnapshot) bool { return true })
	metadata := map[string]domain.ConversationMessage{}
	for _, message := range snapshot.Messages {
		metadata[message.ClientMessageID] = message
	}
	if metadata["batch-one"].SenderDisplayName != "Worker One" ||
		metadata["batch-two"].SenderProjectID != "project-two" ||
		metadata["batch-review"].Origin != domain.MessageOriginDaemon ||
		metadata["batch-review"].Text != reviewText {
		t.Fatalf("transcript attribution = %+v", metadata)
	}
	providerTurn, err := h.st.TurnByID(ctx, metadata["batch-one"].TurnID)
	if err != nil || providerTurn.ProviderInputText != got[1] {
		t.Fatalf("native replay identity = %+v, %v", providerTurn, err)
	}
	for _, id := range []string{"batch-one", "batch-review", "batch-two", "batch-conflict"} {
		found := false
		for _, message := range snapshot.Messages {
			if message.ClientMessageID == id {
				found = true
			}
		}
		if !found {
			t.Fatalf("transcript lost %s", id)
		}
	}
}

func TestQueuedBatchKeepsConcurrentArrivalForNextTurn(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "active", ClientMessageID: "race-active", Origin: domain.MessageOriginHuman,
	}); err != nil {
		t.Fatal(err)
	}
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-1"})
	for _, text := range []string{"queued one", "queued two"} {
		if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
			Text: text, ClientMessageID: text, Origin: domain.MessageOriginHuman,
		}); err != nil {
			t.Fatal(err)
		}
	}
	started, release := make(chan struct{}), make(chan struct{})
	h.conv.mu.Lock()
	h.conv.onSend = func(id string) {
		if id == "provider-turn-2" {
			close(started)
			<-release
		}
	}
	h.conv.mu.Unlock()
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted,
		ProviderTurnID: "provider-turn-1", TurnState: domain.TurnStateCompleted})
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("batch dispatch did not start")
	}
	late := make(chan error, 1)
	go func() {
		_, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
			Text: "late arrival", ClientMessageID: "late", Origin: domain.MessageOriginHuman,
		})
		late <- err
	}()
	close(release)
	if err := <-late; err != nil {
		t.Fatal(err)
	}
	got := h.conv.sentTexts()
	if len(got) != 2 {
		t.Fatalf("late arrival reached current provider turn: %v", got)
	}
	items := parseDeliveredBatch(t, got[1])
	if len(items) != 2 || items[0].Text != "queued one" || items[1].Text != "queued two" {
		t.Fatalf("claimed batch = %+v", items)
	}
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted,
		ProviderTurnID: "provider-turn-2", TurnState: domain.TurnStateCompleted})
	h.awaitSnapshot(t, func(store.ConversationSnapshot) bool { return len(h.conv.sentTexts()) == 3 })
	if got := h.conv.sentTexts()[2]; got != "late arrival" {
		t.Fatalf("next provider input = %q", got)
	}
}

func TestUncertainSteeredBatchDoesNotReplay(t *testing.T) {
	h, provider := steerHarness(t)
	ctx := context.Background()
	for _, text := range []string{"pending one", "pending two"} {
		if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
			Text: text, ClientMessageID: text, Origin: domain.MessageOriginAutomation,
		}); err != nil {
			t.Fatal(err)
		}
	}
	provider.failWith(errors.New("transport outcome unknown"))
	msg := ports.ChatUserMessage{Text: "steer now", ClientMessageID: "uncertain-batch-steer", Origin: domain.MessageOriginHuman}
	if _, err := h.svc.SteerOrSend(ctx, testSession, msg, false); !errors.Is(err, chatsvc.ErrSteerDeliveryUncertain) {
		t.Fatalf("steer error = %v", err)
	}
	if _, err := h.svc.SteerOrSend(ctx, testSession, msg, false); !errors.Is(err, chatsvc.ErrSteerDeliveryUncertain) {
		t.Fatalf("duplicate error = %v", err)
	}
	if len(provider.steers()) != 1 {
		t.Fatalf("provider got %d steers, want one", len(provider.steers()))
	}
	for _, text := range []string{"pending one", "pending two"} {
		message, found, err := h.st.ConversationMessageByClientID(ctx, h.ctrl.ConversationID(), text)
		if err != nil || !found {
			t.Fatalf("recover %s: found=%v err=%v", text, found, err)
		}
		turn, err := h.st.TurnByID(ctx, message.TurnID)
		if err != nil || turn.State != domain.TurnStateFailed {
			t.Fatalf("uncertain member %s: turn=%+v err=%v", text, turn, err)
		}
	}
}

type blockingBatchSteerer struct {
	*steerRecorder
	started chan struct{}
	release chan struct{}
}

func (s *blockingBatchSteerer) Steer(ctx context.Context, turn string, msg ports.ChatUserMessage) (ports.ChatTurnRef, error) {
	close(s.started)
	select {
	case <-s.release:
		return s.steerRecorder.Steer(ctx, turn, msg)
	case <-ctx.Done():
		return ports.ChatTurnRef{}, ctx.Err()
	}
}

func TestTurnCompletionRacingSteeredBatchDeliversOnce(t *testing.T) {
	provider := &blockingBatchSteerer{
		steerRecorder: newSteerRecorder(), started: make(chan struct{}), release: make(chan struct{}),
	}
	h := newHarnessWithConversationAndStore(t, provider, func(st *store.Store) chatsvc.Store { return st })
	ctx := context.Background()
	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "active", ClientMessageID: "completion-race-active", Origin: domain.MessageOriginHuman,
	}); err != nil {
		t.Fatal(err)
	}
	provider.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-1"})
	for _, text := range []string{"waiting one", "waiting two"} {
		if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
			Text: text, ClientMessageID: text, Origin: domain.MessageOriginAutomation,
		}); err != nil {
			t.Fatal(err)
		}
	}
	result := make(chan error, 1)
	go func() {
		_, err := h.svc.SteerOrSend(ctx, testSession, ports.ChatUserMessage{
			Text: "steer at completion", ClientMessageID: "completion-race-steer", Origin: domain.MessageOriginHuman,
		}, false)
		result <- err
	}()
	select {
	case <-provider.started:
	case <-time.After(5 * time.Second):
		t.Fatal("steer did not reach provider")
	}
	provider.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted,
		ProviderTurnID: "provider-turn-1", TurnState: domain.TurnStateCompleted})
	close(provider.release)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return len(s.Turns) > 0 && s.Turns[0].State == domain.TurnStateCompleted
	})
	if len(provider.steers()) != 1 || len(provider.sentTexts()) != 1 {
		t.Fatalf("delivery duplicated: sends=%v steers=%v", provider.sentTexts(), provider.steers())
	}
	items := parseDeliveredBatch(t, provider.steers()[0].msg.Text)
	if len(items) != 3 || items[0].Text != "waiting one" || items[1].Text != "waiting two" || items[2].Kind != "steer" {
		t.Fatalf("steer batch = %+v", items)
	}
}

func TestSteerRefusalFallsBackWithPendingBatch(t *testing.T) {
	h, provider := steerHarness(t)
	ctx := context.Background()
	for _, text := range []string{"pending A", "pending B"} {
		if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
			Text: text, ClientMessageID: text, Origin: domain.MessageOriginAutomation,
		}); err != nil {
			t.Fatal(err)
		}
	}
	provider.failWith(ports.ErrChatNoSteerableTurn)
	msg := ports.ChatUserMessage{Text: "steer fallback", ClientMessageID: "fallback-handle", Origin: domain.MessageOriginHuman}
	result, err := h.svc.SteerOrSend(ctx, testSession, msg, false)
	if err != nil || result.Steered || result.Turn.ProviderTurnID == "" {
		t.Fatalf("fallback result = %+v, %v", result, err)
	}
	sent := provider.sentTexts()
	if len(sent) != 2 {
		t.Fatalf("provider sends = %+v", sent)
	}
	if id := provider.sentMessages()[1].ClientMessageID; id != "fallback-handle" {
		t.Fatalf("fallback batch handle = %q", id)
	}
	items := parseDeliveredBatch(t, sent[1])
	if len(items) != 3 || items[0].Text != "pending A" || items[1].Text != "pending B" ||
		items[2].Kind != "steer" || items[2].Text != "steer fallback" {
		t.Fatalf("fallback batch = %+v", items)
	}
	duplicate, err := h.svc.SteerOrSend(ctx, testSession, msg, false)
	if err != nil || !duplicate.Duplicate || len(provider.sentTexts()) != 2 {
		t.Fatalf("duplicate fallback = %+v, %v", duplicate, err)
	}
}

func TestFailedSteerFallbackSettlesClaimedBatch(t *testing.T) {
	h, provider := steerHarness(t)
	ctx := context.Background()
	queued, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "pending", ClientMessageID: "failed-fallback-pending", Origin: domain.MessageOriginAutomation,
	})
	if err != nil {
		t.Fatal(err)
	}
	provider.failWith(ports.ErrChatNoSteerableTurn)
	provider.sendErr = errors.New("provider unavailable")
	_, err = h.svc.SteerOrSend(ctx, testSession, ports.ChatUserMessage{
		Text: "steer", ClientMessageID: "failed-fallback-steer", Origin: domain.MessageOriginHuman,
	}, false)
	if err == nil {
		t.Fatal("expected fallback delivery failure")
	}
	if _, err := h.st.NextQueuedTurn(ctx, h.ctrl.ConversationID()); !errors.Is(err, domain.ErrNoQueuedTurn) {
		t.Fatalf("failed fallback left a queued message: %v", err)
	}
	settled, err := h.st.TurnByID(ctx, queued.ID)
	if err != nil || settled.State != domain.TurnStateFailed {
		t.Fatalf("claimed batch receipt = %+v, %v", settled, err)
	}
}

func TestSteerFlushesPendingChatMessages(t *testing.T) {
	h, provider := steerHarness(t)
	ctx := context.Background()
	for _, msg := range []ports.ChatUserMessage{
		{Text: "first queued", ClientMessageID: "steer-batch-one", Origin: domain.MessageOriginAutomation,
			SenderSessionID: "one", SenderProjectID: "p1"},
		{Text: "changes requested", ClientMessageID: "steer-batch-two", Origin: domain.MessageOriginDaemon},
	} {
		if _, err := h.svc.Send(ctx, testSession, msg); err != nil {
			t.Fatal(err)
		}
	}
	result, err := h.svc.SteerOrSend(ctx, testSession, ports.ChatUserMessage{
		Text: "new direction", ClientMessageID: "steer-batch-handle", Origin: domain.MessageOriginHuman,
	}, false)
	if err != nil || !result.Steered {
		t.Fatalf("steer result = %+v, %v", result, err)
	}
	steers := provider.steers()
	if len(steers) != 1 {
		t.Fatalf("steer calls = %+v", steers)
	}
	if steers[0].msg.ClientMessageID != "steer-batch-handle" {
		t.Fatalf("steer batch handle = %q", steers[0].msg.ClientMessageID)
	}
	items := parseDeliveredBatch(t, steers[0].msg.Text)
	if len(items) != 3 || items[0].Text != "first queued" || items[1].Text != "changes requested" ||
		items[2].Kind != "steer" || items[2].Text != "new direction" {
		t.Fatalf("steered batch = %+v", items)
	}
	if _, err := h.st.NextQueuedTurn(ctx, h.ctrl.ConversationID()); !errors.Is(err, domain.ErrNoQueuedTurn) {
		t.Fatalf("queue after steer = %v", err)
	}
}

func TestQueuedBatchDoesNotTruncateProviderInput(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "active", ClientMessageID: "large-active", Origin: domain.MessageOriginHuman,
	}); err != nil {
		t.Fatal(err)
	}
	large := strings.Repeat("complete content ", 9000)
	for i, text := range []string{large, "tail is retained"} {
		if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
			Text: text, ClientMessageID: "large-" + string(rune('a'+i)), Origin: domain.MessageOriginHuman,
		}); err != nil {
			t.Fatal(err)
		}
	}
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted,
		ProviderTurnID: "provider-turn-1", TurnState: domain.TurnStateCompleted})
	h.awaitSnapshot(t, func(store.ConversationSnapshot) bool { return len(h.conv.sentTexts()) == 2 })
	items := parseDeliveredBatch(t, h.conv.sentTexts()[1])
	if len(items) != 2 {
		t.Fatalf("batch content item count = %d", len(items))
	}
	if items[0].Text != large || items[1].Text != "tail is retained" {
		t.Fatalf("batch content truncated: firstLength=%d", len(items[0].Text))
	}
}
