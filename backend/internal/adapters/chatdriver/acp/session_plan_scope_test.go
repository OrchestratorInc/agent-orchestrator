package acp

import (
	"context"
	"testing"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func planNotification() acpsdk.SessionNotification {
	return acpsdk.SessionNotification{
		SessionId: "session-1",
		Update: acpsdk.SessionUpdate{Plan: &acpsdk.SessionUpdatePlan{Entries: []acpsdk.PlanEntry{
			{Content: "step one", Status: acpsdk.PlanEntryStatusCompleted},
			{Content: "step two", Status: acpsdk.PlanEntryStatusInProgress},
		}}},
	}
}

func planEvent(t *testing.T, conv *conversation) ports.ChatEvent {
	t.Helper()
	event := nextEvent(t, conv.Events())
	if event.Kind != ports.ChatEventPlanUpdated || event.Plan == nil {
		t.Fatalf("event = %#v, want a plan update", event)
	}
	return event
}

// An agent that keeps one task list for the whole session re-sends it on every
// turn. The plan must say so, or AO treats each copy as a finished turn's plan.
func TestACPPlanUpdateIsSessionScopedWhenTheAgentKeepsOneList(t *testing.T) {
	conv := replayConversation()
	conv.activeTurn = "turn-1"
	conv.sessionScopedPlan = true

	if err := conv.SessionUpdate(context.Background(), planNotification()); err != nil {
		t.Fatalf("SessionUpdate: %v", err)
	}
	if plan := planEvent(t, conv).Plan; !plan.SessionScoped || len(plan.Steps) != 2 {
		t.Fatalf("session update plan = %#v, want two steps marked session scoped", plan)
	}

	conv.UpdatePlan(&domain.ConversationPlan{Steps: []domain.ConversationPlanStep{{Text: "x", Status: domain.PlanStepPending}}})
	if plan := planEvent(t, conv).Plan; !plan.SessionScoped {
		t.Fatalf("extension plan = %#v, want session scoped", plan)
	}
}

// Other agents keep their plans per turn, as before.
func TestACPPlanUpdateStaysTurnScopedByDefault(t *testing.T) {
	conv := replayConversation()
	conv.activeTurn = "turn-1"

	if err := conv.SessionUpdate(context.Background(), planNotification()); err != nil {
		t.Fatalf("SessionUpdate: %v", err)
	}
	if plan := planEvent(t, conv).Plan; plan.SessionScoped {
		t.Fatalf("session update plan = %#v, want turn scoped", plan)
	}

	conv.UpdatePlan(&domain.ConversationPlan{Steps: []domain.ConversationPlanStep{{Text: "x", Status: domain.PlanStepPending}}})
	if plan := planEvent(t, conv).Plan; plan.SessionScoped {
		t.Fatalf("extension plan = %#v, want turn scoped", plan)
	}
}
