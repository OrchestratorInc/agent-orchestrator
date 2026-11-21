package chat_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/store"
)

// The Codex driver files a child's steps under their launch and leaves them on
// the child's own turn. Each side's turn settles only its own rows.

const childStepDetail = `{"parentProviderItemId":"call-spawn","childTurn":true}`

func startCodexParentWithChild(t *testing.T, h *harness) {
	t.Helper()
	if _, err := h.svc.Send(context.Background(), testSession, ports.ChatUserMessage{
		Text: "root work", ClientMessageID: "c1", Origin: domain.MessageOriginHuman,
	}); err != nil {
		t.Fatalf("send root turn: %v", err)
	}
	h.conv.emit(
		ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-1", ProviderConversationID: "thread-1"},
		ports.ChatEvent{
			Kind: ports.ChatEventActivityCompleted, ProviderTurnID: "provider-turn-1", ProviderItemID: "call-spawn",
			ActivityKind: domain.ActivityKindMCPTool, ActivityStatus: domain.ActivityStatusCompleted, Summary: "Check the build",
			Detail: []byte(`{"providerToolName":"spawnAgent","background":true}`),
		},
		ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-child-1", ProviderConversationID: "child-thread-1"},
	)
}

func TestCodexChildStepKeepsRunningWhenTheParentFinishesFirst(t *testing.T) {
	h := newHarness(t)
	startCodexParentWithChild(t, h)
	h.conv.emit(
		ports.ChatEvent{
			Kind: ports.ChatEventActivityStarted, ProviderTurnID: "provider-child-1", ProviderConversationID: "child-thread-1",
			ProviderItemID: "child-cmd", ActivityKind: domain.ActivityKindCommand, ActivityStatus: domain.ActivityStatusRunning,
			Summary: "go build ./...", Detail: []byte(childStepDetail),
		},
		ports.ChatEvent{
			Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "provider-turn-1", ProviderConversationID: "thread-1",
			TurnState: domain.TurnStateCompleted,
		},
	)
	snapshot := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return turnStateByText(t, s)["root work"] == domain.TurnStateCompleted
	})
	if command := activityByItem(snapshot, "child-cmd"); command.Status != domain.ActivityStatusRunning {
		t.Fatalf("child command after the parent finished = %+v, want still running", command)
	}

	h.conv.emit(ports.ChatEvent{
		Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "provider-child-1", ProviderConversationID: "child-thread-1",
		TurnState: domain.TurnStateInterrupted,
	})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return activityByItem(s, "child-cmd").Status == domain.ActivityStatusCancelled
	})
}

func TestCodexChildFailureSettlesItsOwnRowsOnly(t *testing.T) {
	h := newHarness(t)
	startCodexParentWithChild(t, h)
	h.conv.emit(
		ports.ChatEvent{
			Kind: ports.ChatEventActivityStarted, ProviderTurnID: "provider-turn-1", ProviderConversationID: "thread-1",
			ProviderItemID: "root-cmd", ActivityKind: domain.ActivityKindCommand, ActivityStatus: domain.ActivityStatusRunning,
			Summary: "go test ./...",
		},
		// A retry the child's failure ends: the pump sends no completion for it.
		ports.ChatEvent{
			Kind: ports.ChatEventActivityStarted, ProviderTurnID: "provider-child-1", ProviderConversationID: "child-thread-1",
			ProviderItemID: "codex-retry:child-thread-1:provider-child-1", ActivityKind: domain.ActivityKindSystem,
			ActivityStatus: domain.ActivityStatusRunning, Summary: "Reconnecting", Detail: []byte(childStepDetail),
		},
		ports.ChatEvent{
			Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "provider-child-1", ProviderConversationID: "child-thread-1",
			TurnState: domain.TurnStateFailed, Err: errors.New("stream disconnected"),
		},
	)
	snapshot := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return activityByItem(s, "codex-retry:child-thread-1:provider-child-1").Status == domain.ActivityStatusFailed
	})
	if states := turnStateByText(t, snapshot); states["root work"] != domain.TurnStateRunning {
		t.Fatalf("parent turn after the child failed = %q, want running", states["root work"])
	}
	if command := activityByItem(snapshot, "root-cmd"); command.Status != domain.ActivityStatusRunning {
		t.Fatalf("parent command after the child failed = %+v, want still running", command)
	}
}

// The parent and the child each plan. The parent's turn keeps its own plan, and the
// child's plan row reads as one of the child's steps.
func TestCodexChildPlanIsAStepNotTheParentPlan(t *testing.T) {
	h := newHarness(t)
	startCodexParentWithChild(t, h)
	plan := func(text string) *domain.ConversationPlan {
		return &domain.ConversationPlan{Steps: []domain.ConversationPlanStep{{Text: text, Status: domain.PlanStepInProgress}}}
	}
	h.conv.emit(
		ports.ChatEvent{Kind: ports.ChatEventPlanUpdated, ProviderTurnID: "provider-turn-1", Plan: plan("Spawn a checker")},
		ports.ChatEvent{
			Kind: ports.ChatEventPlanUpdated, ProviderTurnID: "provider-child-1", ProviderConversationID: "child-thread-1",
			Plan: plan("Run the build"), Detail: []byte(childStepDetail),
		},
	)
	snapshot := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		plans := 0
		for _, activity := range s.Activities {
			if activity.Kind == domain.ActivityKindPlan {
				plans++
			}
		}
		return plans == 2
	})

	turnByID := map[string]domain.ConversationTurn{}
	for _, turn := range snapshot.Turns {
		turnByID[turn.ID] = turn
		if turn.ProviderTurnID == "provider-turn-1" && (turn.Plan == nil || turn.Plan.Steps[0].Text != "Spawn a checker") {
			t.Fatalf("parent turn plan = %+v", turn.Plan)
		}
	}
	for _, activity := range snapshot.Activities {
		if activity.Kind != domain.ActivityKindPlan {
			continue
		}
		var detail struct {
			Parent    string `json:"parentProviderItemId"`
			ChildTurn bool   `json:"childTurn"`
			Steps     []struct {
				Text string `json:"text"`
			} `json:"steps"`
		}
		if err := json.Unmarshal(activity.Detail, &detail); err != nil {
			t.Fatalf("plan detail %s: %v", activity.Detail, err)
		}
		switch turnByID[activity.TurnID].ProviderTurnID {
		case "provider-turn-1":
			if detail.Parent != "" || detail.Steps[0].Text != "Spawn a checker" {
				t.Fatalf("parent plan row = %s", activity.Detail)
			}
		case "provider-child-1":
			if detail.Parent != "call-spawn" || !detail.ChildTurn || detail.Steps[0].Text != "Run the build" {
				t.Fatalf("child plan row = %s", activity.Detail)
			}
		default:
			t.Fatalf("plan row on an unexpected turn: %+v", activity)
		}
	}
}
