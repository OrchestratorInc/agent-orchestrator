package chat

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type sideCompactionReceiptConversation struct {
	recoveredSideTestConversation
	ackStarted chan string
	allowAck   chan struct{}
	ackErr     error
}

func (*sideCompactionReceiptConversation) Capabilities() ports.ChatCapabilities {
	return ports.ChatCapabilities{ports.ChatCapabilityCompaction: true}
}

func (c *sideCompactionReceiptConversation) Compact(context.Context) (ports.ChatCompactionResult, error) {
	c.mu.Lock()
	c.sent++
	c.acked = false
	c.mu.Unlock()
	c.events <- ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "compaction",
		ProviderEventID: "receipt", TurnState: domain.TurnStateCompleted}
	return ports.ChatCompactionResult{}, nil
}

func (c *sideCompactionReceiptConversation) AcknowledgeProviderEvent(ctx context.Context, id string) error {
	c.ackStarted <- id
	select {
	case <-c.allowAck:
	case <-ctx.Done():
		return ctx.Err()
	}
	if c.ackErr != nil {
		return c.ackErr
	}
	return c.deferredSideTestConversation.AcknowledgeProviderEvent(ctx, id)
}

func TestSideCompactionAcknowledgesReceiptBeforeDispatch(t *testing.T) {
	for _, failAck := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "ack failure"}[failAck], func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			svc := New(Options{AppRunID: "run", Sessions: sideSessionReader{}})
			now := time.Now().UTC()
			if _, err := svc.sides.store.ClaimSideLaunch(ctx, "run", now); err != nil {
				t.Fatal(err)
			}
			side := domain.SideConversation{PolicyVersion: 1, ID: "side", SessionID: "session", AppRunID: "run", Generation: "g", State: "ready"}
			if _, _, err := svc.sides.store.CreateSideConversation(ctx, side); err != nil {
				t.Fatal(err)
			}
			conv := &sideCompactionReceiptConversation{ackStarted: make(chan string, 1), allowAck: make(chan struct{})}
			conv.events = make(chan ports.ChatEvent, 1)
			conv.acknowledged = make(chan string, 1)
			if failAck {
				conv.ackErr = errors.New("host receipt acknowledgement failed")
			}
			runtime := &sideRuntime{side: side, conv: conv}
			svc.sides.runtimes[side.ID] = runtime
			done := make(chan struct{})
			go func() { svc.sides.consumeEvents(ctx, runtime); close(done) }()
			defer func() { cancel(); <-done }()
			if _, err := svc.CompactSideChat(ctx, side.SessionID, side.ID); err != nil {
				t.Fatal(err)
			}
			select {
			case id := <-conv.ackStarted:
				if id != "receipt" {
					t.Fatalf("acknowledgement ID = %q", id)
				}
			case <-time.After(time.Second):
				t.Fatal("compaction receipt was not acknowledged")
			}
			runtime.mu.Lock()
			compacting := runtime.compacting
			runtime.mu.Unlock()
			if !compacting {
				t.Fatal("dispatch unblocked before acknowledgement")
			}
			close(conv.allowAck)
			select {
			case <-svc.sides.wake:
			case <-time.After(time.Second):
				t.Fatal("compaction did not settle")
			}
			runtime.mu.Lock()
			blocked, compacting := runtime.dispatchBlocked, runtime.compacting
			runtime.mu.Unlock()
			if compacting || blocked != failAck {
				t.Fatalf("compacting=%v blocked=%v, want false/%v", compacting, blocked, failAck)
			}
			if failAck {
				stored, err := svc.sides.store.SideConversation(ctx, side.ID)
				if err != nil || stored.State != "failed" || stored.ErrorMessage == "" {
					t.Fatalf("ack failure not surfaced: %#v, %v", stored, err)
				}
			} else if _, err := conv.SendTurn(ctx, ports.ChatUserMessage{Text: "next question"}); err != nil {
				t.Fatalf("next prompt rejected after compaction: %v", err)
			}
		})
	}
}
