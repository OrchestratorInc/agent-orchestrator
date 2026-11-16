package controllers

import (
	"encoding/json"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestCapturedExcerptSummaryPreservesSnapshot(t *testing.T) {
	snapshot := &domain.ChatExcerptSnapshot{Version: 1, ReferenceFormat: "numbered-v1", ContainsFlowchart: true, BadgeOnly: true, RawText: "**raw**\n\tcode", CaptureMode: "exact"}
	snapshot.SourceRange = &domain.ChatExcerptRange{Start: 100, End: 100 + len(snapshot.RawText), OffsetUnit: "utf-16"}
	snapshot.DiagramRange = &domain.ChatExcerptRange{Start: 100, End: 100 + len(snapshot.RawText), OffsetUnit: "utf-16"}
	snapshot.SourceTurnID = "turn"
	snapshot.Turn = &domain.ChatExcerptTurnSnapshot{ID: "turn", Messages: []domain.ChatExcerptTurnMessage{{Role: "assistant", Text: "frozen context"}}}
	content, err := json.Marshal([]ports.ChatContent{{Type: "excerpt", Excerpt: &ports.ChatExcerptContext{Reference: ports.ChatExcerptReference{ID: "quote", ConversationID: "c", MessageID: "deleted", Snapshot: snapshot}, SelectedText: "raw\n\tcode"}}})
	if err != nil {
		t.Fatal(err)
	}
	got, ok := conversationContentSummary(domain.ConversationMessage{Role: domain.MessageRoleUser, Origin: domain.MessageOriginHuman, DeliveryContentJSON: string(content)})
	if !ok || len(got) != 1 || got[0].Snapshot == nil || got[0].Snapshot.RawText != snapshot.RawText || got[0].SourceMessageID != "deleted" || got[0].Text != "raw\n\tcode" {
		t.Fatalf("summary = %+v", got)
	}
	if got[0].SourceRevision == nil || *got[0].SourceRevision != 0 {
		t.Fatal("revision zero must survive transcript reload")
	}
	if !got[0].Snapshot.ContainsFlowchart || !got[0].Snapshot.BadgeOnly || got[0].Snapshot.ReferenceFormat != "numbered-v1" {
		t.Fatal("preview and prompt metadata must survive transcript reload")
	}
	if got[0].Snapshot.DiagramRange == nil || *got[0].Snapshot.DiagramRange != *snapshot.DiagramRange {
		t.Fatal("diagram navigation anchor must survive transcript reload")
	}
	if got[0].Snapshot.Turn != nil || got[0].Snapshot.SourceTurnID != "turn" {
		t.Fatal("summary must omit turn context but retain source identity")
	}
	var delivery []ports.ChatContent
	if err := json.Unmarshal(content, &delivery); err != nil {
		t.Fatal(err)
	}
	if delivery[0].Excerpt.Reference.Snapshot.Turn.Messages[0].Text != "frozen context" {
		t.Fatal("summary must not change durable delivery context")
	}
	again, _ := conversationContentSummary(domain.ConversationMessage{Role: domain.MessageRoleUser, Origin: domain.MessageOriginHuman, DeliveryContentJSON: string(content)})
	if again[0].Snapshot.Turn != nil || snapshot.Turn == nil {
		t.Fatal("repeated reads must omit context without changing the original snapshot")
	}
}
