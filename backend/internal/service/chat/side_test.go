package chat

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type deferredSideTestConversation struct{ started chan string }

func (*deferredSideTestConversation) ProviderConversationID() string       { return "provider-side-1" }
func (*deferredSideTestConversation) Capabilities() ports.ChatCapabilities { return nil }
func (*deferredSideTestConversation) SendTurn(context.Context, ports.ChatUserMessage) (ports.ChatTurnRef, error) {
	return ports.ChatTurnRef{ProviderTurnID: "provider-turn-1"}, nil
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

func TestSideTurnStartsDeferredACPProviderTurn(t *testing.T) {
	ctx := context.Background()
	svc := New(Options{AppRunID: "launch-1"})
	manager := svc.sides
	now := time.Now().UTC()
	if _, err := manager.store.ClaimSideLaunch(ctx, "launch-1", now); err != nil {
		t.Fatal(err)
	}
	side := domain.SideConversation{ID: "side-1", SessionID: "session-1", MainConversationID: "main-1",
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
	turn, _, found, err := manager.store.ClaimNextSideTurn(ctx, "launch-1", now)
	if err != nil || !found {
		t.Fatalf("claim side turn: found=%v err=%v", found, err)
	}
	started := make(chan string, 1)
	conv := &deferredSideTestConversation{started: started}
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
		ProviderTurnID: providerTurnID, TurnState: domain.TurnStateCompleted}
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("side did not settle the completed provider turn")
	}
	settled, err := manager.store.SideTurn(ctx, side.ID, turn.ID)
	if err != nil || settled.State != "completed" {
		t.Fatalf("side turn = %#v, %v", settled, err)
	}
}
