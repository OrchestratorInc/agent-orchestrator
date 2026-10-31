package chat

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func segmentedFixture() (ConversationRows, ports.ChatExcerptReference) {
	rows := ConversationRows{Turns: []domain.ConversationTurn{{ID: "turn", ConversationID: "main", State: domain.TurnStateCompleted}},
		Messages: []domain.ConversationMessage{{ID: "user", ConversationID: "main", TurnID: "turn", Role: domain.MessageRoleUser, Text: "Explain this"},
			{ID: "reply", ConversationID: "main", TurnID: "turn", Role: domain.MessageRoleAssistant, Revision: 2, Text: "Before **Code**.\n\nAfter."}},
		Activities: []domain.ConversationActivity{{ID: "cmd", ConversationID: "main", TurnID: "turn", Revision: 3, Kind: domain.ActivityKindCommand, Status: domain.ActivityStatusCompleted,
			Detail: []byte(`{"command":"Write-Output 'Copy'","output":"aggregate","terminalInput":"ignored"}`), CommandOutput: "Copied\nDiagram ✓", StreamedText: "plain input"}}}
	parts := []domain.ExcerptSegment{{Kind: "message", SourceID: "reply", Revision: 2, Text: "Before Code."},
		{Kind: "command", SourceID: "cmd", Revision: 3, Field: "command", Text: "Write-Output 'Copy'"},
		{Kind: "command", SourceID: "cmd", Revision: 3, Field: "terminalInput", Text: "plain input"},
		{Kind: "command", SourceID: "cmd", Revision: 3, Field: "output", Text: "Copied\nDiagram ✓"}}
	return rows, ports.ChatExcerptReference{ConversationID: "main", Text: joinSegments(parts), Segments: parts}
}
func joinSegments(parts []domain.ExcerptSegment) string {
	texts := make([]string, len(parts))
	for i, p := range parts {
		texts[i] = strings.TrimSpace(p.Text)
	}
	return strings.Join(texts, "\n\n")
}

func TestMixedExcerptValidation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*ConversationRows, *ports.ChatExcerptReference)
		want   error
	}{
		{"valid", func(*ConversationRows, *ports.ChatExcerptReference) {}, nil},
		{"wrong conversation", func(_ *ConversationRows, r *ports.ChatExcerptReference) { r.ConversationID = "other" }, ErrExcerptStale},
		{"foreign activity", func(rows *ConversationRows, _ *ports.ChatExcerptReference) {
			rows.Activities[0].ConversationID = "other"
		}, ErrExcerptStale},
		{"changed activity", func(rows *ConversationRows, _ *ports.ChatExcerptReference) { rows.Activities[0].Revision++ }, ErrExcerptStale},
		{"changed message", func(rows *ConversationRows, _ *ports.ChatExcerptReference) { rows.Messages[1].Revision++ }, ErrExcerptStale},
		{"running activity", func(rows *ConversationRows, _ *ports.ChatExcerptReference) {
			rows.Activities[0].Status = domain.ActivityStatusRunning
		}, ErrExcerptStale},
		{"running turn", func(rows *ConversationRows, _ *ports.ChatExcerptReference) {
			rows.Turns[0].State = domain.TurnStateRunning
		}, ErrExcerptStale},
		{"cross turn", func(rows *ConversationRows, _ *ports.ChatExcerptReference) { rows.Activities[0].TurnID = "another" }, ErrExcerptInvalid},
		{"mixed legacy", func(_ *ConversationRows, r *ports.ChatExcerptReference) { r.MessageID = "reply" }, ErrExcerptInvalid},
		{"forged wrapper", func(_ *ConversationRows, r *ports.ChatExcerptReference) { r.Text += " forged" }, ErrExcerptInvalid},
		{"forged source", func(_ *ConversationRows, r *ports.ChatExcerptReference) {
			r.Segments[3].Text = "fabricated"
			r.Text = joinSegments(r.Segments)
		}, ErrExcerptInvalid},
		{"UI label", func(_ *ConversationRows, r *ports.ChatExcerptReference) {
			r.Segments[3].Text = "Agent typed"
			r.Text = joinSegments(r.Segments)
		}, ErrExcerptInvalid},
		{"wrong field", func(_ *ConversationRows, r *ports.ChatExcerptReference) { r.Segments[3].Field = "summary" }, ErrExcerptInvalid},
		{"unknown kind", func(_ *ConversationRows, r *ports.ChatExcerptReference) { r.Segments[3].Kind = "tool" }, ErrExcerptInvalid},
		{"empty list", func(_ *ConversationRows, r *ports.ChatExcerptReference) { r.Segments = []domain.ExcerptSegment{} }, ErrExcerptInvalid},
		{"too many", func(_ *ConversationRows, r *ports.ChatExcerptReference) {
			r.Segments = make([]domain.ExcerptSegment, 33)
		}, ErrExcerptInvalid},
		{"oversized", func(_ *ConversationRows, r *ports.ChatExcerptReference) {
			r.Text = strings.Repeat("x", maxExcerptTextBytes+1)
		}, ErrExcerptInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows, ref := segmentedFixture()
			tc.change(&rows, &ref)
			_, err := resolveExcerptSegments(rows, ref)
			if !errors.Is(err, tc.want) {
				t.Fatalf("error=%v want=%v", err, tc.want)
			}
		})
	}
	rows, ref := segmentedFixture()
	content, err := hydrateSegmentedExcerpt(rows, ref)
	if err != nil || content.Excerpt.SelectedText != ref.Text || content.Excerpt.UserMessage != "Explain this" || content.Excerpt.AssistantMessage != rows.Messages[1].Text {
		t.Fatalf("content=%+v err=%v", content, err)
	}
	if err = validateExcerptReferences([]ports.ChatExcerptReference{ref}, "main"); err != nil {
		t.Fatal(err)
	}
	ref2 := ref
	ref2.Segments = append([]domain.ExcerptSegment(nil), ref.Segments...)
	ref2.Segments[0].Text = "After."
	ref2.Text = joinSegments(ref2.Segments)
	other, err := hydrateSegmentedExcerpt(rows, ref2)
	if err != nil || other.URI == content.URI {
		t.Fatalf("distinct excerpts share URI: %v", err)
	}
}

func TestPlainCommandExcerptValidation(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		accepted  bool
	}{
		{"whitespace", "Copied   Diagram ✓", true},
		{"ANSI", "\x1b[32mCopied Diagram ✓\x1b[0m", false},
		{"carriage return", "old\rCopied Diagram ✓", false},
		{"backspace", "x\bCopied Diagram ✓", false},
		{"forged", "other", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows, ref := segmentedFixture()
			rows.Activities[0].CommandOutput = tc.raw
			ref.Segments = []domain.ExcerptSegment{{Kind: "command", SourceID: "cmd", Revision: 3, Field: "output", Text: "Copied\nDiagram ✓"}}
			ref.Text = ref.Segments[0].Text
			_, err := resolveExcerptSegments(rows, ref)
			if (err == nil) != tc.accepted {
				t.Fatalf("error=%v accepted=%v", err, tc.accepted)
			}
		})
	}
	rows, ref := segmentedFixture()
	rows.Activities[0].CommandOutput = ""
	rows.Activities[0].Detail = json.RawMessage(`{"output":{"text":"Copied Diagram ✓"}}`)
	ref.Segments = []domain.ExcerptSegment{{Kind: "command", SourceID: "cmd", Revision: 3, Field: "output", Text: "Copied Diagram ✓"}}
	ref.Text = ref.Segments[0].Text
	if _, err := resolveExcerptSegments(rows, ref); !errors.Is(err, ErrExcerptInvalid) {
		t.Fatalf("structured output accepted: %v", err)
	}
}

func TestMixedExcerptCombinedBytesIncludeWhitespace(t *testing.T) {
	text := strings.Repeat(" ", 12287) + "x" + strings.Repeat(" ", 12287)
	ref := ports.ChatExcerptReference{ConversationID: "main", Text: text, Segments: []domain.ExcerptSegment{{Kind: "command", SourceID: "command", Revision: 1, Field: "output", Text: text}}}
	if err := validateExcerptReferences([]ports.ChatExcerptReference{ref}, "main"); err != nil {
		t.Fatal(err)
	}
	if err := validateExcerptReferences([]ports.ChatExcerptReference{ref, ref}, "main"); !errors.Is(err, ErrExcerptInvalid) {
		t.Fatalf("combined bytes accepted: %v", err)
	}
}

func TestSegmentedSideSelfReferenceAndRevision(t *testing.T) {
	ctx := context.Background()
	svc := New(Options{AppRunID: "run"})
	now := svc.now()
	_, _ = svc.sides.store.ClaimSideLaunch(ctx, "run", now)
	side := domain.SideConversation{ID: "side", SessionID: "session", AppRunID: "run", Generation: "g", State: "opening"}
	_, _, err := svc.sides.store.CreateSideConversation(ctx, side)
	if err != nil {
		t.Fatal(err)
	}
	turn := domain.SideTurn{ID: "turn", SideID: side.ID, ClientMessageID: "seed", Text: "Explain", State: "completed"}
	_, _, err = svc.sides.store.ReserveSideTurn(ctx, turn, "run")
	if err != nil {
		t.Fatal(err)
	}
	if err = svc.sides.store.SettleSideTurn(ctx, side.ID, turn.ID, "g", "completed", "", "", now); err != nil {
		t.Fatal(err)
	}
	if err = svc.sides.store.UpsertSideMessage(ctx, domain.SideMessage{ID: "user", SideID: side.ID, TurnID: turn.ID, Role: "user", Text: "Explain"}, "g"); err != nil {
		t.Fatal(err)
	}
	a := domain.SideActivity{ID: "cmd", SideID: side.ID, TurnID: turn.ID, Kind: "command", Status: "completed", Text: "Copy ✓", Detail: json.RawMessage(`{"command":"echo Copy"}`)}
	if err = svc.sides.store.UpsertSideActivity(side.ID, "g", a); err != nil {
		t.Fatal(err)
	}
	parts := []domain.ExcerptSegment{{Kind: "command", SourceID: a.ID, Revision: 1, Field: "output", Text: a.Text}}
	ref := ports.ChatExcerptReference{ConversationID: side.ID, Text: a.Text, Segments: parts}
	if _, _, err = svc.sideSelfReferenceContext(ctx, side, ref); err != nil {
		t.Fatal(err)
	}
	if err = svc.sides.store.UpsertSideActivity(side.ID, "g", a); err != nil {
		t.Fatal(err)
	}
	if _, _, err = svc.sideSelfReferenceContext(ctx, side, ref); !errors.Is(err, ErrExcerptStale) {
		t.Fatalf("old revision accepted: %v", err)
	}
}

func TestLegacyExcerptSerializationUnchanged(t *testing.T) {
	ref := ports.ChatExcerptReference{ConversationID: "main", MessageID: "message", Revision: 0, Text: "Copy"}
	raw, err := json.Marshal(ref)
	if err != nil || string(raw) != `{"conversationId":"main","messageId":"message","revision":0,"text":"Copy"}` {
		t.Fatalf("legacy=%s err=%v", raw, err)
	}
}

func TestSegmentedAnchorUnavailableMatchesLegacy(t *testing.T) {
	rows, ref := segmentedFixture()
	rows.Turns = nil
	_, err := resolveExcerptSegmentsWithAnchorError(rows, ref, ErrSideAnchorUnavailable)
	if !errors.Is(err, ErrSideAnchorUnavailable) {
		t.Fatalf("anchor error=%v", err)
	}
	_, err = resolveExcerptSegments(rows, ref)
	if !errors.Is(err, ErrExcerptStale) {
		t.Fatalf("non-anchor error=%v", err)
	}
}
