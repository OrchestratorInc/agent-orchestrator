package codexappserver

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/codexappserver/codexproto"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestCancelledApprovalClearsStaleActivity(t *testing.T) {
	c := &conversation{
		pending:       map[string]*parkedRequest{},
		pendingInputs: map[string]*parkedInput{},
		events:        make(chan ports.ChatEvent, 4),
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := c.handleServerRequest(ctx, serverRequest{
			ID:     json.RawMessage("42"),
			Method: codexproto.MethodItemCommandExecutionRequestApproval,
			Params: json.RawMessage(`{"command":"echo synthetic","availableDecisions":["accept","cancel"]}`),
		})
		result <- err
	}()

	var requested ports.ChatEvent
	select {
	case requested = <-c.events:
	case <-time.After(2 * time.Second):
		t.Fatal("provider approval did not become pending")
	}
	if requested.Kind != ports.ChatEventApprovalRequested || requested.RequestID == "" {
		t.Fatalf("request event = %+v", requested)
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel result = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled provider approval stayed blocked")
	}
	select {
	case resolved := <-c.events:
		if resolved.Kind != ports.ChatEventApprovalResolved || resolved.RequestID != requested.RequestID {
			t.Fatalf("stale request was not resolved: %+v", resolved)
		}
		var detail struct {
			ResolvedBy string `json:"resolvedBy"`
			Reason     string `json:"reason"`
		}
		if err := json.Unmarshal(resolved.Detail, &detail); err != nil {
			t.Fatal(err)
		}
		if detail.ResolvedBy != "orchestrator" || detail.Reason != "cancelled" {
			t.Fatalf("resolution must not imply consent: %+v", detail)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expired approval still has an actionable UI card")
	}
	if err := c.ResolveRequest(context.Background(), requested.RequestID, ports.ChatDecision{ID: "accept"}); !errors.Is(err, ports.ErrChatRequestNotPending) {
		t.Fatalf("late decision = %v, want ErrChatRequestNotPending", err)
	}
}

func TestExpiredApprovalResolutionIsIdempotent(t *testing.T) {
	c := &conversation{
		pending: map[string]*parkedRequest{"req": {ch: make(chan ports.ChatDecision, 1)}},
		events:  make(chan ports.ChatEvent, 2),
	}
	c.expirePendingApproval("req", "timeout")
	c.expirePendingApproval("req", "timeout")
	if got := len(c.events); got != 1 {
		t.Fatalf("stale request emitted %d resolutions, want 1", got)
	}
	got := <-c.events
	if got.Kind != ports.ChatEventApprovalResolved || got.RequestID != "req" {
		t.Fatalf("resolved event = %+v", got)
	}
	if string(got.Detail) != `{"reason":"timeout","resolvedBy":"orchestrator"}` {
		t.Fatalf("unexpected resolution detail = %s", got.Detail)
	}
}
