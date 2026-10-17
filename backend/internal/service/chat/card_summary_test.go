package chat

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestAfterProjectEmitsCardSignals(t *testing.T) {
	var got []domain.CardSignal
	c := &Controller{
		sessionID: "worker-1",
		now:       time.Now,
		onCardSignal: func(_ context.Context, id domain.SessionID, signal domain.CardSignal) {
			if id != "worker-1" {
				t.Errorf("session id = %q", id)
			}
			got = append(got, signal)
		},
	}
	send := func(event ports.ChatEvent, primary bool) {
		defer func() { _ = recover() }() // a primary turn end drains a queue that needs a real store, after the signal
		c.afterProject(context.Background(), event, primary)
	}
	activity := func(kind domain.ActivityKind, summary string) ports.ChatEvent {
		return ports.ChatEvent{Kind: ports.ChatEventActivityStarted, ActivityKind: kind, Summary: summary}
	}
	send(ports.ChatEvent{Kind: ports.ChatEventTurnStarted}, true)
	send(activity(domain.ActivityKindCommand, "go test ./..."), false)
	send(activity(domain.ActivityKindFileChange, "Created todo.py"), false)
	send(activity(domain.ActivityKindReasoning, "Thinking"), false) // says nothing about progress
	send(ports.ChatEvent{Kind: ports.ChatEventActivityCompleted, ActivityKind: domain.ActivityKindCommand, ActivityStatus: domain.ActivityStatusCompleted, Summary: "go test ./..."}, false)
	send(ports.ChatEvent{Kind: ports.ChatEventActivityCompleted, ActivityKind: domain.ActivityKindCommand, ActivityStatus: domain.ActivityStatusFailed, Summary: "go build"}, false)
	send(ports.ChatEvent{Kind: ports.ChatEventMessageCompleted, Text: " All tests pass. "}, false)
	send(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, TurnState: domain.TurnStateCompleted}, false) // a side turn
	send(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, TurnState: domain.TurnStateCompleted}, true)
	want := []domain.CardSignal{
		{Kind: domain.CardSignalTurnStarted},
		{Kind: domain.CardSignalActivity, Text: "go test ./..."},
		{Kind: domain.CardSignalActivity, Text: "Created todo.py"},
		{Kind: domain.CardSignalActivity, Text: "Failed: go build"},
		{Kind: domain.CardSignalAssistant, Text: "All tests pass."},
		{Kind: domain.CardSignalTurnCompleted},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("signals = %#v\nwant     %#v", got, want)
	}

	// A reviewer's conversation must never feed the reviewed task's card.
	got, c.reviewID = nil, "review-1"
	send(activity(domain.ActivityKindFileChange, "Edited"), false)
	send(ports.ChatEvent{Kind: ports.ChatEventMessageCompleted, Text: "Looks fine"}, false)
	send(ports.ChatEvent{Kind: ports.ChatEventTurnStarted}, true)
	if len(got) != 0 {
		t.Fatalf("reviewer conversation emitted card signals: %#v", got)
	}
}

func TestTurnEndSignalSaysWhetherTheTurnFinished(t *testing.T) {
	for _, tt := range []struct {
		name    string
		event   ports.ChatEvent
		stopped bool
	}{
		{"completed", ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, TurnState: domain.TurnStateCompleted}, false},
		{"failed", ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, TurnState: domain.TurnStateFailed}, true},
		{"interrupted", ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, TurnState: domain.TurnStateInterrupted}, true},
		{"errored", ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, TurnState: domain.TurnStateCompleted, Err: errors.New("boom")}, true},
		{"reauth", ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, TurnState: domain.TurnStateCompleted, Err: ports.ErrChatAuthRequired}, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var got []domain.CardSignal
			c := &Controller{sessionID: "w", now: time.Now,
				onCardSignal: func(_ context.Context, _ domain.SessionID, s domain.CardSignal) { got = append(got, s) }}
			func() {
				defer func() { _ = recover() }() // the queue drain after the signal needs a real store
				c.afterProject(context.Background(), tt.event, true)
			}()
			if len(got) != 1 || got[0].Kind != domain.CardSignalTurnCompleted || got[0].Stopped != tt.stopped {
				t.Fatalf("signals = %#v, want one turn_completed with Stopped=%v", got, tt.stopped)
			}
		})
	}
}
