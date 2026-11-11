package chat

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestCapturedExcerptDoesNotReadSource(t *testing.T) {
	// A nil store panics if hydration tries to resolve the deleted source.
	c := &Controller{conversation: domain.ConversationRecord{ID: "conversation"}}
	ref := ports.ChatExcerptReference{ConversationID: "conversation", MessageID: "deleted", Revision: 10, Text: "bold\n\tcode();", Snapshot: &domain.ChatExcerptSnapshot{Version: 1, RawText: "**bold**\n\tcode();", CaptureMode: "exact"}}
	msg := ports.ChatUserMessage{Excerpts: []ports.ChatExcerptReference{ref}}
	if err := hydrateExcerptReferences(context.Background(), c, &msg); err != nil {
		t.Fatal(err)
	}
	if len(msg.Excerpts) != 0 || len(msg.Content) != 1 {
		t.Fatalf("hydration = %+v", msg)
	}
	delivery := excerptDeliveryMessage(msg)
	if !strings.Contains(delivery.Text, ref.Text) || !strings.Contains(delivery.Text, ref.Snapshot.RawText) {
		t.Fatal(delivery.Text)
	}
}

func TestChartOnlyExcerptDelivery(t *testing.T) {
	ref := ports.ChatExcerptReference{ConversationID: "c", MessageID: "m", Revision: 1, Snapshot: &domain.ChatExcerptSnapshot{Version: 1, ReferenceFormat: "numbered-v1", RawText: "```mermaid\nflowchart LR; A[Start]-->B[End]\n```", CaptureMode: "block", ContainsFlowchart: true, BadgeOnly: true}}
	if err := validateExcerptReferences([]ports.ChatExcerptReference{ref}, "c"); err != nil {
		t.Fatal(err)
	}
	msg := ports.ChatUserMessage{Excerpts: []ports.ChatExcerptReference{ref}}
	if err := hydrateExcerptReferences(context.Background(), &Controller{conversation: domain.ConversationRecord{ID: "c"}}, &msg); err != nil {
		t.Fatal(err)
	}
	if msg.Content[0].Excerpt.SelectedText != "" {
		t.Fatal("chart text must not be put in the readable quote")
	}
	prompt := excerptDeliveryMessage(msg).Text
	if !strings.Contains(prompt, "Annotation 1 — Flowchart attached") || !strings.Contains(prompt, ref.Snapshot.RawText) || strings.Contains(prompt, "Selected text:") {
		t.Fatal(prompt)
	}
	svc := &Service{}
	if _, err := svc.validateSideReferences(context.Background(), "s", domain.SideConversation{ID: "side", MainConversationID: "c"}, []ports.ChatExcerptReference{ref}); err != nil {
		t.Fatal(err)
	}
	for _, snapshot := range []*domain.ChatExcerptSnapshot{nil, {Version: 1, CaptureMode: "block", RawText: "raw"}, {Version: 1, CaptureMode: "block", ContainsFlowchart: true, BadgeOnly: true}, {Version: 1, CaptureMode: "legacy", ContainsFlowchart: true, BadgeOnly: true}} {
		invalid := ref
		invalid.Snapshot = snapshot
		if err := validateExcerptReferences([]ports.ChatExcerptReference{invalid}, "c"); !errors.Is(err, ErrExcerptInvalid) {
			t.Fatalf("empty quote accepted without valid chart context: %+v, %v", snapshot, err)
		}
	}
}

func TestCapturedDiagramAnchorBounds(t *testing.T) {
	snapshot := domain.ChatExcerptSnapshot{Version: 1, RawText: "diagram source", CaptureMode: "block", ContainsFlowchart: true, SourceRange: &domain.ChatExcerptRange{Start: 100, End: 114, OffsetUnit: "utf-16"}, DiagramRange: &domain.ChatExcerptRange{Start: 102, End: 110, OffsetUnit: "utf-16"}}
	if err := validateExcerptSnapshot(&snapshot); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		mutate func(*domain.ChatExcerptSnapshot)
	}{
		{"outside raw context", func(s *domain.ChatExcerptSnapshot) {
			s.DiagramRange = &domain.ChatExcerptRange{Start: 102, End: 115, OffsetUnit: "utf-16"}
		}},
		{"missing source range", func(s *domain.ChatExcerptSnapshot) { s.SourceRange = nil }},
		{"not a diagram", func(s *domain.ChatExcerptSnapshot) { s.ContainsFlowchart = false }},
		{"wrong offset unit", func(s *domain.ChatExcerptSnapshot) {
			s.DiagramRange = &domain.ChatExcerptRange{Start: 102, End: 110, OffsetUnit: "bytes"}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			invalid := snapshot
			test.mutate(&invalid)
			if err := validateExcerptSnapshot(&invalid); !errors.Is(err, ErrExcerptInvalid) {
				t.Fatalf("invalid diagram anchor accepted: %v", err)
			}
		})
	}
}

func TestChartOnlyForkKeepsSourceEligibilityChecks(t *testing.T) {
	raw := "```mermaid\nflowchart LR; A-->B\n```"
	ref := &ports.ChatExcerptReference{ConversationID: "c", MessageID: "m", Revision: 1, Snapshot: &domain.ChatExcerptSnapshot{Version: 1, RawText: raw, CaptureMode: "block", ContainsFlowchart: true, BadgeOnly: true}}
	controller := &Controller{conversation: domain.ConversationRecord{ID: "c"}}
	rows := ConversationRows{Messages: []domain.ConversationMessage{{ID: "m", Revision: 1, Text: raw, TurnID: "t", Role: domain.MessageRoleAssistant}}, Turns: []domain.ConversationTurn{{ID: "t", State: domain.TurnStateCompleted}}}
	turn, _, selected, err := resolveSideAnchor(rows, controller, ref)
	if err != nil || turn != "t" || selected != "" {
		t.Fatalf("anchor = %q, %q, %v", turn, selected, err)
	}
	rows.Turns[0].State = domain.TurnStateRunning
	if _, _, _, err := resolveSideAnchor(rows, controller, ref); !errors.Is(err, ErrSideAnchorUnavailable) {
		t.Fatalf("unfinished turn = %v", err)
	}
	rows.Messages[0].Revision++
	if _, _, _, err := resolveSideAnchor(rows, controller, ref); !errors.Is(err, ErrExcerptStale) {
		t.Fatalf("changed source = %v", err)
	}
}

func TestCapturedExcerptInputBounds(t *testing.T) {
	ref := ports.ChatExcerptReference{ConversationID: "c", MessageID: "m", Text: "quote", Snapshot: &domain.ChatExcerptSnapshot{Version: 1, RawText: "raw", CaptureMode: "block"}}
	for _, test := range []struct {
		name   string
		mutate func(*ports.ChatExcerptReference)
	}{
		{"version", func(r *ports.ChatExcerptReference) { r.Snapshot.Version = 2 }},
		{"raw bytes", func(r *ports.ChatExcerptReference) { r.Snapshot.RawText = strings.Repeat("é", maxExcerptTextBytes) }},
		{"range", func(r *ports.ChatExcerptReference) {
			r.Snapshot.SourceRange = &domain.ChatExcerptRange{Start: 5, End: 1, OffsetUnit: "utf-16"}
		}},
		{"prefix", func(r *ports.ChatExcerptReference) { r.Snapshot.Prefix = strings.Repeat("🙂", 65) }},
		{"foreign", func(r *ports.ChatExcerptReference) { r.ConversationID = "other" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := ref
			snapshot := *ref.Snapshot
			r.Snapshot = &snapshot
			test.mutate(&r)
			if err := validateExcerptReferences([]ports.ChatExcerptReference{r}, "c"); !errors.Is(err, ErrExcerptInvalid) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestCapturedExcerptTurnDeduplicated(t *testing.T) {
	snapshot := &domain.ChatExcerptSnapshot{Version: 1, RawText: "raw", CaptureMode: "exact", SourceTurnID: "turn", Turn: &domain.ChatExcerptTurnSnapshot{ID: "turn", Messages: []domain.ChatExcerptTurnMessage{{Role: "assistant", Text: "one"}, {Role: "assistant", Text: "two"}}}}
	ref := ports.ChatExcerptReference{ConversationID: "c", Text: "quote", Snapshot: snapshot}
	msg := excerptDeliveryMessage(ports.ChatUserMessage{Content: []ports.ChatContent{snapshotExcerptContent(ref), snapshotExcerptContent(ref)}})
	if strings.Count(msg.Text, "Captured completed source turn") != 1 || strings.Count(msg.Text, "Captured raw source") != 2 {
		t.Fatal(msg.Text)
	}
}

func TestNumberedCapturedAnnotationsAndLegacyDelivery(t *testing.T) {
	snapshot := &domain.ChatExcerptSnapshot{Version: 1, ReferenceFormat: "numbered-v1", RawText: "**quote**", CaptureMode: "exact", SourceTurnID: "t", Turn: &domain.ChatExcerptTurnSnapshot{ID: "t", Messages: []domain.ChatExcerptTurnMessage{{Role: "assistant", Text: "background"}}}}
	ref := ports.ChatExcerptReference{ConversationID: "c", Text: "quote", Order: 1, Snapshot: snapshot}
	last := ref
	last.Order = 3
	msg := ports.ChatUserMessage{Text: "Explain the first annotation", Content: []ports.ChatContent{
		{Type: "file_reference", Text: FileReferenceQuotedPath, FileReference: &ports.ChatFileReference{Path: "a.ts", Order: 2}},
		snapshotExcerptContent(last), snapshotExcerptContent(ref),
	}}
	text := excerptDeliveryMessage(msg).Text
	if strings.Count(text, annotationPreamble) != 1 || strings.Count(text, "Captured completed source turn") != 1 || !strings.Contains(text, "**quote**") {
		t.Fatal(text)
	}
	if first, file, last := strings.Index(text, "Annotation 1 — Selected text:"), strings.Index(text, "Annotation 2 — File:"), strings.Index(text, "Annotation 3 — Selected text:"); first < 0 || file <= first || last <= file {
		t.Fatal(text)
	}
	// An already-prepared snapshot without the opt-in marker keeps its old prompt.
	legacy := *snapshot
	legacy.ReferenceFormat = ""
	ref.Snapshot = &legacy
	text = excerptDeliveryMessage(ports.ChatUserMessage{Content: []ports.ChatContent{snapshotExcerptContent(ref)}}).Text
	if strings.Contains(text, annotationPreamble) || !strings.Contains(text, "Referenced chat excerpt.") {
		t.Fatal(text)
	}
}

func TestCapturedExcerptSideDoesNotResolveSource(t *testing.T) {
	svc := &Service{}
	side := domain.SideConversation{ID: "side", MainConversationID: "main"}
	for _, conversation := range []string{"main", "side"} {
		ref := ports.ChatExcerptReference{ID: "annotation", Order: 42, ConversationID: conversation, MessageID: "deleted", Text: "selected", Snapshot: &domain.ChatExcerptSnapshot{Version: 1, RawText: "**selected**", CaptureMode: "exact"}}
		refs, err := svc.validateSideReferences(context.Background(), "session", side, []ports.ChatExcerptReference{ref})
		if err != nil || len(refs) != 1 {
			t.Fatalf("references = %+v, %v", refs, err)
		}
		if refs[0].ID != ref.ID || refs[0].Order != ref.Order || refs[0].Snapshot == nil {
			t.Fatalf("metadata lost: %+v", refs)
		}
		if text := sideQuestionWithReferences(domain.SideTurn{Text: "explain", References: refs}); !strings.Contains(text, "**selected**") {
			t.Fatal(text)
		}
	}
}
