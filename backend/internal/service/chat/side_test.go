package chat

import (
	"context"
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

func TestSideTurnStartsAndAcknowledgesDeferredACPProviderTurn(t *testing.T) {
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
	next, _, found, err := manager.store.ClaimNextSideTurn(ctx, "launch-1", now.Add(time.Second))
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
