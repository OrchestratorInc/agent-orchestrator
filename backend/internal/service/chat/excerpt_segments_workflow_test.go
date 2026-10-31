package chat_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/store"
)

func mixedWorkflowSource(t *testing.T, h *sideWorkflow) ports.ChatExcerptReference {
	t.Helper()
	ctx := context.Background()
	turn, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "Explain the command output", ClientMessageID: "mixed-seed", Origin: domain.MessageOriginHuman})
	if err != nil {
		t.Fatal(err)
	}
	detail := json.RawMessage(`{"command":"Write-Output 'Code'","output":"aggregate"}`)
	h.conv.emit(startedCommand(turn.ProviderTurnID, "mixed-command", "Ran powershell"), outputDelta(turn.ProviderTurnID, "mixed-command", "Copied ✓\nDiagram"),
		ports.ChatEvent{Kind: ports.ChatEventActivityCompleted, ProviderTurnID: turn.ProviderTurnID, ProviderItemID: "mixed-command", ActivityKind: domain.ActivityKindCommand, ActivityStatus: domain.ActivityStatusCompleted, Summary: "Ran powershell", Detail: detail},
		ports.ChatEvent{Kind: ports.ChatEventMessageCompleted, ProviderTurnID: turn.ProviderTurnID, ProviderItemID: "mixed-reply", Text: "The **Code** result follows."},
		ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: turn.ProviderTurnID, TurnState: domain.TurnStateCompleted})
	snap := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return len(s.Messages) == 2 && len(s.Activities) == 1 && s.Activities[0].Status == domain.ActivityStatusCompleted && s.Turns[0].State == domain.TurnStateCompleted
	})
	if err = h.svc.DrainQueued(ctx, testSession); err != nil {
		t.Fatal(err)
	}
	command, reply := snap.Activities[0], snap.Messages[1]
	return ports.ChatExcerptReference{ConversationID: h.ctrl.ConversationID(), Text: "Write-Output 'Code'\n\nCopied ✓\nDiagram\n\nThe Code result follows.", Segments: []domain.ExcerptSegment{
		{Kind: "command", SourceID: command.ID, Revision: command.Revision, Field: "command", Text: "Write-Output 'Code'"},
		{Kind: "command", SourceID: command.ID, Revision: command.Revision, Field: "output", Text: "Copied ✓\nDiagram"},
		{Kind: "message", SourceID: reply.ID, Revision: reply.Revision, Text: "The Code result follows."}}}
}

func TestMixedExcerptDeliveredToMainAndSideProviders(t *testing.T) {
	for _, destination := range []string{"main", "existing side", "new side"} {
		t.Run(destination, func(t *testing.T) {
			h := newSideWorkflow(t, "")
			ref := mixedWorkflowSource(t, h)
			ctx := context.Background()
			if destination == "main" {
				if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "Explain this", ClientMessageID: "mixed-followup", Origin: domain.MessageOriginHuman, Excerpts: []ports.ChatExcerptReference{ref}}); err != nil {
					t.Fatal(err)
				}
				sent := h.conv.sentMessages()
				if len(sent) != 2 || !strings.Contains(sent[1].Text, ref.Text) {
					t.Fatalf("main delivery=%+v", sent)
				}
				return
			}
			var side domain.SideConversation
			if destination == "new side" {
				var err error
				side, err = h.svc.CreateIndependentSideChat(ctx, testSession, chatsvc.SideCreateRequest{IdempotencyKey: "mixed-create", ForceNew: true, Reference: &ref})
				if err != nil {
					t.Fatal(err)
				}
				h.awaitSide(t, side.ID, func(s domain.SideSnapshot) bool { return s.Side.State == "ready" })
				recovered, err := h.svc.CreateIndependentSideChat(ctx, testSession, chatsvc.SideCreateRequest{IdempotencyKey: "mixed-create", ForceNew: true, Reference: &ref})
				if err != nil || recovered.ID != side.ID {
					t.Fatalf("creation recovery=%+v %v", recovered, err)
				}
			} else {
				side = h.side(t, "existing", true)
			}
			request := ports.ChatUserMessage{Text: "Explain this", ClientMessageID: "mixed-side", Excerpts: []ports.ChatExcerptReference{ref}}
			accepted, err := h.svc.SendSideQuestion(ctx, testSession, side.ID, request)
			if err != nil {
				t.Fatal(err)
			}
			h.awaitSide(t, side.ID, func(s domain.SideSnapshot) bool { return h.provider(0).sendCallCount() == 1 })
			if got := h.provider(0).sentTexts()[0]; !strings.Contains(got, "Copied ✓") || !strings.Contains(got, "Write-Output 'Code'") {
				t.Fatalf("side delivery=%s", got)
			}
			if len(accepted.References) != 1 || len(accepted.References[0].Segments) != 3 {
				t.Fatalf("lost provenance=%+v", accepted)
			}
			duplicate, err := h.svc.SendSideQuestion(ctx, testSession, side.ID, request)
			if err != nil || duplicate.ID != accepted.ID {
				t.Fatalf("side retry=%+v %v", duplicate, err)
			}
			request.Excerpts[0].Segments = append([]domain.ExcerptSegment(nil), ref.Segments...)
			request.Excerpts[0].Segments[0].Revision++
			if _, err = h.svc.SendSideQuestion(ctx, testSession, side.ID, request); !errors.Is(err, chatsvc.ErrSideIdempotencyConflict) {
				t.Fatalf("changed receipt accepted=%v", err)
			}
		})
	}
}
