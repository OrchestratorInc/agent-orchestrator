package chat_test

import (
	"context"
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

// probeStore runs a callback right after a user message is recorded, which is the
// window where the turn row is queued but dispatch has not moved it on.
type probeStore struct {
	chatsvc.Store
	afterAppend func()
}

func (s *probeStore) AppendUserMessage(ctx context.Context, conversationID string, session domain.SessionID, generation string, msg domain.ConversationMessage, turnID string, now time.Time) (bool, error) {
	created, err := s.Store.AppendUserMessage(ctx, conversationID, session, generation, msg, turnID, now)
	if s.afterAppend != nil {
		s.afterAppend()
	}
	return created, err
}

// newProbeHarness is the shared harness plus a snapshot reader, so the test can read
// what a client would be told at a precise moment of a send.
func newProbeHarness(t *testing.T) (*harness, *probeStore) {
	t.Helper()
	st := openStore(t)
	base := newFakeConversation()
	h := &harness{
		st:       st,
		conv:     base,
		activity: &recordingActivity{},
		clock:    time.Date(2026, 8, 2, 10, 0, 0, 0, time.UTC),
	}
	probe := &probeStore{Store: st}
	var (
		idMu    sync.Mutex
		counter int
	)
	svc := chatsvc.New(chatsvc.Options{
		Store:            probe,
		Reader:           fullSnapshotReader(st),
		Sessions:         st,
		StopProviderHost: func(context.Context, domain.SessionID) error { return nil },
		Drivers:          fakeRegistry{driver: fakeDriver{conv: base}},
		Activity:         h.activity,
		Log:              slog.New(slog.DiscardHandler),
		NewID: func() string {
			idMu.Lock()
			defer idMu.Unlock()
			counter++
			return fmt.Sprintf("id-%03d", counter)
		},
		Now: h.now,
	})
	ctrl, err := svc.Start(context.Background(), chatsvc.StartConfig{
		SessionID:     testSession,
		ProjectID:     testProject,
		Harness:       domain.HarnessCodex,
		WorkspacePath: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = svc.Stop(context.Background(), testSession) })
	h.svc, h.ctrl = svc, ctrl
	return h, probe
}

func (h *harness) apiTurnStates(t *testing.T) []domain.TurnState {
	t.Helper()
	snapshot, err := h.svc.Snapshot(context.Background(), testSession)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	states := make([]domain.TurnState, 0, len(snapshot.Turns))
	for _, turn := range snapshot.Turns {
		states = append(states, turn.State)
	}
	return states
}

func (h *harness) storedTurnStates(t *testing.T) []domain.TurnState {
	t.Helper()
	rows, err := h.st.LoadConversationSnapshot(context.Background(), h.ctrl.ConversationID())
	if err != nil {
		t.Fatalf("load rows: %v", err)
	}
	states := make([]domain.TurnState, 0, len(rows.Turns))
	for _, turn := range rows.Turns {
		states = append(states, turn.State)
	}
	return states
}

func sameStates(got, want []domain.TurnState) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestSnapshotReportsASendToAnIdleAgentAsRunningWhileItIsBeingRecorded(t *testing.T) {
	h, probe := newProbeHarness(t)
	var stored, reported []domain.TurnState
	probe.afterAppend = func() {
		stored = h.storedTurnStates(t)
		reported = h.apiTurnStates(t)
	}

	if _, err := h.svc.Send(context.Background(), testSession, ports.ChatUserMessage{
		Text: "hello", ClientMessageID: "c1", Origin: domain.MessageOriginHuman,
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	if want := []domain.TurnState{domain.TurnStateQueued}; !sameStates(stored, want) {
		t.Fatalf("stored rows right after the insert = %v, want %v (the window under test)", stored, want)
	}
	if want := []domain.TurnState{domain.TurnStateRunning}; !sameStates(reported, want) {
		t.Fatalf("snapshot in that window = %v, want %v: an idle agent's message must not look queued", reported, want)
	}
}

func TestSnapshotKeepsAMessageSentBehindARunningTurnQueued(t *testing.T) {
	h, probe := newProbeHarness(t)
	ctx := context.Background()
	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "first", ClientMessageID: "c1", Origin: domain.MessageOriginHuman,
	}); err != nil {
		t.Fatalf("first Send: %v", err)
	}
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-1"})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return len(s.Turns) == 1 && s.Turns[0].State == domain.TurnStateRunning
	})

	var reported []domain.TurnState
	probe.afterAppend = func() { reported = h.apiTurnStates(t) }
	queued, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "second", ClientMessageID: "c2", Origin: domain.MessageOriginHuman,
	})
	if err != nil {
		t.Fatalf("second Send: %v", err)
	}

	want := []domain.TurnState{domain.TurnStateRunning, domain.TurnStateQueued}
	if !sameStates(reported, want) {
		t.Fatalf("snapshot while the second send was recorded = %v, want %v", reported, want)
	}
	for _, id := range h.ctrl.DispatchingTurnIDs() {
		if id == queued.ID {
			t.Fatalf("a genuinely queued turn %s was marked as dispatching", queued.ID)
		}
	}
	if got := h.apiTurnStates(t); !sameStates(got, want) {
		t.Fatalf("snapshot after the second send = %v, want %v", got, want)
	}
}

func TestDispatchingMarkerSurvivesASuccessfulSendAndIgnoresDuplicates(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	first, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "hello", ClientMessageID: "c1", Origin: domain.MessageOriginHuman,
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if got := h.ctrl.DispatchingTurnIDs()[0]; got != first.ID {
		t.Fatalf("marker after a dispatched send = %q, want %q", got, first.ID)
	}

	duplicate, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "hello", ClientMessageID: "c1", Origin: domain.MessageOriginHuman,
	})
	if err != nil {
		t.Fatalf("duplicate Send: %v", err)
	}
	if duplicate.ID != "" {
		t.Fatalf("duplicate Send created turn %q", duplicate.ID)
	}
	if got := h.ctrl.DispatchingTurnIDs()[0]; got != first.ID {
		t.Fatalf("marker after a duplicate send = %q, want the original %q", got, first.ID)
	}
}

func TestDispatchingMarkerIsClearedWhenDispatchFails(t *testing.T) {
	h := newHarness(t)
	h.conv.mu.Lock()
	h.conv.sendErr = errors.New("provider refused")
	h.conv.mu.Unlock()

	if _, err := h.svc.Send(context.Background(), testSession, ports.ChatUserMessage{
		Text: "hello", ClientMessageID: "c1", Origin: domain.MessageOriginHuman,
	}); err == nil {
		t.Fatal("Send succeeded, want the provider error")
	}

	if got := h.ctrl.DispatchingTurnIDs()[0]; got != "" {
		t.Fatalf("marker after a failed dispatch = %q, want it cleared", got)
	}
}
